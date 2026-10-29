package output_test

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"nexora.local/nli-v20/internal/output"
)

func TestLokiAuthenticationTenantAndAcknowledgedFlush(t *testing.T) {
	t.Setenv("TEST_LOKI_TOKEN", "test-token")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("X-Scope-OrgID") != "tenant-a" || r.URL.Path != "/loki/api/v1/push" {
			t.Errorf("incorrect authenticated push: %s %v", r.URL.Path, r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	router, err := output.NewRouter([]output.Config{{Type: "loki", URL: server.URL, Required: true, BatchSize: 100, BearerTokenEnv: "TEST_LOKI_TOKEN", TenantID: "tenant-a"}})
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()
	if err := router.Write([]byte(eventPayload), "normal"); err != nil {
		t.Fatal(err)
	}
	if err := router.Flush(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected acknowledged push, got %d", calls.Load())
	}
}

func TestClickHouseRetriesTransientFailure(t *testing.T) {
	var inserts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Query().Get("query"), "INSERT") && inserts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	w, err := output.NewClickHouseWriter(output.Config{Type: "clickhouse", URL: server.URL, BatchSize: 1, RetryMax: 1, RetryBackoffMS: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Write([]byte(eventPayload), "normal"); err != nil {
		t.Fatal(err)
	}
	if inserts.Load() != 2 {
		t.Fatalf("expected retry, got %d", inserts.Load())
	}
}

func TestRemoteConfigurationRejectsInvalidURLAndMissingCredential(t *testing.T) {
	for _, cfg := range []output.Config{
		{Type: "loki", URL: "file:///tmp/output"},
		{Type: "loki", URL: "http://user:pass@example.com"},
		{Type: "loki", URL: "https://example.com", BearerTokenEnv: "NLI_TEST_MISSING_CREDENTIAL"},
		{Type: "loki", URL: "https://example.com", RetryMax: 100},
	} {
		if w, err := output.NewLokiWriter(cfg); err == nil {
			w.Close()
			t.Fatalf("accepted invalid config %+v", cfg)
		}
	}
}

func TestTelemetryWriterUsesEnrolledIdentityAndPrivateCA(t *testing.T) {
	root := t.TempDir()
	caPath := filepath.Join(root, "controller-ca.pem")
	nodePath, tokenPath := filepath.Join(root, "install.id"), filepath.Join(root, "node.token")
	if err := os.WriteFile(nodePath, []byte("enrolled-node\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("enrolled-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/telemetry" || r.Header.Get("Authorization") != "Bearer enrolled-token" {
			t.Errorf("unexpected telemetry request: path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["node_id"] != "enrolled-node" {
			t.Errorf("telemetry used untrusted parser node identity: %#v", body["node_id"])
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caPath, pemCert, 0600); err != nil {
		t.Fatal(err)
	}

	writer, err := output.NewTelemetryWriter(output.Config{
		Type: "telemetry", URL: server.URL, Routes: []string{"telemetry"},
		NodeIDFile: nodePath, TokenFile: tokenPath, CAFile: caPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Write([]byte(`{"node_id":"source-host","event_id":"evt","parser_id":"p"}`), "normal"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write([]byte(`{"node_id":"source-host","event_id":"evt","parser_id":"p"}`), "telemetry"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one telemetry-only request, got %d", calls.Load())
	}
}
