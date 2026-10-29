package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"time"

	"nexora.local/nli-v20/internal/engine"
	"nexora.local/nli-v20/internal/fleet"
	metastore "nexora.local/nli-v20/internal/store"
	"nexora.local/nli-v20/telemetry"
)

func (s *Server) handleSyncSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonOK(w, s.canonicalSyncSnapshot(r.Context()))
}

func (s *Server) canonicalSyncSnapshot(parent context.Context) map[string]any {
	snapshot := s.streamTelemetry.CanonicalSnapshot()
	ctx, cancel := context.WithTimeout(parent, 1500*time.Millisecond)
	defer cancel()
	storageStatus := "ok"
	storageError := ""
	if err := s.metadata.Health(ctx); err != nil {
		storageStatus, storageError = "degraded", storageDiagnostic(err.Error())
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	startedAt := s.startedAt
	if startedAt.IsZero() {
		startedAt = snapshot.GeneratedAt
	}
	uptime := snapshot.GeneratedAt.Sub(startedAt).Seconds()
	if uptime < 0 {
		uptime = 0
	}
	controllerHealth := "ok"
	if storageStatus != "ok" {
		controllerHealth = "degraded"
	}
	auditStatus := "ok"
	if err := s.audit.Verify(); err != nil {
		auditStatus = "tampered"
		controllerHealth = "degraded"
	}
	quarantineStatus := "ok"
	if s.quarantine == nil || s.quarantine.store == nil || s.quarantine.store.Health(ctx) != nil {
		quarantineStatus = "degraded"
		controllerHealth = "degraded"
	}
	controller := map[string]any{"health": controllerHealth, "version": engine.Version, "started_at": startedAt, "uptime_seconds": uptime, "process_memory_alloc_bytes": memory.Alloc, "process_memory_sys_bytes": memory.Sys, "goroutines": runtime.NumGoroutine(), "cpu_count": runtime.NumCPU(), "cpu_percent": nil, "cpu_percent_status": telemetry.FreshnessNotInstrumented, "restart_count": nil, "restart_count_status": telemetry.FreshnessNotInstrumented, "disk_usage_bytes": nil, "disk_usage_status": telemetry.FreshnessNotInstrumented, "network_bytes": nil, "network_status": telemetry.FreshnessNotInstrumented}
	if descriptors, err := os.ReadDir("/proc/self/fd"); err == nil {
		controller["open_file_descriptors"] = len(descriptors)
		controller["open_file_descriptors_status"] = "available"
	} else {
		controller["open_file_descriptors_status"] = "unavailable"
	}
	clientMetrics := make([]map[string]any, 0)
	for _, node := range s.fleet.All() {
		if node.Metrics == nil || !node.Metrics.HostMetricsAvailable {
			continue
		}
		metric := node.Metrics
		clientMetrics = append(clientMetrics, map[string]any{"node_id": node.ID, "hostname": node.Hostname, "updated_at": metric.UpdatedAt, "cpu_percent": metric.CPUPercent, "memory_bytes": metric.MemoryBytes, "memory_percent": metric.MemoryPercent, "load_1m": metric.Load1, "load_5m": metric.Load5, "load_15m": metric.Load15})
	}
	return map[string]any{
		"schema_version":     snapshot.SchemaVersion,
		"revision":           snapshot.Revision,
		"generated_at":       snapshot.GeneratedAt,
		"target_interval_ms": snapshot.TargetIntervalMS,
		"aggregate":          snapshot.Aggregate,
		"sources":            snapshot.Sources,
		"diagnostics":        snapshot.Diagnostics,
		"subsystems":         s.subsystemSyncSnapshot(ctx, snapshot),
		"controller":         controller,
		"infrastructure":     map[string]any{"clients": clientMetrics},
		"storage":            map[string]any{"status": storageStatus, "diagnostic": storageError, "quarantine_status": quarantineStatus, "audit_chain": auditStatus},
	}
}

func (s *Server) subsystemSyncSnapshot(ctx context.Context, snapshot telemetry.SyncSnapshot) map[string]any {
	nhleStatus, nhleUpdatedAt := subsystemFreshness(snapshot.Sources, snapshot.GeneratedAt)
	if len(snapshot.Sources) == 0 {
		nhleStatus, nhleUpdatedAt = "idle", snapshot.GeneratedAt
	}
	activeNodes := map[string]struct{}{}
	for _, source := range snapshot.Sources {
		if source.Freshness == telemetry.FreshnessLive || source.Freshness == telemetry.FreshnessDelayed {
			activeNodes[source.NodeID] = struct{}{}
		}
	}
	nhle := map[string]any{
		"status": nhleStatus, "updated_at": nhleUpdatedAt,
		"source_count": len(snapshot.Sources), "active_nodes": len(activeNodes),
		"records_read_total":    snapshot.Aggregate.SourceRecordsReadTotal,
		"normalized_total":      snapshot.Aggregate.NormalizedRecordsTotal,
		"quarantined_total":     snapshot.Aggregate.QuarantinedRecordsTotal,
		"queue_depth":           snapshot.Aggregate.QueueDepth,
		"backpressure_sources":  snapshot.Aggregate.BackpressureSourceCount,
		"read_lines_per_second": snapshot.Aggregate.ReadLinesPerSecond,
		"normalized_per_second": snapshot.Aggregate.NormalizedPerSecond,
	}
	requests, _, failures, _ := s.aiMetrics.snapshot()
	napi := map[string]any{"status": telemetry.FreshnessCollectionFailed, "state": "unavailable", "updated_at": snapshot.GeneratedAt, "requests_total": requests, "failures_total": failures}
	if docs, err := s.metadata.List(ctx, "ai-proposals", metastore.Query{Limit: 1000}); err == nil {
		pending := 0
		var latest time.Time
		for _, doc := range docs {
			if doc.State == "draft" || doc.State == "review" {
				pending++
			}
			if doc.UpdatedAt.After(latest) {
				latest = doc.UpdatedAt
			}
		}
		napi["pending_drafts"] = pending
		napi["status"] = "ready"
		napi["state"] = "idle"
		if pending > 0 {
			napi["state"] = "ready"
		}
		napi["updated_at"] = snapshot.GeneratedAt
		if !latest.IsZero() {
			napi["latest_proposal_at"] = latest
		}
	} else {
		napi["status"] = telemetry.FreshnessCollectionFailed
		napi["reason"] = "proposal metadata collection failed"
	}
	nal := map[string]any{"status": telemetry.FreshnessCollectionFailed, "state": "unavailable", "updated_at": snapshot.GeneratedAt}
	if docs, err := s.metadata.List(ctx, "assurance-runs", metastore.Query{Limit: 1000}); err == nil {
		counts := map[string]int{}
		var latest time.Time
		for _, doc := range docs {
			state := doc.State
			if state == "" {
				var record assuranceRunRecord
				if json.Unmarshal(doc.Payload, &record) == nil {
					state = record.State
				}
			}
			counts[state]++
			if doc.UpdatedAt.After(latest) {
				latest = doc.UpdatedAt
			}
		}
		nal["total"] = len(docs)
		for _, state := range []string{"queued", "running", "passed", "failed", "cancelled"} {
			nal[state] = counts[state]
		}
		nal["status"] = "ready"
		nal["state"] = "idle"
		if counts["running"] > 0 {
			nal["status"], nal["state"] = "live", "live"
		} else if counts["queued"] > 0 {
			nal["state"] = "ready"
		}
		nal["updated_at"] = snapshot.GeneratedAt
		if !latest.IsZero() {
			nal["latest_run_at"] = latest
		}
	} else {
		nal["status"] = telemetry.FreshnessCollectionFailed
		nal["reason"] = "assurance run metadata collection failed"
	}
	nursfState := "idle"
	if snapshot.Diagnostics.LiveSources > 0 {
		nursfState = "live"
	}
	nursf := map[string]any{"status": "ready", "state": nursfState, "updated_at": snapshot.GeneratedAt, "revision": snapshot.Revision, "target_interval_ms": snapshot.TargetIntervalMS, "tracked_nodes": snapshot.Diagnostics.TrackedNodes, "tracked_sources": snapshot.Diagnostics.TrackedSources, "accepted_batches": snapshot.Diagnostics.AcceptedBatches}
	storeStats := s.events.Stats()
	delivery := map[string]any{"status": "ready", "updated_at": snapshot.GeneratedAt, "acknowledgements_by_destination": storeStats["delivery_acks"], "retry_count": nil, "backlog": nil, "latency_ms": nil, "retry_backlog_latency_status": telemetry.FreshnessNotInstrumented}
	replay := s.replayQueueSnapshot(ctx, snapshot.GeneratedAt)
	release := s.releaseReadinessSnapshot(ctx, snapshot.GeneratedAt)
	return map[string]any{"nhle": nhle, "napi": napi, "nal": nal, "nursf": nursf, "delivery": delivery, "replay": replay, "release_readiness": release, "upes": upesSyncSnapshot(s.fleet.All(), snapshot.GeneratedAt)}
}

func upesSyncSnapshot(nodes []*fleet.Node, now time.Time) map[string]any {
	result := map[string]any{"status": telemetry.FreshnessUnavailable, "updated_at": nil, "clients_total": len(nodes), "clients_reporting": 0, "clients": make([]map[string]any, 0, len(nodes))}
	clients := result["clients"].([]map[string]any)
	var latest time.Time
	var oldest time.Time
	var unique, observed, queued, dropped, pending uint64
	for _, node := range nodes {
		client := map[string]any{"node_id": node.ID, "hostname": node.Hostname, "status": telemetry.FreshnessUnavailable}
		metric := node.Metrics
		if metric != nil && metric.UPESMetricsAvailable {
			age := now.Sub(metric.UpdatedAt)
			clientStatus := telemetry.FreshnessLive
			if metric.UpdatedAt.IsZero() || age > 5*time.Minute {
				clientStatus = telemetry.FreshnessStale
			} else if age > 90*time.Second {
				clientStatus = telemetry.FreshnessDelayed
			}
			client["status"] = clientStatus
			client["updated_at"] = metric.UpdatedAt
			client["unique_failure_signatures"] = metric.UPESUniqueFailureSignatures
			client["observed_failures"] = metric.UPESObservedFailures
			client["discovery_queued"] = metric.UPESDiscoveryQueued
			client["discovery_dropped"] = metric.UPESDiscoveryDropped
			client["discovery_pending"] = metric.UPESDiscoveryPending
			unique += metric.UPESUniqueFailureSignatures
			observed += metric.UPESObservedFailures
			queued += metric.UPESDiscoveryQueued
			dropped += metric.UPESDiscoveryDropped
			pending += metric.UPESDiscoveryPending
			result["clients_reporting"] = result["clients_reporting"].(int) + 1
			if metric.UpdatedAt.After(latest) {
				latest = metric.UpdatedAt
			}
			if oldest.IsZero() || metric.UpdatedAt.Before(oldest) {
				oldest = metric.UpdatedAt
			}
		}
		clients = append(clients, client)
	}
	result["clients"] = clients
	reporting := result["clients_reporting"].(int)
	if reporting == 0 {
		return result
	}
	result["unique_failure_signatures"] = unique
	result["observed_failures"] = observed
	result["discovery_queued"] = queued
	result["discovery_dropped"] = dropped
	result["discovery_pending"] = pending
	result["updated_at"] = latest
	if reporting < len(nodes) {
		result["status"] = "partial"
	} else {
		age := now.Sub(oldest)
		switch {
		case age > 5*time.Minute:
			result["status"] = telemetry.FreshnessStale
		case age > 90*time.Second:
			result["status"] = telemetry.FreshnessDelayed
		default:
			result["status"] = telemetry.FreshnessLive
		}
	}
	return result
}

func (s *Server) replayQueueSnapshot(ctx context.Context, now time.Time) map[string]any {
	result := map[string]any{"status": "ready", "updated_at": now, "queued": 0, "reprocessing": 0, "resolved": 0, "oldest_queued_age_seconds": nil, "attempts_total": 0}
	if s.quarantine == nil || s.quarantine.store == nil {
		result["status"], result["state"] = telemetry.FreshnessUnavailable, "unavailable"
		return result
	}
	docs, err := s.quarantine.store.List(ctx, quarantineRecordsCollection, metastore.Query{Limit: 1000})
	if err != nil {
		result["status"], result["state"], result["reason"] = telemetry.FreshnessCollectionFailed, "unavailable", "quarantine queue collection failed"
		return result
	}
	oldestAge := time.Duration(0)
	var latestAttempt time.Time
	for _, doc := range docs {
		var record quarantineRecord
		if json.Unmarshal(doc.Payload, &record) != nil {
			continue
		}
		switch record.Status {
		case "waiting", "replay_failed":
			result["queued"] = result["queued"].(int) + 1
			age := now.Sub(record.FirstSeen)
			if age < 0 {
				age = 0
			}
			if age > oldestAge {
				oldestAge = age
			}
		case "reprocessing":
			result["reprocessing"] = result["reprocessing"].(int) + 1
		case "resolved":
			result["resolved"] = result["resolved"].(int) + 1
		}
		result["attempts_total"] = result["attempts_total"].(int) + record.AttemptCount
		if record.LastAttempt.After(latestAttempt) {
			latestAttempt = record.LastAttempt
		}
	}
	if result["queued"].(int) > 0 {
		result["oldest_queued_age_seconds"] = oldestAge.Seconds()
		result["state"] = "queued"
	} else {
		result["oldest_queued_age_seconds"] = nil
		result["state"] = "idle"
	}
	if !latestAttempt.IsZero() {
		result["latest_attempt_at"] = latestAttempt
	}
	return result
}

func (s *Server) releaseReadinessSnapshot(ctx context.Context, now time.Time) map[string]any {
	result := map[string]any{"status": "ready", "updated_at": now, "managed_nodes": 0, "release_drifted_nodes": 0, "evidence": "persisted desired and reported active release hashes"}
	docs, err := s.metadata.List(ctx, "fleet-sync", metastore.Query{Limit: 1000})
	if err != nil {
		result["status"], result["state"], result["reason"] = telemetry.FreshnessCollectionFailed, "unavailable", "fleet release evidence collection failed"
		return result
	}
	managed, drifted := 0, 0
	for _, doc := range docs {
		var state map[string]any
		if json.Unmarshal(doc.Payload, &state) != nil {
			continue
		}
		desired, _ := state["desired_release_hash"].(string)
		if desired == "" {
			continue
		}
		managed++
		active, _ := state["active_release_hash"].(string)
		if active != desired {
			drifted++
		}
	}
	result["managed_nodes"], result["release_drifted_nodes"] = managed, drifted
	result["status"], result["state"] = "idle", "no_release_targets"
	if managed > 0 {
		result["status"], result["state"] = "ready", "ready"
		if drifted > 0 {
			result["state"] = "drifted"
			result["status"] = "degraded"
		}
	}
	return result
}

func subsystemFreshness(sources []telemetry.StreamFileTelemetry, now time.Time) (string, any) {
	if len(sources) == 0 {
		return "idle", now
	}
	status := telemetry.FreshnessDisconnected
	var latest time.Time
	rank := map[string]int{telemetry.FreshnessDisconnected: 0, telemetry.FreshnessStale: 1, telemetry.FreshnessDelayed: 2, telemetry.FreshnessLive: 3}
	for _, source := range sources {
		if rank[source.Freshness] > rank[status] {
			status = source.Freshness
		}
		if source.ControllerReceivedAt.After(latest) {
			latest = source.ControllerReceivedAt
		}
	}
	if latest.IsZero() {
		return freshnessFromAge(now.Sub(sources[0].UpdatedAt)), sources[0].UpdatedAt
	}
	return status, latest
}

func freshnessFromAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	switch {
	case age <= 2*time.Second:
		return telemetry.FreshnessLive
	case age <= 5*time.Second:
		return telemetry.FreshnessDelayed
	case age <= 15*time.Second:
		return telemetry.FreshnessStale
	default:
		return telemetry.FreshnessDisconnected
	}
}
