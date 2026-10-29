// Package synctest validates the current v2 synchronization and rollout contracts.
package synctest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/progressive"
	"nexora.local/nli-v20/internal/protocol"
	syncmanager "nexora.local/nli-v20/internal/sync"
)

func signedManifest(t *testing.T) (protocol.ImmutableManifest, map[string]ed25519.PublicKey, time.Time) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(50_000, 0).UTC()
	manifest := protocol.ImmutableManifest{
		SchemaVersion: protocol.SyncProtocolVersion,
		ID:            "synctest-manifest",
		Revision:      1,
		Desired: protocol.HashState{
			PackHash: strings.Repeat("a", 64), ConfigHash: strings.Repeat("b", 64), ReleaseHash: strings.Repeat("c", 64),
		},
		Rollback: protocol.HashState{
			PackHash: strings.Repeat("d", 64), ConfigHash: strings.Repeat("e", 64), ReleaseHash: strings.Repeat("f", 64),
		},
		Artifacts: []protocol.ArtifactRef{{
			Name: "parser-pack.json", URL: "/api/v1/artifacts/parser-pack.json", SHA256: strings.Repeat("a", 64), Size: 42, Type: "parser_pack",
		}},
		MinimumClientVersion: "5.0.0",
		RequiredCapabilities: []string{"stage", "validate", "activate", "rollback"},
		IssuedAt:             now.Add(-time.Minute),
		ExpiresAt:            now.Add(time.Hour),
		KeyID:                "controller",
	}
	if err := manifest.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	return manifest, map[string]ed25519.PublicKey{"controller": publicKey}, now
}

func capableRequest(nodeID string, now time.Time) protocol.SyncRequest {
	return protocol.SyncRequest{
		NodeID: nodeID, ReportedAt: now,
		Capabilities: protocol.NodeCapabilities{
			ProtocolVersion:  protocol.SyncProtocolVersion,
			ClientVersion:    "5.0.0",
			Commands:         []string{"stage", "validate", "activate", "rollback"},
			SupportsRollback: true,
		},
	}
}

func TestSignedSyncLifecycle(t *testing.T) {
	manifest, trustedKeys, now := signedManifest(t)
	manager := syncmanager.NewSyncManager(syncmanager.Config{
		TrustedManifestKeys:  trustedKeys,
		MinimumClientVersion: "5.0.0",
		LeaseDuration:        time.Second,
	})
	if err := manager.SetDesiredManifest(manifest, now); err != nil {
		t.Fatal(err)
	}
	response, err := manager.Reconcile(capableRequest("node-1", now), now)
	if err != nil || response.Status != syncmanager.StatusDrifted || len(response.Commands) != 3 {
		t.Fatalf("reconcile response=%+v err=%v", response, err)
	}

	for _, statusName := range []string{"staged", "validated", "activated"} {
		command, ok := manager.LeaseNext("node-1", now)
		if !ok {
			t.Fatalf("missing command for %s", statusName)
		}
		status := protocol.SyncCommandStatus{
			NodeID: "node-1", CommandID: command.ID, ManifestID: command.ManifestID,
			Revision: command.Revision, Status: statusName, State: manifest.Desired,
		}
		if err := manager.CompleteCommand(status, now); err != nil {
			t.Fatal(err)
		}
		if err := manager.CompleteCommand(status, now); err != nil {
			t.Fatalf("idempotent duplicate rejected: %v", err)
		}
	}

	state, ok := manager.GetClientSyncState("node-1")
	if !ok || state.SyncStatus != syncmanager.StatusSynced || !state.Active.Equal(manifest.Desired) {
		t.Fatalf("final sync state=%+v", state)
	}
}

func TestHeartbeatWireContract(t *testing.T) {
	request := protocol.HeartbeatRequest{
		NodeID: "node-heartbeat", Version: "5.0.0", ConfigHash: strings.Repeat("a", 64), Health: "healthy",
		Metrics: protocol.Metrics{Received: 100, Parsed: 98, Quarantined: 1, Errors: 1, ParseRate: 98},
		Parsers: []protocol.ParserInventory{{ID: "smtp.native", Version: 5, Source: "builtin", TestStatus: "pass"}},
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded protocol.HeartbeatRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Metrics.Received != 100 || len(decoded.Parsers) != 1 || decoded.Parsers[0].Version != 5 {
		t.Fatalf("heartbeat round trip mismatch: %+v", decoded)
	}
}

func TestProgressiveCanaryCompletion(t *testing.T) {
	manifest, _, _ := signedManifest(t)
	manager := progressive.NewProgressiveManager(progressive.WithNodes([]progressive.Node{{
		ID: "node-canary", Tags: map[string]string{"ring": "canary"},
	}}))
	strategy := progressive.Strategy{
		Name: "one-node-canary", MaximumFailureRate: 0, AutoRollback: true,
		Waves: []progressive.Wave{{Name: "canary", Percent: 100, MinimumNodes: 1}},
	}
	if _, err := manager.Create("rollout-1", "signed parser rollout", strategy, progressive.Selector{
		Tags: map[string]string{"ring": "canary"},
	}, manifest.Desired, manifest.Rollback); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start("rollout-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecordNodeResult("rollout-1", "node-canary", progressive.NodeSucceeded, time.Millisecond, "active"); err != nil {
		t.Fatal(err)
	}
	rollout, ok := manager.Get("rollout-1")
	if !ok || rollout.Status != progressive.RolloutCompleted || !rollout.Active.Equal(manifest.Desired) {
		t.Fatalf("rollout state=%+v", rollout)
	}
}
