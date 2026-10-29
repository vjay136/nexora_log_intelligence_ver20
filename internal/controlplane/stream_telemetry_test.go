package controlplane

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/auth"
	"nexora.local/nli-v20/internal/fleet"
	"nexora.local/nli-v20/internal/middleware"
	"nexora.local/nli-v20/telemetry"
)

func TestStreamHubSnapshotAndSlowSubscriber(t *testing.T) {
	hub := NewStreamHub(1)
	id, ch, ok := hub.Subscribe()
	if !ok {
		t.Fatal("first subscriber rejected")
	}
	if _, _, ok := hub.Subscribe(); ok {
		t.Fatal("subscriber limit was not enforced")
	}
	base := time.Now().UTC()
	for i := 0; i < 100; i++ {
		hub.Publish(telemetry.StreamFileTelemetry{NodeID: "node", Source: "/a.log", ReadLines: uint64(i), UpdatedAt: base.Add(time.Duration(i) * time.Nanosecond)})
	}
	if got := len(ch); got != 1 {
		t.Fatalf("slow subscriber queue grew to %d", got)
	}
	latest := <-ch
	if latest.ReadLines != 99 {
		t.Fatalf("slow subscriber did not get newest sample: %+v", latest)
	}
	snapshot := hub.Snapshot()
	if len(snapshot) != 1 || snapshot[0].ReadLines != 99 {
		t.Fatalf("snapshot mismatch: %+v", snapshot)
	}
	newer := time.Now().UTC()
	hub.Publish(telemetry.StreamFileTelemetry{NodeID: "node", Source: "/stale.log", ReadLines: 4, UpdatedAt: newer})
	hub.Publish(telemetry.StreamFileTelemetry{NodeID: "node", Source: "/stale.log", ReadLines: 100, UpdatedAt: newer.Add(-time.Second)})
	for _, sample := range hub.Snapshot() {
		if sample.Source == "/stale.log" && sample.ReadLines != 4 {
			t.Fatalf("stale sample replaced newer state: %+v", sample)
		}
	}
	hub.Unsubscribe(id)
}

func TestStreamTelemetryAPIParsesPerSourceFailureCounter(t *testing.T) {
	f := newAPIFixture(t)
	f.server.fleet.Register(&fleet.Node{ID: "stream-node", Hostname: "stream-node"})
	const nodeToken = "source-errors-token"
	if err := f.server.fleet.SetToken("stream-node", nodeToken); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"node_id": "stream-node", "stream_files": []any{map[string]any{"source": "/var/log/mail.log", "phase": "reading", "errors": 3}}})
	request, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/node/stream-telemetry", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+nodeToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream telemetry submit %d", response.StatusCode)
	}
	response, data := f.request(http.MethodGet, "/api/v1/telemetry/stream/snapshot", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream snapshot %d: %s", response.StatusCode, data)
	}
	var snapshot struct {
		Files []telemetry.StreamFileTelemetry `json:"files"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil || len(snapshot.Files) != 1 || snapshot.Files[0].Errors != 3 {
		t.Fatalf("per-source errors field did not survive decode and snapshot: %s (%v)", data, err)
	}
}

func TestDashboardSnapshotDoesNotBroadenLeastPrivilegeAccess(t *testing.T) {
	f := newAPIFixture(t)
	// Define a telemetry/system-health-only test role from the existing role;
	// the stock parser-author role also has fleet:read.
	oldPermissions := middleware.RolePermissions[auth.RoleParserAuthor]
	filtered := make([]string, 0, len(oldPermissions))
	for _, permission := range oldPermissions {
		if permission != middleware.PermissionReadFleet {
			filtered = append(filtered, permission)
		}
	}
	middleware.RolePermissions[auth.RoleParserAuthor] = filtered
	defer func() { middleware.RolePermissions[auth.RoleParserAuthor] = oldPermissions }()
	if _, err := f.server.auth.CreateUser(auth.RoleAdmin, "author", "author@example.test", auth.RoleParserAuthor, "Author-password-12345!"); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	body := `{"username":"author","password":"Author-password-12345!"}`
	login, err := client.Post(f.http.URL+"/api/v1/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	login.Body.Close()
	response, err := client.Get(f.http.URL + "/api/v1/dashboard/snapshot?range=1h&step=5m")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var data map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if _, ok := data["fleet_sync"]; ok {
		t.Fatal("dashboard exposed fleet_sync to a role without fleet:read")
	}
	if _, ok := data["overview"]; ok {
		t.Fatal("dashboard overview exposed fleet summary to role without fleet:read")
	}
	if _, ok := data["system"]; !ok {
		t.Fatal("dashboard omitted system data this role may read")
	}
	for _, key := range []string{"metadata", "timeseries", "stream_files"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("dashboard omitted telemetry section %q from role with telemetry:read", key)
		}
	}
	if _, ok := data["ai_usage"]; !ok {
		t.Fatal("dashboard omitted AI usage from role with system:health")
	}
}

func TestDashboardSnapshotAggregatesBoundedSections(t *testing.T) {
	f := newAPIFixture(t)
	response, data := f.request(http.MethodGet, "/api/v1/dashboard/snapshot?range=1h&step=5m", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("dashboard snapshot status %d: %s", response.StatusCode, data)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"overview", "metadata", "system", "ai_usage", "fleet_sync", "timeseries", "stream_files"} {
		if len(got[key]) == 0 {
			t.Errorf("missing %q in %s", key, data)
		}
	}
	if response.Header.Get("X-NLI-Dashboard-Bounded") != "true" {
		t.Fatal("dashboard response not marked bounded")
	}
	unauth, err := http.Get(f.http.URL + "/api/v1/dashboard/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated dashboard status %d", unauth.StatusCode)
	}
}

func TestStreamTelemetryRoutesRequireAuthAndExposeSnapshot(t *testing.T) {
	f := newAPIFixture(t)
	response, err := http.Get(f.http.URL + "/api/v1/telemetry/stream/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated snapshot status %d", response.StatusCode)
	}
	f.server.streamTelemetry.Publish(telemetry.StreamFileTelemetry{NodeID: "node", Source: "/var/log/a.log", Phase: "reading"})
	response, data := f.request(http.MethodGet, "/api/v1/telemetry/stream/snapshot", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status %d: %s", response.StatusCode, data)
	}
	var got struct {
		Files []telemetry.StreamFileTelemetry `json:"files"`
	}
	if err := json.Unmarshal(data, &got); err != nil || len(got.Files) != 1 || got.Files[0].Source != "/var/log/a.log" {
		t.Fatalf("bad snapshot: %s (%v)", data, err)
	}

	req, _ := http.NewRequest(http.MethodGet, f.http.URL+"/api/v1/telemetry/stream", nil)
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("SSE response %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(resp.Body)
	var eventID, eventName, eventData string
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "id: ") {
			eventID = strings.TrimPrefix(line, "id: ")
		}
		if strings.HasPrefix(line, "event: ") {
			eventName = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			eventData = strings.TrimPrefix(line, "data: ")
		}
		if line == "" && eventName != "" {
			break
		}
	}
	var envelope map[string]json.RawMessage
	if eventID == "" || eventName != "snapshot" || json.Unmarshal([]byte(eventData), &envelope) != nil || len(envelope["files"]) == 0 {
		t.Fatalf("initial SSE snapshot contract mismatch: id=%q event=%q data=%s", eventID, eventName, eventData)
	}
	if _, bareSample := envelope["source"]; bareSample {
		t.Fatalf("snapshot was a bare sample: %s", eventData)
	}
	f.server.hub.Publish([]byte(`{"type":"ai_proposal_changed","id":"p-1","status":"ready","raw":"customer-data"}`))
	firstEventID := eventID
	eventID, eventName, eventData = "", "", ""
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "id: ") {
			eventID = strings.TrimPrefix(line, "id: ")
		}
		if strings.HasPrefix(line, "event: ") {
			eventName = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			eventData = strings.TrimPrefix(line, "data: ")
		}
		if line == "" && eventName != "" {
			break
		}
	}
	if eventID == "" || eventID == firstEventID || eventName != "ai_proposal_changed" || strings.Contains(eventData, "customer-data") || !strings.Contains(eventData, `"id":"p-1"`) {
		t.Fatalf("safe AI event did not arrive, advance its ID, or leaked an unapproved field: id=%q event=%q data=%s", eventID, eventName, eventData)
	}
}
