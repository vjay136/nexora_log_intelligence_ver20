package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/protocol"
)

const (
	rolloutTypeConfigPush    = "config_push"
	rolloutTypePackageUpdate = "package_update"
	rolloutTypeParserSync    = "parser_sync"
	rolloutTypeRestart       = "restart"
	rolloutTypeReplay        = "replay"
	rolloutStatusPending     = "pending"
	rolloutStatusRunning     = "running"
	rolloutStatusCompleted   = "completed"
	rolloutStatusFailed      = "failed"
	rolloutStatusCancelled   = "cancelled"
)

type rolloutNodeResult struct {
	NodeID     string    `json:"node_id"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	DoneAt     time.Time `json:"done_at,omitempty"`
	LeaseUntil time.Time `json:"lease_until,omitempty"`
	Attempts   int       `json:"attempts"`
	Error      string    `json:"error,omitempty"`
	Message    string    `json:"message,omitempty"`
}
type rolloutRecord struct {
	ID          string                        `json:"id"`
	Type        string                        `json:"type"`
	Name        string                        `json:"name"`
	CreatedBy   string                        `json:"created_by"`
	CreatedAt   time.Time                     `json:"created_at"`
	StartedAt   *time.Time                    `json:"started_at,omitempty"`
	CompletedAt *time.Time                    `json:"completed_at,omitempty"`
	Status      string                        `json:"status"`
	TargetNodes []string                      `json:"target_nodes"`
	Payload     json.RawMessage               `json:"payload"`
	NodeResults map[string]*rolloutNodeResult `json:"node_results"`
	TotalNodes  int                           `json:"total_nodes"`
	DoneNodes   int                           `json:"done_nodes"`
	FailedNodes int                           `json:"failed_nodes"`
}
type rolloutStateManager struct {
	mu    sync.RWMutex
	path  string
	items map[string]*rolloutRecord
}

func newRolloutStateManager(path string) *rolloutStateManager {
	m := &rolloutStateManager{path: path, items: map[string]*rolloutRecord{}}
	data, _ := os.ReadFile(path)
	var items []*rolloutRecord
	if json.Unmarshal(data, &items) == nil {
		for _, item := range items {
			m.items[item.ID] = item
		}
	}
	return m
}
func (m *rolloutStateManager) save() error {
	items := make([]*rolloutRecord, 0, len(m.items))
	for _, item := range m.items {
		items = append(items, item)
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
func (m *rolloutStateManager) Create(kind, name, actor string, targets []string, payload any) (*rolloutRecord, error) {
	if name == "" {
		return nil, errors.New("rollout name required")
	}
	allowed := map[string]bool{rolloutTypeConfigPush: true, rolloutTypePackageUpdate: true, rolloutTypeParserSync: true, rolloutTypeRestart: true, rolloutTypeReplay: true}
	if !allowed[kind] {
		return nil, errors.New("unsupported rollout type")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	item := &rolloutRecord{ID: fmt.Sprintf("rl-%d", time.Now().UnixNano()), Type: kind, Name: name, CreatedBy: actor, CreatedAt: time.Now().UTC(), Status: rolloutStatusPending, TargetNodes: append([]string(nil), targets...), Payload: encoded, NodeResults: map[string]*rolloutNodeResult{}}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[item.ID] = item
	if err = m.save(); err != nil {
		return nil, err
	}
	copy := *item
	return &copy, nil
}
func (m *rolloutStateManager) Start(id string, targets []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil {
		return errors.New("rollout not found")
	}
	if item.Status != rolloutStatusPending {
		return errors.New("rollout is not pending")
	}
	if len(targets) == 0 {
		return errors.New("rollout has no targets")
	}
	now := time.Now().UTC()
	item.StartedAt = &now
	item.Status = rolloutStatusRunning
	item.TotalNodes = len(targets)
	for _, node := range targets {
		item.NodeResults[node] = &rolloutNodeResult{NodeID: node, Status: rolloutStatusPending}
	}
	return m.save()
}
func (m *rolloutStateManager) RecordNodeResult(id, node, status, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil {
		return errors.New("rollout not found")
	}
	result := item.NodeResults[node]
	if result == nil {
		return errors.New("node is not targeted")
	}
	if status != rolloutStatusCompleted && status != rolloutStatusFailed {
		return errors.New("invalid node result")
	}
	if result.Status == rolloutStatusCompleted || result.Status == rolloutStatusFailed {
		return nil
	}
	result.Status = status
	result.Message = message
	result.DoneAt = time.Now().UTC()
	item.DoneNodes++
	if status == rolloutStatusFailed {
		item.FailedNodes++
		result.Error = message
	}
	if item.DoneNodes >= item.TotalNodes {
		now := time.Now().UTC()
		item.CompletedAt = &now
		item.Status = rolloutStatusCompleted
		if item.FailedNodes > 0 {
			item.Status = rolloutStatusFailed
		}
	}
	return m.save()
}
func (m *rolloutStateManager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil {
		return errors.New("rollout not found")
	}
	if item.Status == rolloutStatusCompleted || item.Status == rolloutStatusCancelled {
		return errors.New("rollout already finished")
	}
	item.Status = rolloutStatusCancelled
	return m.save()
}
func (m *rolloutStateManager) Get(id string) (*rolloutRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item := m.items[id]
	if item == nil {
		return nil, false
	}
	copy := *item
	return &copy, true
}
func (m *rolloutStateManager) List() []*rolloutRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*rolloutRecord, 0, len(m.items))
	for _, item := range m.items {
		copy := *item
		out = append(out, &copy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
func (m *rolloutStateManager) NextForNode(node string, lease time.Duration) (*protocol.Command, bool) {
	if lease <= 0 {
		lease = 2 * time.Minute
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	items := make([]*rolloutRecord, 0)
	for _, item := range m.items {
		result := item.NodeResults[node]
		if item.Status == rolloutStatusRunning && result != nil && result.Status != rolloutStatusCompleted && result.Status != rolloutStatusFailed && !(result.Status == rolloutStatusRunning && result.LeaseUntil.After(now)) {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return nil, false
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	item := items[0]
	result := item.NodeResults[node]
	result.Status = rolloutStatusRunning
	result.Attempts++
	if result.StartedAt.IsZero() {
		result.StartedAt = now
	}
	result.LeaseUntil = now.Add(lease)
	_ = m.save()
	return &protocol.Command{ID: item.ID, Type: item.Type, Payload: append(json.RawMessage(nil), item.Payload...), CreatedAt: item.CreatedAt, LeaseUntil: result.LeaseUntil, Attempt: result.Attempts, RequestedBy: item.CreatedBy}, true
}
