package controlplane

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/fleet"
	"nexora.local/nli-v20/internal/protocol"
	"nexora.local/nli-v20/telemetry"
)

func syncBatch(node, generation string, sequence uint64, files ...telemetry.StreamFileTelemetry) telemetry.SyncBatch {
	now := time.Now().UTC()
	return telemetry.SyncBatch{SchemaVersion: telemetry.SyncSchemaVersion, NodeID: node, ProcessGeneration: generation, Sequence: sequence, CollectedAt: now, PublishedAt: now, CollectionIntervalMS: 1000, PublicationIntervalMS: 1000, FullSnapshot: true, StreamFiles: files}
}

func source(path string, read, normalized, quarantined uint64) telemetry.StreamFileTelemetry {
	return telemetry.StreamFileTelemetry{Source: path, Phase: "reading", ReadLines: read, Normalized: normalized, Quarantined: quarantined, ValidationAttempts: normalized + quarantined, ValidationPassed: normalized, ValidationFailed: quarantined, Bytes: read * 100, UpdatedAt: time.Now().UTC()}
}

func TestSyncFabricOrderingReconnectAndCanonicalAggregate(t *testing.T) {
	hub := NewStreamHub(8)
	first := hub.PublishBatch(syncBatch("node-1", "generation-a", 1, source("/a.log", 10, 8, 2), source("/b.log", 20, 20, 0)))
	if first.Status != "accepted" || first.Revision != 1 || first.Accepted != 2 {
		t.Fatalf("first publication: %+v", first)
	}
	duplicate := hub.PublishBatch(syncBatch("node-1", "generation-a", 1, source("/a.log", 999, 999, 0)))
	if duplicate.Status != "duplicate" || duplicate.Revision != first.Revision {
		t.Fatalf("duplicate mutated canonical revision: %+v", duplicate)
	}
	outOfOrder := hub.PublishBatch(syncBatch("node-1", "generation-a", 0, source("/a.log", 999, 999, 0)))
	if outOfOrder.Status != "out_of_order" {
		t.Fatalf("out-of-order publication accepted: %+v", outOfOrder)
	}
	gapped := hub.PublishBatch(syncBatch("node-1", "generation-a", 4, source("/a.log", 14, 12, 2)))
	if gapped.Status != "accepted" || gapped.SequenceGaps != 2 {
		t.Fatalf("sequence gap not measured: %+v", gapped)
	}
	restarted := hub.PublishBatch(syncBatch("node-1", "generation-b", 1, source("/a.log", 1, 1, 0)))
	if restarted.Status != "accepted" {
		t.Fatalf("new process generation rejected: %+v", restarted)
	}
	snapshot := hub.CanonicalSnapshot()
	if len(snapshot.Sources) != 1 || snapshot.Aggregate.SourceRecordsReadTotal != 1 || snapshot.Aggregate.NormalizedRecordsTotal != 1 || snapshot.Aggregate.ValidationAttemptsTotal != 1 || snapshot.Aggregate.ValidationPassedTotal != 1 || snapshot.Aggregate.ValidationFailedTotal != 0 {
		t.Fatalf("full snapshot/restart did not replace canonical state: %+v", snapshot)
	}
	if snapshot.Diagnostics.DuplicateBatches != 1 || snapshot.Diagnostics.OutOfOrderBatches != 1 || snapshot.Diagnostics.SequenceGaps != 2 || snapshot.Diagnostics.Reconnects != 1 {
		t.Fatalf("ordering diagnostics mismatch: %+v", snapshot.Diagnostics)
	}
}

func TestSyncFabricFreshnessCriticalReasonsAndClockSkew(t *testing.T) {
	hub := NewStreamHub(2)
	hub.liveAfter, hub.delayedAfter, hub.staleAfter = 5*time.Millisecond, 10*time.Millisecond, 20*time.Millisecond
	batch := syncBatch("node-critical", "generation-a", 1, telemetry.StreamFileTelemetry{Source: "/mail.log", Phase: "reading", QueueDepth: 8, QueueCapacity: 10, Backpressure: true, Errors: 3, UpdatedAt: time.Now().UTC()})
	batch.PublishedAt = time.Now().UTC().Add(-time.Hour)
	hub.PublishBatch(batch)
	time.Sleep(25 * time.Millisecond)
	snapshot := hub.CanonicalSnapshot()
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].Freshness != telemetry.FreshnessDisconnected || !snapshot.Sources[0].Critical {
		t.Fatalf("freshness/critical state mismatch: %+v", snapshot.Sources)
	}
	if snapshot.Diagnostics.ClockSkewSamples != 1 || snapshot.Diagnostics.DisconnectedSources != 1 {
		t.Fatalf("clock/freshness diagnostics mismatch: %+v", snapshot.Diagnostics)
	}
	codes := map[string]bool{}
	for _, issue := range snapshot.Diagnostics.Issues {
		codes[issue.Code] = true
		if issue.CorrelationID == "" || issue.Stage == "" || issue.Reason == "" || issue.Occurrences == 0 {
			t.Fatalf("issue lacks actionable bounded evidence: %+v", issue)
		}
	}
	for _, code := range []string{"queue_backpressure", "source_errors", "clock_skew"} {
		if !codes[code] {
			t.Fatalf("missing diagnostic %q in %+v", code, snapshot.Diagnostics.Issues)
		}
	}
}

func TestSyncFabricConcurrentSubscribersAndThousandNodes(t *testing.T) {
	hub := NewStreamHub(64)
	type subscription struct {
		id uint64
		ch <-chan telemetry.StreamFileTelemetry
	}
	subs := make([]subscription, 32)
	for i := range subs {
		id, ch, ok := hub.Subscribe()
		if !ok {
			t.Fatalf("subscriber %d rejected", i)
		}
		subs[i] = subscription{id: id, ch: ch}
	}
	for i := 0; i < 1000; i++ {
		hub.PublishBatch(syncBatch(fmt.Sprintf("node-%04d", i), "generation-a", 1, source("/input.log", 1, 1, 0)))
	}
	for _, sub := range subs {
		select {
		case delta := <-sub.ch:
			if delta.CanonicalAggregate == nil || delta.CanonicalRevision == 0 {
				t.Fatalf("subscriber received non-canonical delta: %+v", delta)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive an incremental update")
		}
		hub.Unsubscribe(sub.id)
	}
	snapshot := hub.CanonicalSnapshot()
	if snapshot.Diagnostics.TrackedNodes != 1000 || snapshot.Diagnostics.TrackedSources != 1000 || snapshot.Aggregate.SourceRecordsReadTotal != 1000 {
		t.Fatalf("large fleet canonical state mismatch: nodes=%d sources=%d total=%d", snapshot.Diagnostics.TrackedNodes, snapshot.Diagnostics.TrackedSources, snapshot.Aggregate.SourceRecordsReadTotal)
	}
	if snapshot.Diagnostics.CoalescedUpdates == 0 {
		t.Fatal("slow subscriber coalescing was not measured")
	}
}

func TestCanonicalSSERefreshesServerWriteDeadline(t *testing.T) {
	hub := NewStreamHub(4)
	hub.target = 10 * time.Millisecond
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.serveCanonicalSSE(w, r, nil, nil)
	}))
	server.Config.WriteTimeout = 35 * time.Millisecond
	server.Start()
	defer server.Close()

	client := &http.Client{Timeout: 750 * time.Millisecond}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	heartbeats := 0
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) && heartbeats < 8 {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("SSE connection was cut at the server write timeout: %v", readErr)
		}
		if strings.TrimSpace(line) == "event: sync_heartbeat" {
			heartbeats++
		}
	}
	if heartbeats < 8 {
		t.Fatalf("received %d heartbeats, want at least 8 beyond the fixed write-timeout boundary", heartbeats)
	}
}

func TestSyncFabricFullSnapshotRemovalRequiresSubscriberResync(t *testing.T) {
	hub := NewStreamHub(2)
	hub.PublishBatch(syncBatch("node-1", "generation-a", 1,
		source("/a.log", 1, 1, 0), source("/b.log", 1, 1, 0)))
	id, updates, ok := hub.Subscribe()
	if !ok {
		t.Fatal("subscriber rejected")
	}
	defer hub.Unsubscribe(id)

	result := hub.PublishBatch(syncBatch("node-1", "generation-a", 2))
	if result.Status != "accepted" || result.Accepted != 0 {
		t.Fatalf("empty full snapshot result: %+v", result)
	}
	select {
	case update := <-updates:
		if !update.ResyncRequired || update.CanonicalRevision != result.Revision {
			t.Fatalf("source removal did not require authoritative resync: %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("source removal did not notify subscriber")
	}
	if snapshot := hub.CanonicalSnapshot(); len(snapshot.Sources) != 0 || snapshot.Aggregate.SourceRecordsReadTotal != 0 {
		t.Fatalf("removed sources remain canonical: %+v", snapshot)
	}
}

func TestCanonicalSyncAPIInitialSnapshotAndVersionedIngest(t *testing.T) {
	f := newAPIFixture(t)
	f.server.fleet.Register(&fleet.Node{ID: "sync-node", Hostname: "sync-node"})
	const token = "sync-node-token"
	if err := f.server.fleet.SetToken("sync-node", token); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(syncBatch("sync-node", "generation-1", 1, source("/var/log/mail.log", 4, 3, 1)))
	request, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/node/stream-telemetry", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("versioned ingest returned %d", response.StatusCode)
	}
	response, data := f.request(http.MethodGet, "/api/v1/sync/snapshot", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("snapshot returned %d: %s", response.StatusCode, data)
	}
	var snapshot struct {
		SchemaVersion string                     `json:"schema_version"`
		Aggregate     telemetry.SyncAggregate    `json:"aggregate"`
		Subsystems    map[string]json.RawMessage `json:"subsystems"`
		Controller    struct {
			Health             string  `json:"health"`
			Version            string  `json:"version"`
			UptimeSeconds      float64 `json:"uptime_seconds"`
			ProcessMemoryBytes uint64  `json:"process_memory_alloc_bytes"`
			CPUStatus          string  `json:"cpu_percent_status"`
		} `json:"controller"`
	}
	if json.Unmarshal(data, &snapshot) != nil || snapshot.SchemaVersion != telemetry.SyncSchemaVersion || snapshot.Aggregate.SourceRecordsReadTotal != 4 || len(snapshot.Subsystems) != 8 || snapshot.Controller.Health != "ok" || snapshot.Controller.Version == "" || snapshot.Controller.UptimeSeconds < 0 || snapshot.Controller.CPUStatus != telemetry.FreshnessNotInstrumented {
		t.Fatalf("canonical snapshot mismatch: %s", data)
	}
	for _, name := range []string{"nhle", "napi", "nal", "nursf", "delivery", "replay", "release_readiness", "upes"} {
		var subsystem map[string]any
		if err := json.Unmarshal(snapshot.Subsystems[name], &subsystem); err != nil {
			t.Fatalf("decode %s subsystem: %v", name, err)
		}
		if subsystem["status"] == nil || (name != "upes" && subsystem["updated_at"] == nil) {
			t.Fatalf("%s subsystem lacks status or fresh timestamp: %s", name, snapshot.Subsystems[name])
		}
	}
	var nhle map[string]any
	if err := json.Unmarshal(snapshot.Subsystems["nhle"], &nhle); err != nil || nhle["active_nodes"] != float64(1) || nhle["source_count"] != float64(1) {
		t.Fatalf("NHLE active node/source counts missing: %s", snapshot.Subsystems["nhle"])
	}
	unauthorized, err := http.Get(f.http.URL + "/api/v1/sync/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated snapshot status %d", unauthorized.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, f.http.URL+"/api/v1/sync/stream", nil)
	stream, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	var event, payload string
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			payload = strings.TrimPrefix(line, "data: ")
		}
		if line == "" && event != "" {
			break
		}
	}
	if event != "sync_snapshot" || !strings.Contains(payload, `"subsystems"`) || !strings.Contains(payload, `"diagnostics"`) {
		t.Fatalf("initial stream event mismatch: event=%q payload=%s", event, payload)
	}
	// A quiet client still receives an in-memory canonical heartbeat at the
	// one-second target. This advances transport/source freshness without
	// querying remote subsystem metadata once per subscriber and second.
	deadline := time.After(1500 * time.Millisecond)
	event = ""
	for {
		select {
		case <-deadline:
			t.Fatal("canonical SSE did not publish a fresh snapshot at the target interval")
		default:
		}
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if line == "" && event != "" {
			break
		}
	}
	if event != "sync_heartbeat" {
		t.Fatalf("quiet stream event = %q, want sync_heartbeat", event)
	}
}

func TestCanonicalSyncSnapshotCarriesHeartbeatUPESMetrics(t *testing.T) {
	f := newAPIFixture(t)
	f.server.fleet.Register(&fleet.Node{ID: "upes-client", Hostname: "upes-client"})
	f.server.fleet.Register(&fleet.Node{ID: "legacy-client", Hostname: "legacy-client"})
	const token = "upes-client-token"
	if err := f.server.fleet.SetToken("upes-client", token); err != nil {
		t.Fatal(err)
	}
	requestBody, err := json.Marshal(protocol.HeartbeatRequest{
		NodeID: "upes-client", Version: "20.0.0", Health: "online",
		Metrics: protocol.Metrics{UPESMetricsAvailable: true, UPESUniqueFailureSignatures: 3,
			UPESObservedFailures: 9, UPESDiscoveryQueued: 4, UPESDiscoveryDropped: 1, UPESDiscoveryPending: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/node/heartbeat", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat returned %d", response.StatusCode)
	}
	response, data := f.request(http.MethodGet, "/api/v1/sync/snapshot", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("canonical snapshot returned %d: %s", response.StatusCode, data)
	}
	var snapshot struct {
		Subsystems map[string]json.RawMessage `json:"subsystems"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	var upes map[string]any
	if err := json.Unmarshal(snapshot.Subsystems["upes"], &upes); err != nil {
		t.Fatalf("decode canonical UPES state: %v; payload=%s", err, data)
	}
	if upes["status"] != "partial" || upes["clients_reporting"] != float64(1) || upes["unique_failure_signatures"] != float64(3) || upes["observed_failures"] != float64(9) || upes["discovery_queued"] != float64(4) || upes["discovery_dropped"] != float64(1) || upes["discovery_pending"] != float64(2) {
		t.Fatalf("canonical UPES telemetry missing or fabricated: %+v", upes)
	}
	clients, ok := upes["clients"].([]any)
	if !ok || len(clients) != 2 || clients[0].(map[string]any)["status"] == clients[1].(map[string]any)["status"] {
		t.Fatalf("reporting and unsupported clients were not distinguished: %+v", upes["clients"])
	}
}
