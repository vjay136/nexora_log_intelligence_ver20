// Package rollout manages fleet-wide configuration pushes and package updates.
// This is the "CrowdStrike-style" command & control mechanism.
package rollout

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/protocol"
)

// RolloutType constants.
const (
	TypeConfigPush    = "config_push"    // push nli.json config to target nodes
	TypePackageUpdate = "package_update" // trigger RPM upgrade on target nodes
	TypeParserSync    = "parser_sync"    // sync parser definitions to target nodes
	TypeRestart       = "restart"        // restart nli-agent service
	TypeReplay        = "replay"         // trigger quarantine replay
)

// RolloutStatus constants.
const (
	StatusPending    = "pending"
	StatusRunning    = "running"
	StatusPaused     = "paused"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
	StatusRolledBack = "rolled_back"
)

// Rollout is a fleet-wide deployment job.
type Rollout struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Name        string                 `json:"name"`
	CreatedBy   string                 `json:"created_by"`
	CreatedAt   time.Time              `json:"created_at"`
	StartedAt   *time.Time             `json:"started_at,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Status      string                 `json:"status"`
	TargetNodes []string               `json:"target_nodes"`          // node IDs; empty = all
	TargetTags  map[string]string      `json:"target_tags,omitempty"` // e.g. env=prod
	Payload     json.RawMessage        `json:"payload"`               // type-specific data
	ManifestID  string                 `json:"manifest_id,omitempty"`
	Revision    uint64                 `json:"manifest_revision,omitempty"`
	Desired     protocol.HashState     `json:"desired,omitempty"`
	Active      protocol.HashState     `json:"active,omitempty"`
	Rollback    protocol.HashState     `json:"rollback,omitempty"`
	NodeResults map[string]*NodeResult `json:"node_results"`
	TotalNodes  int                    `json:"total_nodes"`
	DoneNodes   int                    `json:"done_nodes"`
	FailedNodes int                    `json:"failed_nodes"`
	Notes       string                 `json:"notes,omitempty"`
}

// NodeResult records the outcome on a specific node.
type NodeResult struct {
	NodeID     string    `json:"node_id"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	DoneAt     time.Time `json:"done_at,omitempty"`
	LeaseUntil time.Time `json:"lease_until,omitempty"`
	Attempts   int       `json:"attempts"`
	Error      string    `json:"error,omitempty"`
	Message    string    `json:"message,omitempty"`
}

// ConfigPayload is used for TypeConfigPush.
type ConfigPayload struct {
	Config json.RawMessage `json:"config"` // nli.json content to push
}

// PackagePayload is used for TypePackageUpdate.
type PackagePayload struct {
	Version     string `json:"version"`      // e.g. "0.4.1"
	DownloadURL string `json:"download_url"` // RPM URL
	Checksum    string `json:"checksum"`     // SHA256
}

// Manager manages rollouts.
type Manager struct {
	mu       sync.RWMutex
	rollouts map[string]*Rollout
	path     string
}

// NewManager creates a Manager.
func NewManager(path string) *Manager {
	m := &Manager{rollouts: make(map[string]*Rollout), path: path}
	m.load()
	return m
}

func (m *Manager) load() {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return
	}
	var rollouts []*Rollout
	if err := json.Unmarshal(data, &rollouts); err != nil {
		return
	}
	for _, r := range rollouts {
		m.rollouts[r.ID] = r
	}
}

func (m *Manager) save() {
	rollouts := make([]*Rollout, 0, len(m.rollouts))
	for _, r := range m.rollouts {
		rollouts = append(rollouts, r)
	}
	data, _ := json.MarshalIndent(rollouts, "", "  ")
	tmp := m.path + ".tmp"
	os.WriteFile(tmp, data, 0640)
	os.Rename(tmp, m.path)
}

// Create creates a new rollout (not yet started).
func (m *Manager) Create(rolloutType, name, createdBy string, targetNodes []string, targetTags map[string]string, payload interface{}) (*Rollout, error) {
	if rolloutType == "" || name == "" {
		return nil, fmt.Errorf("type and name are required")
	}
	switch rolloutType {
	case TypeConfigPush, TypePackageUpdate, TypeParserSync, TypeRestart, TypeReplay:
	default:
		return nil, fmt.Errorf("unsupported rollout type: %s", rolloutType)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	id := fmt.Sprintf("rl-%d", time.Now().UnixNano())
	r := &Rollout{
		ID:          id,
		Type:        rolloutType,
		Name:        name,
		CreatedBy:   createdBy,
		CreatedAt:   time.Now().UTC(),
		Status:      StatusPending,
		TargetNodes: targetNodes,
		TargetTags:  targetTags,
		Payload:     payloadBytes,
		NodeResults: make(map[string]*NodeResult),
	}
	m.mu.Lock()
	m.rollouts[id] = r
	m.save()
	m.mu.Unlock()
	return r, nil
}

// Start marks a rollout as running and assigns target node count.
func (m *Manager) Start(rolloutID string, nodeIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rollouts[rolloutID]
	if !ok {
		return fmt.Errorf("rollout not found: %s", rolloutID)
	}
	if r.Status != StatusPending {
		return fmt.Errorf("rollout is not pending")
	}
	if len(nodeIDs) == 0 {
		return fmt.Errorf("rollout has no eligible target nodes")
	}
	now := time.Now().UTC()
	r.StartedAt = &now
	r.Status = StatusRunning
	r.TotalNodes = len(nodeIDs)
	for _, nid := range nodeIDs {
		r.NodeResults[nid] = &NodeResult{NodeID: nid, Status: StatusPending}
	}
	m.save()
	return nil
}

// RecordNodeResult records the outcome for one node in a rollout.
func (m *Manager) RecordNodeResult(rolloutID, nodeID, status, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rolloutRecord, ok := m.rollouts[rolloutID]
	if !ok {
		return fmt.Errorf("rollout not found: %s", rolloutID)
	}
	if status != StatusCompleted && status != StatusFailed {
		return fmt.Errorf("invalid terminal node status: %s", status)
	}
	result := rolloutRecord.NodeResults[nodeID]
	if result == nil {
		return fmt.Errorf("node %s is not targeted by rollout", nodeID)
	}
	if result.Status == StatusCompleted || result.Status == StatusFailed {
		return nil
	}
	result.Status = status
	result.DoneAt = time.Now().UTC()
	result.Message = message
	if status == StatusFailed {
		result.Error = message
		rolloutRecord.FailedNodes++
	}
	rolloutRecord.DoneNodes++

	if rolloutRecord.DoneNodes >= rolloutRecord.TotalNodes {
		now := time.Now().UTC()
		rolloutRecord.CompletedAt = &now
		if rolloutRecord.FailedNodes == 0 {
			rolloutRecord.Status = StatusCompleted
		} else {
			rolloutRecord.Status = StatusFailed
		}
	}
	m.save()
	return nil
}

// Cancel cancels a pending or running rollout.
func (m *Manager) Cancel(rolloutID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rollouts[rolloutID]
	if !ok {
		return fmt.Errorf("rollout not found")
	}
	if r.Status == StatusCompleted || r.Status == StatusCancelled {
		return fmt.Errorf("cannot cancel a finished rollout")
	}
	r.Status = StatusCancelled
	m.save()
	return nil
}

// List returns all rollouts, newest first (simplified).
func (m *Manager) List() []*Rollout {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Rollout, 0, len(m.rollouts))
	for _, r := range m.rollouts {
		cp := *r
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Get returns a rollout by ID.
func (m *Manager) Get(id string) (*Rollout, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rollouts[id]
	if !ok {
		return nil, false
	}
	cp := *r
	return &cp, true
}

// NextForNode leases the oldest pending command for a node. An unacknowledged
// command becomes eligible again after leaseDuration, providing at-least-once
// delivery; clients must therefore apply commands idempotently.
func (m *Manager) NextForNode(nodeID string, leaseDuration time.Duration) (*protocol.Command, bool) {
	if leaseDuration <= 0 {
		leaseDuration = 2 * time.Minute
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	eligible := make([]*Rollout, 0)
	for _, rolloutRecord := range m.rollouts {
		if rolloutRecord.Status != StatusRunning {
			continue
		}
		result := rolloutRecord.NodeResults[nodeID]
		if result == nil || result.Status == StatusCompleted || result.Status == StatusFailed {
			continue
		}
		if result.Status == StatusRunning && result.LeaseUntil.After(now) {
			continue
		}
		eligible = append(eligible, rolloutRecord)
	}
	if len(eligible) == 0 {
		return nil, false
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].CreatedAt.Before(eligible[j].CreatedAt) })
	selected := eligible[0]
	result := selected.NodeResults[nodeID]
	result.Status = StatusRunning
	if result.StartedAt.IsZero() {
		result.StartedAt = now
	}
	result.Attempts++
	result.LeaseUntil = now.Add(leaseDuration)
	m.save()
	return &protocol.Command{
		ID: selected.ID, Type: selected.Type, Payload: append(json.RawMessage(nil), selected.Payload...),
		CreatedAt: selected.CreatedAt, LeaseUntil: result.LeaseUntil,
		Attempt: result.Attempts, RequestedBy: selected.CreatedBy,
	}, true
}

// CreateSignedManifest verifies the controller signature before creating an
// immutable parser synchronization rollout.
func (m *Manager) CreateSignedManifest(manifest protocol.ImmutableManifest, trustedKeys map[string]ed25519.PublicKey, now time.Time, name, createdBy string, targetNodes []string, targetTags map[string]string) (*Rollout, error) {
	if err := manifest.Verify(trustedKeys, now); err != nil {
		return nil, err
	}
	rolloutRecord, err := m.Create(TypeParserSync, name, createdBy, targetNodes, targetTags, manifest)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stored := m.rollouts[rolloutRecord.ID]
	stored.ManifestID = manifest.ID
	stored.Revision = manifest.Revision
	stored.Desired = manifest.Desired
	stored.Active = manifest.Active
	stored.Rollback = manifest.Rollback
	m.save()
	copyOfStored := *stored
	return &copyOfStored, nil
}

// Pause prevents further command leases while preserving in-flight results.
func (m *Manager) Pause(rolloutID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rolloutRecord, exists := m.rollouts[rolloutID]
	if !exists || rolloutRecord.Status != StatusRunning {
		return fmt.Errorf("rollout is not running")
	}
	rolloutRecord.Status = StatusPaused
	m.save()
	return nil
}

func (m *Manager) Resume(rolloutID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rolloutRecord, exists := m.rollouts[rolloutID]
	if !exists || rolloutRecord.Status != StatusPaused {
		return fmt.Errorf("rollout is not paused")
	}
	rolloutRecord.Status = StatusRunning
	m.save()
	return nil
}

// Rollback closes the rollout on its signed rollback generation.
func (m *Manager) RollbackToPrevious(rolloutID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rolloutRecord, exists := m.rollouts[rolloutID]
	if !exists || rolloutRecord.Rollback.Empty() {
		return fmt.Errorf("rollout rollback generation is unavailable")
	}
	if rolloutRecord.Status == StatusCancelled || rolloutRecord.Status == StatusRolledBack {
		return fmt.Errorf("rollout is already terminal")
	}
	now := time.Now().UTC()
	rolloutRecord.Status = StatusRolledBack
	rolloutRecord.Active = rolloutRecord.Rollback
	rolloutRecord.CompletedAt = &now
	rolloutRecord.Notes = reason
	for _, result := range rolloutRecord.NodeResults {
		if result.Status == StatusRunning || result.Status == StatusCompleted {
			result.Status = StatusRolledBack
			result.DoneAt = now
		}
	}
	m.save()
	return nil
}
