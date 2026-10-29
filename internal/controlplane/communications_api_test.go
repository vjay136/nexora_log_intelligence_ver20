package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/auditlog"
	metastore "nexora.local/nli-v20/internal/store"
)

func TestCommunicationStatusUsesObservedEvidence(t *testing.T) {
	f := newAPIFixture(t)
	response, body := f.request(http.MethodGet, "/api/v1/communications/status", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("communication status %d: %s", response.StatusCode, body)
	}
	status := decodeMap(t, body)
	path, ok := status["path"].([]any)
	if !ok || len(path) != 3 {
		t.Fatalf("expected three communication hops: %s", body)
	}
	if path[0].(map[string]any)["state"] != "idle_no_registered_clients" {
		t.Fatalf("no registered clients must be idle, not live: %s", body)
	}
	if path[2].(map[string]any)["state"] != "unavailable" {
		t.Fatalf("browser must remain unavailable until heartbeat: %s", body)
	}
	response, body = f.request(http.MethodPost, "/api/v1/communications/browser-heartbeat", map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("browser heartbeat %d: %s", response.StatusCode, body)
	}
	response, body = f.request(http.MethodGet, "/api/v1/communications/status", nil)
	status = decodeMap(t, body)
	path = status["path"].([]any)
	if path[2].(map[string]any)["state"] != "live" {
		t.Fatalf("persisted browser heartbeat not reflected: %s", body)
	}
}

func TestNotificationPolicyAndAuditFeed(t *testing.T) {
	f := newAPIFixture(t)
	policy := map[string]any{
		"channels": map[string]any{
			"in_app":    map[string]any{"enabled": true},
			"email":     map[string]any{"enabled": true, "server_env": "NLI_SMTP_SERVER", "sender_env": "NLI_SMTP_FROM", "recipient_env": "NLI_SMTP_TO"},
			"pagerduty": map[string]any{"enabled": true, "secret_env": "NLI_PAGERDUTY_KEY", "endpoint": "https://events.pagerduty.com/v2/enqueue"},
		},
		"admin_fallback": true,
		"recipients":     []string{},
		"events":         []string{"approval", "quarantine", "sync", "system", "delivery", "security"},
	}
	response, body := f.request(http.MethodPut, "/api/v1/notifications/config", policy)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save notification policy %d: %s", response.StatusCode, body)
	}
	if !json.Valid(body) {
		t.Fatalf("notification policy response is invalid JSON: %s", body)
	}
	response, body = f.request(http.MethodGet, "/api/v1/notifications/config", nil)
	if response.StatusCode != http.StatusOK || !json.Valid(body) {
		t.Fatalf("read notification policy %d: %s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), "configuration_incomplete") {
		t.Fatalf("missing environment values must remain visibly incomplete: %s", body)
	}
	t.Setenv("NLI_SMTP_FROM", "nli@example.test")
	t.Setenv("NLI_SMTP_TO", "ops@example.test")
	t.Setenv("NLI_SMTP_SERVER", "smtp.example.test:465")
	t.Setenv("NLI_PAGERDUTY_KEY", "test-routing-key")
	response, body = f.request(http.MethodGet, "/api/v1/notifications/config", nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"ready"`) {
		t.Fatalf("configured external channels must be ready without claiming delivery: %s", body)
	}
	if strings.Contains(string(body), "test-routing-key") {
		t.Fatal("notification config exposed the PagerDuty secret value")
	}
	policy["channels"].(map[string]any)["email"].(map[string]any)["enabled"] = false
	response, body = f.request(http.MethodPut, "/api/v1/notifications/config", policy)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disable live SMTP for isolated PagerDuty test: %s", body)
	}
	var calls int
	f.server.notificationHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != "https://events.pagerduty.com/v2/enqueue" {
			t.Errorf("unexpected PagerDuty URL: %s", req.URL)
		}
		data, _ := io.ReadAll(req.Body)
		if !strings.Contains(string(data), "test-routing-key") {
			t.Errorf("routing key missing from provider request")
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(`{"status":"success"}`)), Header: make(http.Header)}, nil
	})}
	event, err := f.server.audit.Append(auditEventForTest("quarantine.reprocess", "failure"))
	if err != nil {
		t.Fatal(err)
	}
	f.server.dispatchAuditNotification(event)
	waitNotificationAttemptCount(t, f.server, event.Sequence, 1)
	if calls != 1 {
		t.Fatalf("expected one PagerDuty delivery, got %d", calls)
	}
	response, body = f.request(http.MethodGet, "/api/v1/notifications", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("notification feed %d: %s", response.StatusCode, body)
	}
	feed := decodeMap(t, body)
	items, ok := feed["notifications"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("persisted quarantine audit event missing from in-app feed: %s", body)
	}
	item := items[0].(map[string]any)
	if item["category"] != "quarantine" || item["source"] != "controller_audit_chain" {
		t.Fatalf("feed item not tied to canonical audit evidence: %s", body)
	}
	response, body = f.request(http.MethodPost, "/api/v1/notifications/"+item["id"].(string), map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("acknowledge notification %d: %s", response.StatusCode, body)
	}
	response, body = f.request(http.MethodGet, "/api/v1/notifications?limit=100", nil)
	feed = decodeMap(t, body)
	history := feed["delivery_history"].(map[string]any)["attempts"].([]any)
	if len(history) != 2 {
		t.Fatalf("expected persisted job and PagerDuty delivery evidence, got %s", body)
	}
}

func TestNotificationPolicyWriteRequiresAdminPermission(t *testing.T) {
	f := newAPIFixture(t)
	if _, err := f.server.auth.CreateUser("admin", "notify-viewer", "notify-viewer@example.test", "viewer", "Viewer-password-12345!"); err != nil {
		t.Fatal(err)
	}
	f.login("notify-viewer", "Viewer-password-12345!")
	f.refreshCSRF()
	response, body := f.request(http.MethodPut, "/api/v1/notifications/config", defaultNotificationPolicy())
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer notification config write should be forbidden, got %d: %s", response.StatusCode, body)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestPagerDutyRetriesAndDoesNotAuditDeliveryAttempts(t *testing.T) {
	f := newAPIFixture(t)
	t.Setenv("NLI_PD_TEST", "routing-key")
	policy := defaultNotificationPolicy()
	policy.Events = []string{"quarantine"}
	policy.Channels["pagerduty"] = notificationChannelConfig{Enabled: true, SecretEnv: "NLI_PD_TEST"}
	encoded, _ := json.Marshal(policy)
	if _, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: notificationConfigCollection, ID: "default", State: "active", Payload: encoded}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.server.notificationHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		status := http.StatusServiceUnavailable
		body := `{"status":"error"}`
		if calls == 2 {
			status, body = http.StatusAccepted, `{"status":"success"}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	event := auditEventForTest("quarantine.reprocess", "failure")
	event.Sequence = 41
	before := len(f.server.audit.List(1000))
	f.server.dispatchAuditNotification(event)
	waitNotificationAttemptCount(t, f.server, event.Sequence, 2)
	if calls != 2 {
		t.Fatalf("expected one retry after rejection, calls=%d", calls)
	}
	if after := len(f.server.audit.List(1000)); after != before {
		t.Fatalf("delivery attempt recursively appended audit events: before=%d after=%d", before, after)
	}
	docs, err := f.server.metadata.List(context.Background(), notificationDeliveryCollection, metastore.Query{Limit: 10})
	if err != nil || len(docs) != 3 {
		t.Fatalf("expected the queued job and both attempts persisted: docs=%d err=%v", len(docs), err)
	}
}

func TestNotificationBusinessPathDoesNotWaitForProvider(t *testing.T) {
	f := newAPIFixture(t)
	t.Setenv("NLI_PD_ASYNC", "routing-key")
	policy := defaultNotificationPolicy()
	policy.Events = []string{"quarantine"}
	policy.Channels["pagerduty"] = notificationChannelConfig{Enabled: true, SecretEnv: "NLI_PD_ASYNC"}
	encoded, _ := json.Marshal(policy)
	if _, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: notificationConfigCollection, ID: "default", State: "active", Payload: encoded}); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	f.server.notificationHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		close(started)
		<-release
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(`{"status":"success"}`)), Header: make(http.Header)}, nil
	})}
	event := auditEventForTest("quarantine.reprocess", "failure")
	event.Sequence = 42
	start := time.Now()
	f.server.dispatchAuditNotification(event)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("business path waited %s to enqueue delivery", elapsed)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start provider request")
	}
	close(release)
	waitNotificationAttemptCount(t, f.server, event.Sequence, 1)
}

func waitNotificationAttemptCount(t *testing.T, server *Server, sequence uint64, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		docs, err := server.metadata.List(context.Background(), notificationDeliveryCollection, metastore.Query{Limit: 20})
		if err == nil {
			attempts := 0
			for _, doc := range docs {
				var item notificationDeliveryAttempt
				if json.Unmarshal(doc.Payload, &item) == nil && item.Sequence == sequence && item.Channel != "dispatch" {
					attempts++
				}
			}
			if attempts >= count {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("delivery attempts for audit sequence %d did not reach %d", sequence, count)
}

func auditEventForTest(action, outcome string) auditlog.Event {
	return auditlog.Event{Action: action, Resource: "test", Outcome: outcome, Actor: "admin"}
}
