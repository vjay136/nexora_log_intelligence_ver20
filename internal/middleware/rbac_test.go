package middleware_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"nexora.local/nli-v20/internal/auditlog"
	"nexora.local/nli-v20/internal/auth"
	"nexora.local/nli-v20/internal/middleware"
)

func securityFixture(t *testing.T) (*middleware.SecurityMiddleware, *auth.Store, string) {
	t.Helper()
	store, err := auth.NewStoreWithBootstrap(filepath.Join(t.TempDir(), "users.json"), "Admin-password-12345!")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Authenticate("admin", "Admin-password-12345!")
	if err != nil {
		t.Fatal(err)
	}
	security := middleware.NewSecurityMiddleware(store, auditlog.NewMemoryAuditLogger())
	if err := security.SetCSRFSecret(bytes.Repeat([]byte{7}, 32)); err != nil {
		t.Fatal(err)
	}
	return security, store, token
}

func TestFiveRolePermissionBoundaries(t *testing.T) {
	security, store, _ := securityFixture(t)
	for _, entry := range []struct {
		user, role, permission string
		allowed                bool
	}{
		{"author", auth.RoleParserAuthor, middleware.PermissionManageParsers, true},
		{"author2", auth.RoleParserAuthor, middleware.PermissionApproveRollout, false},
		{"reviewer", auth.RoleReviewer, middleware.PermissionReviewParsers, true},
		{"ops", auth.RoleOps, middleware.PermissionTriggerRollout, true},
		{"viewer", auth.RoleViewer, middleware.PermissionManageFleet, false},
	} {
		_, err := store.CreateUser(auth.RoleAdmin, entry.user, entry.user+"@example.test", entry.role, "User-password-12345!")
		if err != nil {
			t.Fatal(err)
		}
		token, _, _ := store.Authenticate(entry.user, "User-password-12345!")
		handler := security.RequirePermission(entry.permission)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/resource", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if entry.allowed && recorder.Code != http.StatusNoContent {
			t.Errorf("%s denied: %d", entry.role, recorder.Code)
		}
		if !entry.allowed && recorder.Code != http.StatusForbidden {
			t.Errorf("%s unexpectedly allowed: %d", entry.role, recorder.Code)
		}
	}
}

func TestStrictContentTypeAndCookieCSRF(t *testing.T) {
	security, _, token := securityFixture(t)
	mutation := security.Wrap(security.RequirePermission(middleware.PermissionSystemHealth)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))

	wrongType := httptest.NewRequest(http.MethodPost, "/api/v1/resource", bytes.NewBufferString(`{}`))
	wrongType.Header.Set("Authorization", "Bearer "+token)
	wrongType.Header.Set("Content-Type", "text/plain")
	wrongRecorder := httptest.NewRecorder()
	mutation.ServeHTTP(wrongRecorder, wrongType)
	if wrongRecorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type status=%d", wrongRecorder.Code)
	}

	missing := httptest.NewRequest(http.MethodPost, "/api/v1/resource", bytes.NewBufferString(`{}`))
	missing.Header.Set("Content-Type", "application/json")
	missing.AddCookie(&http.Cookie{Name: "nli_token", Value: token})
	missingRecorder := httptest.NewRecorder()
	mutation.ServeHTTP(missingRecorder, missing)
	if missingRecorder.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", missingRecorder.Code)
	}

	seed := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	seed.AddCookie(&http.Cookie{Name: "nli_token", Value: token})
	seed.Header.Set("User-Agent", "middleware-test")
	seedRecorder := httptest.NewRecorder()
	csrf := security.IssueCSRF(seedRecorder, seed)
	valid := httptest.NewRequest(http.MethodPost, "/api/v1/resource", bytes.NewBufferString(`{}`))
	valid.Header.Set("Content-Type", "application/json")
	valid.Header.Set("User-Agent", "middleware-test")
	valid.Header.Set("X-CSRF-Token", csrf)
	valid.AddCookie(&http.Cookie{Name: "nli_token", Value: token})
	valid.AddCookie(&http.Cookie{Name: "nli_csrf", Value: csrf})
	validRecorder := httptest.NewRecorder()
	mutation.ServeHTTP(validRecorder, valid)
	if validRecorder.Code != http.StatusNoContent {
		t.Fatalf("valid CSRF status=%d body=%s", validRecorder.Code, validRecorder.Body.String())
	}
}

func TestRateLimitAppliesOnlyToAPIRequests(t *testing.T) {
	security, _, _ := securityFixture(t)
	security.RateLimit = 1
	handler := security.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.WriteHeader(http.StatusOK)
	}))

	firstAPI := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	firstAPI.RemoteAddr = "192.0.2.10:41000"
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, firstAPI)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("first API request status=%d", firstRecorder.Code)
	}

	limitedAPI := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	limitedAPI.RemoteAddr = "192.0.2.10:41001"
	limitedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(limitedRecorder, limitedAPI)
	if limitedRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("second API request status=%d, want %d", limitedRecorder.Code, http.StatusTooManyRequests)
	}

	for _, path := range []string{"/", "/app.js", "/styles.css", "/nli-mark.svg"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.RemoteAddr = "192.0.2.10:41002"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("static request %s status=%d after API budget exhaustion", path, recorder.Code)
		}
	}
}
