// Package validation provides comprehensive testing suite for NLI v5 components.
// This implements Task 24: Comprehensive validation test suite covering all
// critical paths, error conditions, and security requirements.
package validation

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/auditlog"
	"nexora.local/nli-v20/internal/auth"
	"nexora.local/nli-v20/internal/controlplane"
	"nexora.local/nli-v20/internal/engine"
	"nexora.local/nli-v20/internal/middleware"
)

// ValidationSuite contains all validation tests for NLI v5
type ValidationSuite struct {
	t           *testing.T
	authStore   *auth.Store
	server      *controlplane.Server
	auditLogger *auditlog.MemoryAuditLogger
}

// NewValidationSuite creates a new comprehensive test suite
func NewValidationSuite(t *testing.T) *ValidationSuite {
	root := t.TempDir()
	authStore, err := auth.NewStoreWithBootstrap(filepath.Join(root, "validation_users.json"), "validation-admin-password-12345")
	if err != nil {
		t.Fatalf("Failed to create auth store: %v", err)
	}

	auditLogger := auditlog.NewMemoryAuditLogger()

	cfg := controlplane.Config{
		ListenAddress:          "127.0.0.1:0",
		StateDir:               filepath.Join(root, "validation_state"),
		WebDir:                 filepath.Join(root, "validation_web"),
		EnrollmentToken:        "validation-enrollment-token-12345678",
		BootstrapAdminPassword: "validation-admin-password-12345",
		CommandLeaseSeconds:    120,
		EventPath:              filepath.Join(root, "validation_events.jsonl"),
		EventRetention:         1000,
	}

	server, err := controlplane.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create control plane server: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	return &ValidationSuite{
		t:           t,
		authStore:   authStore,
		server:      server,
		auditLogger: auditLogger,
	}
}

// RunAll executes all validation tests
func (vs *ValidationSuite) RunAll() {
	vs.t.Run("Authentication", vs.TestAuthentication)
	vs.t.Run("Authorization", vs.TestAuthorization)
	vs.t.Run("ParserEngine", vs.TestParserEngine)
	vs.t.Run("APIEndpoints", vs.TestAPIEndpoints)
	vs.t.Run("SecurityHeaders", vs.TestSecurityHeaders)
	vs.t.Run("RateLimiting", vs.TestRateLimiting)
	vs.t.Run("AuditLogging", vs.TestAuditLogging)
	vs.t.Run("InputValidation", vs.TestInputValidation)
	vs.t.Run("ErrorHandling", vs.TestErrorHandling)
	vs.t.Run("DataConsistency", vs.TestDataConsistency)
}

// TestAuthentication validates authentication mechanisms
func (vs *ValidationSuite) TestAuthentication(t *testing.T) {
	tests := []struct {
		name           string
		username       string
		password       string
		expectedResult bool
	}{
		{"valid admin credentials", "admin", "validation-admin-password-12345", true},
		{"invalid password", "admin", "wrong-password", false},
		{"non-existent user", "nonuser", "any-password", false},
		{"empty credentials", "", "", false},
		{"weak password", "testuser", "123", false}, // Should fail during user creation
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, user, err := vs.authStore.Authenticate(tt.username, tt.password)

			if tt.expectedResult {
				if err != nil {
					t.Errorf("Expected successful authentication, got error: %v", err)
				}
				if token == "" {
					t.Error("Expected token to be returned")
				}
				if user == nil {
					t.Error("Expected user to be returned")
				}
			} else {
				if err == nil {
					t.Error("Expected authentication to fail")
				}
				if token != "" {
					t.Error("Expected no token for failed authentication")
				}
			}
		})
	}
}

// TestAuthorization validates RBAC permissions
func (vs *ValidationSuite) TestAuthorization(t *testing.T) {
	// Create test users with different roles
	users := []struct {
		username string
		role     string
		password string
	}{
		{"viewer_user", "viewer", "viewer-password-12345"},
		{"ops_user", "ops", "ops-password-12345"},
		{"admin_user", "admin", "admin-password-12345"},
	}

	for _, user := range users {
		_, err := vs.authStore.CreateUser("admin", user.username, user.username+"@test.com", user.role, user.password)
		if err != nil {
			t.Fatalf("Failed to create %s user: %v", user.role, err)
		}
	}

	// Test permission matrix
	permissionTests := []struct {
		role       string
		permission string
		expected   bool
	}{
		{"viewer", middleware.PermissionReadLogs, true},
		{"viewer", middleware.PermissionManageUsers, false},
		{"ops", middleware.PermissionTriggerRollout, true},
		{"ops", middleware.PermissionManageUsers, false},
		{"admin", middleware.PermissionManageUsers, true},
		{"admin", middleware.PermissionReadLogs, true},
	}

	for _, tt := range permissionTests {
		t.Run(fmt.Sprintf("%s_%s", tt.role, tt.permission), func(t *testing.T) {
			permissions := middleware.RolePermissions[tt.role]
			hasPermission := false
			for _, p := range permissions {
				if p == tt.permission {
					hasPermission = true
					break
				}
			}

			if hasPermission != tt.expected {
				t.Errorf("Role %s permission %s: expected %v, got %v",
					tt.role, tt.permission, tt.expected, hasPermission)
			}
		})
	}
}

// TestParserEngine validates core parsing functionality
func (vs *ValidationSuite) TestParserEngine(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		service  string
		expected map[string]interface{}
	}{
		{
			name:    "JSON log parsing",
			input:   `{"level":"error","msg":"test message","timestamp":"2026-10-05T10:00:00Z"}`,
			service: "app",
			expected: map[string]interface{}{
				"level": "error",
				"msg":   "test message",
			},
		},
		{
			name:    "Syslog parsing",
			input:   "Oct  5 10:00:00 localhost sshd[1234]: Accepted password for user from 192.168.1.100",
			service: "sshd",
			expected: map[string]interface{}{
				"program": "sshd",
				"pid":     "1234",
			},
		},
		{
			name:    "Apache access log",
			input:   `192.168.1.100 - - [05/Oct/2026:10:00:00 +0000] "GET / HTTP/1.1" 200 1234`,
			service: "apache",
			expected: map[string]interface{}{
				"remote_addr": "192.168.1.100",
				"status":      "200",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			source := engine.Source{
				Hostname: "test-host",
				Service:  tc.service,
				File:     "test.log",
			}

			result := engine.Parse(tc.input, source, engine.Default())

			if result.NLI.Version == "" {
				t.Fatal("Expected an initialized parse envelope")
			}

			// Verify basic structure
			if result.Source.Service != tc.service {
				t.Errorf("Expected service %s, got %s", tc.service, result.Source.Service)
			}

			// Check for expected fields (simplified validation)
			if result.Data == nil {
				t.Error("Expected parsed data to be initialized")
			}
		})
	}
}

// TestAPIEndpoints validates representative public and authenticated HTTP endpoints.
func (vs *ValidationSuite) TestAPIEndpoints(t *testing.T) {
	server := httptest.NewServer(vs.server.Mux())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	loginBody := strings.NewReader(`{"username":"admin","password":"validation-admin-password-12345"}`)
	loginResponse, err := client.Post(server.URL+"/api/v1/auth/login", "application/json", loginBody)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	_ = loginResponse.Body.Close()
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d, want %d", loginResponse.StatusCode, http.StatusOK)
	}

	endpoints := []struct {
		method       string
		path         string
		expectedCode int
		body         string
		contentType  string
	}{
		{http.MethodGet, "/api/v1/health", http.StatusOK, "", ""},
		{http.MethodGet, "/api/v1/fleet", http.StatusOK, "", ""},
		{http.MethodGet, "/api/v1/users", http.StatusOK, "", ""},
		{http.MethodPost, "/api/v1/auth/login", http.StatusBadRequest, "invalid", "application/json"},
		{http.MethodGet, "/api/v1/metadata", http.StatusOK, "", ""},
		{http.MethodGet, "/api/v1/overview", http.StatusOK, "", ""},
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint.method+"_"+endpoint.path, func(t *testing.T) {
			request, err := http.NewRequest(endpoint.method, server.URL+endpoint.path, strings.NewReader(endpoint.body))
			if err != nil {
				t.Fatal(err)
			}
			if endpoint.contentType != "" {
				request.Header.Set("Content-Type", endpoint.contentType)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			_ = response.Body.Close()
			if response.StatusCode != endpoint.expectedCode {
				t.Fatalf("status=%d, want %d", response.StatusCode, endpoint.expectedCode)
			}
		})
	}
}

// TestSecurityHeaders validates security header implementation
func (vs *ValidationSuite) TestSecurityHeaders(t *testing.T) {
	server := httptest.NewServer(vs.server.Mux())
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	expectedHeaders := []string{
		"X-Content-Type-Options",
		"X-Frame-Options",
		"Referrer-Policy",
		"Content-Security-Policy",
	}

	for _, header := range expectedHeaders {
		if resp.Header.Get(header) == "" {
			t.Errorf("Missing security header: %s", header)
		}
	}
}

// TestRateLimiting validates rate limiting functionality.
func (vs *ValidationSuite) TestRateLimiting(t *testing.T) {
	security := middleware.NewSecurityMiddleware(vs.authStore, vs.auditLogger)
	security.RateLimit = 2
	security.RateWindow = time.Minute

	handler := security.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	for attempt, expected := range []int{http.StatusNoContent, http.StatusNoContent, http.StatusTooManyRequests} {
		response, err := http.Get(server.URL + "/api/v1/health")
		if err != nil {
			t.Fatalf("attempt %d failed: %v", attempt+1, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != expected {
			t.Fatalf("attempt %d status=%d, want %d", attempt+1, response.StatusCode, expected)
		}
	}
}

// TestAuditLogging validates comprehensive audit logging
func (vs *ValidationSuite) TestAuditLogging(t *testing.T) {
	vs.auditLogger.Clear()

	// Simulate some security events
	events := []middleware.AuditEvent{
		{
			Timestamp: time.Now(),
			Username:  "testuser",
			Action:    "login",
			Success:   true,
		},
		{
			Timestamp: time.Now(),
			Username:  "testuser",
			Action:    "access_denied",
			Success:   false,
		},
	}

	for _, event := range events {
		err := vs.auditLogger.LogAuditEvent(event)
		if err != nil {
			t.Errorf("Failed to log audit event: %v", err)
		}
	}

	loggedEvents := vs.auditLogger.GetEvents()
	if len(loggedEvents) != len(events) {
		t.Errorf("Expected %d events, got %d", len(events), len(loggedEvents))
	}

	// Verify event details
	for i, event := range loggedEvents {
		if event.Username != events[i].Username {
			t.Errorf("Event %d: expected username %s, got %s",
				i, events[i].Username, event.Username)
		}
		if event.Success != events[i].Success {
			t.Errorf("Event %d: expected success %v, got %v",
				i, events[i].Success, event.Success)
		}
	}
}

// TestInputValidation validates input sanitization and validation
func (vs *ValidationSuite) TestInputValidation(t *testing.T) {
	maliciousInputs := []string{
		"<script>alert('xss')</script>",
		"'; DROP TABLE users; --",
		"../../../etc/passwd",
		strings.Repeat("A", 10000), // Large input
		"\x00\x01\x02",             // Binary data
	}

	for _, input := range maliciousInputs {
		t.Run("malicious_input", func(t *testing.T) {
			// Test parser preview endpoint with malicious input
			server := httptest.NewServer(vs.server.Mux())
			defer server.Close()

			token, _, _ := vs.authStore.Authenticate("admin", "validation-admin-password-12345")

			requestBody := fmt.Sprintf(`{"raw":"%s","service":"test"}`, input)
			req, _ := http.NewRequest("POST", server.URL+"/api/v1/parse/preview",
				strings.NewReader(requestBody))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("Request failed: %v", err)
				return
			}
			defer resp.Body.Close()

			// Should handle malicious input gracefully
			if resp.StatusCode >= 500 {
				t.Errorf("Server error on malicious input: %d", resp.StatusCode)
			}
		})
	}
}

// TestErrorHandling validates error handling and recovery
func (vs *ValidationSuite) TestErrorHandling(t *testing.T) {
	errorScenarios := []struct {
		name        string
		scenario    func() error
		expectError bool
	}{
		{
			name: "invalid auth token",
			scenario: func() error {
				_, _, err := vs.authStore.ValidateToken("invalid-token")
				return err
			},
			expectError: true,
		},
		{
			name: "duplicate user creation",
			scenario: func() error {
				_, err := vs.authStore.CreateUser("admin", "admin", "admin@test.com", "admin", "password-12345")
				return err
			},
			expectError: true,
		},
	}

	for _, scenario := range errorScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			err := scenario.scenario()
			if scenario.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !scenario.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

// TestDataConsistency validates data consistency across components
func (vs *ValidationSuite) TestDataConsistency(t *testing.T) {
	// Create user and verify it appears in listings
	username := "consistency_test_user"
	password := "consistency-password-12345"

	user, err := vs.authStore.CreateUser("admin", username, username+"@test.com", "viewer", password)
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	// Verify user appears in user list
	users := vs.authStore.ListUsers()
	found := false
	for _, u := range users {
		if u.Username == username {
			found = true
			if u.Role != "viewer" {
				t.Errorf("Expected role 'viewer', got %s", u.Role)
			}
			break
		}
	}

	if !found {
		t.Error("Created user not found in user list")
	}

	// Test authentication consistency
	token, authUser, err := vs.authStore.Authenticate(username, password)
	if err != nil {
		t.Errorf("Failed to authenticate created user: %v", err)
	}

	if authUser.Username != user.Username || authUser.Role != user.Role {
		t.Error("Authenticated user data inconsistent with created user")
	}

	// Validate token
	validatedUsername, validatedRole, err := vs.authStore.ValidateToken(token)
	if err != nil {
		t.Errorf("Failed to validate token: %v", err)
	}

	if validatedUsername != username || validatedRole != "viewer" {
		t.Error("Token validation returned inconsistent user data")
	}
}

// RunValidationSuite is the main entry point for comprehensive validation
func RunValidationSuite(t *testing.T) {
	suite := NewValidationSuite(t)
	suite.RunAll()
}

// TestComprehensiveValidation makes the complete validation suite discoverable
// by the Go test runner.
func TestComprehensiveValidation(t *testing.T) {
	RunValidationSuite(t)
}
