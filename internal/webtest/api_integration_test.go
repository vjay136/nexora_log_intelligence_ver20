package webtest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"nexora.local/nli-v20/internal/controlplane"
)

func projectWeb(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "web"))
}

func TestEveryWebviewTabHasLiveAPI(t *testing.T) {
	state := t.TempDir()
	server, err := controlplane.New(controlplane.Config{StateDir: state, WebDir: projectWeb(t), EnrollmentToken: "webtest-enrollment-token-123456", BootstrapAdminPassword: "Webtest-admin-password-12345!", AuditKey: bytes.Repeat([]byte{1}, 32), CSRFKey: bytes.Repeat([]byte{2}, 32), AssuranceKey: bytes.Repeat([]byte{3}, 32), AssuranceRetentionEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	httpServer := httptest.NewServer(server.Mux())
	defer httpServer.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	credentials, _ := json.Marshal(map[string]string{"username": "admin", "password": "Webtest-admin-password-12345!"})
	response, err := client.Post(httpServer.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(credentials))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login=%d", response.StatusCode)
	}
	contracts := map[string]string{
		"operations": "/api/v1/overview", "assurance": "/api/v1/assurance/cases", "fleet": "/api/v1/fleet/sync",
		"parsers": "/api/v1/parser-packs", "migration": "/api/v1/migration/reports", "kv": "/api/v1/config/kv",
		"static": "/api/v1/config/static-patterns", "projection": "/api/v1/config/projections", "studio": "/api/v1/config/templates",
		"metadata": "/api/v1/metadata", "rollouts": "/api/v1/rollouts", "artifacts": "/api/v1/artifacts",
		"security": "/api/v1/rbac/roles", "system": "/api/v1/system",
	}
	for tab, path := range contracts {
		t.Run(tab, func(t *testing.T) {
			response, err := client.Get(httpServer.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("GET %s=%d: %s", path, response.StatusCode, data)
			}
			if !json.Valid(data) {
				t.Fatalf("invalid JSON: %s", data)
			}
		})
	}
}

func TestWebAssetsAreCSPCompatibleAndConfined(t *testing.T) {
	server, err := controlplane.New(controlplane.Config{StateDir: t.TempDir(), WebDir: projectWeb(t), EnrollmentToken: "webtest-enrollment-token-123456", BootstrapAdminPassword: "Webtest-admin-password-12345!", AuditKey: bytes.Repeat([]byte{1}, 32), CSRFKey: bytes.Repeat([]byte{2}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	httpServer := httptest.NewServer(server.Mux())
	defer httpServer.Close()
	assets := map[string]string{"/": "text/html", "/app.js": "text/javascript", "/styles.css": "text/css"}
	for path, expectedType := range assets {
		response, err := http.Get(httpServer.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || len(body) == 0 {
			t.Fatalf("asset %s=%d", path, response.StatusCode)
		}
		if contentType := response.Header.Get("Content-Type"); !bytes.Contains([]byte(contentType), []byte(expectedType)) {
			t.Fatalf("asset %s content type=%q, expected %q", path, contentType, expectedType)
		}
		if response.Header.Get("Content-Security-Policy") == "" {
			t.Fatalf("asset %s missing CSP", path)
		}
	}
	response, err := http.Get(httpServer.URL + "/../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode == http.StatusOK && bytes.Contains(body, []byte("module nexora")) {
		t.Fatal("static traversal exposed repository")
	}
	if _, err := os.Stat(filepath.Join(projectWeb(t), "app.js")); err != nil {
		t.Fatal(err)
	}
}

func TestWebAssetsResolveFromNestedHostingPath(t *testing.T) {
	server := httptest.NewServer(http.StripPrefix("/console/", http.FileServer(http.Dir(projectWeb(t)))))
	defer server.Close()

	response, err := http.Get(server.URL + "/console/index.html")
	if err != nil {
		t.Fatal(err)
	}
	index, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(index, []byte(`href="./styles.css"`)) || !bytes.Contains(index, []byte(`src="./app.js"`)) {
		t.Fatalf("nested index does not use adjacent runtime assets: status=%d", response.StatusCode)
	}
	for _, asset := range []string{"styles.css", "app.js"} {
		response, err := http.Get(server.URL + "/console/" + asset)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || len(body) == 0 {
			t.Fatalf("nested asset %s=%d", asset, response.StatusCode)
		}
	}
}
