// Package protocol defines the JSON wire contract between NLI clients and the
// controller. It contains data only and deliberately has no transport logic.
package protocol

import (
	"encoding/json"
	"nexora.local/nli-v20/telemetry"
	"time"
)

const APIVersion = "v1"

type RegisterRequest struct {
	ID         string            `json:"id"`
	Hostname   string            `json:"hostname"`
	IP         string            `json:"ip,omitempty"`
	OS         string            `json:"os"`
	Arch       string            `json:"arch"`
	Version    string            `json:"version"`
	ConfigHash string            `json:"config_hash"`
	MetricsURL string            `json:"metrics_url,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type RegisterResponse struct {
	Status    string `json:"status"`
	NodeID    string `json:"node_id"`
	NodeToken string `json:"node_token"`
}

type Metrics struct {
	Received                    uint64  `json:"logs_received"`
	Parsed                      uint64  `json:"logs_parsed"`
	Quarantined                 uint64  `json:"logs_quarantined"`
	Errors                      uint64  `json:"errors"`
	ParseRate                   float64 `json:"parse_rate_pct,omitempty"`
	ParseLatencyMs              float64 `json:"parse_latency_ms,omitempty"`
	CPUPercent                  float64 `json:"cpu_percent,omitempty"`
	MemoryBytes                 uint64  `json:"memory_bytes,omitempty"`
	MemoryPercent               float64 `json:"memory_percent,omitempty"`
	Load1                       float64 `json:"load_1m,omitempty"`
	Load5                       float64 `json:"load_5m,omitempty"`
	Load15                      float64 `json:"load_15m,omitempty"`
	HostMetricsAvailable        bool    `json:"host_metrics_available"`
	UPESMetricsAvailable        bool    `json:"upes_metrics_available"`
	UPESUniqueFailureSignatures uint64  `json:"upes_unique_failure_signatures,omitempty"`
	UPESObservedFailures        uint64  `json:"upes_observed_failures,omitempty"`
	UPESDiscoveryQueued         uint64  `json:"upes_discovery_queued,omitempty"`
	UPESDiscoveryDropped        uint64  `json:"upes_discovery_dropped,omitempty"`
	UPESDiscoveryPending        uint64  `json:"upes_discovery_pending,omitempty"`
}

type HeartbeatRequest struct {
	NodeID                string                          `json:"node_id"`
	Version               string                          `json:"version"`
	ConfigHash            string                          `json:"config_hash"`
	Metrics               Metrics                         `json:"metrics"`
	Health                string                          `json:"health"`
	Issues                []string                        `json:"issues,omitempty"`
	Parsers               []ParserInventory               `json:"parsers,omitempty"`
	ActiveAssetBundleHash string                          `json:"active_asset_bundle_hash,omitempty"`
	ActiveAssetRevisions  map[string]int                  `json:"active_asset_revisions,omitempty"`
	StreamFiles           []telemetry.StreamFileTelemetry `json:"stream_files,omitempty"`
}

// StreamTelemetryRequest carries only bounded source counters on a faster
// cadence than the full node heartbeat.
type StreamTelemetryRequest = telemetry.SyncBatch

type ParserInventory struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
	TestStatus  string `json:"test_status,omitempty"`
}

type Command struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   time.Time       `json:"created_at"`
	LeaseUntil  time.Time       `json:"lease_until"`
	Attempt     int             `json:"attempt"`
	RequestedBy string          `json:"requested_by"`
}

type CommandResult struct {
	NodeID     string    `json:"node_id"`
	CommandID  string    `json:"command_id"`
	Status     string    `json:"status"` // completed or failed
	Message    string    `json:"message,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	ConfigHash string    `json:"config_hash,omitempty"`
}

type EventBatch struct {
	NodeID string            `json:"node_id"`
	Events []json.RawMessage `json:"events"`
}
