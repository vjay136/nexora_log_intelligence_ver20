package integration_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/migration"
	"nexora.local/nli-v20/internal/progressive"
	"nexora.local/nli-v20/internal/protocol"
	syncmanager "nexora.local/nli-v20/internal/sync"
)

func TestMigrationSignedSyncAndCanaryContract(t *testing.T) {
	source := []byte(`loki.process "smtp" { stage.regex { expression = "^(?P<message>.*)$" } }`)
	migrationResult, err := migration.TranslateAlloy(source, "smtp.alloy")
	if err != nil || migrationResult.ParserPack == nil {
		t.Fatalf("migration: result=%+v err=%v", migrationResult, err)
	}
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Unix(40_000, 0).UTC()
	state := protocol.HashState{PackHash: migrationResult.ParserPack.SHA256, ConfigHash: strings.Repeat("b", 64), ReleaseHash: strings.Repeat("c", 64)}
	manifest := protocol.ImmutableManifest{
		SchemaVersion: protocol.SyncProtocolVersion, ID: "integration", Revision: 1,
		Desired: state, Rollback: protocol.HashState{PackHash: strings.Repeat("d", 64)},
		Artifacts:            []protocol.ArtifactRef{{Name: "pack.json", URL: "/api/v1/artifacts/pack.json", SHA256: migrationResult.ParserPack.SHA256, Size: 1, Type: "parser_pack"}},
		MinimumClientVersion: "5.0.0", RequiredCapabilities: []string{"stage", "validate", "activate", "rollback"},
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), KeyID: "controller",
	}
	if err := manifest.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	synchronizer := syncmanager.NewSyncManager(syncmanager.Config{TrustedManifestKeys: map[string]ed25519.PublicKey{"controller": publicKey}, MinimumClientVersion: "5.0.0"})
	if err := synchronizer.SetDesiredManifest(manifest, now); err != nil {
		t.Fatal(err)
	}
	response, err := synchronizer.Reconcile(protocol.SyncRequest{NodeID: "node-1", Capabilities: protocol.NodeCapabilities{
		ProtocolVersion: protocol.SyncProtocolVersion, ClientVersion: "5.0.0", Commands: []string{"stage", "validate", "activate", "rollback"},
	}}, now)
	if err != nil || response.Status != syncmanager.StatusDrifted || len(response.Commands) != 3 {
		t.Fatalf("sync response=%+v err=%v", response, err)
	}

	rollouts := progressive.NewProgressiveManager(progressive.WithNodes([]progressive.Node{{ID: "node-1", Tags: map[string]string{"ring": "canary"}}}))
	strategy := progressive.Strategy{Name: "one-node-canary", MaximumFailureRate: 0, AutoRollback: true, Waves: []progressive.Wave{{Name: "canary", Percent: 100, MinimumNodes: 1}}}
	if _, err := rollouts.Create("integration", "integration", strategy, progressive.Selector{Tags: map[string]string{"ring": "canary"}}, state, manifest.Rollback); err != nil {
		t.Fatal(err)
	}
	if err := rollouts.Start("integration"); err != nil {
		t.Fatal(err)
	}
	if err := rollouts.RecordNodeResult("integration", "node-1", progressive.NodeSucceeded, time.Millisecond, "active"); err != nil {
		t.Fatal(err)
	}
	rollout, _ := rollouts.Get("integration")
	if rollout.Status != progressive.RolloutCompleted || !rollout.Active.Equal(state) {
		t.Fatalf("rollout did not activate desired state: %+v", rollout)
	}
}
