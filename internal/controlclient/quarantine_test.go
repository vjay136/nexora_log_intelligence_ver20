package controlclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/quarantine"
)

func TestRawQuarantineUploadIsAuthenticatedAndBoundedToWaitingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quarantine.jsonl")
	waiting := quarantine.New("node-a", "imap", "/var/log/mail.log", 2, "exact raw line", "rev-1", 1, []string{"no_match"}, time.Now())
	resolved := quarantine.New("node-a", "imap", "/var/log/mail.log", 1, "already resolved", "rev-1", 2, nil, time.Now())
	resolved.Status = "resolved"
	if err := quarantine.Append(path, waiting); err != nil {
		t.Fatal(err)
	}
	if err := quarantine.Append(path, resolved); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/node/quarantine/records" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer enrolled-node-secret" {
			t.Errorf("raw upload did not use enrolled node auth")
		}
		var body struct {
			Records     []json.RawMessage `json:"records"`
			RawIncluded bool              `json:"raw_included"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if !body.RawIncluded || len(body.Records) != 1 {
			t.Errorf("upload bounds/status filter failed: raw=%v records=%d", body.RawIncluded, len(body.Records))
		}
		var record quarantine.Record
		if err := json.Unmarshal(body.Records[0], &record); err != nil || record.Raw != "exact raw line" {
			t.Errorf("raw payload mismatch: record=%+v err=%v", record, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	c := &Client{
		cfg:  Config{ControllerURL: server.URL, QuarantinePath: path, QuarantineRawUpload: true, QuarantineMaxRecords: 1, QuarantineMaxBytes: 4096},
		http: server.Client(), token: "enrolled-node-secret",
	}
	if err := c.uploadQuarantineRecords(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultQueueSyncOmitsRawLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quarantine.jsonl")
	record := quarantine.New("node-a", "imap", "/var/log/mail.log", 2, "private exact raw line", "rev-1", 1, []string{"no_match"}, time.Now())
	if err := quarantine.Append(path, record); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer enrolled-node-secret" {
			t.Errorf("queue metadata upload did not use enrolled node auth")
		}
		var body struct {
			Records     []map[string]json.RawMessage `json:"records"`
			RawIncluded bool                         `json:"raw_included"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if body.RawIncluded || len(body.Records) != 1 {
			t.Errorf("unexpected raw upload setting or record count: %+v", body)
		}
		if _, exists := body.Records[0]["raw"]; exists {
			t.Error("raw line was sent while raw upload was disabled")
		}
		if string(body.Records[0]["id"]) != `"`+record.ID+`"` || string(body.Records[0]["status"]) != `"waiting"` {
			t.Errorf("queue status metadata missing: %s", body.Records[0])
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	c := &Client{
		cfg:  Config{ControllerURL: server.URL, QuarantinePath: path, QuarantineMaxRecords: 10, QuarantineMaxBytes: 4096},
		http: server.Client(), token: "enrolled-node-secret",
	}
	if err := c.uploadQuarantineRecords(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureNLIGroupReadableRepairsLegacyMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce Unix group mode bits; Rocky lifecycle tests cover 0640")
	}
	path := filepath.Join(t.TempDir(), "install.id")
	if err := os.WriteFile(path, []byte("node-id\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureNLIGroupReadable(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("mode=%#o, want 0640", info.Mode().Perm())
	}
}
