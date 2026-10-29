package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nexora.local/nli-v20/internal/eventstore"
	"nexora.local/nli-v20/internal/wsrelay"
	"nexora.local/nli-v20/telemetry"
)

type TelemetryController struct{ store *eventstore.Store }

// StreamHub publishes bounded latest-state updates to non-blocking subscribers.
type StreamHub struct {
	mu                                                          sync.Mutex
	next                                                        uint64
	latest                                                      map[string]telemetry.StreamFileTelemetry
	subs                                                        map[uint64]chan telemetry.StreamFileTelemetry
	max                                                         int
	maxSources                                                  int
	sequence                                                    atomic.Uint64
	revision                                                    atomic.Uint64
	nodes                                                       map[string]syncNodeState
	issues                                                      map[string]telemetry.SyncIssue
	delays                                                      []float64
	processing                                                  []float64
	target                                                      time.Duration
	liveAfter                                                   time.Duration
	delayedAfter                                                time.Duration
	staleAfter                                                  time.Duration
	acceptedBatches, duplicateBatches, outOfOrderBatches        uint64
	sequenceGaps, reconnects, coalescedUpdates, rejectedBatches uint64
	missedFreshnessTargets, clockSkewSamples                    uint64
}

type syncNodeState struct {
	Generation string
	Sequence   uint64
	LastSeen   time.Time
}

func NewStreamHub(maxSubscribers int) *StreamHub {
	if maxSubscribers < 1 {
		maxSubscribers = 64
	}
	return &StreamHub{latest: map[string]telemetry.StreamFileTelemetry{}, subs: map[uint64]chan telemetry.StreamFileTelemetry{}, max: maxSubscribers, maxSources: 10000, nodes: map[string]syncNodeState{}, issues: map[string]telemetry.SyncIssue{}, target: time.Second, liveAfter: 2 * time.Second, delayedAfter: 5 * time.Second, staleAfter: 15 * time.Second}
}

func (h *StreamHub) Publish(sample telemetry.StreamFileTelemetry) {
	h.mu.Lock()
	if h.storeLocked(sample) {
		revision := h.revision.Add(1)
		now := time.Now().UTC()
		sources := h.snapshotLocked(now)
		aggregate := h.aggregateFromSourcesLocked(sources)
		diagnostics := h.diagnosticsLocked(sources)
		sample.CanonicalRevision = revision
		sample.CanonicalAggregate = &aggregate
		sample.CanonicalDiagnostics = &diagnostics
		h.latest[sample.NodeID+"\x00"+sample.Source] = sample
		h.broadcastLocked(sample)
	}
	h.mu.Unlock()
}

func (h *StreamHub) storeLocked(sample telemetry.StreamFileTelemetry) bool {
	key := sample.NodeID + "\x00" + sample.Source
	if _, exists := h.latest[key]; !exists && len(h.latest) >= h.maxSources {
		h.rejectedBatches++
		h.recordIssueLocked("source_capacity", "controller_aggregation", "warning", "controller", sample.NodeID, sample.Source, "canonical source capacity reached", fmt.Sprintf("capacity=%d", h.maxSources), time.Now().UTC())
		return false
	}
	if previous, exists := h.latest[key]; exists {
		if sample.ProcessGeneration != "" && previous.ProcessGeneration != "" {
			if sample.ProcessGeneration == previous.ProcessGeneration && sample.Sequence <= previous.Sequence {
				return false
			}
		} else if !previous.UpdatedAt.IsZero() && !sample.UpdatedAt.After(previous.UpdatedAt) {
			return false
		}
	}
	h.latest[key] = sample
	return true
}

func (h *StreamHub) broadcastLocked(sample telemetry.StreamFileTelemetry) {
	for _, ch := range h.subs {
		select {
		case ch <- sample:
		default:
			h.coalescedUpdates++
			sample.ResyncRequired = true
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- sample:
			default:
			}
		}
	}
}

// PublishBatch validates ordering once per compact client publication, updates
// the canonical source state, and broadcasts only incremental source changes.
func (h *StreamHub) PublishBatch(batch telemetry.SyncBatch) telemetry.SyncIngestResult {
	started := time.Now()
	now := started.UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	state, seen := h.nodes[batch.NodeID]
	if seen && state.Generation == batch.ProcessGeneration {
		if batch.Sequence == state.Sequence {
			h.duplicateBatches++
			return telemetry.SyncIngestResult{Status: "duplicate", Revision: h.revision.Load(), Sequence: batch.Sequence}
		}
		if batch.Sequence < state.Sequence {
			h.outOfOrderBatches++
			h.recordIssueLocked("out_of_order", "controller_ingestion", "warning", "sync-fabric", batch.NodeID, "", "publication sequence is older than canonical state", fmt.Sprintf("received=%d canonical=%d", batch.Sequence, state.Sequence), now)
			return telemetry.SyncIngestResult{Status: "out_of_order", Revision: h.revision.Load(), Sequence: batch.Sequence}
		}
		if batch.Sequence > state.Sequence+1 {
			gap := batch.Sequence - state.Sequence - 1
			h.sequenceGaps += gap
			h.recordIssueLocked("sequence_gap", "controller_ingestion", "warning", "sync-fabric", batch.NodeID, "", "one or more telemetry publications were not observed", fmt.Sprintf("missing=%d after=%d", gap, state.Sequence), now)
		}
	} else if seen {
		h.reconnects++
	}
	removedSources := 0
	if batch.FullSnapshot {
		present := make(map[string]struct{}, len(batch.StreamFiles))
		for _, sample := range batch.StreamFiles {
			present[sample.Source] = struct{}{}
		}
		prefix := batch.NodeID + "\x00"
		for key := range h.latest {
			if strings.HasPrefix(key, prefix) {
				if _, ok := present[strings.TrimPrefix(key, prefix)]; !ok {
					delete(h.latest, key)
					removedSources++
				}
			}
		}
	}
	var observedDelay *float64
	if !batch.PublishedAt.IsZero() {
		delay := float64(now.Sub(batch.PublishedAt)) / float64(time.Millisecond)
		if delay >= 0 && delay <= 30000 {
			observedDelay = &delay
			h.delays = appendBounded(h.delays, delay, 4096)
		} else {
			h.clockSkewSamples++
			h.recordIssueLocked("clock_skew", "transit_estimate", "info", "sync-fabric", batch.NodeID, "", "wall clocks prevent a trustworthy transit estimate", "delay marked unavailable; local monotonic durations remain valid", now)
		}
	}
	if seen && !state.LastSeen.IsZero() && now.Sub(state.LastSeen) > h.target*2 {
		h.missedFreshnessTargets++
	}
	h.nodes[batch.NodeID] = syncNodeState{Generation: batch.ProcessGeneration, Sequence: batch.Sequence, LastSeen: now}
	processingMS := float64(time.Since(started)) / float64(time.Millisecond)
	h.processing = appendBounded(h.processing, processingMS, 4096)
	accepted := make([]telemetry.StreamFileTelemetry, 0, len(batch.StreamFiles))
	for i := range batch.StreamFiles {
		sample := batch.StreamFiles[i]
		sample.SchemaVersion = telemetry.SyncSchemaVersion
		sample.NodeID = batch.NodeID
		sample.ProcessGeneration = batch.ProcessGeneration
		sample.Sequence = batch.Sequence
		sample.CollectedAt = batch.CollectedAt
		sample.PublishedAt = batch.PublishedAt
		sample.ControllerReceivedAt = now
		sample.CollectionIntervalMS = batch.CollectionIntervalMS
		sample.PublicationIntervalMS = batch.PublicationIntervalMS
		sample.ControllerProcessingMS = processingMS
		sample.ObservedPropagationDelayMS = observedDelay
		sample.Freshness = telemetry.FreshnessLive
		sample.FreshnessReason = "controller received the current process generation within the live threshold"
		if sample.UpdatedAt.IsZero() {
			sample.UpdatedAt = batch.CollectedAt
		}
		key := batch.NodeID + "\x00" + sample.Source
		if previous, ok := h.latest[key]; ok && previous.ProcessGeneration == batch.ProcessGeneration && (sample.ReadLines < previous.ReadLines || sample.Normalized < previous.Normalized || sample.Quarantined < previous.Quarantined || sample.ValidationAttempts < previous.ValidationAttempts || sample.ValidationPassed < previous.ValidationPassed || sample.ValidationFailed < previous.ValidationFailed || sample.Bytes < previous.Bytes) {
			sample.DiagnosticCodes = append(sample.DiagnosticCodes, "counter_reset")
			h.recordIssueLocked("counter_reset", "rate_calculation", "warning", "sync-fabric", batch.NodeID, sample.Source, "one or more cumulative counters decreased within the same process generation", "rates use the client observation and canonical totals restart from the received cumulative value", now)
		}
		if sample.Backpressure {
			sample.Critical = true
			sample.DiagnosticCodes = append(sample.DiagnosticCodes, "queue_backpressure")
			h.recordIssueLocked("queue_backpressure", "client_queue", "critical", "nli-agent", batch.NodeID, sample.Source, "source queue reached its configured backpressure threshold", fmt.Sprintf("depth=%d capacity=%d", sample.QueueDepth, sample.QueueCapacity), now)
		}
		if sample.Errors > 0 {
			sample.DiagnosticCodes = append(sample.DiagnosticCodes, "source_errors")
			h.recordIssueLocked("source_errors", "collection", "warning", "nli-agent", batch.NodeID, sample.Source, "source reported cumulative read or output errors", fmt.Sprintf("errors=%d", sample.Errors), now)
		}
		if h.storeLocked(sample) {
			accepted = append(accepted, sample)
		}
	}
	h.acceptedBatches++
	revision := h.revision.Add(1)
	sources := h.snapshotLocked(now)
	aggregate := h.aggregateFromSourcesLocked(sources)
	diagnostics := h.diagnosticsLocked(sources)
	for _, sample := range accepted {
		sample.CanonicalRevision = revision
		sample.CanonicalAggregate = &aggregate
		sample.CanonicalDiagnostics = &diagnostics
		h.latest[sample.NodeID+"\x00"+sample.Source] = sample
		h.broadcastLocked(sample)
	}
	// A full client snapshot can remove sources without producing a source
	// delta (including the valid empty-snapshot case). Notify subscribers to
	// fetch the authoritative snapshot so browser state cannot retain a source
	// that the controller has already removed.
	if removedSources > 0 {
		h.broadcastLocked(telemetry.StreamFileTelemetry{
			SchemaVersion:        telemetry.SyncSchemaVersion,
			NodeID:               batch.NodeID,
			Source:               "__nli_sync_resync__",
			ProcessGeneration:    batch.ProcessGeneration,
			Sequence:             batch.Sequence,
			CanonicalRevision:    revision,
			CanonicalAggregate:   &aggregate,
			CanonicalDiagnostics: &diagnostics,
			ResyncRequired:       true,
		})
	}
	return telemetry.SyncIngestResult{Status: "accepted", Revision: revision, Sequence: batch.Sequence, SequenceGaps: h.sequenceGaps, Accepted: len(accepted)}
}

func (h *StreamHub) Snapshot() []telemetry.StreamFileTelemetry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snapshotLocked(time.Now().UTC())
}

func (h *StreamHub) snapshotLocked(now time.Time) []telemetry.StreamFileTelemetry {
	out := make([]telemetry.StreamFileTelemetry, 0, len(h.latest))
	for _, sample := range h.latest {
		age := now.Sub(sample.ControllerReceivedAt)
		if sample.ControllerReceivedAt.IsZero() {
			age = now.Sub(sample.UpdatedAt)
		}
		if age < 0 {
			age = 0
		}
		sample.FreshnessAgeMS = age.Milliseconds()
		switch {
		case age <= h.liveAfter:
			sample.Freshness = telemetry.FreshnessLive
			sample.FreshnessReason = "latest update is within the live threshold"
		case age <= h.delayedAfter:
			sample.Freshness = telemetry.FreshnessDelayed
			sample.FreshnessReason = "latest update missed the live threshold"
		case age <= h.staleAfter:
			sample.Freshness = telemetry.FreshnessStale
			sample.FreshnessReason = "latest update exceeded the delayed threshold"
		default:
			sample.Freshness = telemetry.FreshnessDisconnected
			sample.FreshnessReason = "no telemetry arrived before the disconnect threshold"
		}
		out = append(out, sample)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeID == out[j].NodeID {
			return out[i].Source < out[j].Source
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out
}

func appendBounded(values []float64, value float64, limit int) []float64 {
	values = append(values, value)
	if len(values) > limit {
		copy(values, values[len(values)-limit:])
		values = values[:limit]
	}
	return values
}

func percentile(values []float64, pct float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyOfValues := append([]float64(nil), values...)
	sort.Float64s(copyOfValues)
	index := int(math.Ceil(float64(len(copyOfValues))*pct)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(copyOfValues) {
		index = len(copyOfValues) - 1
	}
	return copyOfValues[index]
}

func (h *StreamHub) recordIssueLocked(code, stage, severity, component, nodeID, source, reason, evidence string, now time.Time) {
	keyBytes := sha256.Sum256([]byte(code + "\x00" + component + "\x00" + nodeID + "\x00" + source))
	correlation := hex.EncodeToString(keyBytes[:8])
	issue, exists := h.issues[correlation]
	if !exists {
		if len(h.issues) >= 128 {
			var oldestKey string
			var oldest time.Time
			for key, candidate := range h.issues {
				if oldest.IsZero() || candidate.LastSeen.Before(oldest) {
					oldestKey, oldest = key, candidate.LastSeen
				}
			}
			delete(h.issues, oldestKey)
		}
		issue = telemetry.SyncIssue{Code: code, Stage: stage, Severity: severity, Component: component, NodeID: nodeID, Source: source, Reason: reason, Evidence: evidence, CorrelationID: correlation, FirstSeen: now}
	}
	issue.LastSeen = now
	issue.Occurrences++
	h.issues[correlation] = issue
}

func (h *StreamHub) CanonicalSnapshot() telemetry.SyncSnapshot {
	now := time.Now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	sources := h.snapshotLocked(now)
	aggregate := h.aggregateFromSourcesLocked(sources)
	diagnostics := h.diagnosticsLocked(sources)
	return telemetry.SyncSnapshot{SchemaVersion: telemetry.SyncSchemaVersion, Revision: h.revision.Load(), GeneratedAt: now, TargetIntervalMS: h.target.Milliseconds(), Aggregate: aggregate, Sources: sources, Diagnostics: diagnostics}
}

func (h *StreamHub) aggregateFromSourcesLocked(sources []telemetry.StreamFileTelemetry) telemetry.SyncAggregate {
	var aggregate telemetry.SyncAggregate
	var lastSuccess time.Time
	for _, sample := range sources {
		aggregate.SourceRecordsReadTotal += sample.ReadLines
		aggregate.SourceBytesReadTotal += sample.Bytes
		aggregate.ParserSucceededTotal += sample.Normalized
		aggregate.NormalizedRecordsTotal += sample.Normalized
		aggregate.ParserFailedTotal += sample.Quarantined
		aggregate.QuarantinedRecordsTotal += sample.Quarantined
		aggregate.ValidationAttemptsTotal += sample.ValidationAttempts
		aggregate.ValidationPassedTotal += sample.ValidationPassed
		aggregate.ValidationFailedTotal += sample.ValidationFailed
		aggregate.QueueDepth += sample.QueueDepth
		aggregate.ReadLinesPerSecond += sample.ReadLinesPerSecond
		aggregate.NormalizedPerSecond += sample.NormalizedPerSecond
		aggregate.QuarantinedPerSecond += sample.QuarantinedPerSecond
		aggregate.ValidationAttemptsPerSecond += sample.ValidationAttemptsPerSecond
		aggregate.ValidationPassedPerSecond += sample.ValidationPassedPerSecond
		aggregate.ValidationFailedPerSecond += sample.ValidationFailedPerSecond
		aggregate.BytesPerSecond += sample.BytesPerSecond
		if sample.Backpressure {
			aggregate.BackpressureSourceCount++
		}
		if sample.ControllerReceivedAt.After(lastSuccess) {
			lastSuccess = sample.ControllerReceivedAt
		}
	}
	if !lastSuccess.IsZero() {
		aggregate.TelemetryLastSuccessUnix = lastSuccess.Unix()
	}
	aggregate.PropagationDelaySecondsP95 = percentile(h.delays, .95) / 1000
	return aggregate
}

func (h *StreamHub) diagnosticsLocked(sources []telemetry.StreamFileTelemetry) telemetry.SyncDiagnostics {
	var live, delayed, stale, disconnected int
	for _, sample := range sources {
		switch sample.Freshness {
		case telemetry.FreshnessLive:
			live++
		case telemetry.FreshnessDelayed:
			delayed++
		case telemetry.FreshnessStale:
			stale++
		case telemetry.FreshnessDisconnected:
			disconnected++
		}
	}
	issues := make([]telemetry.SyncIssue, 0, len(h.issues))
	for _, issue := range h.issues {
		issues = append(issues, issue)
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].LastSeen.After(issues[j].LastSeen) })
	return telemetry.SyncDiagnostics{TargetIntervalMS: h.target.Milliseconds(), LiveThresholdMS: h.liveAfter.Milliseconds(), DelayedThresholdMS: h.delayedAfter.Milliseconds(), StaleThresholdMS: h.staleAfter.Milliseconds(), Subscribers: len(h.subs), TrackedNodes: len(h.nodes), TrackedSources: len(sources), LiveSources: live, DelayedSources: delayed, StaleSources: stale, DisconnectedSources: disconnected, AcceptedBatches: h.acceptedBatches, DuplicateBatches: h.duplicateBatches, OutOfOrderBatches: h.outOfOrderBatches, SequenceGaps: h.sequenceGaps, Reconnects: h.reconnects, CoalescedUpdates: h.coalescedUpdates, RejectedBatches: h.rejectedBatches, MissedFreshnessTargets: h.missedFreshnessTargets, ClockSkewSamples: h.clockSkewSamples, ObservedDelayP50MS: percentile(h.delays, .50), ObservedDelayP95MS: percentile(h.delays, .95), ObservedDelayP99MS: percentile(h.delays, .99), ControllerP50MS: percentile(h.processing, .50), ControllerP95MS: percentile(h.processing, .95), ControllerP99MS: percentile(h.processing, .99), DelayMeasurement: "Observed delay uses client and controller wall clocks and is unavailable when skew exceeds 30 seconds; controller processing uses a local monotonic clock.", Issues: issues}
}

func (h *StreamHub) Subscribe() (uint64, <-chan telemetry.StreamFileTelemetry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= h.max {
		return 0, nil, false
	}
	h.next++
	ch := make(chan telemetry.StreamFileTelemetry, 1)
	h.subs[h.next] = ch
	return h.next, ch, true
}

func (h *StreamHub) Unsubscribe(id uint64) {
	h.mu.Lock()
	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
	h.mu.Unlock()
}

// NewTelemetryController uses the same bounded metadata store as the
// controller's live metadata, overview, and time-series APIs.
func NewTelemetryController(store *eventstore.Store) (*TelemetryController, error) {
	if store == nil {
		return nil, fmt.Errorf("telemetry store is required")
	}
	return &TelemetryController{store: store}, nil
}

func validateParseTelemetry(record *telemetry.ParseTelemetry) error {
	if record == nil {
		return errors.New("telemetry is required")
	}
	if record.NodeID == "" || record.ParserID == "" {
		return errors.New("node_id and parser_id are required")
	}
	if record.Confidence < 0 || record.Confidence > 1 || record.EvidenceCoverage < 0 || record.EvidenceCoverage > 1 {
		return errors.New("confidence and evidence_coverage must be between zero and one")
	}
	if record.AttemptCount < 0 || record.FallbackDepth < 0 || record.StageCount < 0 || record.InputFieldCount < 0 || record.OutputFieldCount < 0 || record.LatencyMs < 0 {
		return errors.New("telemetry counters must be non-negative")
	}
	if len(record.NodeID) > 128 || len(record.ParserID) > 128 || len(record.RuleID) > 128 || len(record.EventHash) > 64 || len(record.PolicyHash) > 64 {
		return errors.New("telemetry field exceeds bounds")
	}
	if len(record.OrderedAttempts) > 20 || len(record.FieldEvidence) > 512 || len(record.ErrorCodes) > 10 || len(record.DeliveryAcks) > 32 {
		return errors.New("telemetry collection exceeds bounds")
	}
	for _, attempt := range record.OrderedAttempts {
		if len(attempt.StageResults) > 64 {
			return errors.New("telemetry stage collection exceeds bounds")
		}
	}
	for _, hash := range []string{record.EventHash, record.PolicyHash} {
		if hash != "" && (len(hash) != 64 || !isHex(hash)) {
			return errors.New("telemetry hashes must be 64 hexadecimal characters")
		}
	}
	return nil
}
func isHex(value string) bool {
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F') {
			return false
		}
	}
	return true
}

func (tc *TelemetryController) Decode(body io.Reader) (*telemetry.ParseTelemetry, error) {
	decoder := json.NewDecoder(io.LimitReader(body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	var record telemetry.ParseTelemetry
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("invalid telemetry: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("telemetry body must contain one JSON object")
	}
	if err := validateParseTelemetry(&record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (tc *TelemetryController) Store(record *telemetry.ParseTelemetry) (eventstore.Record, error) {
	if record.ProcessedAt.IsZero() {
		record.ProcessedAt = time.Now().UTC()
	}
	acks := make([]eventstore.DeliveryAck, 0, len(record.DeliveryAcks))
	for _, ack := range record.DeliveryAcks {
		acks = append(acks, eventstore.DeliveryAck{Destination: ack.Destination, Status: ack.Status, ErrorCode: ack.ErrorCode, Timestamp: ack.Timestamp})
	}
	return tc.store.Append(record.NodeID, eventstore.TelemetryMetadata{EventID: record.EventID, EventHash: record.EventHash, Service: "telemetry", ParserID: record.ParserID, RuleID: record.RuleID, Route: record.Route, Confidence: record.Confidence, EvidenceCoverage: record.EvidenceCoverage, AttemptCount: record.AttemptCount, FallbackDepth: record.FallbackDepth, FieldCount: record.OutputFieldCount, LatencyMs: record.LatencyMs, Result: record.Result, ErrorCodes: record.ErrorCodes, DeliveryAcks: acks, Timestamp: record.ProcessedAt})
}

func (tc *TelemetryController) GetTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 1000 {
			jsonError(w, "limit must be 1..1000", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	query := eventstore.Query{NodeID: r.URL.Query().Get("node_id"), Service: "telemetry", Parser: r.URL.Query().Get("parser_id"), Route: r.URL.Query().Get("route"), Search: r.URL.Query().Get("search"), Limit: limit}
	if after := r.URL.Query().Get("after"); after != "" {
		value, err := time.Parse(time.RFC3339, after)
		if err != nil {
			jsonError(w, "after must be RFC3339", http.StatusBadRequest)
			return
		}
		query.After = value
	}
	jsonOK(w, map[string]any{"telemetry": tc.store.Query(query), "bounded": true, "normal_payload_retained": false})
}
func (tc *TelemetryController) GetTelemetryStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	stats := tc.store.Stats()
	stats["normal_payload_retained"] = false
	jsonOK(w, stats)
}

func (h *StreamHub) SnapshotHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	snapshot := h.CanonicalSnapshot()
	jsonOK(w, map[string]any{"files": snapshot.Sources, "bounded": true, "schema_version": snapshot.SchemaVersion, "revision": snapshot.Revision, "generated_at": snapshot.GeneratedAt, "aggregate": snapshot.Aggregate, "diagnostics": snapshot.Diagnostics})
}

func (h *StreamHub) DiagnosticsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	jsonOK(w, h.CanonicalSnapshot().Diagnostics)
}

func (h *StreamHub) CanonicalSnapshotHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonOK(w, h.CanonicalSnapshot())
}

func (h *StreamHub) SSEHandler(w http.ResponseWriter, r *http.Request) {
	h.serveSSE(w, r, nil)
}

// SSEHandlerWithEvents multiplexes bounded stream samples and safe controller
// metadata events. Event payloads are filtered by wsrelay before subscription.
func (h *StreamHub) SSEHandlerWithEvents(events *wsrelay.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { h.serveSSE(w, r, events) }
}

func (h *StreamHub) CanonicalSSEHandlerWithEvents(events *wsrelay.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { h.serveCanonicalSSE(w, r, events, nil) }
}

func (h *StreamHub) CanonicalSSEHandlerWithSnapshot(events *wsrelay.Hub, snapshot func(*http.Request) any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { h.serveCanonicalSSE(w, r, events, snapshot) }
}

func (h *StreamHub) serveCanonicalSSE(w http.ResponseWriter, r *http.Request, events *wsrelay.Hub, snapshot func(*http.Request) any) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	id, ch, ok := h.Subscribe()
	if !ok {
		http.Error(w, "subscriber limit reached", http.StatusServiceUnavailable)
		return
	}
	defer h.Unsubscribe(id)
	var eventID uint64
	var eventCh <-chan []byte
	if events != nil {
		var subscribed bool
		eventID, eventCh, subscribed = events.SubscribeSafe()
		if !subscribed {
			http.Error(w, "subscriber limit reached", http.StatusServiceUnavailable)
			return
		}
		defer events.UnsubscribeSafe(eventID)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	stream := newSSEDeadlineWriter(w, 15*time.Second)
	enc := json.NewEncoder(stream)
	initial := any(h.CanonicalSnapshot())
	if snapshot != nil {
		initial = snapshot(r)
	}
	if !writeSSE(stream, flusher, enc, strconv.FormatUint(h.sequence.Add(1), 10), "sync_snapshot", initial) {
		return
	}
	keepalive := time.NewTicker(h.target)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case sample, open := <-ch:
			if !open {
				return
			}
			nhle := map[string]any{"status": sample.Freshness, "updated_at": sample.ControllerReceivedAt}
			if sample.CanonicalAggregate != nil {
				nhle["records_read_total"] = sample.CanonicalAggregate.SourceRecordsReadTotal
				nhle["normalized_total"] = sample.CanonicalAggregate.NormalizedRecordsTotal
				nhle["quarantined_total"] = sample.CanonicalAggregate.QuarantinedRecordsTotal
				nhle["queue_depth"] = sample.CanonicalAggregate.QueueDepth
				nhle["backpressure_sources"] = sample.CanonicalAggregate.BackpressureSourceCount
				nhle["read_lines_per_second"] = sample.CanonicalAggregate.ReadLinesPerSecond
				nhle["normalized_per_second"] = sample.CanonicalAggregate.NormalizedPerSecond
			}
			envelope := map[string]any{"schema_version": telemetry.SyncSchemaVersion, "revision": sample.CanonicalRevision, "emitted_at": time.Now().UTC(), "kind": "source", "source": sample, "aggregate": sample.CanonicalAggregate, "diagnostics": sample.CanonicalDiagnostics, "subsystems": map[string]any{"nhle": nhle}, "resync_required": sample.ResyncRequired}
			if !writeSSE(stream, flusher, enc, strconv.FormatUint(h.sequence.Add(1), 10), "sync_delta", envelope) {
				return
			}
			if sample.Critical {
				critical := map[string]any{"schema_version": telemetry.SyncSchemaVersion, "revision": h.revision.Load(), "emitted_at": time.Now().UTC(), "kind": "critical", "node_id": sample.NodeID, "source": sample.Source, "diagnostic_codes": sample.DiagnosticCodes, "raw_payload_retained": false}
				if !writeSSE(stream, flusher, enc, strconv.FormatUint(h.sequence.Add(1), 10), "sync_critical", critical) {
					return
				}
			}
		case message, open := <-eventCh:
			if !open {
				return
			}
			var event struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(message, &event) != nil || event.Type == "" {
				continue
			}
			if !writeSSERaw(stream, flusher, strconv.FormatUint(h.sequence.Add(1), 10), event.Type, message) {
				return
			}
		case <-keepalive.C:
			// Keep transport freshness and source disconnect detection live from
			// the in-memory canonical store. Do not execute the full snapshot
			// callback here: it can query remote metadata and would multiply that
			// work by every connected browser once per second.
			heartbeat := h.CanonicalSnapshot()
			if !writeSSE(stream, flusher, enc, strconv.FormatUint(h.sequence.Add(1), 10), "sync_heartbeat", heartbeat) {
				return
			}
		}
	}
}

func (h *StreamHub) serveSSE(w http.ResponseWriter, r *http.Request, events *wsrelay.Hub) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	id, ch, ok := h.Subscribe()
	if !ok {
		http.Error(w, "subscriber limit reached", http.StatusServiceUnavailable)
		return
	}
	defer h.Unsubscribe(id)
	var eventID uint64
	var eventCh <-chan []byte
	if events != nil {
		var subscribed bool
		eventID, eventCh, subscribed = events.SubscribeSafe()
		if !subscribed {
			http.Error(w, "subscriber limit reached", http.StatusServiceUnavailable)
			return
		}
		defer events.UnsubscribeSafe(eventID)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	stream := newSSEDeadlineWriter(w, 30*time.Second)
	enc := json.NewEncoder(stream)
	if !writeSSE(stream, flusher, enc, strconv.FormatUint(h.sequence.Add(1), 10), "snapshot", map[string]any{"files": h.Snapshot()}) {
		return
	}
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case sample, open := <-ch:
			if !open {
				return
			}
			if !writeSSE(stream, flusher, enc, strconv.FormatUint(h.sequence.Add(1), 10), "telemetry", sample) {
				return
			}
		case message, open := <-eventCh:
			if !open {
				return
			}
			var event struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(message, &event) != nil || event.Type == "" {
				continue
			}
			if !writeSSERaw(stream, flusher, strconv.FormatUint(h.sequence.Add(1), 10), event.Type, message) {
				return
			}
		case <-keepalive.C:
			if _, err := io.WriteString(stream, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// sseDeadlineWriter refreshes the connection write deadline for each frame.
// net/http's Server.WriteTimeout is an absolute per-request deadline, which is
// appropriate for bounded API responses but otherwise severs healthy SSE
// sessions at the timeout boundary. A bounded rolling deadline keeps streams
// alive while still evicting a subscriber that stops reading.
type sseDeadlineWriter struct {
	w          io.Writer
	controller *http.ResponseController
	timeout    time.Duration
}

func newSSEDeadlineWriter(w http.ResponseWriter, timeout time.Duration) *sseDeadlineWriter {
	return &sseDeadlineWriter{w: w, controller: http.NewResponseController(w), timeout: timeout}
}

func (w *sseDeadlineWriter) Write(payload []byte) (int, error) {
	if err := w.controller.SetWriteDeadline(time.Now().Add(w.timeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	return w.w.Write(payload)
}

func writeSSE(w io.Writer, flusher http.Flusher, enc *json.Encoder, id, event string, sample any) bool {
	if _, err := io.WriteString(w, "id: "+id+"\nevent: "+event+"\ndata: "); err != nil {
		return false
	}
	if err := enc.Encode(sample); err != nil {
		return false
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func writeSSERaw(w io.Writer, flusher http.Flusher, id, event string, payload []byte) bool {
	if _, err := io.WriteString(w, "id: "+id+"\nevent: "+event+"\ndata: "); err != nil {
		return false
	}
	if _, err := w.Write(payload); err != nil {
		return false
	}
	if _, err := io.WriteString(w, "\n\n"); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// Compatibility handlers are intentionally unauthenticated only when called
// directly in unit tests. Production routing performs authorization first.
func (tc *TelemetryController) PostTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	record, err := tc.Decode(r.Body)
	if err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "too large") {
			code = http.StatusRequestEntityTooLarge
		}
		jsonError(w, err.Error(), code)
		return
	}
	stored, err := tc.Store(record)
	if err != nil {
		jsonError(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	jsonOK(w, map[string]any{"status": "accepted", "event_id": stored.EventID})
}
