package sync

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/protocol"
)

func signedManifest(t *testing.T, revision uint64) (protocol.ImmutableManifest, map[string]ed25519.PublicKey, time.Time) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10_000, 0).UTC()
	manifest := protocol.ImmutableManifest{
		SchemaVersion: protocol.SyncProtocolVersion, ID: "release-5", Revision: revision,
		Desired:              protocol.HashState{PackHash: strings.Repeat("a", 64), ConfigHash: strings.Repeat("b", 64), ReleaseHash: strings.Repeat("c", 64)},
		Rollback:             protocol.HashState{PackHash: strings.Repeat("d", 64), ConfigHash: strings.Repeat("e", 64), ReleaseHash: strings.Repeat("f", 64)},
		Artifacts:            []protocol.ArtifactRef{{Name: "pack.json", URL: "/api/v1/artifacts/pack.json", SHA256: strings.Repeat("a", 64), Size: 100, Type: "parser_pack"}},
		MinimumClientVersion: "5.0.0", RequiredCapabilities: []string{"stage", "validate", "activate", "rollback"},
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), KeyID: "controller",
	}
	if err := manifest.Sign(privateKey); err != nil {
		t.Fatal(err)
	}
	return manifest, map[string]ed25519.PublicKey{"controller": publicKey}, now
}

func capableRequest(node string, now time.Time) protocol.SyncRequest {
	return protocol.SyncRequest{
		NodeID: node, ReportedAt: now,
		Capabilities: protocol.NodeCapabilities{
			ProtocolVersion: protocol.SyncProtocolVersion, ClientVersion: "5.1.0",
			Commands: []string{"stage", "validate", "activate", "rollback"}, SupportsRollback: true,
		},
	}
}

func TestSignedManifestReconciliationAndIdempotentLifecycle(t *testing.T) {
	manifest, keys, now := signedManifest(t, 1)
	manager := NewSyncManager(Config{TrustedManifestKeys: keys, MinimumClientVersion: "5.0.0", LeaseDuration: time.Second})
	if err := manager.SetDesiredManifest(manifest, now); err != nil {
		t.Fatal(err)
	}
	response, err := manager.Reconcile(capableRequest("node-1", now), now)
	if err != nil || response.Status != StatusDrifted || len(response.Commands) != 3 {
		t.Fatalf("response=%+v err=%v", response, err)
	}

	statuses := []string{"staged", "validated", "activated"}
	for _, wantedStatus := range statuses {
		command, ok := manager.LeaseNext("node-1", now)
		if !ok {
			t.Fatalf("no command for %s", wantedStatus)
		}
		status := protocol.SyncCommandStatus{
			NodeID: "node-1", CommandID: command.ID, ManifestID: command.ManifestID,
			Revision: command.Revision, Status: wantedStatus, State: manifest.Desired,
		}
		if err := manager.CompleteCommand(status, now); err != nil {
			t.Fatal(err)
		}
		if err := manager.CompleteCommand(status, now); err != nil {
			t.Fatalf("idempotent duplicate rejected: %v", err)
		}
	}
	state, _ := manager.GetClientSyncState("node-1")
	if state.SyncStatus != StatusSynced || !state.Active.Equal(manifest.Desired) || !state.Rollback.Empty() {
		t.Fatalf("final state: %+v", state)
	}
}

func TestExponentialRetryAndLeaseRequeue(t *testing.T) {
	manifest, keys, now := signedManifest(t, 1)
	manager := NewSyncManager(Config{
		TrustedManifestKeys: keys, MinimumClientVersion: "5.0.0", BaseRetryDelay: time.Second,
		MaxRetryDelay: 8 * time.Second, MaxRetries: 4, LeaseDuration: time.Second,
	})
	_ = manager.SetDesiredManifest(manifest, now)
	_, _ = manager.Reconcile(capableRequest("node-retry", now), now)
	first, ok := manager.LeaseNext("node-retry", now)
	if !ok || first.Attempt != 1 {
		t.Fatalf("first lease: %+v %v", first, ok)
	}
	failed := protocol.SyncCommandStatus{NodeID: "node-retry", CommandID: first.ID, ManifestID: first.ManifestID, Revision: first.Revision, Status: "failed"}
	if err := manager.CompleteCommand(failed, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.LeaseNext("node-retry", now.Add(999*time.Millisecond)); ok {
		t.Fatal("command re-leased before retry deadline")
	}
	second, ok := manager.LeaseNext("node-retry", now.Add(time.Second))
	if !ok || second.Attempt != 2 {
		t.Fatalf("second lease: %+v %v", second, ok)
	}
	if err := manager.CompleteCommand(failed, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	state, _ := manager.GetClientSyncState("node-retry")
	entry := state.PendingCommands[first.ID]
	if !entry.NextAttemptAt.Equal(now.Add(3 * time.Second)) {
		t.Fatalf("second retry at %s, want %s", entry.NextAttemptAt, now.Add(3*time.Second))
	}
}

func TestManifestConflictAndControllerOlderRevisionResolution(t *testing.T) {
	manifest, keys, now := signedManifest(t, 3)
	manager := NewSyncManager(Config{TrustedManifestKeys: keys, MinimumClientVersion: "5.0.0"})
	_ = manager.SetDesiredManifest(manifest, now)
	request := capableRequest("node-conflict", now)
	request.LastManifestID, request.LastManifestRevision = "other", 4
	response, _ := manager.Reconcile(request, now)
	if response.Status != StatusConflict {
		t.Fatalf("status=%s", response.Status)
	}
	manager.ResolveSyncConflicts(nil)
	state, _ := manager.GetClientSyncState("node-conflict")
	if state.Conflict == nil || state.Conflict.Resolution != "manual_review" {
		t.Fatal("newer client conflict was silently overwritten")
	}
}

func TestPartitionRecoveryPreservesQueuedCommands(t *testing.T) {
	manifest, keys, now := signedManifest(t, 1)
	manager := NewSyncManager(Config{TrustedManifestKeys: keys, MinimumClientVersion: "5.0.0", HeartbeatTimeout: time.Second, PartitionConfirmAfter: 2 * time.Second})
	_ = manager.SetDesiredManifest(manifest, now)
	request := capableRequest("node-partition", now)
	_, _ = manager.Reconcile(request, now)
	manager.CheckPartitionsAt(now.Add(3 * time.Second))
	state, _ := manager.GetClientSyncState("node-partition")
	queued := len(state.PendingCommands)
	if !state.NetworkPartition || state.SyncStatus != StatusDisconnected || queued == 0 {
		t.Fatalf("partition state: %+v", state)
	}
	request.ReportedAt = now.Add(4 * time.Second)
	_, _ = manager.Reconcile(request, request.ReportedAt)
	state, _ = manager.GetClientSyncState("node-partition")
	if state.NetworkPartition || state.LastRecoveryAt.IsZero() || len(state.PendingCommands) != queued {
		t.Fatalf("recovery lost state: %+v", state)
	}
}

func TestHeartbeatRetainsUPESMetrics(t *testing.T) {
	manager := NewSyncManager(Config{})
	request := &protocol.HeartbeatRequest{NodeID: "upes-node", Metrics: protocol.Metrics{
		UPESMetricsAvailable: true, UPESUniqueFailureSignatures: 7,
		UPESObservedFailures: 19, UPESDiscoveryQueued: 4,
		UPESDiscoveryDropped: 2, UPESDiscoveryPending: 3,
	}}
	if err := manager.ProcessHeartbeat(request); err != nil {
		t.Fatal(err)
	}
	state, ok := manager.GetClientSyncState("upes-node")
	if !ok || !state.Metrics.UPESMetricsAvailable || state.Metrics.UPESUniqueFailureSignatures != 7 || state.Metrics.UPESObservedFailures != 19 || state.Metrics.UPESDiscoveryQueued != 4 || state.Metrics.UPESDiscoveryDropped != 2 || state.Metrics.UPESDiscoveryPending != 3 {
		t.Fatalf("UPES counters were lost in sync heartbeat state: %+v", state)
	}
}

func TestSignedRollbackCommand(t *testing.T) {
	manifest, keys, now := signedManifest(t, 1)
	manager := NewSyncManager(Config{TrustedManifestKeys: keys, MinimumClientVersion: "5.0.0"})
	_ = manager.SetDesiredManifest(manifest, now)
	_, _ = manager.Reconcile(capableRequest("node-rollback", now), now)
	if err := manager.QueueRollback("node-rollback"); err != nil {
		t.Fatal(err)
	}
	state, _ := manager.GetClientSyncState("node-rollback")
	if !state.Desired.Equal(manifest.Rollback) {
		t.Fatal("rollback generation was not selected")
	}
}
