package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/auditlog"
	"nexora.local/nli-v20/internal/middleware"
	metastore "nexora.local/nli-v20/internal/store"
)

const (
	notificationConfigCollection   = "notification-config"
	notificationAckCollection      = "notification-acks"
	notificationDeliveryCollection = "notification-deliveries"
)

var envNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,127}$`)

// handleCommunicationStatus describes the evidence available for each hop in
// the client -> controller -> browser path. A successful request proves the
// controller answered this request; it does not prove browser rendering.
func (s *Server) handleCommunicationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	now := time.Now().UTC()
	clients := make([]map[string]any, 0)
	for _, node := range s.fleet.All() {
		age := now.Sub(node.LastSeen)
		if age < 0 {
			age = 0
		}
		state := "stale"
		if !node.LastSeen.IsZero() && age <= 30*time.Second && node.Status == "online" {
			state = "live"
		}
		clients = append(clients, map[string]any{"node_id": node.ID, "hostname": node.Hostname, "state": state, "last_seen": nullableTime(node.LastSeen), "age_seconds": age.Seconds(), "heartbeat_status": state})
	}
	webapp := map[string]any{"state": "unavailable", "status": "no_browser_heartbeat", "observed_at": nil, "age_seconds": nil, "evidence": "The browser must post /api/v1/communications/browser-heartbeat to prove it is receiving the controller."}
	s.browserHeartbeatMu.RLock()
	heartbeatAt, heartbeatUser := s.browserHeartbeatAt, s.browserHeartbeatUser
	s.browserHeartbeatMu.RUnlock()
	if !heartbeatAt.IsZero() {
		age := now.Sub(heartbeatAt)
		state := "stale"
		if age <= 30*time.Second && age >= 0 {
			state = "live"
		}
		webapp = map[string]any{"state": state, "status": state, "observed_at": heartbeatAt, "age_seconds": maxFloat(age.Seconds(), 0), "username": heartbeatUser, "evidence": "Authenticated browser heartbeat observed in controller memory; it resets after controller restart."}
	}
	snapshot := s.streamTelemetry.CanonicalSnapshot()
	jsonOK(w, map[string]any{
		"generated_at": now,
		"path": []map[string]any{
			{"hop": "client_to_controller", "state": aggregateClientState(clients), "evidence": "Registered client heartbeat last_seen", "clients": clients, "client_count": len(clients)},
			{"hop": "controller_internal", "state": "live", "evidence": "This authenticated status request was served by controller", "observed_at": now},
			{"hop": "controller_to_webapp", "state": webapp["state"], "observed_at": webapp["observed_at"], "evidence": webapp["evidence"], "browser": webapp},
		},
		"canonical": map[string]any{"revision": snapshot.Revision, "generated_at": snapshot.GeneratedAt, "schema_version": snapshot.SchemaVersion, "target_interval_ms": snapshot.TargetIntervalMS},
	})
}

func (s *Server) handleBrowserCommunicationHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	principal := s.authorize(w, r, middleware.PermissionReadTelemetry)
	if principal == nil {
		return
	}
	now := time.Now().UTC()
	s.browserHeartbeatMu.Lock()
	s.browserHeartbeatAt, s.browserHeartbeatUser = now, principal.Username
	s.browserHeartbeatMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	jsonOK(w, map[string]any{"status": "recorded", "observed_at": now})
}

type notificationChannelConfig struct {
	Enabled        bool   `json:"enabled"`
	SecretEnv      string `json:"secret_env,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`
	SenderEnv      string `json:"sender_env,omitempty"`
	RecipientEnv   string `json:"recipient_env,omitempty"`
	ServerEnv      string `json:"server_env,omitempty"`
	UsernameEnv    string `json:"username_env,omitempty"`
	PasswordEnv    string `json:"password_env,omitempty"`
	DeliveryStatus string `json:"delivery_status,omitempty"`
}

type notificationPolicy struct {
	Channels      map[string]notificationChannelConfig `json:"channels"`
	AdminFallback bool                                 `json:"admin_fallback"`
	Recipients    []string                             `json:"recipients"`
	Events        []string                             `json:"events"`
	UpdatedBy     string                               `json:"updated_by,omitempty"`
	UpdatedAt     time.Time                            `json:"updated_at,omitempty"`
}

func defaultNotificationPolicy() notificationPolicy {
	return notificationPolicy{
		Channels: map[string]notificationChannelConfig{
			"in_app":    {Enabled: true, DeliveryStatus: "available"},
			"email":     {Enabled: false, DeliveryStatus: "not_configured"},
			"pagerduty": {Enabled: false, DeliveryStatus: "not_configured"},
		},
		AdminFallback: true,
		Recipients:    []string{},
		Events:        []string{"approval", "quarantine", "sync", "system", "delivery", "security"},
	}
}

func (s *Server) handleNotificationConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if s.authorize(w, r, middleware.PermissionReadTelemetry) == nil {
			return
		}
		policy := s.readNotificationPolicy(r)
		jsonOK(w, map[string]any{"policy": policy, "channels": s.notificationChannelStatus(policy)})
	case http.MethodPut:
		principal := s.authorize(w, r, middleware.PermissionManageUsers)
		if principal == nil {
			return
		}
		var policy notificationPolicy
		if err := decodeJSON(r, &policy, 32<<10); err != nil || !validateNotificationPolicy(policy) {
			jsonError(w, "invalid notification policy: supported channels are in_app, email, pagerduty; configure environment variable names, not secret values", http.StatusBadRequest)
			return
		}
		policy.UpdatedBy, policy.UpdatedAt = principal.Username, time.Now().UTC()
		payload, _ := json.Marshal(policy)
		if _, err := s.metadata.Put(r.Context(), metastore.Document{Collection: notificationConfigCollection, ID: "default", State: "active", Payload: payload, CreatedBy: principal.Username}); err != nil {
			jsonError(w, "notification policy persistence failed", http.StatusServiceUnavailable)
			return
		}
		s.auditAction(principal, "notification.policy.update", "notifications/default", "success", map[string]string{"channels": strings.Join(enabledChannelNames(policy), ",")})
		jsonOK(w, map[string]any{"policy": policy, "channels": s.notificationChannelStatus(policy)})
	default:
		jsonError(w, "GET or PUT required", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleNotificationFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	principal := s.authorize(w, r, middleware.PermissionReadTelemetry)
	if principal == nil {
		return
	}
	limit := 100
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 && value <= 500 {
		limit = value
	}
	policy := s.readNotificationPolicy(r)
	acknowledged := map[uint64]bool{}
	acks, _ := s.metadata.List(r.Context(), notificationAckCollection, metastore.Query{Limit: 1000})
	for _, ack := range acks {
		var payload struct {
			Username string `json:"username"`
			Sequence uint64 `json:"sequence"`
		}
		if json.Unmarshal(ack.Payload, &payload) == nil && payload.Username == principal.Username {
			acknowledged[payload.Sequence] = true
		}
	}
	items := make([]map[string]any, 0, limit)
	for _, event := range s.audit.List(1000) {
		category, severity, actionable := classifyNotificationEvent(event)
		if !actionable || !policyWatches(policy, category) || !notificationRecipient(policy, principal.Username, principal.Role, severity) {
			continue
		}
		items = append(items, map[string]any{"id": fmt.Sprintf("audit-%d", event.Sequence), "sequence": event.Sequence, "timestamp": event.Timestamp, "category": category, "severity": severity, "title": notificationTitle(event, category), "action": event.Action, "resource": event.Resource, "outcome": event.Outcome, "actor": event.Actor, "acknowledged": acknowledged[event.Sequence], "source": "controller_audit_chain"})
		if len(items) == limit {
			break
		}
	}
	deliveries, _ := s.metadata.List(r.Context(), notificationDeliveryCollection, metastore.Query{Limit: 500})
	history := make([]map[string]any, 0, len(deliveries))
	for _, doc := range deliveries {
		var attempt notificationDeliveryAttempt
		if json.Unmarshal(doc.Payload, &attempt) == nil {
			history = append(history, map[string]any{"id": attempt.ID, "sequence": attempt.Sequence, "channel": attempt.Channel, "attempt": attempt.Attempt, "status": attempt.Status, "timestamp": attempt.Timestamp, "error": attempt.Error})
		}
	}
	jsonOK(w, map[string]any{"notifications": items, "count": len(items), "delivery_history": map[string]any{"in_app": "derived from persisted audit records", "attempts": history}, "policy": policy})
}

func (s *Server) handleNotificationAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	principal := s.authorize(w, r, middleware.PermissionReadTelemetry)
	if principal == nil {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/notifications/"), "/")
	if !strings.HasPrefix(id, "audit-") {
		jsonError(w, "notification id invalid", http.StatusBadRequest)
		return
	}
	sequence, err := strconv.ParseUint(strings.TrimPrefix(id, "audit-"), 10, 64)
	if err != nil || sequence == 0 {
		jsonError(w, "notification id invalid", http.StatusBadRequest)
		return
	}
	for _, event := range s.audit.List(1000) {
		if event.Sequence != sequence {
			continue
		}
		if _, _, ok := classifyNotificationEvent(event); !ok {
			jsonError(w, "notification not found", http.StatusNotFound)
			return
		}
		payload, _ := json.Marshal(map[string]any{"username": principal.Username, "sequence": sequence, "acknowledged_at": time.Now().UTC()})
		id := safeID(principal.Username + "-" + strconv.FormatUint(sequence, 10))
		if _, err := s.metadata.Put(r.Context(), metastore.Document{Collection: notificationAckCollection, ID: id, State: "acknowledged", Payload: payload, CreatedBy: principal.Username}); err != nil {
			jsonError(w, "notification acknowledgement persistence failed", http.StatusServiceUnavailable)
			return
		}
		jsonOK(w, map[string]any{"status": "acknowledged", "sequence": sequence})
		return
	}
	jsonError(w, "notification not found", http.StatusNotFound)
}

func (s *Server) readNotificationPolicy(r *http.Request) notificationPolicy {
	doc, err := s.metadata.Get(r.Context(), notificationConfigCollection, "default", 0)
	if err != nil {
		return defaultNotificationPolicy()
	}
	var policy notificationPolicy
	if json.Unmarshal(doc.Payload, &policy) != nil || policy.Channels == nil {
		return defaultNotificationPolicy()
	}
	return policy
}

func (s *Server) notificationChannelStatus(policy notificationPolicy) map[string]any {
	result := map[string]any{}
	latest := map[string]notificationDeliveryAttempt{}
	docs, _ := s.metadata.List(context.Background(), notificationDeliveryCollection, metastore.Query{Limit: 500})
	for _, doc := range docs {
		var attempt notificationDeliveryAttempt
		if json.Unmarshal(doc.Payload, &attempt) == nil {
			if current, ok := latest[attempt.Channel]; !ok || attempt.Timestamp.After(current.Timestamp) {
				latest[attempt.Channel] = attempt
			}
		}
	}
	for name, channel := range policy.Channels {
		status := "disabled"
		switch name {
		case "in_app":
			if channel.Enabled {
				status = "available"
			}
		case "email":
			if channel.Enabled && channelConfiguredEmail(channel) {
				status = "ready"
			} else if channel.Enabled {
				status = "configuration_incomplete"
			} else {
				status = "not_configured"
			}
		case "pagerduty":
			if channel.Enabled && envConfigured(channel.SecretEnv) {
				status = "ready"
			} else if channel.Enabled {
				status = "configuration_incomplete"
			} else {
				status = "not_configured"
			}
		default:
			status = "unsupported"
		}
		delivery := "available in the authenticated in-app notification feed"
		if name == "email" {
			delivery = "SMTP over implicit TLS (port 465); accepted only after SMTP DATA succeeds"
		}
		if name == "pagerduty" {
			delivery = "PagerDuty Events API v2; accepted only after success acknowledgement"
		}
		entry := map[string]any{"enabled": channel.Enabled, "status": status, "state": status, "delivery": delivery}
		if attempt, ok := latest[name]; ok {
			entry["last_attempt"] = attempt
			if attempt.Status == "delivered" {
				entry["status"], entry["state"] = "delivered", "delivered"
			}
			if attempt.Status == "failed" {
				entry["status"], entry["state"] = "delivery_failed", "delivery_failed"
			}
		}
		result[name] = entry
	}
	return result
}

func validateNotificationPolicy(policy notificationPolicy) bool {
	if len(policy.Recipients) > 100 || len(policy.Events) > 20 || len(policy.Channels) > 3 {
		return false
	}
	for _, recipient := range policy.Recipients {
		if len(recipient) > 128 || strings.TrimSpace(recipient) == "" {
			return false
		}
	}
	for _, event := range policy.Events {
		if !oneOf(event, "approval", "quarantine", "sync", "system", "delivery", "security") {
			return false
		}
	}
	for name, channel := range policy.Channels {
		if !oneOf(name, "in_app", "email", "pagerduty") {
			return false
		}
		for _, value := range []string{channel.SecretEnv, channel.SenderEnv, channel.RecipientEnv, channel.ServerEnv, channel.UsernameEnv, channel.PasswordEnv} {
			if value != "" && !envNamePattern.MatchString(value) {
				return false
			}
		}
		if (channel.UsernameEnv == "") != (channel.PasswordEnv == "") {
			return false
		}
		if len(channel.Endpoint) > 512 {
			return false
		}
		if name == "pagerduty" && channel.Endpoint != "" && channel.Endpoint != "https://events.pagerduty.com/v2/enqueue" {
			return false
		}
	}
	return policy.Channels["in_app"].Enabled
}

func classifyNotificationEvent(event auditlog.Event) (string, string, bool) {
	action := strings.ToLower(event.Action)
	category := "system"
	if strings.Contains(action, "approval") || strings.Contains(action, "rollout.approve") {
		category = "approval"
	}
	if strings.Contains(action, "quarantine") || strings.Contains(action, "reprocess") {
		category = "quarantine"
	}
	if strings.Contains(action, "sync") || strings.Contains(action, "heartbeat") || strings.Contains(action, "telemetry") {
		category = "sync"
	}
	if strings.Contains(action, "delivery") || strings.Contains(action, "endpoint") {
		category = "delivery"
	}
	if strings.Contains(action, "login") || strings.Contains(action, "auth") || strings.Contains(action, "user.") {
		category = "security"
	}
	if event.Outcome == "failure" || event.Outcome == "partial_failure" || event.Outcome == "local_created_sync_failed" {
		return category, "critical", true
	}
	if category != "system" || event.Outcome != "success" {
		return category, "warning", true
	}
	return "", "", false
}

func notificationTitle(event auditlog.Event, category string) string {
	verb := strings.ReplaceAll(strings.ReplaceAll(event.Action, ".", " "), "_", " ")
	if event.Outcome == "failure" || event.Outcome == "partial_failure" {
		return strings.Title(category) + " issue: " + verb
	}
	return strings.Title(category) + " activity: " + verb
}

func policyWatches(policy notificationPolicy, category string) bool {
	for _, event := range policy.Events {
		if event == category {
			return true
		}
	}
	return false
}

func notificationRecipient(policy notificationPolicy, username, role, severity string) bool {
	for _, recipient := range policy.Recipients {
		if strings.EqualFold(strings.TrimSpace(recipient), username) {
			return true
		}
	}
	return policy.AdminFallback && role == "admin" && (severity == "critical" || len(policy.Recipients) == 0)
}

func enabledChannelNames(policy notificationPolicy) []string {
	values := make([]string, 0, len(policy.Channels))
	for name, channel := range policy.Channels {
		if channel.Enabled {
			values = append(values, name)
		}
	}
	return values
}

func envConfigured(name string) bool { return name != "" && strings.TrimSpace(os.Getenv(name)) != "" }

func aggregateClientState(clients []map[string]any) string {
	if len(clients) == 0 {
		return "idle_no_registered_clients"
	}
	for _, client := range clients {
		if client["state"] == "live" {
			return "live"
		}
	}
	return "stale"
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func maxFloat(value, floor float64) float64 {
	if value < floor {
		return floor
	}
	return value
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
