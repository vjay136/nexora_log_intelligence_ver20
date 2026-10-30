package controlplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/eventstore"
	metastore "nexora.local/nli-v20/internal/store"
)

type apiFixture struct {
	t       *testing.T
	server  *Server
	http    *httptest.Server
	client  *http.Client
	csrf    string
	public  ed25519.PublicKey
	private ed25519.PrivateKey
	state   string
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	return newAPIFixtureWithRetention(t, true)
}

func newAPIFixtureWithRetention(t *testing.T, retentionEnabled bool) *apiFixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	web := t.TempDir()
	_ = os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html><title>NLI</title>"), 0600)
	server, err := New(Config{StateDir: state, WebDir: web, EnrollmentToken: "Enrollment-token-123456789012", BootstrapAdminPassword: "Admin-password-12345!", AuditKey: bytes.Repeat([]byte{1}, 32), CSRFKey: bytes.Repeat([]byte{2}, 32), AssuranceKey: bytes.Repeat([]byte{3}, 32), AssuranceRetentionEnabled: retentionEnabled, AssuranceRetentionManual: true, ArtifactPublicKeys: []string{base64.StdEncoding.EncodeToString(public)}})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Mux())
	jar, _ := cookiejar.New(nil)
	fixture := &apiFixture{t: t, server: server, http: httpServer, client: &http.Client{Jar: jar}, public: public, private: private, state: state}
	fixture.login("admin", "Admin-password-12345!")
	fixture.refreshCSRF()
	t.Cleanup(func() { httpServer.Close(); _ = server.Close() })
	return fixture
}

func TestBootstrapAdminPasswordFileAcceptsEnterprisePasswordCharacters(t *testing.T) {
	state := t.TempDir()
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html><title>NLI</title>"), 0600); err != nil {
		t.Fatal(err)
	}
	password := "MNC password with spaces & $shell!"
	passwordFile := filepath.Join(state, "bootstrap-admin-password")
	if err := os.WriteFile(passwordFile, []byte(password), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{
		StateDir: state, WebDir: web,
		EnrollmentToken:            "Enrollment-token-123456789012",
		BootstrapAdminPasswordFile: passwordFile,
		AuditKey:                   bytes.Repeat([]byte{1}, 32), CSRFKey: bytes.Repeat([]byte{2}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	httpServer := httptest.NewServer(server.Mux())
	defer httpServer.Close()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
	response, err := http.Post(httpServer.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("password-file login %d: %s", response.StatusCode, data)
	}
}

func (f *apiFixture) login(username, password string) {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	response, err := f.client.Post(f.http.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		f.t.Fatalf("login %d: %s", response.StatusCode, data)
	}
}
func (f *apiFixture) refreshCSRF() {
	response, err := f.client.Get(f.http.URL + "/api/v1/auth/csrf")
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	var data map[string]string
	_ = json.NewDecoder(response.Body).Decode(&data)
	f.csrf = data["csrf_token"]
	if f.csrf == "" {
		f.t.Fatal("missing csrf token")
	}
}
func (f *apiFixture) request(method, path string, body any) (*http.Response, []byte) {
	var reader io.Reader
	headers := map[string]string{}
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
		headers["Content-Type"] = "application/json"
	}
	request, _ := http.NewRequest(method, f.http.URL+path, reader)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set("X-CSRF-Token", f.csrf)
	}
	response, err := f.client.Do(request)
	if err != nil {
		f.t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	return response, data
}
func decodeMap(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("JSON: %v: %s", err, data)
	}
	return value
}

func TestControllerWebviewAPIContracts(t *testing.T) {
	f := newAPIFixture(t)
	paths := []string{"/api/v1/overview", "/api/v1/fleet/sync", "/api/v1/parser-sync", "/api/v1/parsers", "/api/v1/parser-packs", "/api/v1/config/kv", "/api/v1/config/static-patterns", "/api/v1/config/projections", "/api/v1/config/templates", "/api/v1/config/regex", "/api/v1/config/known-keys", "/api/v1/config/semantic-extraction", "/api/v1/config/input-paths", "/api/v1/migration/reports", "/api/v1/assurance/cases", "/api/v1/assurance/ai-proposals", "/api/v1/admin/ai-usage", "/api/v1/metadata", "/api/v1/telemetry/stats", "/api/v1/metrics/timeseries", "/api/v1/verification/sample-corpus", "/api/v1/artifacts", "/api/v1/rollouts", "/api/v1/rbac/roles", "/api/v1/audit", "/api/v1/system"}
	for _, path := range paths {
		response, data := f.request(http.MethodGet, path, nil)
		if response.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d: %s", path, response.StatusCode, data)
		}
		if !json.Valid(data) {
			t.Errorf("GET %s invalid JSON", path)
		}
	}
}

func TestParserCatalogAssetsAreEditableAndDownloadable(t *testing.T) {
	f := newAPIFixture(t)
	response, data := f.request(http.MethodPost, "/api/v1/config/known-keys", map[string]any{"id": "imap-keys", "name": "IMAP known keys", "config": map[string]any{"family": "imap", "keys": []string{"user", "fromhost"}, "delimiters": []string{":", "="}}})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create known keys %d: %s", response.StatusCode, data)
	}
	created := decodeMap(t, data)
	if created["state"] != "draft" || created["revision"] != float64(1) {
		t.Fatalf("unexpected created asset: %s", data)
	}
	response, data = f.request(http.MethodGet, "/api/v1/config/known-keys/imap-keys", nil)
	if response.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(`"fromhost"`)) {
		t.Fatalf("get asset %d: %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodPut, "/api/v1/config/known-keys/imap-keys", map[string]any{"name": "IMAP keys v2", "config": map[string]any{"family": "imap", "keys": []string{"user", "fromhost", "respcode"}}})
	if response.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(`"revision":2`)) {
		t.Fatalf("update asset %d: %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodGet, "/api/v1/config/known-keys/imap-keys/download", nil)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Disposition") == "" || !json.Valid(bytes.TrimSpace(data)) {
		t.Fatalf("download asset %d: %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodPost, "/api/v1/config/regex", map[string]any{"id": "bad-regex", "name": "Bad regex", "config": map[string]any{"pattern": "("}})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid regex accepted: %d %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodPost, "/api/v1/config/semantic-extraction", map[string]any{"id": "entities-v1", "name": "Network and mail entities", "config": map[string]any{"entities": []string{"ipv4", "ipv6", "domain", "url", "email"}, "decode_mime_headers": true, "max_input_bytes": 65536}})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create semantic extraction %d: %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodPost, "/api/v1/config/semantic-extraction", map[string]any{"id": "bad-entities", "name": "Unbounded extraction", "config": map[string]any{"entities": []string{"decrypt_anything"}}})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported semantic extractor accepted: %d %s", response.StatusCode, data)
	}
}

func TestTimeSeriesMetricsAreBoundedAndContainNoEventPayload(t *testing.T) {
	f := newAPIFixture(t)
	now := time.Now().UTC()
	for _, metadata := range []eventstore.TelemetryMetadata{
		{ParserID: "p1", Route: "normal", Result: "parsed", LatencyMs: 12, Timestamp: now.Add(-2 * time.Minute)},
		{ParserID: "p1", Route: "quarantine", Result: "quarantined", ErrorCodes: []string{"no_match"}, LatencyMs: 5, Timestamp: now.Add(-time.Minute)},
	} {
		if _, err := f.server.events.Append("node-1", metadata); err != nil {
			t.Fatal(err)
		}
	}
	f.server.aiMetrics.record(true, 20*time.Millisecond)
	response, data := f.request(http.MethodGet, "/api/v1/metrics/timeseries?range=1h&step=5m", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("timeseries %d: %s", response.StatusCode, data)
	}
	result := decodeMap(t, data)
	points, ok := result["points"].([]any)
	if !ok || len(points) != 12 || result["schema_version"] != "1" {
		t.Fatalf("bad bounded points contract: %s", data)
	}
	retention, _ := result["retention"].(map[string]any)
	if retention["raw_logs"] != false || retention["event_payloads"] != false || bytes.Contains(data, []byte("customer-secret")) {
		t.Fatalf("metrics exposed retained event content: %s", data)
	}
	response, data = f.request(http.MethodGet, "/api/v1/metrics/timeseries?range=365d&step=1m", nil)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unbounded range was accepted: %d %s", response.StatusCode, data)
	}
}

func TestLoginRequiresJSONAndDoesNotReturnBearer(t *testing.T) {
	f := newAPIFixture(t)
	request, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"Admin-password-12345!"}`))
	request.Header.Set("Content-Type", "text/plain")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d", response.StatusCode)
	}
	valid, _ := json.Marshal(map[string]string{"username": "admin", "password": "Admin-password-12345!"})
	response, err = http.Post(f.http.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if bytes.Contains(data, []byte(`"token"`)) {
		t.Fatalf("bearer token exposed: %s", data)
	}
}

func TestAssuranceUsesProductionParserAndPreservesSMTPPrefix(t *testing.T) {
	f := newAPIFixture(t)
	raw := `20260822 050048557+0200 severrgo001 smtp 230675 0 140347436861184 Note;SieveRuleMsg(2005/1) id=6A291BF5048F23DE:policy=outbound:checkpoint=AUTH:rule=INOUT_action:msg=PolicyMsgInfo:eventType=PolicyMsgInfo:action=disconnect:reason=badpassword:fromhost=11.22.33.44:geo=IL:helo=Site6:auth_ok=false:auth_user=managers999@012.meg.in:auth_fail=6:auth_tries=7:score=0`
	response, data := f.request(http.MethodPost, "/api/v1/assurance/run", map[string]any{"raw": raw, "service": "auto", "desired": map[string]any{}})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("run %d: %s", response.StatusCode, data)
	}
	result := decodeMap(t, data)
	if result["passed"] != true || result["comparison_enabled"] != false {
		t.Fatalf("parse-only verdict=%s", data)
	}
	actual, _ := result["actual"].(map[string]any)
	for field, expected := range map[string]any{
		"date": "20260822", "time": "050048557+0200", "hostname": "severrgo001",
		"service": "smtp", "major_pid": "230675", "pid_status": float64(0),
		"minor_pid": "140347436861184", "event_type": "Note", "event": "SieveRuleMsg",
		"code": "2005/1", "id": "6A291BF5048F23DE", "action": "disconnect",
		"auth_fail": float64(6), "auth_tries": float64(7), "score": float64(0),
	} {
		if actual[field] != expected {
			t.Errorf("%s=%#v, want %#v; actual=%v", field, actual[field], expected, actual)
		}
	}
	if len(actual) != 26 {
		t.Fatalf("actual field count=%d, want 26: %v", len(actual), actual)
	}
	trace, _ := result["trace"].([]any)
	hasStaticStage := false
	for _, item := range trace {
		row, _ := item.(map[string]any)
		if row["stage_type"] == "static_layout" && row["outcome"] == "success" {
			hasStaticStage = true
		}
	}
	if !hasStaticStage {
		t.Fatalf("production static-layout stage absent: %s", data)
	}
}

func TestAssuranceEncryptedVersionedRetentionAndPurge(t *testing.T) {
	f := newAPIFixture(t)
	marker := "fixture-secret-marker-42"
	request := map[string]any{"raw": `{"level":"info","message":"` + marker + `"}`, "service": "json", "desired": map[string]any{"level": "info", "message": marker}}
	response, data := f.request(http.MethodPost, "/api/v1/assurance/run", request)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("run %d: %s", response.StatusCode, data)
	}
	result := decodeMap(t, data)
	if result["retained"] != false {
		t.Fatal("one-shot result marked retained")
	}
	save := map[string]any{"name": "encrypted fixture", "retain": true, "case": request}
	response, data = f.request(http.MethodPost, "/api/v1/assurance/cases", save)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("save %d: %s", response.StatusCode, data)
	}
	saved := decodeMap(t, data)
	id := saved["id"].(string)
	err := filepath.Walk(f.state, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		content, readErr := os.ReadFile(path)
		if readErr == nil && bytes.Contains(content, []byte(marker)) {
			t.Errorf("plaintext retained in %s", path)
		}
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	response, data = f.request(http.MethodGet, "/api/v1/assurance/cases/"+id, nil)
	if response.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(marker)) {
		t.Fatalf("decrypt %d: %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodGet, "/api/v1/assurance/cases/"+id+"/download", nil)
	if response.StatusCode != http.StatusOK || string(data) != `{"level":"info","message":"`+marker+`"}`+"\n" {
		t.Fatalf("download %d: %q", response.StatusCode, data)
	}
	if disposition := response.Header.Get("Content-Disposition"); !strings.Contains(disposition, ".log") {
		t.Fatalf("download disposition=%q", disposition)
	}
	response, data = f.request(http.MethodGet, "/api/v1/assurance/cases", nil)
	listed := decodeMap(t, data)
	cases, _ := listed["cases"].([]any)
	if len(cases) != 1 || cases[0].(map[string]any)["manual_retention"] != true {
		t.Fatalf("manual retention list=%s", data)
	}
	response, data = f.request(http.MethodDelete, "/api/v1/assurance/cases/"+id, map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("purge %d: %s", response.StatusCode, data)
	}
	response, _ = f.request(http.MethodGet, "/api/v1/assurance/cases/"+id, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("purged case status=%d", response.StatusCode)
	}
}

func TestAssuranceExactParserAndCorpusDefaultsAreHonored(t *testing.T) {
	f := newAPIFixture(t)
	selected := map[string]any{"raw": "level=INFO user=alice", "service": "auto", "parser_id": "generic.kv", "desired": map[string]any{"level": "INFO", "user": "alice"}}
	response, data := f.request(http.MethodPost, "/api/v1/assurance/run", selected)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("selected parser run %d: %s", response.StatusCode, data)
	}
	result := decodeMap(t, data)
	parser := result["parser"].(map[string]any)
	if result["passed"] != true || parser["name"] != "generic.kv" || result["requested_parser"] != "generic.kv" {
		t.Fatalf("selected parser result=%s", data)
	}

	response, data = f.request(http.MethodPost, "/api/v1/assurance/corpus", map[string]any{"cases": []any{selected}})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("selected corpus run %d: %s", response.StatusCode, data)
	}
	corpus := decodeMap(t, data)
	if corpus["passed"] != float64(1) || corpus["failed"] != float64(0) {
		t.Fatalf("selected corpus result=%s", data)
	}
}

func TestParserIndependentApproval(t *testing.T) {
	f := newAPIFixture(t)
	for _, user := range []map[string]any{{"username": "author", "email": "author@example.test", "role": "parser_author", "password": "Author-password-12345!"}, {"username": "reviewer", "email": "reviewer@example.test", "role": "reviewer", "password": "Reviewer-password-12345!"}} {
		response, data := f.request(http.MethodPost, "/api/v1/users", user)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create user %d: %s", response.StatusCode, data)
		}
	}
	f.login("author", "Author-password-12345!")
	f.refreshCSRF()
	pack := map[string]any{"id": "mail-pack", "name": "Mail Pack", "version": "1.0.0", "priority": 10, "rules": []any{}}
	response, data := f.request(http.MethodPost, "/api/v1/parser-packs", pack)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create pack %d: %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodPost, "/api/v1/parser-packs/mail-pack/submit", map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("submit %d: %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodPost, "/api/v1/parser-packs/mail-pack/approve", map[string]any{})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("author approved own pack: %d %s", response.StatusCode, data)
	}
	f.login("reviewer", "Reviewer-password-12345!")
	f.refreshCSRF()
	response, data = f.request(http.MethodPost, "/api/v1/parser-packs/mail-pack/approve", map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("review approve %d: %s", response.StatusCode, data)
	}
	approved := decodeMap(t, data)
	if approved["catalog_state"] != "approved" || approved["state"] != "blocked" || approved["status"] != "blocked" {
		t.Fatalf("approval and runtime readiness were not reported separately: %s", data)
	}
}

func TestParserAssetApprovalPublishesAndMaterializesFleetBundle(t *testing.T) {
	f := newAPIFixture(t)
	for _, user := range []map[string]any{
		{"username": "asset-author", "email": "asset-author@example.test", "role": "parser_author", "password": "Asset-author-password-12345!"},
		{"username": "asset-reviewer", "email": "asset-reviewer@example.test", "role": "reviewer", "password": "Asset-reviewer-password-12345!"},
		{"username": "asset-ops", "email": "asset-ops@example.test", "role": "ops", "password": "Asset-ops-password-12345!"},
	} {
		response, data := f.request(http.MethodPost, "/api/v1/users", user)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create user %d: %s", response.StatusCode, data)
		}
	}
	f.login("asset-author", "Asset-author-password-12345!")
	f.refreshCSRF()
	response, data := f.request(http.MethodPost, "/api/v1/config/regex", map[string]any{
		"id": "fleet-sync-test", "name": "Fleet sync test", "config": map[string]any{"pattern": `level=(?P<level>[A-Za-z]+)`},
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create parser asset %d: %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodPost, "/api/v1/config/regex/fleet-sync-test/submit", map[string]any{})
	if response.StatusCode != http.StatusOK || decodeMap(t, data)["state"] != "in_review" {
		t.Fatalf("submit parser asset %d: %s", response.StatusCode, data)
	}

	f.login("asset-reviewer", "Asset-reviewer-password-12345!")
	f.refreshCSRF()
	response, data = f.request(http.MethodPost, "/api/v1/config/regex/fleet-sync-test/approve", map[string]any{})
	if response.StatusCode != http.StatusOK || decodeMap(t, data)["state"] != "approved" {
		t.Fatalf("approve parser asset %d: %s", response.StatusCode, data)
	}
	response, _ = f.request(http.MethodPost, "/api/v1/config/regex/fleet-sync-test/publish", map[string]any{})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("reviewer published without rollout permission: %d", response.StatusCode)
	}

	f.login("asset-ops", "Asset-ops-password-12345!")
	f.refreshCSRF()
	response, data = f.request(http.MethodPost, "/api/v1/config/regex/fleet-sync-test/publish", map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("publish parser asset %d: %s", response.StatusCode, data)
	}
	published := decodeMap(t, data)
	if published["bundle_hash"] == "" || published["controller_applied_hash"] != published["bundle_hash"] || published["auto_rollout_status"] != "pending_approval" || published["rollout_proposal"] == nil {
		t.Fatalf("publish was not materialized: %s", data)
	}
	response, data = f.request(http.MethodGet, "/api/v1/parser-sync", nil)
	if response.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(`"asset_id":"fleet-sync-test"`)) || !bytes.Contains(data, []byte(`"controller_status":"applied"`)) {
		t.Fatalf("parser fleet sync does not report published asset: %d %s", response.StatusCode, data)
	}
	bundle, err := os.ReadFile(filepath.Join(f.state, "parser-assets", "bundle.json"))
	if err != nil || !bytes.Contains(bundle, []byte(`"fleet-sync-test"`)) {
		t.Fatalf("controller bundle missing published asset: %v %s", err, bundle)
	}
}

func TestMigrationDoesNotRetainSource(t *testing.T) {
	f := newAPIFixture(t)
	marker := "MIGRATION_SOURCE_SECRET_77"
	source := `loki.source.file "logs" {
  # ` + marker + `
  stage.regex { expression = "(?P<level>INFO)" }
}`
	response, data := f.request(http.MethodPost, "/api/v1/migration/import", map[string]any{"filename": "mail.alloy", "source_type": "alloy", "content": source, "create_draft": true})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("migration %d: %s", response.StatusCode, data)
	}
	err := filepath.Walk(filepath.Join(f.state, "metadata"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		content, _ := os.ReadFile(path)
		if bytes.Contains(content, []byte(marker)) {
			t.Errorf("migration source retained in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSignedConfigArtifactUpload(t *testing.T) {
	f := newAPIFixture(t)
	content := []byte(`{"parser":"mail","enabled":true}`)
	digest := sha256.Sum256(content)
	signature := ed25519.Sign(f.private, digest[:])
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "controller.json")
	_, _ = part.Write(content)
	_ = writer.Close()
	request, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/artifacts/upload?type=config", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-CSRF-Token", f.csrf)
	request.Header.Set("X-Artifact-SHA256", hex.EncodeToString(digest[:]))
	request.Header.Set("X-Artifact-Signature", base64.StdEncoding.EncodeToString(signature))
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload %d: %s", response.StatusCode, data)
	}
	if !bytes.Contains(data, []byte(`"signature_verified":true`)) {
		t.Fatalf("signature status missing: %s", data)
	}
}

func TestNodeStateDriftIsAuthenticated(t *testing.T) {
	f := newAPIFixture(t)
	registration := map[string]any{"id": "node-1", "hostname": "node-1", "os": "linux", "arch": "x86_64", "version": "5.0.0", "config_hash": strings.Repeat("a", 64)}
	encoded, _ := json.Marshal(registration)
	request, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/node/register", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-NLI-Enrollment", "Enrollment-token-123456789012")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register %d: %s", response.StatusCode, data)
	}
	token := decodeMap(t, data)["node_token"].(string)
	proposals, err := f.server.metadata.List(context.Background(), "rollout-proposals", metastore.Query{State: "pending_approval", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	proposalID := ""
	for _, proposal := range proposals {
		var payload map[string]any
		if json.Unmarshal(proposal.Payload, &payload) == nil && payload["name"] == "Initial parser asset synchronization" {
			proposalID = proposal.ID
			break
		}
	}
	if proposalID == "" {
		t.Fatal("new enrollment did not create an approval-gated initial parser sync proposal")
	}
	approvalResponse, approvalData := f.request(http.MethodPost, "/api/v1/rollouts/"+proposalID+"/approve", map[string]any{})
	if approvalResponse.StatusCode != http.StatusOK {
		t.Fatalf("independent initial parser sync approval %d: %s", approvalResponse.StatusCode, approvalData)
	}
	commandRequest, _ := http.NewRequest(http.MethodGet, f.http.URL+"/api/v1/node/commands/next?node_id=node-1", nil)
	commandRequest.Header.Set("Authorization", "Bearer "+token)
	commandResponse, err := http.DefaultClient.Do(commandRequest)
	if err != nil {
		t.Fatal(err)
	}
	commandData, _ := io.ReadAll(commandResponse.Body)
	commandResponse.Body.Close()
	if commandResponse.StatusCode != http.StatusOK || !bytes.Contains(commandData, []byte(`"type":"parser_sync"`)) || !bytes.Contains(commandData, []byte(`"asset_bundle"`)) {
		t.Fatalf("new client did not receive initial parser sync command: %d %s", commandResponse.StatusCode, commandData)
	}
	stateBody, _ := json.Marshal(map[string]any{"node_id": "node-1", "active_pack_hash": strings.Repeat("b", 64), "active_config_hash": strings.Repeat("a", 64), "active_release_hash": strings.Repeat("c", 64), "status": "active"})
	stateRequest, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/node/state", bytes.NewReader(stateBody))
	stateRequest.Header.Set("Content-Type", "application/json")
	stateRequest.Header.Set("Authorization", "Bearer "+token)
	response, err = http.DefaultClient.Do(stateRequest)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("node state %d: %s", response.StatusCode, payload)
	}
}

func TestNodeParserTelemetryIsAuthenticatedAndVisibleToWebApp(t *testing.T) {
	f := newAPIFixture(t)
	registration := map[string]any{"id": "telemetry-node", "hostname": "mail-host", "os": "linux", "arch": "x86_64", "version": "20.0.0", "config_hash": strings.Repeat("a", 64)}
	encoded, _ := json.Marshal(registration)
	request, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/node/register", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-NLI-Enrollment", "Enrollment-token-123456789012")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register %d: %s", response.StatusCode, data)
	}
	token := decodeMap(t, data)["node_token"].(string)

	// The legacy endpoint stays disabled even for enrolled nodes.
	legacyBody, _ := json.Marshal(map[string]any{"node_id": "telemetry-node", "events": []any{map[string]any{"id": "customer-event"}}})
	legacyRequest, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/node/events", bytes.NewReader(legacyBody))
	legacyRequest.Header.Set("Content-Type", "application/json")
	legacyRequest.Header.Set("Authorization", "Bearer "+token)
	legacyResponse, err := http.DefaultClient.Do(legacyRequest)
	if err != nil {
		t.Fatal(err)
	}
	legacyResponse.Body.Close()
	if legacyResponse.StatusCode != http.StatusGone {
		t.Fatalf("legacy event ingest status=%d, want 410", legacyResponse.StatusCode)
	}

	telemetryBody := map[string]any{
		"event_id": "evt-123", "event_hash": strings.Repeat("b", 64), "node_id": "telemetry-node",
		"parser_id": "reviewed_smtp", "route": "normal", "result": "success",
		"confidence": 0.99, "evidence_coverage": 1.0, "attempt_count": 1,
		"fallback_depth": 0, "output_field_count": 14, "latency_ms": 3,
		"processed_at": time.Now().UTC(),
	}
	telemetryEncoded, _ := json.Marshal(telemetryBody)
	telemetryRequest, _ := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/telemetry", bytes.NewReader(telemetryEncoded))
	telemetryRequest.Header.Set("Content-Type", "application/json")
	telemetryRequest.Header.Set("Authorization", "Bearer "+token)
	response, err = http.DefaultClient.Do(telemetryRequest)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("telemetry ingest %d: %s", response.StatusCode, data)
	}

	response, data = f.request(http.MethodGet, "/api/v1/telemetry/query?node_id=telemetry-node", nil)
	if response.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(`"parser":"reviewed_smtp"`)) || !bytes.Contains(data, []byte(`"event_id":"evt-123"`)) {
		t.Fatalf("WebApp telemetry query did not show accepted parser metadata: %d %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodGet, "/api/v1/metadata?node_id=telemetry-node", nil)
	metadata := decodeMap(t, data)
	records, _ := metadata["records"].([]any)
	if response.StatusCode != http.StatusOK || len(records) != 1 || !bytes.Contains(data, []byte(`"parser":"reviewed_smtp"`)) {
		t.Fatalf("live metadata snapshot did not show telemetry exactly once: %d %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodGet, "/api/v1/overview", nil)
	overview := decodeMap(t, data)
	overviewMetadata, _ := overview["metadata"].(map[string]any)
	if response.StatusCode != http.StatusOK || overviewMetadata["retained_metadata"] != float64(1) || overviewMetadata["events_last_hour"] != float64(1) {
		t.Fatalf("overview did not include the accepted telemetry record: %d %s", response.StatusCode, data)
	}
	response, data = f.request(http.MethodGet, "/api/v1/metrics/timeseries?range=1h&step=1m&node_id=telemetry-node", nil)
	series := decodeMap(t, data)
	points, _ := series["points"].([]any)
	received := 0.0
	parsed := 0.0
	for _, item := range points {
		point, _ := item.(map[string]any)
		received += point["logs_received"].(float64)
		parsed += point["logs_parsed"].(float64)
	}
	if response.StatusCode != http.StatusOK || received != 1 || parsed != 1 {
		t.Fatalf("time series did not include accepted parse telemetry: %d received=%v parsed=%v: %s", response.StatusCode, received, parsed, data)
	}
	stored, err := os.ReadFile(filepath.Join(f.state, "event-metadata.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("customer-event")) || bytes.Contains(stored, []byte(`"fields"`)) || bytes.Contains(stored, []byte(`"payload"`)) {
		t.Fatalf("telemetry store retained customer event data: %s", stored)
	}
}

func TestControllerIdentityAndLoginPreflight(t *testing.T) {
	f := newAPIFixture(t)

	response, data := f.request(http.MethodGet, "/api/v1/health", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status=%d body=%s", response.StatusCode, data)
	}
	if response.Header.Get("X-NLI-Controller") != "v20" {
		t.Fatalf("health controller identity=%q", response.Header.Get("X-NLI-Controller"))
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("health cache control=%q", response.Header.Get("Cache-Control"))
	}
	health := decodeMap(t, data)
	if health["service"] != "nli-controller-agent" || health["api_version"] != "v1" {
		t.Fatalf("unexpected health identity: %s", data)
	}

	request, err := http.NewRequest(http.MethodOptions, f.http.URL+"/api/v1/auth/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("login preflight status=%d", response.StatusCode)
	}
	if response.Header.Get("X-NLI-Controller") != "v20" || !strings.Contains(response.Header.Get("Allow"), "POST") {
		t.Fatalf("login preflight identity=%q allow=%q", response.Header.Get("X-NLI-Controller"), response.Header.Get("Allow"))
	}
}

func TestEnrollmentTokenIsSingleUseAcrossRestart(t *testing.T) {
	state := t.TempDir()
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html>"), 0600); err != nil {
		t.Fatal(err)
	}
	const enrollment = "Single-use-enrollment-token-123456"
	cfg := Config{
		StateDir: state, WebDir: web, EnrollmentToken: enrollment,
		BootstrapAdminPassword: "Admin-password-12345!",
		AuditKey:               bytes.Repeat([]byte{1}, 32), CSRFKey: bytes.Repeat([]byte{2}, 32), AssuranceKey: bytes.Repeat([]byte{3}, 32),
	}
	register := func(baseURL, nodeID string) (int, []byte) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"id": nodeID, "hostname": nodeID, "os": "linux", "arch": "x86_64", "version": "5.0.0"})
		request, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/node/register", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-NLI-Enrollment", enrollment)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, data
	}

	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	firstHTTP := httptest.NewServer(first.Mux())
	if status, data := register(firstHTTP.URL, "node-first"); status != http.StatusCreated {
		t.Fatalf("first enrollment status=%d body=%s", status, data)
	}
	if status, data := register(firstHTTP.URL, "node-replay"); status != http.StatusUnauthorized {
		t.Fatalf("replayed enrollment status=%d body=%s", status, data)
	}
	firstHTTP.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	ledger, err := os.ReadFile(filepath.Join(state, "consumed-enrollment-tokens.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ledger, []byte(enrollment)) || len(strings.TrimSpace(string(ledger))) != 64 {
		t.Fatalf("consumed-token ledger must contain one digest only: %q", ledger)
	}

	second, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondHTTP := httptest.NewServer(second.Mux())
	defer secondHTTP.Close()
	if status, data := register(secondHTTP.URL, "node-after-restart"); status != http.StatusUnauthorized {
		t.Fatalf("replayed enrollment after restart status=%d body=%s", status, data)
	}
}

func TestDisabledAssuranceRetentionListsEmptyAndRejectsWrites(t *testing.T) {
	f := newAPIFixtureWithRetention(t, false)
	response, data := f.request(http.MethodGet, "/api/v1/assurance/cases", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disabled retention list status=%d body=%s", response.StatusCode, data)
	}
	result := decodeMap(t, data)
	if result["count"] != float64(0) || result["retention_enabled"] != false {
		t.Fatalf("unexpected disabled retention response: %s", data)
	}
	response, data = f.request(http.MethodPost, "/api/v1/assurance/cases", map[string]any{
		"name": "must-not-save", "retain": true,
		"case": map[string]any{"raw": `{"level":"info"}`, "service": "json", "desired": map[string]any{"level": "info"}},
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled retention write status=%d body=%s", response.StatusCode, data)
	}
}

func TestOutputDestinationConfigValidation(t *testing.T) {
	if isParserAssetCollection("output-destinations") || isParserAssetCollection("input-paths") || !isParserAssetCollection("regex-patterns") {
		t.Fatal("configuration and parser asset collection boundaries are incorrect")
	}
	valid := map[string]any{"outputs": []any{
		map[string]any{"type": "file", "path_template": "/var/log/nli/output/imap_normalised_{hour_utc}.jsonl"},
		map[string]any{"type": "clickhouse", "url": "https://clickhouse.example:8443", "database": "logs", "table": "events", "fallback_path": "/var/log/nli/output/delivery-fallback.jsonl", "field_columns": map[string]any{"fromhost": "client_ip"}},
	}}
	if err := validateCatalogConfig("output-destinations", valid); err != nil {
		t.Fatalf("valid output config rejected: %v", err)
	}
	invalid := map[string]any{"outputs": []any{map[string]any{"type": "clickhouse", "url": "https://clickhouse.example", "database": "logs; DROP TABLE x", "table": "events", "fallback_path": "/tmp/spool"}}}
	if err := validateCatalogConfig("output-destinations", invalid); err == nil {
		t.Fatal("unsafe ClickHouse identifier was accepted")
	}
	missingFallback := map[string]any{"outputs": []any{map[string]any{"type": "loki", "url": "https://loki.example"}}}
	if err := validateCatalogConfig("output-destinations", missingFallback); err == nil {
		t.Fatal("remote output without fallback accounting was accepted")
	}
	badTemplate := map[string]any{"outputs": []any{map[string]any{"type": "file", "path_template": "/var/log/nli/output/imap.jsonl"}}}
	if err := validateCatalogConfig("output-destinations", badTemplate); err == nil {
		t.Fatal("hourly file template without the UTC token was accepted")
	}
}

func TestInputPathProfileConfigValidation(t *testing.T) {
	valid := map[string]any{"inputs": []any{
		map[string]any{"path": "/var/log/mail/imap.log", "service": "imap"},
		map[string]any{"path": "/var/log/mail/smtp.log", "service": "smtp-in", "selection": map[string]any{"mode": "default"}},
	}}
	if err := validateCatalogConfig("input-paths", valid); err != nil {
		t.Fatalf("valid family inputs rejected: %v", err)
	}
	for _, invalid := range []map[string]any{
		{"inputs": []any{map[string]any{"path": "relative/mail.log", "service": "auto"}}},
		{"inputs": []any{map[string]any{"path": "/var/log/mail.log", "service": "unknown"}}},
		{"inputs": []any{map[string]any{"path": "/var/log/mail.log", "service": "auto"}, map[string]any{"path": "/var/log/mail.log", "service": "imap"}}},
	} {
		if err := validateCatalogConfig("input-paths", invalid); err == nil {
			t.Fatalf("invalid input path profile accepted: %#v", invalid)
		}
	}
}
