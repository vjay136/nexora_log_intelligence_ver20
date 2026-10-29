package rollout_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/protocol"
	"nexora.local/nli-v20/internal/rollout"
)

func signedRolloutManifest(t *testing.T) (protocol.ImmutableManifest, map[string]ed25519.PublicKey, time.Time) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(20_000, 0).UTC()
	manifest := protocol.ImmutableManifest{
		SchemaVersion: protocol.SyncProtocolVersion, ID: "manifest-rollout", Revision: 1,
		Desired:              protocol.HashState{PackHash: strings.Repeat("a", 64)},
		Rollback:             protocol.HashState{PackHash: strings.Repeat("b", 64)},
		Artifacts:            []protocol.ArtifactRef{{Name: "pack.json", URL: "/api/v1/artifacts/pack.json", SHA256: strings.Repeat("a", 64), Size: 10, Type: "parser_pack"}},
		MinimumClientVersion: "5.0.0", RequiredCapabilities: []string{"stage", "activate", "rollback"},
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), KeyID: "controller",
	}
	if err := manifest.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	return manifest, map[string]ed25519.PublicKey{"controller": publicKey}, now
}

func TestSignedManifestPauseResumeLeaseAndRollback(t *testing.T) {
	manifest, keys, now := signedRolloutManifest(t)
	manager := tmpRollout(t)
	record, err := manager.CreateSignedManifest(manifest, keys, now, "signed", "ops", []string{"node-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if record.ManifestID != manifest.ID || !record.Desired.Equal(manifest.Desired) {
		t.Fatalf("manifest state not retained: %+v", record)
	}
	if err := manager.Start(record.ID, []string{"node-1"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Pause(record.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.NextForNode("node-1", time.Second); ok {
		t.Fatal("paused rollout leased a command")
	}
	if err := manager.Resume(record.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.NextForNode("node-1", time.Second); !ok {
		t.Fatal("resumed rollout did not lease a command")
	}
	if err := manager.RollbackToPrevious(record.ID, "operator rollback"); err != nil {
		t.Fatal(err)
	}
	stored, _ := manager.Get(record.ID)
	if stored.Status != rollout.StatusRolledBack || !stored.Active.Equal(manifest.Rollback) {
		t.Fatalf("rollback state: %+v", stored)
	}
}

func TestSignedManifestRolloutRejectsTamper(t *testing.T) {
	manifest, keys, now := signedRolloutManifest(t)
	manifest.Desired.PackHash = strings.Repeat("c", 64)
	if _, err := tmpRollout(t).CreateSignedManifest(manifest, keys, now, "tampered", "ops", []string{"node-1"}, nil); err == nil {
		t.Fatal("tampered manifest was accepted")
	}
}
