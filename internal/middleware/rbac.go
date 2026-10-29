// Package middleware provides the production HTTP security boundary for the NLI
// controller. Route handlers opt into explicit permissions; node routes retain
// their separate node-token authorization.
package middleware

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/auth"
)

const (
	PermissionReadLogs       = "logs:read"
	PermissionWriteLogs      = "logs:write"
	PermissionReadFleet      = "fleet:read"
	PermissionManageFleet    = "fleet:manage"
	PermissionReadParsers    = "parsers:read"
	PermissionManageParsers  = "parsers:manage"
	PermissionReviewParsers  = "parsers:review"
	PermissionReadUsers      = "users:read"
	PermissionManageUsers    = "users:manage"
	PermissionTriggerRollout = "rollout:trigger"
	PermissionApproveRollout = "rollout:approve"
	PermissionCancelRollout  = "rollout:cancel"
	PermissionReadTelemetry  = "telemetry:read"
	PermissionWriteTelemetry = "telemetry:write"
	PermissionParsePreview   = "parse:preview"
	PermissionAssuranceSave  = "assurance:save"
	PermissionMigration      = "migration:manage"
	PermissionReadArtifacts  = "artifacts:read"
	PermissionUploadArtifact = "artifacts:upload"
	PermissionReadAudit      = "audit:read"
	PermissionSystemHealth   = "system:health"
)

var RolePermissions = map[string][]string{
	auth.RoleViewer: {
		PermissionReadLogs, PermissionReadFleet, PermissionReadParsers,
		PermissionReadTelemetry, PermissionParsePreview, PermissionReadArtifacts,
		PermissionSystemHealth,
	},
	auth.RoleParserAuthor: {
		PermissionReadLogs, PermissionReadFleet, PermissionReadParsers,
		PermissionManageParsers, PermissionReadTelemetry, PermissionParsePreview,
		PermissionAssuranceSave, PermissionMigration, PermissionReadArtifacts,
		PermissionUploadArtifact, PermissionSystemHealth,
	},
	auth.RoleReviewer: {
		PermissionReadLogs, PermissionReadFleet, PermissionReadParsers,
		PermissionReviewParsers, PermissionReadTelemetry, PermissionParsePreview,
		PermissionAssuranceSave, PermissionReadArtifacts, PermissionApproveRollout,
		PermissionSystemHealth,
	},
	auth.RoleOps: {
		PermissionReadLogs, PermissionWriteLogs, PermissionReadFleet, PermissionManageFleet,
		PermissionReadParsers, PermissionReadTelemetry, PermissionWriteTelemetry,
		PermissionParsePreview, PermissionAssuranceSave, PermissionMigration,
		PermissionReadArtifacts, PermissionUploadArtifact, PermissionTriggerRollout,
		PermissionCancelRollout, PermissionReadAudit, PermissionSystemHealth,
	},
	auth.RoleAdmin: {
		PermissionReadLogs, PermissionWriteLogs, PermissionReadFleet, PermissionManageFleet,
		PermissionReadParsers, PermissionManageParsers, PermissionReviewParsers,
		PermissionReadUsers, PermissionManageUsers, PermissionReadTelemetry,
		PermissionWriteTelemetry, PermissionParsePreview, PermissionAssuranceSave,
		PermissionMigration, PermissionReadArtifacts, PermissionUploadArtifact,
		PermissionTriggerRollout, PermissionApproveRollout, PermissionCancelRollout, PermissionReadAudit,
		PermissionSystemHealth,
	},
}

type SecurityContext struct {
	Username    string   `json:"username"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	RemoteIP    string   `json:"remote_ip"`
	UserAgent   string   `json:"user_agent"`
}

type AuditEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Action    string    `json:"action"`
	Resource  string    `json:"resource"`
	RemoteIP  string    `json:"remote_ip"`
	UserAgent string    `json:"user_agent"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
	RequestID string    `json:"request_id"`
}

type AuditLogger interface{ LogAuditEvent(interface{}) error }

type rateEntry struct {
	Count int
	Reset time.Time
}

type SecurityMiddleware struct {
	authStore          *auth.Store
	auditLogger        AuditLogger
	rateMu             sync.Mutex
	rates              map[string]*rateEntry
	csrfKey            []byte
	RateLimit          int
	RateWindow         time.Duration
	MaxRequestBodySize int64
	EnableCSRF         bool
	EnableAuditLogging bool
}

func NewSecurityMiddleware(store *auth.Store, logger AuditLogger) *SecurityMiddleware {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return &SecurityMiddleware{authStore: store, auditLogger: logger, rates: map[string]*rateEntry{}, csrfKey: key, RateLimit: 300, RateWindow: time.Minute, MaxRequestBodySize: 8 << 20, EnableCSRF: true, EnableAuditLogging: true}
}

func (sm *SecurityMiddleware) SetCSRFSecret(key []byte) error {
	if len(key) < 32 {
		return fmt.Errorf("CSRF key must contain at least 32 bytes")
	}
	sm.csrfKey = append([]byte(nil), key...)
	return nil
}

func permissions(role string) []string { return append([]string(nil), RolePermissions[role]...) }
func (sm *SecurityMiddleware) hasPermission(role, permission string) bool {
	for _, p := range RolePermissions[role] {
		if p == permission {
			return true
		}
	}
	return false
}

func (sm *SecurityMiddleware) authenticate(r *http.Request) (*SecurityContext, error) {
	token := extractToken(r)
	if token == "" {
		return nil, fmt.Errorf("missing authentication")
	}
	username, role, err := sm.authStore.ValidateToken(token)
	if err != nil {
		return nil, err
	}
	return &SecurityContext{Username: username, Role: role, Permissions: permissions(role), RemoteIP: getRemoteIP(r), UserAgent: r.UserAgent()}, nil
}

func (sm *SecurityMiddleware) RequirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := sm.authenticate(r)
			if err != nil {
				sm.jsonError(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if !sm.hasPermission(principal.Role, permission) {
				sm.audit(principal, "permission_denied", r.URL.Path, false, permission)
				sm.jsonError(w, "forbidden", http.StatusForbidden)
				return
			}
			r.Header.Set("X-NLI-User", principal.Username)
			r.Header.Set("X-NLI-Role", principal.Role)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), securityContextKey{}, principal)))
		})
	}
}

func (sm *SecurityMiddleware) AuthorizeRequest(r *http.Request, permission string) (*SecurityContext, error) {
	principal, err := sm.authenticate(r)
	if err != nil {
		return nil, fmt.Errorf("unauthorized")
	}
	if !sm.hasPermission(principal.Role, permission) {
		return nil, fmt.Errorf("forbidden")
	}
	r.Header.Set("X-NLI-User", principal.Username)
	r.Header.Set("X-NLI-Role", principal.Role)
	return principal, nil
}

func (sm *SecurityMiddleware) Require(permission string, handler http.HandlerFunc) http.HandlerFunc {
	return sm.RequirePermission(permission)(handler).ServeHTTP
}

func (sm *SecurityMiddleware) RequireRole(required string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := sm.authenticate(r)
			if err != nil {
				sm.jsonError(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if !hasMinimumRole(principal.Role, required) {
				sm.jsonError(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), securityContextKey{}, principal)))
		})
	}
}

func hasMinimumRole(role, required string) bool {
	levels := map[string]int{auth.RoleViewer: 1, auth.RoleParserAuthor: 2, auth.RoleReviewer: 2, auth.RoleOps: 3, auth.RoleAdmin: 4}
	return levels[role] >= levels[required]
}

type securityContextKey struct{}

func GetSecurityContext(r *http.Request) *SecurityContext {
	value, _ := r.Context().Value(securityContextKey{}).(*SecurityContext)
	return value
}

func extractToken(r *http.Request) string {
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	}
	if cookie, err := r.Cookie("nli_token"); err == nil {
		return cookie.Value
	}
	return ""
}
func cookieAuthenticated(r *http.Request) bool {
	_, err := r.Cookie("nli_token")
	return err == nil && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (sm *SecurityMiddleware) csrfFor(r *http.Request) string {
	bucket := time.Now().UTC().Unix() / 3600
	mac := hmac.New(sha256.New, sm.csrfKey)
	fmt.Fprintf(mac, "%d|%s|%s", bucket, extractToken(r), r.UserAgent())
	return fmt.Sprintf("%d.%s", bucket, hex.EncodeToString(mac.Sum(nil)))
}
func (sm *SecurityMiddleware) validateCSRFToken(r *http.Request, token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	for offset := int64(0); offset <= 1; offset++ {
		bucket := time.Now().UTC().Unix()/3600 - offset
		mac := hmac.New(sha256.New, sm.csrfKey)
		fmt.Fprintf(mac, "%d|%s|%s", bucket, extractToken(r), r.UserAgent())
		expected := fmt.Sprintf("%d.%s", bucket, hex.EncodeToString(mac.Sum(nil)))
		if hmac.Equal([]byte(expected), []byte(token)) {
			return true
		}
	}
	return false
}

func (sm *SecurityMiddleware) IssueCSRF(w http.ResponseWriter, r *http.Request) string {
	token := sm.csrfFor(r)
	http.SetCookie(w, &http.Cookie{Name: "nli_csrf", Value: token, Path: "/", HttpOnly: false, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 7200})
	return token
}

func (sm *SecurityMiddleware) ValidateCSRF() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return sm.csrfMiddleware(next) }
}
func (sm *SecurityMiddleware) csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sm.EnableCSRF || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || r.URL.Path == "/api/v1/auth/login" || strings.HasPrefix(r.URL.Path, "/api/v1/node/") || !cookieAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		header := r.Header.Get("X-CSRF-Token")
		cookie, err := r.Cookie("nli_csrf")
		if err != nil || header == "" || !hmac.Equal([]byte(header), []byte(cookie.Value)) || !sm.validateCSRFToken(r, header) {
			sm.jsonError(w, "CSRF token required or invalid", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (sm *SecurityMiddleware) Wrap(next http.Handler) http.Handler {
	next = sm.csrfMiddleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w, r)
		requestID := fmt.Sprintf("req-%x", sha256.Sum256([]byte(fmt.Sprintf("%d-%s", time.Now().UnixNano(), r.RemoteAddr))))[:24]
		w.Header().Set("X-Request-ID", requestID)
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
			if contentType != "application/json" && contentType != "multipart/form-data" {
				sm.jsonError(w, "unsupported content type", http.StatusUnsupportedMediaType)
				return
			}
		}
		limit := sm.MaxRequestBodySize
		if strings.HasPrefix(r.URL.Path, "/api/v1/artifacts/upload") {
			limit = 520 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		// Apply the control-plane rate budget only to API traffic. The WebView
		// assets are immutable local files and must remain loadable after an
		// operator has used the API budget; otherwise a refresh can return JSON
		// 429 data for app.js and strand the login screen before its controller
		// verification guard can run.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			key := getRemoteIP(r)
			if !sm.allow(key) {
				w.Header().Set("Retry-After", "60")
				sm.jsonError(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
		}
		capture := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(capture, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			principal := GetSecurityContext(r)
			actor, role := r.Header.Get("X-NLI-User"), r.Header.Get("X-NLI-Role")
			if actor == "" {
				actor = "anonymous"
			}
			if principal != nil {
				actor, role = principal.Username, principal.Role
			}
			sm.audit(&SecurityContext{Username: actor, Role: role, RemoteIP: getRemoteIP(r), UserAgent: r.UserAgent()}, r.Method, r.URL.Path, capture.status < 400, fmt.Sprintf("status=%d duration_ms=%d request_id=%s", capture.status, time.Since(start).Milliseconds(), requestID))
		}
	})
}

func (sm *SecurityMiddleware) allow(key string) bool {
	sm.rateMu.Lock()
	defer sm.rateMu.Unlock()
	now := time.Now()
	entry := sm.rates[key]
	if entry == nil || now.After(entry.Reset) {
		sm.rates[key] = &rateEntry{Count: 1, Reset: now.Add(sm.RateWindow)}
		return true
	}
	entry.Count++
	return entry.Count <= sm.RateLimit
}
func (sm *SecurityMiddleware) audit(ctx *SecurityContext, action, resource string, success bool, detail string) {
	if !sm.EnableAuditLogging || sm.auditLogger == nil {
		return
	}
	event := AuditEvent{Timestamp: time.Now().UTC(), Username: ctx.Username, Role: ctx.Role, Action: action, Resource: resource, RemoteIP: ctx.RemoteIP, UserAgent: ctx.UserAgent, Success: success, RequestID: fmt.Sprintf("req-%d", time.Now().UnixNano()), Error: detail}
	_ = sm.auditLogger.LogAuditEvent(event)
}

func setSecurityHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self' ws: wss:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	if r.TLS != nil {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
	}
}
func (sm *SecurityMiddleware) ContentSecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { setSecurityHeaders(w, r); next.ServeHTTP(w, r) })
	}
}

func getRemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func (sm *SecurityMiddleware) jsonError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}
func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func LimitRequestSize(max int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, max)
			next.ServeHTTP(w, r)
		})
	}
}
