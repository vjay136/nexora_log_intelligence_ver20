package controlplane

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"nexora.local/nli-v20/internal/fleet"
	metastore "nexora.local/nli-v20/internal/store"
	"nexora.local/nli-v20/telemetry"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func enableTestQuarantineRetention(t *testing.T, f *apiFixture) {
	t.Helper()
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	f.server.quarantine.enabled = true
	f.server.quarantine.aead = aead
}

func TestCanonicalReplayAndReleaseReadinessUsePersistedEvidence(t *testing.T) {
	f := newAPIFixture(t)
	firstSeen := time.Now().UTC().Add(-75 * time.Second)
	if err := f.server.storeQuarantineRecord(context.Background(), quarantineRecordInput{ID: "q-metric", NodeID: "node-1", Status: "waiting", FirstSeen: firstSeen, AttemptCount: 2}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: "fleet-sync", ID: safeID("node-1"), Payload: []byte(`{"desired_release_hash":"release-b","active_release_hash":"release-a"}`)}); err != nil {
		t.Fatal(err)
	}
	snapshot := telemetry.SyncSnapshot{GeneratedAt: time.Now().UTC()}
	subsystems := f.server.subsystemSyncSnapshot(context.Background(), snapshot)
	replay := subsystems["replay"].(map[string]any)
	if replay["queued"] != 1 || replay["attempts_total"] != 2 || replay["state"] != "queued" || replay["oldest_queued_age_seconds"].(float64) < 70 {
		t.Fatalf("replay metrics not derived from queue record: %#v", replay)
	}
	release := subsystems["release_readiness"].(map[string]any)
	if release["managed_nodes"] != 1 || release["release_drifted_nodes"] != 1 || release["state"] != "drifted" || release["status"] != "degraded" {
		t.Fatalf("release drift evidence not reflected: %#v", release)
	}
}

func TestQuarantineRawIsEncryptedInDedicatedLocalStoreAndDownloadAudited(t *testing.T) {
	f := newAPIFixture(t)
	enableTestQuarantineRetention(t, f)
	raw := `mail.example: rejected user@example.test`
	digest := sha256.Sum256([]byte(raw))
	input := quarantineRecordInput{ID: "q-record-1", NodeID: "node-1", Service: "mta", EventHash: hex.EncodeToString(digest[:]), Raw: raw, FirstSeen: time.Now().Add(-time.Minute), Status: "waiting", SourcePath: "/var/log/maillog", SourceLine: 42}
	if err := f.server.storeQuarantineRecord(context.Background(), input, true); err != nil {
		t.Fatal(err)
	}
	meta, _, err := f.server.getQuarantineRecord(context.Background(), input.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.RawAvailable || meta.SourcePath != input.SourcePath || meta.SourceLine != input.SourceLine {
		t.Fatalf("metadata lost raw/source details: %+v", meta)
	}
	sealedDoc, err := f.server.quarantine.store.Get(context.Background(), quarantineRawCollection, input.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealedDoc.Payload), raw) {
		t.Fatal("raw log line appears in plaintext in the local metastore")
	}
	if _, err := f.server.metadata.Get(context.Background(), quarantineRecordsCollection, input.ID, 0); err == nil {
		t.Fatal("quarantine metadata was written to the configurable primary store")
	}
	response, data := f.request(http.MethodGet, "/api/v1/quarantine/records/"+input.ID+"/raw", nil)
	if response.StatusCode != http.StatusOK || string(data) != raw {
		t.Fatalf("raw download status=%d body=%q", response.StatusCode, data)
	}
}

func TestQuarantineAnalyzeUsesExactRetainedRawAndDoesNotResolveOrPublish(t *testing.T) {
	f := newAPIFixture(t)
	enableTestQuarantineRetention(t, f)
	raw := "status=UNKNOWN\t\r\n"
	if err := f.server.storeQuarantineRecord(context.Background(), quarantineRecordInput{ID: "q-analyze", NodeID: "node-1", Service: "mail", Raw: raw, Status: "waiting"}, true); err != nil {
		t.Fatal(err)
	}
	response, data := f.request(http.MethodPost, "/api/v1/quarantine/records/q-analyze/analyze", map[string]any{"desired": map[string]any{"status": "UNKNOWN"}})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("analyze %d: %s", response.StatusCode, data)
	}
	body := decodeMap(t, data)
	if body["parser_changed"] != false || body["review_state"] != "analysis_only" {
		t.Fatalf("analysis unexpectedly changed parser state: %s", data)
	}
	result := body["result"].(map[string]any)
	if result["raw"] != "status=UNKNOWN" {
		t.Fatalf("analysis did not normalize exact retained sample correctly: %#v", result["raw"])
	}
	record, _, err := f.server.getQuarantineRecord(context.Background(), "q-analyze")
	if err != nil || record.Status != "waiting" {
		t.Fatalf("analysis changed quarantine status: record=%+v err=%v", record, err)
	}
	historyResponse, historyData := f.request(http.MethodGet, "/api/v1/quarantine/history", nil)
	if historyResponse.StatusCode != http.StatusOK || !strings.Contains(string(historyData), "failure_analyzed") {
		t.Fatalf("analysis history missing: %d %s", historyResponse.StatusCode, historyData)
	}
}

func TestQuarantineDispositionAndRawPurgeAreAuditedAndReplaySafe(t *testing.T) {
	f := newAPIFixture(t)
	enableTestQuarantineRetention(t, f)
	f.server.fleet.Register(&fleet.Node{ID: "node-1", Hostname: "node-1"})
	if err := f.server.storeQuarantineRecord(context.Background(), quarantineRecordInput{ID: "q-disposition", NodeID: "node-1", Service: "mail", Raw: "secret-bearing source line", Status: "waiting"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.auth.CreateUser("admin", "quarantine-viewer", "q-viewer@example.test", "viewer", "Viewer-password-12345!"); err != nil {
		t.Fatal(err)
	}
	f.login("quarantine-viewer", "Viewer-password-12345!")
	f.refreshCSRF()
	response, data := f.request(http.MethodPost, "/api/v1/quarantine/records/q-disposition/exclude", map[string]any{"reason": "not authorized"})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer exclusion status=%d: %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodDelete, "/api/v1/quarantine/records/q-disposition/raw", map[string]any{})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer raw deletion status=%d: %s", response.StatusCode, data)
	}
	f.login("admin", "Admin-password-12345!")
	f.refreshCSRF()
	response, data = f.request(http.MethodPost, "/api/v1/quarantine/records/q-disposition/exclude", map[string]any{"reason": "confirmed duplicate source"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("exclude %d: %s", response.StatusCode, data)
	}
	got, _, err := f.server.getQuarantineRecord(context.Background(), "q-disposition")
	if err != nil || got.Status != "excluded" || got.ExcludeReason != "confirmed duplicate source" || !got.RawAvailable {
		t.Fatalf("exclude should preserve metadata and evidence: record=%+v err=%v", got, err)
	}
	response, data = f.request(http.MethodPost, "/api/v1/quarantine/reprocess", map[string]any{"record_ids": []string{"q-disposition"}, "node_id": "node-1"})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("excluded record replay status=%d: %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodPost, "/api/v1/quarantine/records/q-disposition/include", map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("include %d: %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodDelete, "/api/v1/quarantine/records/q-disposition/raw", map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete raw %d: %s", response.StatusCode, data)
	}
	got, _, err = f.server.getQuarantineRecord(context.Background(), "q-disposition")
	if err != nil || got.RawAvailable {
		t.Fatalf("raw evidence should be deleted while metadata remains: record=%+v err=%v", got, err)
	}
	if _, err := f.server.quarantine.store.Get(context.Background(), quarantineRawCollection, "q-disposition", 0); !os.IsNotExist(err) {
		t.Fatalf("raw evidence remains after delete: %v", err)
	}
	response, data = f.request(http.MethodGet, "/api/v1/quarantine/history?limit=10", nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(data), "record_excluded") || !strings.Contains(string(data), "record_included") || !strings.Contains(string(data), "raw_evidence_deleted") {
		t.Fatalf("mutation history incomplete: %d %s", response.StatusCode, data)
	}
}

func TestQuarantineRetentionDisabledKeepsMetadataButNeverRaw(t *testing.T) {
	f := newAPIFixture(t)
	raw := "exact line must stay client-local"
	if err := f.server.storeQuarantineRecord(context.Background(), quarantineRecordInput{ID: "q-no-raw", NodeID: "node-1", Raw: raw, Status: "waiting"}, true); err != nil {
		t.Fatal(err)
	}
	meta, _, err := f.server.getQuarantineRecord(context.Background(), "q-no-raw")
	if err != nil {
		t.Fatal(err)
	}
	if meta.RawAvailable {
		t.Fatal("raw was marked available while retention is disabled")
	}
	if _, err := f.server.quarantine.store.Get(context.Background(), quarantineRawCollection, "q-no-raw", 0); !os.IsNotExist(err) {
		t.Fatalf("raw should not be stored, get error=%v", err)
	}
}

func TestQuarantineReplayResultUpdatesRecordsPerAgentConfirmation(t *testing.T) {
	f := newAPIFixture(t)
	for _, item := range []struct{ id, revision string }{{"q-resolved", "old-1"}, {"q-still-waiting", "old-2"}, {"q-omitted", "old-3"}, {"q-telemetry", "old-4"}, {"q-missing", "old-5"}} {
		if err := f.server.storeQuarantineRecord(context.Background(), quarantineRecordInput{ID: item.id, NodeID: "node-1", Status: "reprocessing", ParserRevision: item.revision}, false); err != nil {
			t.Fatal(err)
		}
	}
	rollout, err := f.server.rollouts.Create(rolloutTypeReplay, "test replay", "admin", []string{"node-1"}, map[string]any{"node_id": "node-1", "record_ids": []string{"q-resolved", "q-still-waiting", "q-omitted", "q-telemetry", "q-missing"}})
	if err != nil {
		t.Fatal(err)
	}
	f.server.recordQuarantineReplayResult(context.Background(), rollout.ID, "node-1", rolloutStatusCompleted, `{"replayed":4,"resolved":1,"waiting":1,"resolved_ids":["q-resolved"],"waiting_ids":["q-still-waiting"],"telemetry_failed_ids":["q-telemetry"],"missing_ids":["q-missing"],"parser_revisions":{"q-resolved":"pack-7"}}`)
	statuses := map[string]string{"q-resolved": "resolved", "q-still-waiting": "waiting", "q-omitted": "waiting", "q-telemetry": "waiting", "q-missing": "waiting"}
	revisions := map[string]string{"q-resolved": "pack-7", "q-still-waiting": "old-2", "q-omitted": "old-3", "q-telemetry": "old-4", "q-missing": "old-5"}
	for id, want := range statuses {
		got, _, err := f.server.getQuarantineRecord(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want {
			t.Errorf("record %s status=%q, want %q", id, got.Status, want)
		}
		if got.ParserRevision != revisions[id] {
			t.Errorf("record %s parser revision=%q, want %q", id, got.ParserRevision, revisions[id])
		}
	}
}

func TestQuarantineRetryBackoffAndDeadLetterLimit(t *testing.T) {
	if got := quarantineRetryBackoff(1); got != time.Minute {
		t.Fatalf("first replay backoff=%s, want 1m", got)
	}
	if got := quarantineRetryBackoff(2); got != time.Minute {
		t.Fatalf("second parse attempt backoff=%s, want 1m", got)
	}
	if got := quarantineRetryBackoff(3); got != 5*time.Minute {
		t.Fatalf("third parse attempt backoff=%s, want 5m", got)
	}
	if got := quarantineRetryBackoff(4); got != 15*time.Minute {
		t.Fatalf("fourth parse attempt backoff=%s, want 15m", got)
	}
	if got := quarantineRetryBackoff(5); got != time.Hour {
		t.Fatalf("fifth parse attempt backoff=%s, want capped 1h", got)
	}

	f := newAPIFixture(t)
	if err := f.server.storeQuarantineRecord(context.Background(), quarantineRecordInput{ID: "q-dead-letter", NodeID: "node-1", Status: "reprocessing", AttemptCount: 5}, false); err != nil {
		t.Fatal(err)
	}
	rollout, err := f.server.rollouts.Create(rolloutTypeReplay, "test final replay", "admin", []string{"node-1"}, map[string]any{"node_id": "node-1", "record_ids": []string{"q-dead-letter"}})
	if err != nil {
		t.Fatal(err)
	}
	f.server.recordQuarantineReplayResult(context.Background(), rollout.ID, "node-1", rolloutStatusCompleted, `{"waiting_ids":["q-dead-letter"]}`)
	got, _, err := f.server.getQuarantineRecord(context.Background(), "q-dead-letter")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "dead_letter" || !got.RetryAfter.IsZero() {
		t.Fatalf("retry limit should dead-letter without another retry time: %+v", got)
	}
}

func TestQuarantineLocalMetadataSurvivesServerRestart(t *testing.T) {
	state := t.TempDir()
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{StateDir: state, WebDir: web, EnrollmentToken: "Enrollment-token-123456789012", BootstrapAdminPassword: "Admin-password-12345!", AuditKey: make([]byte, 32), CSRFKey: make([]byte, 32)}
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.storeQuarantineRecord(context.Background(), quarantineRecordInput{ID: "q-persist", NodeID: "node-1", Status: "waiting"}, false); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	record, _, err := second.getQuarantineRecord(context.Background(), "q-persist")
	if err != nil || record.Status != "waiting" {
		t.Fatalf("local quarantine metadata did not survive restart: record=%+v error=%v", record, err)
	}
	// Ensure persisted payload remains well-formed JSON for bounded local storage.
	encoded, err := json.Marshal(record)
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("invalid quarantine record JSON: %s err=%v", encoded, err)
	}
}
