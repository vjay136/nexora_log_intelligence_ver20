package output_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"nexora.local/nli-v20/internal/output"
)

const eventPayload = `{"level":"info","service":"app","timestamp":"2026-10-03T09:31:00Z"}`

func TestFileWriterRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "normalized.jsonl")
	writer, err := output.NewFileWriter(output.Config{Type: "file", Path: path, MaxMB: 1, MaxBackups: 2})
	if err != nil {
		t.Fatal(err)
	}
	large := []byte(strings.Repeat("x", 700_000))
	if err := writer.Write(large, "normal"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(large, "normal"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
}

func TestFileWriterFlushMakesBufferedRecordDurable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "normalized.jsonl")
	writer, err := output.NewFileWriter(output.Config{Type: "file", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Write([]byte(eventPayload), "normal"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != eventPayload+"\n" {
		t.Fatalf("durable JSONL mismatch: %q", data)
	}
}

func TestFileWriterDoesNotCreateUnusedDestinationOnInitOrFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "imap_normalised.jsonl")
	writer, err := output.NewFileWriter(output.Config{Type: "file", Path: path, Routes: []string{"normal"}, Families: []string{"imap"}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unused output destination exists after init/flush, stat err=%v", err)
	}
	if err := writer.Write([]byte(eventPayload), "quarantine"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("route-filtered record created output destination, stat err=%v", err)
	}
	if err := writer.Write([]byte(eventPayload), "normal"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != eventPayload+"\n" {
		t.Fatalf("first matching event was not persisted: data=%q err=%v", data, err)
	}
}

func TestLokiWriterSpoolsOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	spool := filepath.Join(t.TempDir(), "loki-spool.jsonl")
	writer, err := output.NewLokiWriter(output.Config{
		Type: "loki", URL: server.URL, BatchSize: 1, RetryMax: 1,
		RetryBackoffMS: 1, FallbackPath: spool,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeErr := writer.Write([]byte(eventPayload), "normal")
	var deliveryErr *output.DeliveryError
	if !errors.As(writeErr, &deliveryErr) || !deliveryErr.Accounted {
		t.Fatalf("expected an accounted delivery failure, got %v", writeErr)
	}
	_ = writer.Close()
	data, err := os.ReadFile(spool)
	if err != nil {
		t.Fatalf("delivery receipt missing: %v", err)
	}
	var receipt map[string]interface{}
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatalf("invalid delivery receipt: %v", err)
	}
	if receipt["status"] != "delivery_failed" || len(receipt["event_sha256"].(string)) != 64 {
		t.Fatalf("incomplete delivery receipt: %s", data)
	}
	if strings.Contains(string(data), "level") || strings.Contains(string(data), "service") {
		t.Fatalf("fallback retained EventPayload content: %s", data)
	}
}

func TestClickHouseWriterCreatesAndInserts(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	var insertBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		queries = append(queries, r.URL.Query().Get("query"))
		if strings.HasPrefix(r.URL.Query().Get("query"), "INSERT") {
			insertBody = string(body)
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	writer, err := output.NewClickHouseWriter(output.Config{
		Type: "clickhouse", URL: server.URL, Database: "default", Table: "nli_logs",
		BatchSize: 1, Required: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Write([]byte(eventPayload), "normal"); err != nil {
		t.Fatal(err)
	}
	writer.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 5 || !strings.HasPrefix(queries[0], "CREATE TABLE") ||
		!strings.Contains(queries[1], "ADD COLUMN IF NOT EXISTS `level`") ||
		!strings.Contains(queries[2], "ADD COLUMN IF NOT EXISTS `service`") ||
		!strings.Contains(queries[3], "ADD COLUMN IF NOT EXISTS `timestamp`") ||
		!strings.HasPrefix(queries[4], "INSERT") {
		t.Fatalf("unexpected queries: %#v", queries)
	}
	if strings.Contains(queries[0], " raw String") || strings.Contains(queries[0], " data String") ||
		!strings.Contains(queries[0], "event_json String") || !strings.Contains(queries[0], "event_sha256 FixedString(64)") {
		t.Fatalf("unsafe ClickHouse schema: %s", queries[0])
	}
	if !strings.Contains(insertBody, `"route":"normal"`) || !strings.Contains(insertBody, `"received_at"`) ||
		!strings.Contains(insertBody, `"event_json"`) || !strings.Contains(insertBody, `"event_sha256"`) ||
		!strings.Contains(insertBody, `"level":"info"`) || !strings.Contains(insertBody, `"service":"app"`) {
		t.Fatalf("unexpected insert body: %s", insertBody)
	}
}

func TestClickHouseWriterMapsCustomerFieldsToSafeColumns(t *testing.T) {
	var queries []string
	var insertBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		query := r.URL.Query().Get("query")
		queries = append(queries, query)
		if strings.HasPrefix(query, "INSERT") {
			insertBody = string(body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	writer, err := output.NewClickHouseWriter(output.Config{Type: "clickhouse", URL: server.URL, Database: "default", Table: "nli_logs", BatchSize: 1, FieldColumns: map[string]string{"source_ip": "client_ip"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Write([]byte(`{"source_ip":"2001:db8::1","status":207}`), "normal"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(queries, "\n")
	if !strings.Contains(joined, "`client_ip` Nullable(String)") || strings.Contains(joined, "`status` Nullable(String)") {
		t.Fatalf("customer columns were not created: %s", joined)
	}
	if !strings.Contains(insertBody, `"client_ip":"2001:db8::1"`) || strings.Contains(insertBody, `"status":"207"`) || !strings.Contains(insertBody, `"event_json":"{\"source_ip\":\"2001:db8::1\",\"status\":207}"`) {
		t.Fatalf("mapped values or lossless event_json missing: %s", insertBody)
	}
}

func TestOptionalRemoteOutputRequiresFallback(t *testing.T) {
	_, err := output.NewRouter([]output.Config{{Type: "loki", URL: "http://127.0.0.1:1"}})
	if err == nil || !strings.Contains(err.Error(), "fallback_path") {
		t.Fatalf("expected fallback validation, got %v", err)
	}
}

func TestRemoteWritersNeverDeliverQuarantineOrUnknownRoutes(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	loki, err := output.NewLokiWriter(output.Config{Type: "loki", URL: server.URL, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"quarantine", "unknown", "partial", "error"} {
		if err := loki.Write([]byte(eventPayload), route); err != nil {
			t.Fatalf("Loki rejected route %s: %v", route, err)
		}
	}
	if requests != 0 {
		t.Fatalf("Loki sent %d forbidden requests", requests)
	}
	for _, route := range []string{"normal", "warning"} {
		if err := loki.Write([]byte(eventPayload), route); err != nil {
			t.Fatal(err)
		}
	}
	if err := loki.Close(); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("Loki sent %d requests, want 2 parsed events", requests)
	}

	requests = 0
	clickhouse, err := output.NewClickHouseWriter(output.Config{Type: "clickhouse", URL: server.URL, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"quarantine", "unknown", "partial", "error"} {
		if err := clickhouse.Write([]byte(eventPayload), route); err != nil {
			t.Fatalf("ClickHouse rejected route %s: %v", route, err)
		}
	}
	if requests != 0 {
		t.Fatalf("ClickHouse sent %d forbidden requests", requests)
	}
	if err := clickhouse.Write([]byte(eventPayload), "normal"); err != nil {
		t.Fatal(err)
	}
	if err := clickhouse.Close(); err != nil {
		t.Fatal(err)
	}
	if requests < 2 {
		t.Fatalf("ClickHouse did not insert parsed event: %d requests", requests)
	}

	requests = 0
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	controller, err := output.NewControllerWriter(output.Config{Type: "controller", URL: server.URL, TokenFile: tokenPath, NodeID: "test-node", BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"quarantine", "unknown", "partial", "error"} {
		if err := controller.Write([]byte(eventPayload), route); err != nil {
			t.Fatalf("controller rejected route %s: %v", route, err)
		}
	}
	if requests != 0 {
		t.Fatalf("controller sent %d forbidden requests", requests)
	}
	if err := controller.Write([]byte(eventPayload), "warning"); err != nil {
		t.Fatal(err)
	}
	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("controller sent %d requests, want one parsed event", requests)
	}
}

func TestRouterWritesOnlyMatchingFamily(t *testing.T) {
	dir := t.TempDir()
	imapPath := filepath.Join(dir, "imap.jsonl")
	popPath := filepath.Join(dir, "pop.jsonl")
	router, err := output.NewRouter([]output.Config{
		{Type: "file", Path: imapPath, Routes: []string{"normal"}, Families: []string{"imap"}},
		{Type: "file", Path: popPath, Routes: []string{"normal"}, Families: []string{"pop"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := router.WriteForFamily([]byte(eventPayload), "normal", "imap"); err != nil {
		t.Fatal(err)
	}
	if err := router.Close(); err != nil {
		t.Fatal(err)
	}
	imapData, err := os.ReadFile(imapPath)
	if err != nil || !strings.Contains(string(imapData), `"service":"app"`) {
		t.Fatalf("imap output missing routed payload: %q, %v", imapData, err)
	}
	if popData, err := os.ReadFile(popPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unused pop destination must stay absent, data=%q err=%v", popData, err)
	}
}

func TestSpoolIsJSONLines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	spool := filepath.Join(t.TempDir(), "spool.jsonl")
	writer, _ := output.NewLokiWriter(output.Config{Type: "loki", URL: server.URL, BatchSize: 1, RetryMax: 1, RetryBackoffMS: 1, FallbackPath: spool})
	_ = writer.Write([]byte(eventPayload), "normal")
	_ = writer.Close()
	file, err := os.Open(spool)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "{") {
		t.Fatal("expected one JSONL delivery receipt")
	}
	line := scanner.Text()
	if !strings.Contains(line, `"event_sha256"`) || strings.Contains(line, `"level"`) || strings.Contains(line, `"service"`) {
		t.Fatalf("unsafe fallback accounting record: %s", line)
	}
}
