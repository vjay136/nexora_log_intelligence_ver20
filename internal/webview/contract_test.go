package webview

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func webRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "web"))
}
func readAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(webRoot(t), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRequiredTabsAndAPIBackings(t *testing.T) {
	app := readAsset(t, "app.js")
	for _, marker := range []string{
		"Command Center", "Assurance Lab", "Fleet Operations", "Parser Catalog & Priority", "Migration Center",
		"KV Delimiter Config", "Static Positional Patterns", "Field Capture & Projection", "Regex, Templates & Studio",
		"Live Metadata", "Rollouts", "Signed Artifacts & Upgrades", "Users, RBAC & Audit", "System Health & Storage",
		"/overview", "/assurance/run", "/assurance/corpus", "/assurance/cases", "/fleet/sync", "/parser-packs",
		"/migration/reports", "/config/kv", "/config/static-patterns", "/config/projections", "/config/templates",
		"/metadata", "/rollouts", "/artifacts", "/rbac/roles", "/audit", "/system",
		"/assurance/ai-proposal", "/assurance/ai-proposals", "AI remediation review",
		"Approve as second reviewer", "Create validated parser draft", "ai_proposal_changed",
		"Read / processed total", "Parsed / quarantine total", "Cumulative counts use the same authoritative source snapshot",
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("missing WebView contract %q", marker)
		}
	}
}

func TestCSPAssetsAndExplicitStates(t *testing.T) {
	index, app, css, e2e := readAsset(t, "index.html"), readAsset(t, "app.js"), readAsset(t, "styles.css"), readAsset(t, "e2e.spec.js")
	if strings.Contains(index, "<style") || strings.Contains(index, "onclick=") || strings.Contains(index, "<script>") {
		t.Fatal("inline CSP-unsafe content found")
	}
	for _, marker := range []string{`src="./app.js"`, `href="./styles.css"`, "auth-showcase", "demo-access", "environment-chip"} {
		if !strings.Contains(index, marker) {
			t.Errorf("missing external asset or enterprise-shell marker %s", marker)
		}
	}
	for _, forbidden := range []string{`src="/app.js"`, `href="/styles.css"`, "Browser-admin-password-12345!"} {
		if strings.Contains(index, forbidden) || strings.Contains(e2e, forbidden) {
			t.Errorf("forbidden root-only asset or fallback credential %q", forbidden)
		}
	}
	if !strings.Contains(e2e, "NLI_ADMIN_PASSWORD is required") {
		t.Error("browser test must fail closed when runtime credentials are absent")
	}
	for _, marker := range []string{"loading-state", "empty-state", "error-state", "stale-banner", "Reconnecting", "setInterval", "auth-shell", "showcase-grid"} {
		if !strings.Contains(app, marker) && !strings.Contains(css, marker) && !strings.Contains(index, marker) {
			t.Errorf("missing state %q", marker)
		}
	}
	for _, forbidden := range []string{"Math.random()", "demo counter", "mockCount", "fakeData"} {
		if strings.Contains(app, forbidden) {
			t.Errorf("placeholder data marker %q", forbidden)
		}
	}
}

func TestLoginFailsClosedUntilControllerIdentityIsVerified(t *testing.T) {
	index := readAsset(t, "index.html")
	app := readAsset(t, "app.js")

	for _, marker := range []string{
		`type="submit" disabled data-controller-control`,
		`name="username" value="admin" autocomplete="username" required maxlength="128" spellcheck="false" disabled`,
		`name="password" type="password" autocomplete="current-password" required maxlength="512" placeholder="Installation-specific password" disabled`,
		"Checking controller…",
		"Open-NLI-WebView.ps1",
	} {
		if !strings.Contains(index, marker) {
			t.Errorf("login does not fail closed; missing %q", marker)
		}
	}
	for _, marker := range []string{
		"controllerReady: false",
		"if (path === '/auth/login' && !state.controllerReady)",
		"method: 'OPTIONS'",
		"health?.service !== 'nli-controller-agent'",
		"health?.api_version !== 'v1'",
		"loginResponse.headers.get('x-nli-controller') !== 'v20'",
		"if(!state.controllerReady){showPreviewOnly(CONTROLLER_REQUIRED);return;}",
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("controller verification contract missing %q", marker)
		}
	}
}
