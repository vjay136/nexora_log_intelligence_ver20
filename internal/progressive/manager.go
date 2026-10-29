package progressive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/protocol"
)

const (
	RolloutPending          = "pending"
	RolloutRunning          = "running"
	RolloutAwaitingApproval = "awaiting_approval"
	RolloutPaused           = "paused"
	RolloutCompleted        = "completed"
	RolloutFailed           = "failed"
	RolloutCancelled        = "cancelled"
	RolloutRolledBack       = "rolled_back"

	NodePending    = "pending"
	NodeRunning    = "running"
	NodeSucceeded  = "succeeded"
	NodeFailed     = "failed"
	NodeRolledBack = "rolled_back"
)

type Node struct {
	ID   string            `json:"id"`
	Tags map[string]string `json:"tags,omitempty"`
}

type Selector struct {
	NodeIDs    []string          `json:"node_ids,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	ExcludeIDs []string          `json:"exclude_ids,omitempty"`
}

type Wave struct {
	Name             string  `json:"name"`
	Percent          float64 `json:"percent"`
	MinimumNodes     int     `json:"minimum_nodes,omitempty"`
	Remaining        bool    `json:"remaining,omitempty"`
	ApprovalRequired bool    `json:"approval_required,omitempty"`
}

type Strategy struct {
	Name               string  `json:"name"`
	Waves              []Wave  `json:"waves"`
	MaximumFailureRate float64 `json:"maximum_failure_rate"`
	AutoRollback       bool    `json:"auto_rollback"`
}

func CanaryStrategy() Strategy {
	return Strategy{Name: "canary", MaximumFailureRate: 5, AutoRollback: true, Waves: []Wave{
		{Name: "canary", Percent: 5, MinimumNodes: 1},
		{Name: "early", Percent: 20, ApprovalRequired: true},
		{Name: "main", Percent: 25},
		{Name: "final", Remaining: true},
	}}
}

func BlueGreenStrategy() Strategy {
	return Strategy{Name: "blue_green", MaximumFailureRate: 0, AutoRollback: true, Waves: []Wave{
		{Name: "green", Percent: 100},
		{Name: "traffic_switch", Percent: 0, ApprovalRequired: true},
	}}
}

type WaveState struct {
	Wave        Wave       `json:"wave"`
	Status      string     `json:"status"`
	TargetNodes []string   `json:"target_nodes"`
	ApprovedBy  string     `json:"approved_by,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type NodeResult struct {
	NodeID      string        `json:"node_id"`
	Wave        string        `json:"wave"`
	Status      string        `json:"status"`
	Latency     time.Duration `json:"latency,omitempty"`
	Message     string        `json:"message,omitempty"`
	StartedAt   *time.Time    `json:"started_at,omitempty"`
	CompletedAt *time.Time    `json:"completed_at,omitempty"`
}

type Rollout struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Strategy      Strategy               `json:"strategy"`
	Selector      Selector               `json:"selector"`
	Desired       protocol.HashState     `json:"desired"`
	Previous      protocol.HashState     `json:"previous"`
	Active        protocol.HashState     `json:"active"`
	Status        string                 `json:"status"`
	CurrentWave   int                    `json:"current_wave"`
	Waves         []WaveState            `json:"waves"`
	NodeResults   map[string]*NodeResult `json:"node_results"`
	Metrics       RolloutMetrics         `json:"metrics"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
	CompletedAt   *time.Time             `json:"completed_at,omitempty"`
	FailureReason string                 `json:"failure_reason,omitempty"`
}

type Option func(*ProgressiveManager)

func WithStateFile(path string) Option {
	return func(manager *ProgressiveManager) { manager.path = path }
}

func WithNodes(nodes []Node) Option {
	return func(manager *ProgressiveManager) {
		for _, node := range nodes {
			manager.nodes[node.ID] = cloneNode(node)
		}
	}
}

type ProgressiveManager struct {
	mu       sync.RWMutex
	path     string
	nodes    map[string]Node
	rollouts map[string]*Rollout
	now      func() time.Time
}

func NewProgressiveManager(options ...Option) *ProgressiveManager {
	manager := &ProgressiveManager{nodes: map[string]Node{}, rollouts: map[string]*Rollout{}, now: func() time.Time { return time.Now().UTC() }}
	for _, option := range options {
		option(manager)
	}
	manager.load()
	return manager
}

func (manager *ProgressiveManager) SetNodes(nodes []Node) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.nodes = map[string]Node{}
	for _, node := range nodes {
		manager.nodes[node.ID] = cloneNode(node)
	}
}

func (manager *ProgressiveManager) Create(id, name string, strategy Strategy, selector Selector, desired, previous protocol.HashState) (*Rollout, error) {
	if id == "" || name == "" {
		return nil, fmt.Errorf("rollout id and name are required")
	}
	if err := validateStrategy(strategy); err != nil {
		return nil, err
	}
	if err := desired.Validate(); err != nil || desired.Empty() {
		return nil, fmt.Errorf("valid desired state is required")
	}
	if err := previous.Validate(); err != nil {
		return nil, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.rollouts[id]; exists {
		return nil, fmt.Errorf("rollout %q already exists", id)
	}
	now := manager.now()
	rollout := &Rollout{
		ID: id, Name: name, Strategy: strategy, Selector: selector, Desired: desired, Previous: previous,
		Active: previous, Status: RolloutPending, CurrentWave: -1, NodeResults: map[string]*NodeResult{}, CreatedAt: now, UpdatedAt: now,
	}
	manager.rollouts[id] = rollout
	if err := manager.saveLocked(); err != nil {
		delete(manager.rollouts, id)
		return nil, err
	}
	return cloneRollout(rollout), nil
}

func (manager *ProgressiveManager) Start(id string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	rollout, exists := manager.rollouts[id]
	if !exists {
		return fmt.Errorf("rollout %q not found", id)
	}
	if rollout.Status != RolloutPending {
		return fmt.Errorf("rollout cannot start from %s", rollout.Status)
	}
	targets := manager.selectNodesLocked(rollout.Selector)
	if len(targets) == 0 {
		return fmt.Errorf("rollout has no eligible nodes")
	}
	rollout.Waves = allocateWaves(targets, rollout.Strategy.Waves)
	for _, wave := range rollout.Waves {
		for _, nodeID := range wave.TargetNodes {
			rollout.NodeResults[nodeID] = &NodeResult{NodeID: nodeID, Wave: wave.Wave.Name, Status: NodePending}
		}
	}
	rollout.CurrentWave = 0
	manager.startCurrentWaveLocked(rollout)
	rollout.UpdatedAt = manager.now()
	return manager.saveLocked()
}

func (manager *ProgressiveManager) Approve(id, approver string) error {
	if approver == "" {
		return fmt.Errorf("approver is required")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	rollout, exists := manager.rollouts[id]
	if !exists || rollout.Status != RolloutAwaitingApproval || rollout.CurrentWave < 0 {
		return fmt.Errorf("rollout is not awaiting approval")
	}
	wave := &rollout.Waves[rollout.CurrentWave]
	wave.ApprovedBy = approver
	manager.beginWaveLocked(rollout, wave)
	if len(wave.TargetNodes) == 0 {
		manager.completeWaveLocked(rollout, wave)
		manager.advanceLocked(rollout)
	}
	rollout.UpdatedAt = manager.now()
	return manager.saveLocked()
}

func (manager *ProgressiveManager) Pause(id string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	rollout, exists := manager.rollouts[id]
	if !exists || (rollout.Status != RolloutRunning && rollout.Status != RolloutAwaitingApproval) {
		return fmt.Errorf("rollout cannot be paused")
	}
	rollout.Status, rollout.UpdatedAt = RolloutPaused, manager.now()
	return manager.saveLocked()
}

func (manager *ProgressiveManager) Resume(id string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	rollout, exists := manager.rollouts[id]
	if !exists || rollout.Status != RolloutPaused {
		return fmt.Errorf("rollout is not paused")
	}
	wave := &rollout.Waves[rollout.CurrentWave]
	if wave.Wave.ApprovalRequired && wave.ApprovedBy == "" {
		rollout.Status = RolloutAwaitingApproval
	} else {
		rollout.Status = RolloutRunning
	}
	rollout.UpdatedAt = manager.now()
	return manager.saveLocked()
}

func (manager *ProgressiveManager) Cancel(id string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	rollout, exists := manager.rollouts[id]
	if !exists || terminalStatus(rollout.Status) {
		return fmt.Errorf("rollout cannot be cancelled")
	}
	now := manager.now()
	rollout.Status, rollout.CompletedAt, rollout.UpdatedAt = RolloutCancelled, &now, now
	return manager.saveLocked()
}

func (manager *ProgressiveManager) Rollback(id, reason string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	rollout, exists := manager.rollouts[id]
	if !exists || rollout.Previous.Empty() {
		return fmt.Errorf("rollback generation is unavailable")
	}
	now := manager.now()
	for _, result := range rollout.NodeResults {
		if result.Status == NodeSucceeded || result.Status == NodeRunning {
			result.Status, result.CompletedAt, result.Message = NodeRolledBack, &now, reason
		}
	}
	rollout.Active = rollout.Previous
	rollout.Status, rollout.CompletedAt, rollout.UpdatedAt = RolloutRolledBack, &now, now
	rollout.FailureReason = reason
	rollout.Metrics = calculateMetrics(rollout.NodeResults)
	return manager.saveLocked()
}

func (manager *ProgressiveManager) RecordNodeResult(id, nodeID, status string, latency time.Duration, message string) error {
	if status != NodeSucceeded && status != NodeFailed {
		return fmt.Errorf("terminal node status must be succeeded or failed")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	rollout, exists := manager.rollouts[id]
	if !exists || rollout.Status != RolloutRunning {
		return fmt.Errorf("rollout is not running")
	}
	result, exists := rollout.NodeResults[nodeID]
	if !exists || result.Wave != rollout.Waves[rollout.CurrentWave].Wave.Name {
		return fmt.Errorf("node is not targeted by the current wave")
	}
	if result.Status == status {
		return nil
	}
	if result.Status != NodeRunning {
		return fmt.Errorf("invalid node transition from %s", result.Status)
	}
	now := manager.now()
	result.Status, result.Latency, result.Message, result.CompletedAt = status, latency, message, &now
	rollout.Metrics = calculateMetrics(rollout.NodeResults)
	wave := &rollout.Waves[rollout.CurrentWave]
	if waveTerminal(wave, rollout.NodeResults) {
		failed, total := waveFailures(wave, rollout.NodeResults), len(wave.TargetNodes)
		failureRate := float64(0)
		if total > 0 {
			failureRate = 100 * float64(failed) / float64(total)
		}
		if failureRate > rollout.Strategy.MaximumFailureRate {
			wave.Status, rollout.Status = RolloutFailed, RolloutFailed
			rollout.FailureReason = fmt.Sprintf("wave %s failure rate %.2f%% exceeds %.2f%%", wave.Wave.Name, failureRate, rollout.Strategy.MaximumFailureRate)
			rollout.UpdatedAt = now
			if rollout.Strategy.AutoRollback && !rollout.Previous.Empty() {
				for _, item := range rollout.NodeResults {
					if item.Status == NodeSucceeded {
						item.Status, item.CompletedAt = NodeRolledBack, &now
					}
				}
				rollout.Active, rollout.Status = rollout.Previous, RolloutRolledBack
				rollout.CompletedAt = &now
			}
		} else {
			manager.completeWaveLocked(rollout, wave)
			manager.advanceLocked(rollout)
		}
	}
	rollout.Metrics = calculateMetrics(rollout.NodeResults)
	rollout.UpdatedAt = now
	return manager.saveLocked()
}

func (manager *ProgressiveManager) Get(id string) (*Rollout, bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	rollout, exists := manager.rollouts[id]
	if !exists {
		return nil, false
	}
	return cloneRollout(rollout), true
}

func (manager *ProgressiveManager) List() []*Rollout {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := make([]*Rollout, 0, len(manager.rollouts))
	for _, rollout := range manager.rollouts {
		result = append(result, cloneRollout(rollout))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func (manager *ProgressiveManager) startCurrentWaveLocked(rollout *Rollout) {
	wave := &rollout.Waves[rollout.CurrentWave]
	if wave.Wave.ApprovalRequired && wave.ApprovedBy == "" {
		wave.Status, rollout.Status = RolloutAwaitingApproval, RolloutAwaitingApproval
		return
	}
	manager.beginWaveLocked(rollout, wave)
	if len(wave.TargetNodes) == 0 {
		manager.completeWaveLocked(rollout, wave)
		manager.advanceLocked(rollout)
	}
}

func (manager *ProgressiveManager) beginWaveLocked(rollout *Rollout, wave *WaveState) {
	now := manager.now()
	wave.Status, wave.StartedAt, rollout.Status = RolloutRunning, &now, RolloutRunning
	for _, nodeID := range wave.TargetNodes {
		result := rollout.NodeResults[nodeID]
		result.Status, result.StartedAt = NodeRunning, &now
	}
}

func (manager *ProgressiveManager) completeWaveLocked(_ *Rollout, wave *WaveState) {
	now := manager.now()
	wave.Status, wave.CompletedAt = RolloutCompleted, &now
}

func (manager *ProgressiveManager) advanceLocked(rollout *Rollout) {
	for {
		rollout.CurrentWave++
		if rollout.CurrentWave >= len(rollout.Waves) {
			now := manager.now()
			rollout.Status, rollout.Active, rollout.CompletedAt = RolloutCompleted, rollout.Desired, &now
			return
		}
		wave := &rollout.Waves[rollout.CurrentWave]
		if wave.Wave.ApprovalRequired && wave.ApprovedBy == "" {
			wave.Status, rollout.Status = RolloutAwaitingApproval, RolloutAwaitingApproval
			return
		}
		manager.beginWaveLocked(rollout, wave)
		if len(wave.TargetNodes) > 0 {
			return
		}
		manager.completeWaveLocked(rollout, wave)
	}
}

func (manager *ProgressiveManager) selectNodesLocked(selector Selector) []string {
	excluded := make(map[string]bool, len(selector.ExcludeIDs))
	for _, id := range selector.ExcludeIDs {
		excluded[id] = true
	}
	allowed := map[string]bool{}
	for _, id := range selector.NodeIDs {
		allowed[id] = true
	}
	result := []string{}
	for id, node := range manager.nodes {
		if excluded[id] || (len(allowed) > 0 && !allowed[id]) || !matchesTags(node.Tags, selector.Tags) {
			continue
		}
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func allocateWaves(nodes []string, definitions []Wave) []WaveState {
	remaining := append([]string(nil), nodes...)
	states := make([]WaveState, 0, len(definitions))
	for index, definition := range definitions {
		count := 0
		if definition.Remaining || index == len(definitions)-1 {
			count = len(remaining)
		} else if definition.Percent > 0 {
			count = int((float64(len(nodes))*definition.Percent + 99.999999) / 100)
		}
		if count < definition.MinimumNodes && len(remaining) > 0 {
			count = definition.MinimumNodes
		}
		if count > len(remaining) {
			count = len(remaining)
		}
		targets := append([]string(nil), remaining[:count]...)
		remaining = remaining[count:]
		states = append(states, WaveState{Wave: definition, Status: RolloutPending, TargetNodes: targets})
	}
	return states
}

func validateStrategy(strategy Strategy) error {
	if strategy.Name == "" || len(strategy.Waves) == 0 || strategy.MaximumFailureRate < 0 || strategy.MaximumFailureRate > 100 {
		return fmt.Errorf("invalid rollout strategy")
	}
	seen := map[string]bool{}
	for _, wave := range strategy.Waves {
		if wave.Name == "" || seen[wave.Name] || wave.Percent < 0 || wave.Percent > 100 || wave.MinimumNodes < 0 {
			return fmt.Errorf("invalid rollout wave %q", wave.Name)
		}
		seen[wave.Name] = true
	}
	return nil
}

func matchesTags(actual, required map[string]string) bool {
	for key, value := range required {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func waveTerminal(wave *WaveState, results map[string]*NodeResult) bool {
	for _, id := range wave.TargetNodes {
		if result := results[id]; result == nil || (result.Status != NodeSucceeded && result.Status != NodeFailed) {
			return false
		}
	}
	return true
}

func waveFailures(wave *WaveState, results map[string]*NodeResult) int {
	failed := 0
	for _, id := range wave.TargetNodes {
		if results[id].Status == NodeFailed {
			failed++
		}
	}
	return failed
}

func terminalStatus(status string) bool {
	return status == RolloutCompleted || status == RolloutFailed || status == RolloutCancelled || status == RolloutRolledBack
}

func cloneNode(node Node) Node {
	copyOfNode := node
	copyOfNode.Tags = map[string]string{}
	for key, value := range node.Tags {
		copyOfNode.Tags[key] = value
	}
	return copyOfNode
}

func cloneRollout(rollout *Rollout) *Rollout {
	data, _ := json.Marshal(rollout)
	var copyOfRollout Rollout
	_ = json.Unmarshal(data, &copyOfRollout)
	return &copyOfRollout
}

func (manager *ProgressiveManager) load() {
	if manager.path == "" {
		return
	}
	data, err := os.ReadFile(manager.path)
	if err != nil {
		return
	}
	var rollouts []*Rollout
	if json.Unmarshal(data, &rollouts) != nil {
		return
	}
	for _, rollout := range rollouts {
		manager.rollouts[rollout.ID] = rollout
	}
}

func (manager *ProgressiveManager) saveLocked() error {
	if manager.path == "" {
		return nil
	}
	items := make([]*Rollout, 0, len(manager.rollouts))
	for _, rollout := range manager.rollouts {
		items = append(items, rollout)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(manager.path), 0o750); err != nil {
		return err
	}
	temporary := manager.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o640); err != nil {
		return err
	}
	return os.Rename(temporary, manager.path)
}
