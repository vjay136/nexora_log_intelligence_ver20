package controlplane

import (
	"math"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/fleet"
	metastore "nexora.local/nli-v20/internal/store"
)

type aiUsageEvent struct {
	At        time.Time
	Success   bool
	LatencyMs float64
}

type aiUsageStore struct {
	mu     sync.RWMutex
	events []aiUsageEvent
}

func (s *aiUsageStore) record(success bool, latency time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.events = append(s.events, aiUsageEvent{At: time.Now().UTC(), Success: success, LatencyMs: float64(latency) / float64(time.Millisecond)})
	if len(s.events) > 4096 {
		s.events = append([]aiUsageEvent(nil), s.events[len(s.events)-4096:]...)
	}
	s.mu.Unlock()
}

func (s *aiUsageStore) window(after, before time.Time) []aiUsageEvent {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]aiUsageEvent, 0)
	for _, item := range s.events {
		if !item.At.Before(after) && !item.At.After(before) {
			out = append(out, item)
		}
	}
	return out
}

func (s *aiUsageStore) snapshot() (requests, successes, failures int, latencyAverage float64) {
	if s == nil {
		return 0, 0, 0, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latency float64
	for _, item := range s.events {
		requests++
		if item.Success {
			successes++
		} else {
			failures++
		}
		latency += item.LatencyMs
	}
	if requests > 0 {
		latencyAverage = latency / float64(requests)
	}
	return
}

// handleAIUsage supplies the dashboard with bounded in-memory request metrics
// and the current review queue size. Provider tokens are deliberately not
// inferred when the upstream API does not return verified accounting.
func (s *Server) handleAIUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	requests, successes, failures, latency := s.aiMetrics.snapshot()
	pending := 0
	if drafts, err := s.metadata.List(r.Context(), "ai-proposals", metastore.Query{State: "draft", Limit: 1000}); err == nil {
		pending = len(drafts)
	}
	jsonOK(w, map[string]any{
		"requests": requests, "successes": successes, "failures": failures,
		"latency_ms_avg": latency, "pending_drafts": pending,
		"tokens": 0, "token_accounting_available": false,
		"retention": "bounded_controller_memory",
	})
}

type metricBucket struct {
	time                  time.Time
	received, parsed      int
	quarantined, errors   int
	latencySum            float64
	latencyCount          int
	aiRequests, aiSuccess int
	aiFailure             int
	aiLatencySum          float64
	aiLatencyCount        int
}

func parseMetricDuration(value string, choices map[string]time.Duration, fallback time.Duration) (time.Duration, bool) {
	if value == "" {
		return fallback, true
	}
	duration, ok := choices[value]
	return duration, ok
}

func parseHistoricalRange(value string, now time.Time, earliest time.Time) (time.Time, time.Time, bool) {
	now = now.UTC()
	if value == "" {
		value = "1h"
	}
	if value == "90d" {
		return now.Add(-90 * 24 * time.Hour), now, true
	}
	if value == "all" {
		if earliest.IsZero() {
			return now.Add(-time.Hour), now, true
		}
		return earliest, now, true
	}
	// Fixed ranges accept a count followed by h/d/w. Calendar ranges use
	// AddDate, so month and year views follow calendar boundaries.
	if len(value) > 1 {
		unit := value[len(value)-1:]
		if unit == "h" || unit == "d" || unit == "w" {
			n, err := strconv.Atoi(value[:len(value)-1])
			if err != nil || n < 1 || n > 24*365*10 {
				return time.Time{}, time.Time{}, false
			}
			d := time.Duration(n) * time.Hour
			if unit == "d" {
				d *= 24
			}
			if unit == "w" {
				d *= 24 * 7
			}
			return now.Add(-d), now, true
		}
		if unit == "y" || strings.HasSuffix(value, "mo") {
			suffix := "y"
			if strings.HasSuffix(value, "mo") {
				suffix = "mo"
			}
			countText := strings.TrimSuffix(value, suffix)
			n, err := strconv.Atoi(countText)
			if err != nil || n < 1 || n > 10 {
				return time.Time{}, time.Time{}, false
			}
			if suffix == "y" {
				return now.AddDate(-n, 0, 0), now, true
			}
			return now.AddDate(0, -n, 0), now, true
		}
	}
	aliases := map[string]time.Duration{"hour": time.Hour, "day": 24 * time.Hour, "week": 7 * 24 * time.Hour, "month": 30 * 24 * time.Hour, "year": 365 * 24 * time.Hour}
	if d, ok := aliases[value]; ok {
		return now.Add(-d), now, true
	}
	return time.Time{}, time.Time{}, false
}

func queryTimeBounds(query url.Values, history *metricHistory, now time.Time) (time.Time, time.Time, string, bool) {
	fromText, toText := query.Get("from"), query.Get("to")
	if (fromText == "") != (toText == "") {
		return time.Time{}, time.Time{}, "", false
	}
	if fromText != "" {
		from, err1 := time.Parse(time.RFC3339Nano, fromText)
		to, err2 := time.Parse(time.RFC3339Nano, toText)
		if err1 != nil || err2 != nil || !to.After(from) || to.Sub(from) > metricHistoryRetention || to.After(now.Add(time.Minute)) {
			return time.Time{}, time.Time{}, "", false
		}
		return from.UTC(), to.UTC(), "custom", true
	}
	earliest, _, _ := history.bounds()
	from, to, ok := parseHistoricalRange(query.Get("range"), now, earliest)
	if ok && to.Sub(from) > metricHistoryRetention {
		ok = false
	}
	return from, to, query.Get("range"), ok
}

func adaptiveMetricStep(span time.Duration) time.Duration {
	choices := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}
	for _, step := range choices {
		if span/step <= 1000 {
			return step
		}
	}
	return choices[len(choices)-1]
}

func (s *Server) handleMetricsTimeSeries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	steps := map[string]time.Duration{"1m": time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour, "1d": 24 * time.Hour}
	stepName := r.URL.Query().Get("step")
	now := time.Now().UTC()
	from, to, normalizedRange, ok := queryTimeBounds(r.URL.Query(), s.metricHistory, now)
	if !ok || to.After(now.Add(time.Minute)) {
		jsonError(w, "range must be an hour/day/week/month/year duration or all; from and to must be a valid UTC RFC3339 pair within retention", http.StatusBadRequest)
		return
	}
	step := adaptiveMetricStep(to.Sub(from))
	if stepName != "" && stepName != "auto" {
		step, ok = parseMetricDuration(stepName, steps, time.Minute)
	} else {
		ok = true
	}
	if !ok || step > to.Sub(from) || int((to.Sub(from)+step-1)/step) > 1000 {
		jsonError(w, "step is unsupported or would create more than 1000 buckets", http.StatusBadRequest)
		return
	}
	nodeID := strings.TrimSpace(r.URL.Query().Get("node_id"))
	if len(nodeID) > 128 {
		jsonError(w, "node_id exceeds 128 bytes", http.StatusBadRequest)
		return
	}
	// Include the current partial interval so a newly accepted client record is
	// visible immediately instead of waiting for the next bucket boundary.
	bucketCount := int((to.Sub(from) + step - 1) / step)
	buckets := make([]metricBucket, bucketCount)
	for index := range buckets {
		buckets[index].time = from.Add(time.Duration(index) * step)
	}
	indexFor := func(value time.Time) int {
		index := int(value.Sub(from) / step)
		if index < 0 || index >= len(buckets) {
			return -1
		}
		return index
	}
	for _, record := range s.events.Window(from, to, nodeID) {
		index := indexFor(record.Timestamp)
		if index < 0 {
			continue
		}
		bucket := &buckets[index]
		bucket.received++
		if record.Route == "normal" && record.Result != "quarantined" {
			bucket.parsed++
		}
		if record.Route == "quarantine" || record.Result == "quarantined" {
			bucket.quarantined++
		}
		if len(record.ErrorCodes) > 0 || record.Route == "error" {
			bucket.errors++
		}
		if record.LatencyMs > 0 {
			bucket.latencySum += float64(record.LatencyMs)
			bucket.latencyCount++
		}
	}
	for _, event := range s.aiMetrics.window(from, to) {
		index := indexFor(event.At)
		if index < 0 {
			continue
		}
		bucket := &buckets[index]
		bucket.aiRequests++
		if event.Success {
			bucket.aiSuccess++
		} else {
			bucket.aiFailure++
		}
		bucket.aiLatencySum += event.LatencyMs
		bucket.aiLatencyCount++
	}
	points := make([]map[string]any, 0, len(buckets))
	for _, bucket := range buckets {
		latency, aiLatency := 0.0, 0.0
		if bucket.latencyCount > 0 {
			latency = bucket.latencySum / float64(bucket.latencyCount)
		}
		if bucket.aiLatencyCount > 0 {
			aiLatency = bucket.aiLatencySum / float64(bucket.aiLatencyCount)
		}
		parseRate := 0.0
		if bucket.received > 0 {
			parseRate = float64(bucket.parsed) * 100 / float64(bucket.received)
		}
		points = append(points, map[string]any{"timestamp": bucket.time.Format(time.RFC3339), "logs_received": bucket.received, "logs_parsed": bucket.parsed, "logs_quarantined": bucket.quarantined, "errors": bucket.errors, "parse_latency_ms_avg": latency, "parse_rate_pct": parseRate, "ai_requests": bucket.aiRequests, "ai_success": bucket.aiSuccess, "ai_failure": bucket.aiFailure, "ai_latency_ms_avg": aiLatency})
	}
	canonical := make([]map[string]any, 0, bucketCount)
	if nodeID == "" {
		for index, bucket := range s.metricHistory.window(from, to, step) {
			points[index]["received"] = bucket.Read
			points[index]["parsed"] = bucket.Parsed
			points[index]["quarantine"] = bucket.Quarantine
			points[index]["errors"] = bucket.Errors
			points[index]["logs_received"] = bucket.Read
			points[index]["logs_parsed"] = bucket.Parsed
			points[index]["logs_quarantined"] = bucket.Quarantine
			canonical = append(canonical, map[string]any{
				"timestamp": bucket.At.Format(time.RFC3339),
				"received":  bucket.Read, "parsed": bucket.Parsed, "quarantine": bucket.Quarantine, "errors": bucket.Errors,
				"read_delta": strconv.FormatUint(bucket.Read, 10), "parsed_delta": strconv.FormatUint(bucket.Parsed, 10), "quarantine_delta": strconv.FormatUint(bucket.Quarantine, 10),
				"read_per_second": float64(bucket.Read) / step.Seconds(), "parsed_per_second": float64(bucket.Parsed) / step.Seconds(), "quarantine_per_second": float64(bucket.Quarantine) / step.Seconds(),
			})
		}
	}
	fleetSnapshot := s.fleet.All()
	active := make([]*fleet.Node, 0, len(fleetSnapshot))
	for _, node := range fleetSnapshot {
		if nodeID == "" || node.ID == nodeID {
			if node.Metrics != nil {
				active = append(active, node)
			}
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })
	clientMetrics := make([]map[string]any, 0, len(active))
	var cpuSum, memorySum, loadSum float64
	var cpuCount, memoryCount, loadCount int
	for _, node := range active {
		metric := node.Metrics
		clientMetrics = append(clientMetrics, map[string]any{"node_id": node.ID, "hostname": node.Hostname, "updated_at": metric.UpdatedAt, "cpu_percent": metric.CPUPercent, "memory_bytes": metric.MemoryBytes, "memory_percent": metric.MemoryPercent, "load_1m": metric.Load1, "load_5m": metric.Load5, "load_15m": metric.Load15, "host_metrics_available": metric.HostMetricsAvailable, "parse_latency_ms": metric.ParseLatencyMs})
		if metric.HostMetricsAvailable {
			cpuSum += metric.CPUPercent
			cpuCount++
			memorySum += metric.MemoryPercent
			memoryCount++
			loadSum += metric.Load1
			loadCount++
		}
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	system := map[string]any{"controller_cpu_count": runtime.NumCPU(), "controller_memory_alloc_bytes": mem.Alloc, "controller_memory_sys_bytes": mem.Sys, "controller_goroutines": runtime.NumGoroutine(), "client_cpu_percent_avg": nullableAverage(cpuSum, cpuCount), "client_memory_percent_avg": nullableAverage(memorySum, memoryCount), "client_load_1m_avg": nullableAverage(loadSum, loadCount), "availability": map[string]bool{"controller_cpu_percent": false, "controller_memory": true, "client_cpu_percent": cpuCount > 0, "client_memory_percent": memoryCount > 0, "client_load": loadCount > 0}}
	lastObserved := time.Time{}
	if s.metricHistory != nil {
		s.metricHistory.mu.Lock()
		lastObserved = s.metricHistory.lastObserved
		s.metricHistory.mu.Unlock()
	}
	freshness := map[string]any{"state": "unavailable", "age_seconds": nil}
	if !lastObserved.IsZero() {
		age := time.Since(lastObserved)
		freshness = map[string]any{"state": "live", "age_seconds": age.Seconds(), "observed_at": lastObserved.Format(time.RFC3339)}
		if age > 15*time.Second {
			freshness["state"] = "stale"
		}
	}
	resolvedStep := rangeNameOrDefault(stepName, "1m")
	if stepName == "" && step != time.Minute {
		resolvedStep = "auto"
	}
	jsonOK(w, map[string]any{"schema_version": "1", "range": rangeNameOrDefault(normalizedRange, "1h"), "step": resolvedStep, "from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339), "node_id": nodeID, "points": points, "canonical_history": map[string]any{"scope": "all_sources", "bucket_width_seconds": 60, "query_step_seconds": int64(step.Seconds()), "count_encoding": "decimal_string", "points": canonical, "freshness": freshness, "retention_seconds": int64(metricHistoryRetention.Seconds()), "source": "canonical_latest_cumulative_counters"}, "fleet": map[string]any{"stats": s.fleet.Stats(), "clients": clientMetrics}, "system": system, "retention": map[string]any{"raw_logs": false, "event_payloads": false, "secrets": false, "series_source": "bounded_parser_metadata_and_latest_heartbeat"}})
}

func nullableAverage(total float64, count int) any {
	if count == 0 || math.IsNaN(total) || math.IsInf(total, 0) {
		return nil
	}
	return total / float64(count)
}

func rangeNameOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
