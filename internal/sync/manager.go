package sync

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/protocol"
)

const (
	StatusNoDesired    = "no_desired_state"
	StatusSynced       = "synced"
	StatusDrifted      = "drifted"
	StatusPartial      = "partial"
	StatusDisconnected = "disconnected"
	StatusConflict     = "conflict"
	StatusIncompatible = "incompatible"
)

type Config struct {
	HeartbeatTimeout      time.Duration
	PartitionConfirmAfter time.Duration
	BaseRetryDelay        time.Duration
	MaxRetryDelay         time.Duration
	LeaseDuration         time.Duration
	MaxRetries            int
	MinimumClientVersion  string
	RequiredCapabilities  []string
	TrustedManifestKeys   map[string]ed25519.PublicKey
}

type SyncIssue struct {
	Type        string    `json:"type"`
	Description string    `json:"description"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	Count       int       `json:"count"`
	Severity    string    `json:"severity"`
}

type SyncConflict struct {
	Type              string    `json:"type"`
	ClientManifestID  string    `json:"client_manifest_id"`
	ClientRevision    uint64    `json:"client_revision"`
	DesiredManifestID string    `json:"desired_manifest_id"`
	DesiredRevision   uint64    `json:"desired_revision"`
	DetectedAt        time.Time `json:"detected_at"`
	Resolution        string    `json:"resolution"`
}

type CommandState struct {
	Command       protocol.SyncCommand        `json:"command"`
	Status        string                      `json:"status"` // pending, leased, completed, failed
	NextAttemptAt time.Time                   `json:"next_attempt_at,omitempty"`
	LastResult    *protocol.SyncCommandStatus `json:"last_result,omitempty"`
}

type SyncState struct {
	NodeID            string                                `json:"node_id"`
	ClientVersion     string                                `json:"client_version"`
	LastHeartbeat     time.Time                             `json:"last_heartbeat"`
	LastPartitionAt   time.Time                             `json:"last_partition_at,omitempty"`
	LastRecoveryAt    time.Time                             `json:"last_recovery_at,omitempty"`
	NetworkPartition  bool                                  `json:"network_partition"`
	SyncStatus        string                                `json:"sync_status"`
	Capabilities      protocol.NodeCapabilities             `json:"capabilities"`
	Desired           protocol.HashState                    `json:"desired"`
	Active            protocol.HashState                    `json:"active"`
	Staged            protocol.HashState                    `json:"staged"`
	Rollback          protocol.HashState                    `json:"rollback"`
	ManifestID        string                                `json:"manifest_id,omitempty"`
	ManifestRevision  uint64                                `json:"manifest_revision"`
	PendingCommands   map[string]*CommandState              `json:"pending_commands"`
	CompletedCommands map[string]protocol.SyncCommandStatus `json:"completed_commands"`
	Conflict          *SyncConflict                         `json:"conflict,omitempty"`
	Issues            []SyncIssue                           `json:"issues"`
	Metrics           protocol.Metrics                      `json:"metrics"`
}

type SyncManager struct {
	mu      sync.RWMutex
	config  Config
	clients map[string]*SyncState
	desired *protocol.ImmutableManifest
	digest  string
}

func NewSyncManager(config Config) *SyncManager {
	if config.HeartbeatTimeout <= 0 {
		config.HeartbeatTimeout = 90 * time.Second
	}
	if config.PartitionConfirmAfter <= 0 {
		config.PartitionConfirmAfter = 10 * time.Minute
	}
	if config.BaseRetryDelay <= 0 {
		config.BaseRetryDelay = time.Second
	}
	if config.MaxRetryDelay <= 0 {
		config.MaxRetryDelay = 5 * time.Minute
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 2 * time.Minute
	}
	if config.MaxRetries <= 0 {
		config.MaxRetries = 5
	}
	if config.MinimumClientVersion == "" {
		config.MinimumClientVersion = "20.0.0"
	}
	return &SyncManager{config: config, clients: map[string]*SyncState{}}
}

func (manager *SyncManager) SetDesiredManifest(manifest protocol.ImmutableManifest, now time.Time) error {
	if err := manifest.Verify(manager.config.TrustedManifestKeys, now); err != nil {
		return err
	}
	digest, err := manifest.Digest()
	if err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.desired != nil {
		if manifest.Revision < manager.desired.Revision {
			return fmt.Errorf("manifest revision regression: %d < %d", manifest.Revision, manager.desired.Revision)
		}
		if manifest.Revision == manager.desired.Revision && digest != manager.digest {
			return fmt.Errorf("manifest conflict at revision %d", manifest.Revision)
		}
	}
	copyOfManifest := manifest
	manager.desired, manager.digest = &copyOfManifest, digest
	for _, state := range manager.clients {
		state.Desired = manifest.Desired
		state.ManifestID = manifest.ID
		state.ManifestRevision = manifest.Revision
		manager.rebuildCommandsLocked(state)
	}
	return nil
}

func (manager *SyncManager) Reconcile(request protocol.SyncRequest, now time.Time) (protocol.SyncResponse, error) {
	if strings.TrimSpace(request.NodeID) == "" {
		return protocol.SyncResponse{}, fmt.Errorf("node_id is required")
	}
	if err := request.Active.Validate(); err != nil {
		return protocol.SyncResponse{}, err
	}
	if err := request.Staged.Validate(); err != nil {
		return protocol.SyncResponse{}, err
	}
	if err := request.Rollback.Validate(); err != nil {
		return protocol.SyncResponse{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := manager.clientLocked(request.NodeID)
	manager.recordHeartbeatLocked(state, now)
	state.ClientVersion = request.Capabilities.ClientVersion
	state.Capabilities = request.Capabilities
	state.Active, state.Staged, state.Rollback = request.Active, request.Staged, request.Rollback

	response := protocol.SyncResponse{MinimumClient: manager.config.MinimumClientVersion}
	minimumClient := manager.config.MinimumClientVersion
	if manager.desired != nil {
		if comparison, compareErr := protocol.CompareSemver(manager.desired.MinimumClientVersion, minimumClient); compareErr == nil && comparison > 0 {
			minimumClient = manager.desired.MinimumClientVersion
		}
	}
	response.MinimumClient = minimumClient
	comparison, versionErr := protocol.CompareSemver(request.Capabilities.ClientVersion, minimumClient)
	if versionErr != nil || comparison < 0 || request.Capabilities.ProtocolVersion != protocol.SyncProtocolVersion {
		state.SyncStatus = StatusIncompatible
		response.Status = StatusIncompatible
		return response, nil
	}
	missing := manager.missingCapabilities(request.Capabilities)
	if len(missing) > 0 {
		state.SyncStatus = StatusIncompatible
		response.Status, response.MissingCapabilities = StatusIncompatible, missing
		return response, nil
	}
	if manager.desired == nil {
		state.SyncStatus, response.Status = StatusNoDesired, StatusNoDesired
		return response, nil
	}
	manifest := *manager.desired
	response.Manifest = &manifest
	if request.LastManifestRevision > manifest.Revision || (request.LastManifestRevision == manifest.Revision && request.LastManifestID != "" && request.LastManifestID != manifest.ID) {
		state.Conflict = &SyncConflict{
			Type: "manifest_revision", ClientManifestID: request.LastManifestID, ClientRevision: request.LastManifestRevision,
			DesiredManifestID: manifest.ID, DesiredRevision: manifest.Revision, DetectedAt: now, Resolution: "manual_review",
		}
		state.SyncStatus, response.Status = StatusConflict, StatusConflict
		return response, nil
	}
	state.Conflict = nil
	state.Desired, state.ManifestID, state.ManifestRevision = manifest.Desired, manifest.ID, manifest.Revision
	if request.Active.Equal(manifest.Desired) && request.LastManifestID == manifest.ID && request.LastManifestRevision == manifest.Revision {
		state.SyncStatus, response.Status = StatusSynced, StatusSynced
		state.PendingCommands = map[string]*CommandState{}
		return response, nil
	}
	if !request.Staged.Equal(manifest.Desired) {
		delete(state.CompletedCommands, commandID(&manifest, "stage"))
		delete(state.CompletedCommands, commandID(&manifest, "validate"))
		delete(state.CompletedCommands, commandID(&manifest, "activate"))
	}
	manager.rebuildCommandsLocked(state)
	state.SyncStatus, response.Status = StatusDrifted, StatusDrifted
	response.Commands = manager.commandSnapshotLocked(state)
	return response, nil
}

func (manager *SyncManager) LeaseNext(nodeID string, now time.Time) (protocol.SyncCommand, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state, exists := manager.clients[nodeID]
	if !exists || state.NetworkPartition {
		return protocol.SyncCommand{}, false
	}
	ids := sortedCommandIDs(state.PendingCommands)
	for _, id := range ids {
		entry := state.PendingCommands[id]
		if entry.Status == "completed" || entry.Status == "failed" || now.Before(entry.NextAttemptAt) {
			continue
		}
		if entry.Status == "leased" && entry.Command.LeaseUntil.After(now) {
			continue
		}
		if !prerequisitesComplete(state, entry.Command.Prerequisites) {
			continue
		}
		entry.Status = "leased"
		entry.Command.Attempt++
		entry.Command.LeaseUntil = now.Add(manager.config.LeaseDuration)
		return entry.Command, true
	}
	return protocol.SyncCommand{}, false
}

func (manager *SyncManager) CompleteCommand(status protocol.SyncCommandStatus, now time.Time) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state, exists := manager.clients[status.NodeID]
	if !exists {
		return fmt.Errorf("node %q is unknown", status.NodeID)
	}
	entry, exists := state.PendingCommands[status.CommandID]
	if !exists {
		if completed, ok := state.CompletedCommands[status.CommandID]; ok {
			if completed.Status == status.Status && completed.ManifestID == status.ManifestID && completed.Revision == status.Revision {
				return nil
			}
			return fmt.Errorf("conflicting duplicate command result %q", status.CommandID)
		}
		return fmt.Errorf("command %q is unknown", status.CommandID)
	}
	if entry.Command.ManifestID != status.ManifestID || entry.Command.Revision != status.Revision {
		return fmt.Errorf("command manifest correlation failed")
	}
	status.CompletedAt = now
	entry.LastResult = &status
	if status.Status == "failed" {
		if entry.Command.Attempt >= manager.config.MaxRetries {
			entry.Status = "failed"
			state.SyncStatus = StatusPartial
			manager.addIssueLocked(state, "retry_exhausted", "command "+status.CommandID+" exhausted retries", "critical", now)
			return nil
		}
		entry.Status = "pending"
		entry.Command.LeaseUntil = time.Time{}
		entry.NextAttemptAt = now.Add(manager.retryDelay(entry.Command.Attempt))
		state.SyncStatus = StatusPartial
		return nil
	}
	valid := map[string]string{"staged": "stage", "validated": "validate", "activated": "activate", "rolled_back": "rollback"}
	if valid[status.Status] != entry.Command.Type {
		return fmt.Errorf("status %q is invalid for %s command", status.Status, entry.Command.Type)
	}
	entry.Status = "completed"
	state.CompletedCommands[status.CommandID] = status
	delete(state.PendingCommands, status.CommandID)
	switch status.Status {
	case "staged":
		state.Staged = status.State
	case "activated":
		state.Rollback, state.Active, state.Staged = state.Active, status.State, protocol.HashState{}
	case "rolled_back":
		state.Active, state.Staged = status.State, protocol.HashState{}
	}
	if state.Active.Equal(state.Desired) && len(state.PendingCommands) == 0 {
		state.SyncStatus = StatusSynced
	} else {
		state.SyncStatus = StatusPartial
	}
	return nil
}

func (manager *SyncManager) ProcessHeartbeat(request *protocol.HeartbeatRequest) error {
	if request == nil || request.NodeID == "" {
		return fmt.Errorf("valid heartbeat is required")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := manager.clientLocked(request.NodeID)
	manager.recordHeartbeatLocked(state, time.Now().UTC())
	state.ClientVersion = request.Version
	state.Active.ConfigHash = request.ConfigHash
	state.Metrics = request.Metrics
	if manager.desired == nil {
		state.SyncStatus = StatusNoDesired
	} else if state.Active.Equal(manager.desired.Desired) {
		state.SyncStatus = StatusSynced
	} else {
		state.SyncStatus = StatusDrifted
	}
	for _, issue := range request.Issues {
		manager.addIssueLocked(state, "client_reported", issue, "warning", state.LastHeartbeat)
	}
	return nil
}

func (manager *SyncManager) CheckPartitionsAt(now time.Time) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, state := range manager.clients {
		age := now.Sub(state.LastHeartbeat)
		if !state.LastHeartbeat.IsZero() && age >= manager.config.PartitionConfirmAfter {
			if !state.NetworkPartition {
				state.NetworkPartition = true
				state.LastPartitionAt = now
				manager.addIssueLocked(state, "network_partition", fmt.Sprintf("heartbeat absent for %s", age.Round(time.Second)), "critical", now)
			}
			state.SyncStatus = StatusDisconnected
		} else if age >= manager.config.HeartbeatTimeout && state.SyncStatus == StatusSynced {
			state.SyncStatus = StatusPartial
		}
	}
}

func (manager *SyncManager) DetectPartitions(ctx context.Context) {
	interval := manager.config.HeartbeatTimeout / 2
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			manager.CheckPartitionsAt(now.UTC())
		}
	}
}

// ResolveSyncConflicts applies controller authority only when the client's
// revision is older. Same/newer divergent revisions remain manual conflicts.
func (manager *SyncManager) ResolveSyncConflicts(_ context.Context) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.desired == nil {
		return
	}
	for _, state := range manager.clients {
		if state.Conflict == nil || state.Conflict.ClientRevision >= manager.desired.Revision {
			continue
		}
		state.Conflict.Resolution = "controller_wins"
		state.Conflict = nil
		state.Desired = manager.desired.Desired
		state.ManifestID = manager.desired.ID
		state.ManifestRevision = manager.desired.Revision
		manager.rebuildCommandsLocked(state)
		state.SyncStatus = StatusDrifted
	}
}

func (manager *SyncManager) MarkCommandPending(nodeID, commandID string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := manager.clientLocked(nodeID)
	if _, exists := state.PendingCommands[commandID]; !exists {
		state.PendingCommands[commandID] = &CommandState{Command: protocol.SyncCommand{ID: commandID, Type: "legacy", Idempotent: true}, Status: "pending"}
	}
	state.SyncStatus = StatusPartial
}

func (manager *SyncManager) MarkCommandCompleted(nodeID, commandID string, success bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state, exists := manager.clients[nodeID]
	if !exists {
		return
	}
	entry, exists := state.PendingCommands[commandID]
	if !exists {
		return
	}
	if success {
		delete(state.PendingCommands, commandID)
		if len(state.PendingCommands) == 0 {
			state.SyncStatus = StatusSynced
		}
		return
	}
	entry.Command.Attempt++
	entry.NextAttemptAt = time.Now().UTC().Add(manager.retryDelay(entry.Command.Attempt))
	entry.Status = "pending"
	state.SyncStatus = StatusPartial
}

func (manager *SyncManager) GetClientSyncState(nodeID string) (*SyncState, bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	state, exists := manager.clients[nodeID]
	if !exists {
		return nil, false
	}
	copyOfState := cloneState(state)
	return &copyOfState, true
}

func (manager *SyncManager) GetSyncState() map[string]*SyncState {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := make(map[string]*SyncState, len(manager.clients))
	for id, state := range manager.clients {
		copyOfState := cloneState(state)
		result[id] = &copyOfState
	}
	return result
}

type SyncMetrics struct {
	TotalClients       int `json:"total_clients"`
	SyncedClients      int `json:"synced_clients"`
	DriftedClients     int `json:"drifted_clients"`
	PartitionedClients int `json:"partitioned_clients"`
	ConflictClients    int `json:"conflict_clients"`
	PendingCommands    int `json:"pending_commands"`
	ExhaustedCommands  int `json:"exhausted_commands"`
}

func (manager *SyncManager) GetSyncMetrics() SyncMetrics {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	metrics := SyncMetrics{TotalClients: len(manager.clients)}
	for _, state := range manager.clients {
		switch state.SyncStatus {
		case StatusSynced:
			metrics.SyncedClients++
		case StatusDrifted, StatusPartial:
			metrics.DriftedClients++
		case StatusDisconnected:
			metrics.PartitionedClients++
		case StatusConflict:
			metrics.ConflictClients++
		}
		for _, command := range state.PendingCommands {
			if command.Status == "failed" {
				metrics.ExhaustedCommands++
			} else {
				metrics.PendingCommands++
			}
		}
	}
	return metrics
}

func (manager *SyncManager) clientLocked(nodeID string) *SyncState {
	state, exists := manager.clients[nodeID]
	if exists {
		return state
	}
	state = &SyncState{
		NodeID: nodeID, SyncStatus: StatusNoDesired, PendingCommands: map[string]*CommandState{},
		CompletedCommands: map[string]protocol.SyncCommandStatus{}, Issues: []SyncIssue{},
	}
	manager.clients[nodeID] = state
	return state
}

func (manager *SyncManager) recordHeartbeatLocked(state *SyncState, now time.Time) {
	previous := state.LastHeartbeat
	if state.NetworkPartition {
		state.NetworkPartition = false
		state.LastRecoveryAt = now
		duration := time.Duration(0)
		if !previous.IsZero() {
			duration = now.Sub(previous)
		}
		manager.addIssueLocked(state, "partition_recovery", fmt.Sprintf("node reconnected after %s", duration.Round(time.Second)), "info", now)
	}
	state.LastHeartbeat = now
}

func (manager *SyncManager) rebuildCommandsLocked(state *SyncState) {
	if manager.desired == nil || state.Active.Equal(manager.desired.Desired) {
		state.PendingCommands = map[string]*CommandState{}
		return
	}
	manifest := manager.desired
	commands := []protocol.SyncCommand{}
	if !state.Staged.Equal(manifest.Desired) {
		commands = append(commands, protocol.SyncCommand{ID: commandID(manifest, "stage"), ManifestID: manifest.ID, Revision: manifest.Revision, Type: "stage", Idempotent: true})
	}
	stageID := commandID(manifest, "stage")
	if state.Staged.Equal(manifest.Desired) {
		stageID = ""
	}
	validate := protocol.SyncCommand{ID: commandID(manifest, "validate"), ManifestID: manifest.ID, Revision: manifest.Revision, Type: "validate", Idempotent: true}
	if stageID != "" {
		validate.Prerequisites = []string{stageID}
	}
	commands = append(commands, validate)
	commands = append(commands, protocol.SyncCommand{
		ID: commandID(manifest, "activate"), ManifestID: manifest.ID, Revision: manifest.Revision, Type: "activate",
		Prerequisites: []string{validate.ID}, Idempotent: true,
	})
	preserved := state.PendingCommands
	state.PendingCommands = map[string]*CommandState{}
	for _, command := range commands {
		if completed, exists := state.CompletedCommands[command.ID]; exists && completed.Revision == command.Revision {
			continue
		}
		if existing, exists := preserved[command.ID]; exists {
			state.PendingCommands[command.ID] = existing
		} else {
			state.PendingCommands[command.ID] = &CommandState{Command: command, Status: "pending"}
		}
	}
}

func (manager *SyncManager) commandSnapshotLocked(state *SyncState) []protocol.SyncCommand {
	ids := sortedCommandIDs(state.PendingCommands)
	commands := make([]protocol.SyncCommand, 0, len(ids))
	for _, id := range ids {
		commands = append(commands, state.PendingCommands[id].Command)
	}
	return commands
}

func (manager *SyncManager) missingCapabilities(capabilities protocol.NodeCapabilities) []string {
	required := append([]string(nil), manager.config.RequiredCapabilities...)
	if manager.desired != nil {
		required = append(required, manager.desired.RequiredCapabilities...)
	}
	seen := map[string]bool{}
	missing := []string{}
	for _, capability := range required {
		if seen[capability] {
			continue
		}
		seen[capability] = true
		if !capabilities.SupportsCommand(capability) {
			missing = append(missing, capability)
		}
	}
	sort.Strings(missing)
	return missing
}

func (manager *SyncManager) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := manager.config.BaseRetryDelay
	for index := 1; index < attempt && delay < manager.config.MaxRetryDelay; index++ {
		if delay > manager.config.MaxRetryDelay/2 {
			return manager.config.MaxRetryDelay
		}
		delay *= 2
	}
	if delay > manager.config.MaxRetryDelay {
		return manager.config.MaxRetryDelay
	}
	return delay
}

func (manager *SyncManager) addIssueLocked(state *SyncState, kind, description, severity string, now time.Time) {
	for index := range state.Issues {
		issue := &state.Issues[index]
		if issue.Type == kind && issue.Description == description {
			issue.Count++
			issue.LastSeen = now
			return
		}
	}
	state.Issues = append(state.Issues, SyncIssue{Type: kind, Description: description, Severity: severity, Count: 1, FirstSeen: now, LastSeen: now})
}

func commandID(manifest *protocol.ImmutableManifest, kind string) string {
	return fmt.Sprintf("%s:%d:%s", manifest.ID, manifest.Revision, kind)
}

func sortedCommandIDs(commands map[string]*CommandState) []string {
	order := map[string]int{"stage": 0, "validate": 1, "activate": 2, "rollback": 3, "legacy": 4}
	ids := make([]string, 0, len(commands))
	for id := range commands {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := commands[ids[i]].Command, commands[ids[j]].Command
		if order[left.Type] != order[right.Type] {
			return order[left.Type] < order[right.Type]
		}
		return ids[i] < ids[j]
	})
	return ids
}

func prerequisitesComplete(state *SyncState, prerequisites []string) bool {
	for _, id := range prerequisites {
		if _, exists := state.CompletedCommands[id]; !exists {
			return false
		}
	}
	return true
}

func cloneState(state *SyncState) SyncState {
	copyOfState := *state
	copyOfState.Issues = append([]SyncIssue(nil), state.Issues...)
	copyOfState.PendingCommands = make(map[string]*CommandState, len(state.PendingCommands))
	for id, command := range state.PendingCommands {
		copyOfCommand := *command
		if command.LastResult != nil {
			copyOfResult := *command.LastResult
			copyOfCommand.LastResult = &copyOfResult
		}
		copyOfState.PendingCommands[id] = &copyOfCommand
	}
	copyOfState.CompletedCommands = make(map[string]protocol.SyncCommandStatus, len(state.CompletedCommands))
	for id, status := range state.CompletedCommands {
		copyOfState.CompletedCommands[id] = status
	}
	if state.Conflict != nil {
		copyOfConflict := *state.Conflict
		copyOfState.Conflict = &copyOfConflict
	}
	return copyOfState
}

// QueueRollback targets the signed rollback generation from the current
// manifest. The command remains idempotent and uses the same lease/retry path.
func (manager *SyncManager) QueueRollback(nodeID string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.desired == nil || manager.desired.Rollback.Empty() {
		return fmt.Errorf("signed rollback generation is unavailable")
	}
	state, exists := manager.clients[nodeID]
	if !exists {
		return fmt.Errorf("node %q is unknown", nodeID)
	}
	id := commandID(manager.desired, "rollback")
	if _, completed := state.CompletedCommands[id]; completed {
		return nil
	}
	state.PendingCommands = map[string]*CommandState{}
	state.PendingCommands[id] = &CommandState{Command: protocol.SyncCommand{
		ID: id, ManifestID: manager.desired.ID, Revision: manager.desired.Revision,
		Type: "rollback", Idempotent: true,
	}, Status: "pending"}
	state.Desired = manager.desired.Rollback
	state.SyncStatus = StatusPartial
	return nil
}
