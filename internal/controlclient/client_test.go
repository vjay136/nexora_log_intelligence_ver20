package controlclient

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/protocol"
	"nexora.local/nli-v20/internal/rollout"
	"nexora.local/nli-v20/parser-policy"
	"nexora.local/nli-v20/telemetry"
)

type fakeExecutor struct {
	mu           sync.Mutex
	restarts     int
	restartError error
	replayPath   string
	replayIDs    []string
}

func (executor *fakeExecutor) RestartAgent(context.Context) error {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.restarts++
	return executor.restartError
}
func (executor *fakeExecutor) Replay(_ context.Context, path string, ids []string) (string, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.replayPath = path
	executor.replayIDs = append([]string(nil), ids...)
	return `{"resolved_ids":["q-123"],"waiting_ids":[]}`, nil
}
func (*fakeExecutor) UpgradePackage(context.Context, string) error { return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestRunRetriesEnrollmentUntilControllerReady(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NLI_ENROLLMENT_TOKEN", "retry-enrollment-token-123456")
	client, err := New(Config{
		ControllerURL: "http://127.0.0.1:12382", AllowInsecureHTTP: true,
		NodeTokenFile: filepath.Join(root, "node.token"), InstallIDFile: filepath.Join(root, "install.id"),
		JournalFile: filepath.Join(root, "journal.json"), AgentConfigPath: filepath.Join(root, "nli.json"),
		ParserDir: filepath.Join(root, "parsers"), PackageSpoolDir: filepath.Join(root, "packages"),
		MetricsURL: "http://127.0.0.1:1/metrics", PollSeconds: 5,
	}, &fakeExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	client.pollInterval = 10 * time.Millisecond

	var attempts atomic.Int32
	cycleComplete := make(chan struct{})
	var completed sync.Once
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempt := attempts.Add(1)
		if attempt == 1 {
			return nil, errors.New("controller is starting")
		}
		status := http.StatusOK
		body := "{}"
		switch request.URL.Path {
		case "/api/v1/node/register":
			body = `{"node_token":"retry-node-token-12345678901234567890"}`
		case "/api/v1/node/heartbeat":
		case "/api/v1/node/commands/next":
			status = http.StatusNoContent
			body = ""
			completed.Do(func() { close(cycleComplete) })
		default:
			return nil, errors.New("unexpected request path: " + request.URL.Path)
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	select {
	case <-cycleComplete:
	case <-ctx.Done():
		t.Fatal("client did not retry enrollment before the test deadline")
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("long-running client returned a startup error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not stop after cancellation")
	}
	if attempts.Load() < 4 {
		t.Fatalf("HTTP attempts = %d, want initial failure plus enrollment cycle", attempts.Load())
	}
	if token, readErr := os.ReadFile(filepath.Join(root, "node.token")); readErr != nil || strings.TrimSpace(string(token)) == "" {
		t.Fatalf("successful retry did not persist a node token: token=%q err=%v", token, readErr)
	}
}

func TestTelemetryRetryBackoffIsBoundedAndJittered(t *testing.T) {
	for failures := 1; failures <= 12; failures++ {
		delay := telemetryRetryDelay(time.Second, failures)
		if delay < time.Second || delay >= 31*time.Second {
			t.Fatalf("failure %d delay = %s, want 1s..31s", failures, delay)
		}
	}
	if got := telemetryRetryDelay(time.Second, 0); got != time.Second {
		t.Fatalf("healthy publication delay = %s, want 1s", got)
	}
}

func TestControllerTransportPoolsBoundedConnections(t *testing.T) {
	client, err := controllerHTTPClient(Config{ControllerURL: "https://controller.internal", CAFile: ""})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.MaxIdleConnsPerHost < 8 || transport.MaxConnsPerHost != 16 || transport.IdleConnTimeout < time.Minute {
		t.Fatalf("controller transport is not fleet-bounded: %+v", transport)
	}
}

func TestSendStreamTelemetryPublishesFreshFullSnapshotsIncludingEmpty(t *testing.T) {
	root := t.TempDir()
	files := []telemetry.StreamFileTelemetry{{
		Source: "/var/log/input.log", Phase: "eof_wait", ReadLinesPerSecond: 12.5,
		NormalizedPerSecond: 10, QuarantinedPerSecond: 2.5, ReadLines: 120,
		Normalized: 100, Quarantined: 20, UpdatedAt: time.Now().UTC(),
	}}
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
	}))
	defer metrics.Close()
	client, err := New(Config{
		ControllerURL: "http://127.0.0.1:12345", AllowInsecureHTTP: true,
		NodeTokenFile: filepath.Join(root, "node.token"), InstallIDFile: filepath.Join(root, "install.id"),
		JournalFile: filepath.Join(root, "journal.json"), AgentConfigPath: filepath.Join(root, "nli.json"),
		ParserDir: filepath.Join(root, "parsers"), PackageSpoolDir: filepath.Join(root, "packages"),
		MetricsURL: metrics.URL + "/metrics", TelemetryPollSeconds: 1,
	}, &fakeExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	client.nodeID, client.token, client.processGeneration = "node-a", "token", "generation-a"
	var published []protocol.StreamTelemetryRequest
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var batch protocol.StreamTelemetryRequest
		if err := json.NewDecoder(request.Body).Decode(&batch); err != nil {
			return nil, err
		}
		published = append(published, batch)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	})
	if err := client.sendStreamTelemetry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(published) != 1 || !published[0].FullSnapshot || published[0].PublicationIntervalMS != 1000 || len(published[0].StreamFiles) != 1 {
		t.Fatalf("incomplete live snapshot publication: %+v", published)
	}
	got := published[0].StreamFiles[0]
	if got.Phase != "eof_wait" || got.ReadLines != 120 || got.Normalized != 100 || got.Quarantined != 20 || got.ReadLinesPerSecond != 12.5 || got.NormalizedPerSecond != 10 || got.QuarantinedPerSecond != 2.5 {
		t.Fatalf("client changed agent source telemetry: %+v", got)
	}

	files = nil
	if err := client.sendStreamTelemetry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 || !published[1].FullSnapshot || len(published[1].StreamFiles) != 0 {
		t.Fatalf("empty full snapshot was not sent to clear removed sources: %+v", published)
	}
}

func TestReadMetricsKeepsUnavailableHostMetricsDistinctFromZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	got := readMetrics(server.URL)
	if got.HostMetricsAvailable || got.CPUPercent != 0 || got.MemoryBytes != 0 || got.ParseRate != 0 {
		t.Fatalf("failed metrics endpoint must not fabricate host readings: %+v", got)
	}
}

func TestReadMetricsCarriesUPESCountersAndAvailability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "nli_upes_unique_failure_signatures_total 12\nnli_upes_observed_failures_total 34\nnli_upes_discovery_queued_total 5\nnli_upes_discovery_dropped_total 2\nnli_upes_discovery_pending 3\n")
	}))
	defer server.Close()
	got := readMetrics(server.URL)
	if !got.UPESMetricsAvailable || got.UPESUniqueFailureSignatures != 12 || got.UPESObservedFailures != 34 || got.UPESDiscoveryQueued != 5 || got.UPESDiscoveryDropped != 2 || got.UPESDiscoveryPending != 3 {
		t.Fatalf("UPES metrics were not carried from the agent endpoint: %+v", got)
	}
}

func TestReadHostMetricsAvailabilityMatchesPlatform(t *testing.T) {
	metrics, available := readHostMetrics()
	if runtime.GOOS != "linux" {
		if available {
			t.Fatalf("host metrics unexpectedly available on %s: %+v", runtime.GOOS, metrics)
		}
		return
	}
	if !available {
		t.Fatal("Linux /proc metrics should be available")
	}
	if metrics.cpu < 0 || metrics.cpu > 100 || metrics.memoryPercent < 0 || metrics.memoryPercent > 100 || metrics.usedBytes == 0 || metrics.load1 < 0 || metrics.load5 < 0 || metrics.load15 < 0 {
		t.Fatalf("invalid Linux host metrics: %+v", metrics)
	}
}

func TestClientConfigValidationActivationAndRestart(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "nli.json")
	oldConfig := validAgentConfig(t, root, "old-service")
	newConfig := validAgentConfig(t, root, "new-service")
	if err := os.WriteFile(configPath, oldConfig, 0o640); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{}
	client, err := New(Config{
		ControllerURL: "http://127.0.0.1:12345", AllowInsecureHTTP: true,
		NodeTokenFile: filepath.Join(root, "node.token"), InstallIDFile: filepath.Join(root, "install.id"),
		JournalFile: filepath.Join(root, "journal.json"), AgentConfigPath: configPath,
		ParserDir: filepath.Join(root, "parsers"), PackageSpoolDir: filepath.Join(root, "packages"), PollSeconds: 5,
	}, executor)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]json.RawMessage{"config": newConfig})
	if _, err := client.applyConfig(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	applied, _ := os.ReadFile(configPath)
	if !json.Valid(applied) || !containsBytes(applied, []byte("new-service")) {
		t.Fatalf("new configuration not applied: %s", applied)
	}
	backup, _ := os.ReadFile(configPath + ".backup")
	if !containsBytes(backup, []byte("old-service")) || executor.restarts != 1 {
		t.Fatalf("backup/restart mismatch backup=%s restarts=%d", backup, executor.restarts)
	}
}

func TestClientConfigPatchPreservesUnchangedSettings(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "nli.json")
	oldConfig := validAgentConfig(t, root, "old-service")
	var current map[string]any
	if err := json.Unmarshal(oldConfig, &current); err != nil {
		t.Fatal(err)
	}
	current["telemetry_outputs"] = []map[string]any{{"type": "discard", "routes": []string{"telemetry"}}}
	oldConfig, _ = json.Marshal(current)
	if err := os.WriteFile(configPath, oldConfig, 0o640); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{}
	client, err := New(Config{
		ControllerURL: "http://127.0.0.1:12345", AllowInsecureHTTP: true,
		NodeTokenFile: filepath.Join(root, "node.token"), InstallIDFile: filepath.Join(root, "install.id"),
		JournalFile: filepath.Join(root, "journal.json"), AgentConfigPath: configPath,
		ParserDir: filepath.Join(root, "parsers"), PackageSpoolDir: filepath.Join(root, "packages"), PollSeconds: 5,
	}, executor)
	if err != nil {
		t.Fatal(err)
	}
	patch, _ := json.Marshal(map[string]any{"config_patch": map[string]any{
		"inputs": []map[string]any{{"path": filepath.Join(root, "imap.log"), "service": "imap"}},
	}})
	if _, err := client.applyConfig(context.Background(), patch); err != nil {
		t.Fatal(err)
	}
	applied, _ := os.ReadFile(configPath)
	var result map[string]any
	if err := json.Unmarshal(applied, &result); err != nil {
		t.Fatal(err)
	}
	if _, ok := result["telemetry_outputs"]; !ok || !containsBytes(applied, []byte("imap.log")) || containsBytes(applied, []byte("old-service")) {
		t.Fatalf("patch did not preserve telemetry or replace inputs: %s", applied)
	}
	if executor.restarts != 1 {
		t.Fatalf("agent restarted %d times", executor.restarts)
	}
}

func TestParserAssetBundleSynchronizesEveryRuntimeAssetClass(t *testing.T) {
	root := t.TempDir()
	executor := &fakeExecutor{}
	client, err := New(Config{
		ControllerURL: "http://127.0.0.1:12345", AllowInsecureHTTP: true,
		NodeTokenFile: filepath.Join(root, "node.token"), InstallIDFile: filepath.Join(root, "install.id"),
		JournalFile: filepath.Join(root, "journal.json"), AgentConfigPath: filepath.Join(root, "nli.json"),
		ParserDir: filepath.Join(root, "parsers"), AssetDir: filepath.Join(root, "parser-assets"),
		PackageSpoolDir: filepath.Join(root, "packages"), PollSeconds: 5,
	}, executor)
	if err != nil {
		t.Fatal(err)
	}
	classes := []string{"kv-config", "static-patterns", "projections", "templates", "regex-patterns", "known-keys", "semantic-extraction"}
	bundle := synchronizedAssetBundle{SchemaVersion: 1}
	for index, class := range classes {
		payload, marshalErr := json.Marshal(map[string]any{"name": class + " fixture", "config": map[string]any{"enabled": true}})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		digest := sha256.Sum256(payload)
		bundle.Assets = append(bundle.Assets, synchronizedAsset{
			AssetClass: class, AssetID: fmt.Sprintf("fixture-%d", index), Name: class,
			Revision: 1, SHA256: fmt.Sprintf("%x", digest[:]), Payload: payload,
		})
	}
	pack := &parserpolicy.ParserPack{ID: "assurance-runtime", Version: "1.0.0", Revision: 1, SourceType: "manual", Author: "operator", ReviewState: parserpolicy.ReviewStateApproved, CreatedAt: time.Now().UTC(), ResourceLimits: parserpolicy.DefaultResourceLimits(), Rules: []parserpolicy.ParserRule{{ID: "assurance-runtime-rule", Name: "Assurance runtime", Priority: 1, ServiceSelectors: []string{"mail"}, Stages: []parserpolicy.Stage{{ID: "regex", Name: "Regex", Type: parserpolicy.StageTypeRegex, Config: map[string]any{"pattern": `status=(?P<status>\w+)`, "groups": map[string]string{"status": "status"}}, FailurePolicy: parserpolicy.FailurePolicyRejectRule}}, FailurePolicy: parserpolicy.FailurePolicyQuarantine, FieldSchema: map[string]parserpolicy.FieldDef{"status": {Type: parserpolicy.FieldTypeString}}, ProjectionMode: parserpolicy.ProjectionModeAllowlist, AllowedFields: []string{"status"}, CountPolicy: parserpolicy.CountPolicyExactSource}}}
	if err := parserpolicy.SealParserPack(pack); err != nil {
		t.Fatal(err)
	}
	packPayload, _ := json.Marshal(pack)
	packDigest := sha256.Sum256(packPayload)
	bundle.Assets = append(bundle.Assets, synchronizedAsset{AssetClass: "parser-packs", AssetID: pack.ID, Name: "Assurance runtime", Revision: 3, SHA256: fmt.Sprintf("%x", packDigest[:]), Payload: packPayload})
	bundle.BundleHash, err = synchronizedBundleHash(bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundleJSON, _ := json.Marshal(bundle)
	payload, _ := json.Marshal(map[string]json.RawMessage{"asset_bundle": bundleJSON})
	message, err := client.applyParsers(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "8 revisioned parser assets validated and synchronized") || executor.restarts != 1 {
		t.Fatalf("unexpected sync result: message=%q restarts=%d", message, executor.restarts)
	}
	directories := map[string]string{"kv-config": "kv", "static-patterns": "static-patterns", "projections": "projections", "templates": "templates", "regex-patterns": "regex", "known-keys": "known-keys", "semantic-extraction": "semantic-extraction"}
	for index, class := range classes {
		path := filepath.Join(client.cfg.AssetDir, directories[class], fmt.Sprintf("fixture-%d.json", index))
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("%s was not synchronized: %v", class, statErr)
		}
		// Windows reports writable files as 0666 and cannot represent Unix
		// owner/group mode bits. Linux is the production RPM target and must
		// retain the exact group-readable, non-world-readable 0640 contract.
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
			t.Fatalf("%s mode = %o, want 0640", class, info.Mode().Perm())
		}
	}
	hash, revisions := activeAssetBundleStatus(filepath.Join(client.cfg.AssetDir, "bundle.json"))
	if hash != bundle.BundleHash || len(revisions) != len(classes)+1 {
		t.Fatalf("active bundle status mismatch: hash=%q revisions=%v", hash, revisions)
	}
	installed, err := os.ReadFile(filepath.Join(client.cfg.ParserDir, pack.ID+".json"))
	if err != nil || string(installed) != string(packPayload) {
		t.Fatalf("published runtime pack payload did not reach parser directory byte-for-byte: %v", err)
	}
	if _, err := parserpolicy.LoadParserPackBytes(installed); err != nil {
		t.Fatalf("installed parser pack is not runtime-compatible: %v", err)
	}
	quarantineInfo, err := os.Stat(filepath.Join(client.cfg.ParserDir, "quarantine"))
	if err != nil || !quarantineInfo.IsDir() {
		t.Fatalf("atomic parser swap omitted the agent runtime quarantine directory: %v", err)
	}
	if runtime.GOOS != "windows" && quarantineInfo.Mode().Perm() != 0o750 {
		t.Fatalf("runtime quarantine mode = %o, want 0750 for the nli-owned agent runtime directory", quarantineInfo.Mode().Perm())
	}
}

func TestClientRestoresConfigurationWhenRestartFails(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "nli.json")
	oldConfig := validAgentConfig(t, root, "old-service")
	if err := os.WriteFile(configPath, oldConfig, 0o640); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{restartError: errors.New("restart failed")}
	client, err := New(Config{
		ControllerURL: "http://127.0.0.1:12345", AllowInsecureHTTP: true,
		NodeTokenFile: filepath.Join(root, "node.token"), InstallIDFile: filepath.Join(root, "install.id"),
		JournalFile: filepath.Join(root, "journal.json"), AgentConfigPath: configPath,
		ParserDir: filepath.Join(root, "parsers"), PackageSpoolDir: filepath.Join(root, "packages"), PollSeconds: 5,
	}, executor)
	if err != nil {
		t.Fatal(err)
	}
	newConfig := validAgentConfig(t, root, "new-service")
	if _, err := client.applyConfig(context.Background(), newConfig); err == nil {
		t.Fatal("restart failure was ignored")
	}
	restored, _ := os.ReadFile(configPath)
	if !containsBytes(restored, []byte("old-service")) {
		t.Fatalf("configuration was not restored: %s", restored)
	}
}

func validAgentConfig(t *testing.T, root, service string) []byte {
	t.Helper()
	value := map[string]interface{}{
		"version":         2,
		"inputs":          []map[string]interface{}{{"path": filepath.Join(root, "input.log"), "service": service}},
		"state_dir":       filepath.Join(root, "state"),
		"outputs":         []map[string]interface{}{{"type": "file", "path": filepath.Join(root, "output.jsonl"), "max_mb": 10}},
		"metrics_address": "127.0.0.1:12380", "poll_ms": 500,
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func containsBytes(value, fragment []byte) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		matched := true
		for offset := range fragment {
			if value[index+offset] != fragment[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func TestControllerConfigurationRejectsPlaceholderAndTrustsPrivateCA(t *testing.T) {
	root := t.TempDir()
	base := Config{
		ControllerURL: "https://nli-controller.example.com:12382",
		NodeTokenFile: filepath.Join(root, "node.token"), InstallIDFile: filepath.Join(root, "install.id"),
		JournalFile: filepath.Join(root, "journal.json"), AgentConfigPath: filepath.Join(root, "nli.json"),
		ParserDir: filepath.Join(root, "parsers"), PackageSpoolDir: filepath.Join(root, "packages"), PollSeconds: 5,
	}
	if _, err := New(base, &fakeExecutor{}); err == nil || !strings.Contains(err.Error(), "documentation-only placeholder") {
		t.Fatalf("placeholder controller was not rejected: %v", err)
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer server.Close()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	caFile := filepath.Join(root, "controller-ca.crt")
	if err := os.WriteFile(caFile, certificate, 0o644); err != nil {
		t.Fatal(err)
	}
	base.ControllerURL = server.URL
	base.CAFile = caFile
	client, err := New(base, &fakeExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.http.Get(server.URL)
	if err != nil {
		t.Fatalf("private controller CA was not trusted: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("private-CA response status=%d", response.StatusCode)
	}
}

func TestReplayCommandTargetsRecordsAndReturnsPerRecordSummary(t *testing.T) {
	executor := &fakeExecutor{}
	client := &Client{cfg: Config{QuarantinePath: "/var/log/nli/quarantine.jsonl"}, executor: executor}
	command := &protocol.Command{
		Type:    rollout.TypeReplay,
		Payload: json.RawMessage(`{"record_ids":["q-123","q-456"]}`),
	}
	message, err := client.apply(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(message), &summary); err != nil || summary["resolved_ids"] == nil {
		t.Fatalf("per-record replay result missing: %s err=%v", message, err)
	}
	if executor.replayPath != client.cfg.QuarantinePath || strings.Join(executor.replayIDs, ",") != "q-123,q-456" {
		t.Fatalf("replay did not pass selected ids/path: path=%s ids=%v", executor.replayPath, executor.replayIDs)
	}
}
