package controlplane

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/fleet"
	"nexora.local/nli-v20/telemetry"
)

func TestMetricHistoryMonotonicResetZeroAndHighCounts(t *testing.T) {
	h, err := newMetricHistory(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 12, 0, 15, 0, time.UTC)
	sample := telemetry.StreamFileTelemetry{NodeID: "n", Source: "s", ProcessGeneration: "generation-1", ReadLines: 300_000_000, Normalized: 299_500_000, Quarantined: 500_000}
	if err := h.observe([]telemetry.StreamFileTelemetry{sample}, now); err != nil {
		t.Fatal(err)
	}
	sample.ReadLines = 600_000_000
	sample.Normalized = 599_000_000
	sample.Quarantined = 1_000_000
	if err := h.observe([]telemetry.StreamFileTelemetry{sample}, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	got := h.window(now.Truncate(time.Minute), now.Truncate(time.Minute).Add(time.Minute), time.Minute)
	if len(got) != 1 || got[0].Read != 600_000_000 || got[0].Parsed != 599_000_000 {
		t.Fatalf("monotonic deltas: %+v", got)
	}
	if float64(got[0].Read)/60 != 10_000_000 {
		t.Fatalf("10M/s representation changed: %v", float64(got[0].Read)/60)
	}
	// A decrease in the same generation is a counter reset; the new cumulative
	// value starts a fresh delta interval instead of underflowing.
	sample.ReadLines = 7
	sample.Normalized = 6
	sample.Quarantined = 1
	if err := h.observe([]telemetry.StreamFileTelemetry{sample}, now.Add(35*time.Second)); err != nil {
		t.Fatal(err)
	}
	got = h.window(now.Truncate(time.Minute), now.Truncate(time.Minute).Add(time.Minute), time.Minute)
	if got[0].Read != 600_000_007 {
		t.Fatalf("reset delta=%d", got[0].Read)
	}
	// A process restart also starts from the received cumulative count.
	sample.ProcessGeneration = "generation-2"
	sample.ReadLines = 11
	if err := h.observe([]telemetry.StreamFileTelemetry{sample}, now.Add(40*time.Second)); err != nil {
		t.Fatal(err)
	}
	got = h.window(now.Truncate(time.Minute), now.Truncate(time.Minute).Add(time.Minute), time.Minute)
	if got[0].Read != 600_000_018 {
		t.Fatalf("restart delta=%d", got[0].Read)
	}
}

func TestMetricsTimeSeriesExposesCanonicalIntervalCounts(t *testing.T) {
	f := newAPIFixture(t)
	now := time.Now().UTC()
	if err := f.server.metricHistory.observe([]telemetry.StreamFileTelemetry{{NodeID: "n", Source: "s", ProcessGeneration: "generation-1", ReadLines: 600, Normalized: 580, Quarantined: 20, Errors: 3}}, now); err != nil {
		t.Fatal(err)
	}
	response, data := f.request(http.MethodGet, "/api/v1/metrics/timeseries?range=1h&step=1m", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("timeseries status=%d: %s", response.StatusCode, data)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	history := result["canonical_history"].(map[string]any)
	if history["bucket_width_seconds"] != float64(60) || history["retention_seconds"] != float64(metricHistoryRetention.Seconds()) {
		t.Fatalf("history contract: %#v", history)
	}
	points := result["points"].([]any)
	var found bool
	for _, raw := range points {
		p := raw.(map[string]any)
		if p["received"] == float64(600) {
			found = true
			if p["parsed"] != float64(580) || p["quarantine"] != float64(20) || p["errors"] != float64(3) || p["logs_received"] != float64(600) {
				t.Fatalf("canonical interval point: %#v", p)
			}
		}
	}
	if !found {
		t.Fatalf("canonical counts missing from points: %s", data)
	}
}

func TestStreamTelemetryAcceptanceSurvivesHistoryPersistenceFailure(t *testing.T) {
	f := newAPIFixture(t)
	f.server.fleet.Register(&fleet.Node{ID: "history-failure-node", Hostname: "history-failure-node"})
	const token = "history-failure-node-token"
	if err := f.server.fleet.SetToken("history-failure-node", token); err != nil {
		t.Fatal(err)
	}
	blockedPath := filepath.Join(t.TempDir(), "history-is-a-directory")
	if err := os.Mkdir(blockedPath, 0700); err != nil {
		t.Fatal(err)
	}
	f.server.metricHistory.path = blockedPath
	now := time.Now().UTC()
	body, err := json.Marshal(map[string]any{
		"schema_version": telemetry.SyncSchemaVersion, "node_id": "history-failure-node", "process_generation": "generation-123",
		"sequence": 1, "collected_at": now, "published_at": now, "collection_interval_ms": 1000, "publication_interval_ms": 1000, "full_snapshot": true,
		"stream_files": []any{map[string]any{"source": "/var/log/input.log", "phase": "reading", "read_lines": 42}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/node/stream-telemetry", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("accepted batch failed due to retained-history error: status=%d", response.StatusCode)
	}
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "accepted" {
		t.Fatalf("batch was not accepted: %#v", result)
	}
	files := f.server.streamTelemetry.Snapshot()
	if len(files) != 1 || files[0].ReadLines != 42 {
		t.Fatalf("canonical live state lost accepted batch: %+v", files)
	}
}

func TestMetricHistoryZeroTrafficPersistenceAndRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	h, err := newMetricHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	if err := h.observe([]telemetry.StreamFileTelemetry{{NodeID: "idle", Source: "s", ProcessGeneration: "generation-1"}}, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := h.window(now.Add(-time.Minute), now, time.Minute); len(got) != 1 || got[0].Read != 0 {
		t.Fatalf("zero traffic fabricated: %+v", got)
	}
	sample := telemetry.StreamFileTelemetry{NodeID: "n", Source: "s", ProcessGeneration: "generation-1", ReadLines: 100, Normalized: 90}
	if err := h.observe([]telemetry.StreamFileTelemetry{sample}, now); err != nil {
		t.Fatal(err)
	}
	buckets := h.window(now, now.Add(time.Minute), time.Minute)
	if len(buckets) != 1 || buckets[0].Read != 100 {
		t.Fatalf("initial counter baseline: %+v", buckets)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var encoded metricHistoryFile
	if err := json.Unmarshal(data, &encoded); err != nil || len(encoded.Buckets) != 1 || len(encoded.Sources) != 2 {
		t.Fatalf("persisted buckets: %s (%v)", data, err)
	}
	reloaded, err := newMetricHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.window(now, now.Add(time.Minute), time.Minute); len(got) != 1 || got[0].Read != 100 {
		t.Fatalf("reload: %+v", got)
	}
	sample.ReadLines = 102
	if err := reloaded.observe([]telemetry.StreamFileTelemetry{sample}, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.window(now, now.Add(time.Minute), time.Minute); got[0].Read != 102 {
		t.Fatalf("restart duplicated cumulative count: %+v", got)
	}
	reloaded.mu.Lock()
	reloaded.buckets[now.Add(-metricHistoryRetention-time.Minute).Unix()] = &historyBucket{At: now.Add(-metricHistoryRetention - time.Minute)}
	reloaded.mu.Unlock()
	if err := reloaded.observe(nil, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	reloaded.mu.Lock()
	_, exists := reloaded.buckets[now.Add(-metricHistoryRetention-time.Minute).Unix()]
	reloaded.mu.Unlock()
	if exists {
		t.Fatal("expired bucket retained")
	}
}

func TestMetricHistoryYearRangePersistsAndSkipsIdleBuckets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	h, err := newMetricHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(-2, 0, 0).Truncate(time.Minute)
	sample := telemetry.StreamFileTelemetry{NodeID: "long-lived", Source: "source", ProcessGeneration: "gen-1", ReadLines: 900, Normalized: 850}
	if err := h.observe([]telemetry.StreamFileTelemetry{sample}, old); err != nil {
		t.Fatal(err)
	}
	sample.ReadLines += 10
	sample.Normalized += 9
	if err := h.observe([]telemetry.StreamFileTelemetry{sample}, old.Add(61*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := h.observe([]telemetry.StreamFileTelemetry{sample}, old.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newMetricHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.window(old, old.Add(3*time.Minute), time.Minute)
	if len(got) != 3 || got[0].Read != 900 || got[1].Read != 10 || got[2].Read != 0 {
		t.Fatalf("two-year persisted canonical counts: %+v", got)
	}
	if _, _, ok := reloaded.bounds(); !ok {
		t.Fatal("historical bounds missing after reload")
	}
}

func TestMetricsTimeSeriesSupportsCustomBoundsAndAutoStep(t *testing.T) {
	f := newAPIFixture(t)
	from := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Minute)
	to := from.Add(4 * time.Minute)
	sample := telemetry.StreamFileTelemetry{NodeID: "custom", Source: "s", ProcessGeneration: "gen-1", ReadLines: 1200, Normalized: 1170, Quarantined: 30, Errors: 4}
	if err := f.server.metricHistory.observe([]telemetry.StreamFileTelemetry{sample}, from.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	response, data := f.request(http.MethodGet, "/api/v1/metrics/timeseries?from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339)+"&step=auto", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("custom timeseries status=%d: %s", response.StatusCode, data)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result["range"] != "custom" || result["from"] != from.Format(time.RFC3339) || result["to"] != to.Format(time.RFC3339) {
		t.Fatalf("custom bounds not preserved: %#v", result)
	}
	history := result["canonical_history"].(map[string]any)
	points := history["points"].([]any)
	var read string
	for _, raw := range points {
		p := raw.(map[string]any)
		if p["read_delta"] == "1200" {
			read, _ = p["read_delta"].(string)
			if p["parsed_delta"] != "1170" || p["quarantine_delta"] != "30" {
				t.Fatalf("custom exact counts: %#v", p)
			}
		}
	}
	if read != "1200" {
		t.Fatalf("custom canonical measurement missing: %s", data)
	}
	if _, _, ok := parseHistoricalRange("1y", time.Now(), time.Time{}); !ok {
		t.Fatal("1y range rejected")
	}
	if _, _, _, ok := queryTimeBounds(url.Values{"range": []string{"10y"}}, f.server.metricHistory, time.Now().UTC()); !ok {
		t.Fatal("10y range rejected across leap years")
	}
	if _, _, ok := parseHistoricalRange("all", time.Now(), from); !ok {
		t.Fatal("all range rejected")
	}
}

func TestMetricCatalogAndOpenMetricsExposeAddressableDescriptorsWithoutFakeClientSamples(t *testing.T) {
	catalog := buildMetricCatalog()
	if catalog.AddressableSeriesFloor < 1000 || catalog.DescriptorCount != len(catalog.Descriptors) {
		t.Fatalf("catalog series addressability: %+v", catalog)
	}
	groups := map[string]bool{}
	for _, descriptor := range catalog.Descriptors {
		groups[descriptor.Group] = true
		if descriptor.Name == "" || descriptor.Type == "" || descriptor.Source == "" {
			t.Fatalf("incomplete descriptor: %+v", descriptor)
		}
	}
	if !groups["controller"] || !groups["client"] {
		t.Fatalf("metric groups absent: %+v", groups)
	}
	f := newAPIFixture(t)
	response, data := f.request(http.MethodGet, "/api/v1/metrics/openmetrics", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("OpenMetrics status=%d: %s", response.StatusCode, data)
	}
	if !strings.Contains(string(data), "nli_controller_cpu_cores ") || strings.Contains(string(data), "nli_client_cpu_percent{") {
		t.Fatalf("export has missing controller value or fabricated client values: %s", data)
	}
	f.server.streamTelemetry.Publish(telemetry.StreamFileTelemetry{NodeID: "n", Source: "s", ReadLines: 9007199254740993, UpdatedAt: time.Now().UTC()})
	response, data = f.request(http.MethodGet, "/api/v1/metrics/openmetrics", nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(data), "nli_stream_read_lines_total{node_id=\"n\",source=\"s\"} 9007199254740993") {
		t.Fatalf("OpenMetrics rounded canonical uint64 count: status=%d body=%s", response.StatusCode, data)
	}
}
