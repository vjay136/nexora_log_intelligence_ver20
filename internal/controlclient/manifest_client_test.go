package controlclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/protocol"
)

type memoryFetcher map[string][]byte

func (fetcher memoryFetcher) Fetch(_ context.Context, artifact protocol.ArtifactRef) ([]byte, error) {
	data, exists := fetcher[artifact.Name]
	if !exists {
		return nil, fmt.Errorf("missing artifact")
	}
	return append([]byte(nil), data...), nil
}

func clientManifest(t *testing.T, id string, revision uint64, data []byte, rollback protocol.HashState, privateKey ed25519.PrivateKey) protocol.ImmutableManifest {
	t.Helper()
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	now := time.Unix(30_000, 0).UTC()
	manifest := protocol.ImmutableManifest{
		SchemaVersion: protocol.SyncProtocolVersion, ID: id, Revision: revision,
		Desired: protocol.HashState{ConfigHash: hash}, Rollback: rollback,
		Artifacts:            []protocol.ArtifactRef{{Name: "config.json", URL: "/api/v1/artifacts/config.json", SHA256: hash, Size: int64(len(data)), Type: "config"}},
		MinimumClientVersion: "5.0.0", RequiredCapabilities: []string{"stage", "validate", "activate", "rollback"},
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), KeyID: "controller",
	}
	if err := manifest.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestManifestClientStageActivateAndRollback(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Unix(30_000, 0).UTC()
	firstData := []byte(`{"version":1}`)
	first := clientManifest(t, "release-a", 1, firstData, protocol.HashState{PackHash: strings.Repeat("f", 64)}, privateKey)
	fetcher := memoryFetcher{"config.json": firstData}
	client, err := NewManifestClient(t.TempDir(), "5.1.0", map[string]ed25519.PublicKey{"controller": publicKey}, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Stage(context.Background(), first, now); err != nil {
		t.Fatal(err)
	}
	if err := client.Stage(context.Background(), first, now); err != nil {
		t.Fatalf("idempotent stage failed: %v", err)
	}
	if err := client.ValidateStaged(); err != nil {
		t.Fatal(err)
	}
	if err := client.Activate(); err != nil {
		t.Fatal(err)
	}
	if !client.State().Active.Equal(first.Desired) {
		t.Fatal("first generation not active")
	}

	secondData := []byte(`{"version":2}`)
	second := clientManifest(t, "release-b", 2, secondData, first.Desired, privateKey)
	fetcher["config.json"] = secondData
	if err := client.Stage(context.Background(), second, now); err != nil {
		t.Fatal(err)
	}
	if err := client.ValidateStaged(); err != nil {
		t.Fatal(err)
	}
	if err := client.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := client.Rollback(); err != nil {
		t.Fatal(err)
	}
	state := client.State()
	if state.Status != "rolled_back" || !state.Active.Equal(first.Desired) {
		t.Fatalf("rollback state: %+v", state)
	}
}

func TestManifestClientRejectsTamperAndConflictingReplay(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Unix(30_000, 0).UTC()
	data := []byte("correct")
	manifest := clientManifest(t, "release", 1, data, protocol.HashState{}, privateKey)
	client, _ := NewManifestClient(t.TempDir(), "5.0.0", map[string]ed25519.PublicKey{"controller": publicKey}, memoryFetcher{"config.json": []byte("tampered")})
	if err := client.Stage(context.Background(), manifest, now); err == nil || !strings.Contains(err.Error(), "size mismatch") && !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered artifact accepted: %v", err)
	}

	client, _ = NewManifestClient(t.TempDir(), "5.0.0", map[string]ed25519.PublicKey{"controller": publicKey}, memoryFetcher{"config.json": data})
	status := protocol.SyncCommandStatus{NodeID: "node", CommandID: "command", ManifestID: "release", Revision: 1, Status: "staged"}
	if err := client.RecordCommand(status); err != nil {
		t.Fatal(err)
	}
	if err := client.RecordCommand(status); err != nil {
		t.Fatal(err)
	}
	status.Status = "failed"
	if err := client.RecordCommand(status); err == nil {
		t.Fatal("conflicting replay accepted")
	}
}
