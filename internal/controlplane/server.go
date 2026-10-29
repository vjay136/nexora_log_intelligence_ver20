package controlplane

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/adapter"
	"nexora.local/nli-v20/internal/auditlog"
	"nexora.local/nli-v20/internal/auth"
	"nexora.local/nli-v20/internal/corpusevidence"
	"nexora.local/nli-v20/internal/engine"
	"nexora.local/nli-v20/internal/eventstore"
	"nexora.local/nli-v20/internal/fleet"
	"nexora.local/nli-v20/internal/middleware"
	"nexora.local/nli-v20/internal/nupire"
	"nexora.local/nli-v20/internal/protocol"
	"nexora.local/nli-v20/internal/registry"
	metastore "nexora.local/nli-v20/internal/store"
	"nexora.local/nli-v20/internal/wsrelay"
	"nexora.local/nli-v20/telemetry"
)

type Config struct {
	ListenAddress                    string   `json:"listen_address"`
	StateDir                         string   `json:"state_dir"`
	WebDir                           string   `json:"web_dir"`
	TLSCert                          string   `json:"tls_cert,omitempty"`
	TLSKey                           string   `json:"tls_key,omitempty"`
	EdgeManifestPath                 string   `json:"edge_manifest_path,omitempty"`
	AllowInsecureRemote              bool     `json:"allow_insecure_remote,omitempty"`
	ForceSecureCookies               bool     `json:"force_secure_cookies,omitempty"`
	EnrollmentTokenEnv               string   `json:"enrollment_token_env,omitempty"`
	BootstrapAdminPasswordEnv        string   `json:"bootstrap_admin_password_env,omitempty"`
	BootstrapAdminPasswordFile       string   `json:"bootstrap_admin_password_file,omitempty"`
	AuditKeyEnv                      string   `json:"audit_key_env,omitempty"`
	CSRFKeyEnv                       string   `json:"csrf_key_env,omitempty"`
	AssuranceKeyEnv                  string   `json:"assurance_key_env,omitempty"`
	ArtifactPublicKeysEnv            string   `json:"artifact_public_keys_env,omitempty"`
	SupabaseURLEnv                   string   `json:"supabase_url_env,omitempty"`
	SupabaseAnonKeyEnv               string   `json:"supabase_anon_key_env,omitempty"`
	SupabasePublishableKeyEnv        string   `json:"supabase_publishable_key_env,omitempty"`
	SupabaseControllerKeyEnv         string   `json:"supabase_controller_secret_env,omitempty"`
	SupabaseSecretKeyEnv             string   `json:"supabase_secret_key_env,omitempty"`
	EnrollmentToken                  string   `json:"-"`
	BootstrapAdminPassword           string   `json:"-"`
	AuditKey                         []byte   `json:"-"`
	CSRFKey                          []byte   `json:"-"`
	AssuranceKey                     []byte   `json:"-"`
	CommandLeaseSeconds              int      `json:"command_lease_seconds,omitempty"`
	EventPath                        string   `json:"event_path,omitempty"`
	EventRetention                   int      `json:"event_retention,omitempty"`
	EnableLegacyEventIngest          bool     `json:"enable_legacy_event_ingest,omitempty"`
	StorageProvider                  string   `json:"storage_provider,omitempty"`
	StorageDir                       string   `json:"storage_dir,omitempty"`
	StorageAllowInsecure             bool     `json:"storage_allow_insecure,omitempty"`
	AssuranceRetentionEnabled        bool     `json:"assurance_retention_enabled,omitempty"`
	AssuranceRetentionManual         bool     `json:"assurance_retention_manual,omitempty"`
	AssuranceRetentionMax            int      `json:"assurance_retention_max,omitempty"`
	AssuranceRetentionHours          int      `json:"assurance_retention_hours,omitempty"`
	QuarantineRawRetentionEnabled    bool     `json:"quarantine_raw_retention_enabled,omitempty"`
	QuarantineRawRetentionMaxRecords int      `json:"quarantine_raw_retention_max_records,omitempty"`
	QuarantineRawRetentionHours      int      `json:"quarantine_raw_retention_hours,omitempty"`
	QuarantineRawRetentionKeyEnv     string   `json:"quarantine_raw_retention_key_env,omitempty"`
	ArtifactPublicKeys               []string `json:"artifact_public_keys,omitempty"`
	MaxArtifactBytes                 int64    `json:"max_artifact_bytes,omitempty"`
	RateLimit                        int      `json:"rate_limit,omitempty"`
	RateWindowSeconds                int      `json:"rate_window_seconds,omitempty"`
}

type Server struct {
	cfg                      Config
	startedAt                time.Time
	auth                     *auth.Store
	fleet                    *fleet.Fleet
	rollouts                 *rolloutStateManager
	parsers                  *registry.Registry
	events                   *eventstore.Store
	telemetry                *TelemetryController
	streamTelemetry          *StreamHub
	metadata                 metastore.Store
	artifactStorage          metastore.BlobStore
	storageWarning           string
	audit                    *auditlog.ChainLog
	security                 *middleware.SecurityMiddleware
	assurance                *assuranceVault
	assuranceDeps            *engine.Deps
	assuranceRuns            *assuranceRunScheduler
	hub                      *wsrelay.Hub
	mux                      *http.ServeMux
	enrollmentToken          string
	enrollmentConsumedPath   string
	enrollmentMu             sync.Mutex
	artifactKeys             [][]byte
	aiMetrics                *aiUsageStore
	metricHistory            *metricHistory
	quarantine               *quarantineVault
	nupire                   nupireController
	downloadKey              []byte
	browserHeartbeatMu       sync.RWMutex
	browserHeartbeatAt       time.Time
	browserHeartbeatUser     string
	notificationHTTPClient   *http.Client
	notificationQueue        chan notificationDeliveryJob
	notificationWorkerCancel context.CancelFunc
	notificationWorkerDone   chan struct{}
}

func New(cfg Config) (*Server, error) {
	applyConfigDefaults(&cfg)
	if cfg.StateDir == "" {
		return nil, errors.New("state_dir is required")
	}
	if err := validateListenSecurity(cfg); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.StateDir, 0750); err != nil {
		return nil, err
	}
	enrollment := cfg.EnrollmentToken
	if enrollment == "" {
		enrollment = os.Getenv(cfg.EnrollmentTokenEnv)
	}
	if len(enrollment) < 24 {
		return nil, fmt.Errorf("%s must contain at least 24 characters", cfg.EnrollmentTokenEnv)
	}
	password := cfg.BootstrapAdminPassword
	if password == "" {
		password = os.Getenv(cfg.BootstrapAdminPasswordEnv)
	}
	if password == "" && cfg.BootstrapAdminPasswordFile != "" {
		passwordBytes, readErr := os.ReadFile(cfg.BootstrapAdminPasswordFile)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return nil, fmt.Errorf("read bootstrap admin password file: %w", readErr)
		}
		password = strings.TrimSuffix(strings.TrimSuffix(string(passwordBytes), "\n"), "\r")
	}
	authStore, err := auth.NewStoreWithBootstrap(filepath.Join(cfg.StateDir, "users.json"), password)
	if err != nil {
		return nil, fmt.Errorf("auth store: %w", err)
	}
	auditKey, err := loadOrCreateControllerKey(cfg.StateDir, "audit.key", cfg.AuditKey, os.Getenv(cfg.AuditKeyEnv))
	if err != nil {
		return nil, err
	}
	csrfKey, err := loadOrCreateControllerKey(cfg.StateDir, "csrf.key", cfg.CSRFKey, os.Getenv(cfg.CSRFKeyEnv))
	if err != nil {
		return nil, err
	}
	auditLogger, err := auditlog.NewChainLog(filepath.Join(cfg.StateDir, "audit.jsonl"), auditKey)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	security := middleware.NewSecurityMiddleware(authStore, auditLogger)
	if err := security.SetCSRFSecret(csrfKey); err != nil {
		return nil, err
	}
	if cfg.RateLimit > 0 {
		security.RateLimit = cfg.RateLimit
	}
	if cfg.RateWindowSeconds > 0 {
		security.RateWindow = time.Duration(cfg.RateWindowSeconds) * time.Second
	}
	if cfg.EdgeManifestPath == "" {
		cfg.EdgeManifestPath = "/etc/nli/controller-edge.json"
	}
	storage, warning, err := openMetadataStore(cfg)
	if err != nil {
		return nil, err
	}
	if err := seedBuiltinCatalog(context.Background(), storage); err != nil {
		_ = storage.Close()
		return nil, fmt.Errorf("seed parser catalog: %w", err)
	}
	artifactStorage, err := openArtifactBlobStore(cfg)
	if err != nil {
		_ = storage.Close()
		return nil, err
	}
	registryStore := registry.New()
	registry.LoadBuiltin(registryStore)
	productionParser, err := adapter.NewParserPackAdapter()
	if err != nil {
		_ = storage.Close()
		return nil, fmt.Errorf("production parser profiles: %w", err)
	}
	assuranceDeps := engine.Default()
	assuranceDeps.SetV5Parser(productionParser)
	if cfg.EventPath == "" {
		cfg.EventPath = filepath.Join(cfg.StateDir, "event-metadata.jsonl")
	}
	events, err := eventstore.New(cfg.EventPath, cfg.EventRetention)
	if err != nil {
		return nil, err
	}
	telemetryController, err := NewTelemetryController(events)
	if err != nil {
		return nil, err
	}
	vault, err := newAssuranceVault(cfg, storage, auditLogger)
	if err != nil {
		return nil, err
	}
	quarantine, err := newQuarantineVault(cfg, storage, auditLogger)
	if err != nil {
		return nil, err
	}
	nupireStore, err := nupire.NewStore(filepath.Join(cfg.StateDir, "nupire.json"), nupire.DefaultPolicy())
	if err != nil {
		_ = storage.Close()
		return nil, fmt.Errorf("NUPIRE state: %w", err)
	}
	history, err := newMetricHistory(filepath.Join(cfg.StateDir, "metric-history.json"))
	if err != nil {
		_ = storage.Close()
		return nil, fmt.Errorf("metric history: %w", err)
	}
	s := &Server{cfg: cfg, startedAt: time.Now().UTC(), auth: authStore, fleet: fleet.New(filepath.Join(cfg.StateDir, "fleet.json")), rollouts: newRolloutStateManager(filepath.Join(cfg.StateDir, "rollouts.json")), parsers: registryStore, events: events, telemetry: telemetryController, streamTelemetry: NewStreamHub(128), metadata: storage, artifactStorage: artifactStorage, storageWarning: warning, audit: auditLogger, security: security, assurance: vault, assuranceDeps: assuranceDeps, hub: wsrelay.NewHub(), enrollmentToken: enrollment, enrollmentConsumedPath: filepath.Join(cfg.StateDir, "consumed-enrollment-tokens.sha256"), aiMetrics: &aiUsageStore{}, metricHistory: history, quarantine: quarantine, nupire: persistentNUPIREController{store: nupireStore}, notificationQueue: make(chan notificationDeliveryJob, 128), notificationWorkerDone: make(chan struct{})}
	s.downloadKey = append([]byte(nil), csrfKey...)
	s.artifactKeys = parsePublicKeys(cfg)
	s.mux = http.NewServeMux()
	s.registerRoutes()
	// Materialize the release-approved parser catalog during startup. This makes
	// controller state verifiable before the first client registers.
	parserBundle, err := s.buildActiveParserBundle(context.Background())
	if err != nil {
		return nil, fmt.Errorf("build initial parser bundle: %w", err)
	}
	if _, err := s.materializeParserBundle(parserBundle); err != nil {
		return nil, fmt.Errorf("materialize initial parser bundle: %w", err)
	}
	s.assuranceRuns, err = newAssuranceRunScheduler(s, 2)
	if err != nil {
		return nil, err
	}
	s.startNotificationWorker()
	return s, nil
}

func applyConfigDefaults(cfg *Config) {
	if cfg.ListenAddress == "" {
		cfg.ListenAddress = "127.0.0.1:12382"
	}
	if cfg.WebDir == "" {
		cfg.WebDir = "/usr/share/nli/web"
	}
	if cfg.EdgeManifestPath == "" {
		cfg.EdgeManifestPath = "/etc/nli/controller-edge.json"
	}
	if cfg.EnrollmentTokenEnv == "" {
		cfg.EnrollmentTokenEnv = "NLI_ENROLLMENT_TOKEN"
	}
	if cfg.BootstrapAdminPasswordEnv == "" {
		cfg.BootstrapAdminPasswordEnv = "NLI_BOOTSTRAP_ADMIN_PASSWORD"
	}
	if cfg.BootstrapAdminPasswordFile == "" && cfg.StateDir != "" {
		cfg.BootstrapAdminPasswordFile = filepath.Join(cfg.StateDir, "bootstrap-admin-password")
	}
	if cfg.AuditKeyEnv == "" {
		cfg.AuditKeyEnv = "NLI_AUDIT_HMAC_KEY"
	}
	if cfg.CSRFKeyEnv == "" {
		cfg.CSRFKeyEnv = "NLI_CSRF_HMAC_KEY"
	}
	if cfg.AssuranceKeyEnv == "" {
		cfg.AssuranceKeyEnv = "NLI_ASSURANCE_ENCRYPTION_KEY"
	}
	if cfg.ArtifactPublicKeysEnv == "" {
		cfg.ArtifactPublicKeysEnv = "NLI_ARTIFACT_ED25519_PUBLIC_KEYS"
	}
	if cfg.SupabaseURLEnv == "" {
		cfg.SupabaseURLEnv = "SUPABASE_URL"
	}
	if cfg.SupabaseAnonKeyEnv == "" {
		cfg.SupabaseAnonKeyEnv = "SUPABASE_ANON_KEY"
	}
	if cfg.SupabasePublishableKeyEnv == "" {
		cfg.SupabasePublishableKeyEnv = "SUPABASE_PUBLISHABLE_KEY"
	}
	if cfg.SupabaseControllerKeyEnv == "" {
		cfg.SupabaseControllerKeyEnv = "NLI_SUPABASE_CONTROLLER_SECRET"
	}
	if cfg.SupabaseSecretKeyEnv == "" {
		cfg.SupabaseSecretKeyEnv = "SUPABASE_SECRET_KEY"
	}
	if cfg.CommandLeaseSeconds <= 0 {
		cfg.CommandLeaseSeconds = 120
	}
	if cfg.EventRetention <= 0 {
		cfg.EventRetention = 10000
	}
	if cfg.StorageProvider == "" {
		cfg.StorageProvider = "local"
	}
	if cfg.StorageDir == "" && cfg.StateDir != "" {
		cfg.StorageDir = filepath.Join(cfg.StateDir, "metadata")
	}
	if cfg.AssuranceRetentionMax <= 0 {
		cfg.AssuranceRetentionMax = 500
	}
	if cfg.AssuranceRetentionHours <= 0 {
		cfg.AssuranceRetentionHours = 720
	}
	if cfg.QuarantineRawRetentionMaxRecords <= 0 || cfg.QuarantineRawRetentionMaxRecords > 1000 {
		cfg.QuarantineRawRetentionMaxRecords = 1000
	}
	if cfg.QuarantineRawRetentionHours <= 0 || cfg.QuarantineRawRetentionHours > 720 {
		cfg.QuarantineRawRetentionHours = 24
	}
	if cfg.QuarantineRawRetentionKeyEnv == "" {
		cfg.QuarantineRawRetentionKeyEnv = "NLI_QUARANTINE_ENCRYPTION_KEY"
	}
	if cfg.MaxArtifactBytes <= 0 {
		cfg.MaxArtifactBytes = 512 << 20
	}
}

func validateListenSecurity(cfg Config) error {
	host, _, err := net.SplitHostPort(cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("invalid listen_address: %w", err)
	}
	ip := net.ParseIP(host)
	isLoopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if !isLoopback && cfg.TLSCert == "" && !cfg.AllowInsecureRemote {
		return errors.New("TLS is required for non-loopback listeners")
	}
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		return errors.New("both tls_cert and tls_key are required")
	}
	return nil
}

func decodeKey(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	if decoded, err := hex.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return []byte(value), nil
}
func loadOrCreateControllerKey(dir, name string, configured []byte, env string) ([]byte, error) {
	if len(configured) >= 32 {
		return append([]byte(nil), configured...), nil
	}
	if env != "" {
		key, _ := decodeKey(env)
		if len(key) < 32 {
			return nil, fmt.Errorf("%s must decode to at least 32 bytes", name)
		}
		return key, nil
	}
	path := filepath.Join(dir, name)
	if data, err := os.ReadFile(path); err == nil {
		key, _ := decodeKey(strings.TrimSpace(string(data)))
		if len(key) >= 32 {
			return key, nil
		}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0600); err != nil {
		return nil, err
	}
	return key, nil
}

func openMetadataStore(cfg Config) (metastore.Store, string, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.StorageProvider))
	switch provider {
	case "", "local":
		value, err := metastore.Open(metastore.Config{Provider: "local", LocalDir: cfg.StorageDir})
		return value, "", err
	case "supabase":
		projectURL := strings.TrimSpace(os.Getenv(cfg.SupabaseURLEnv))
		anonKey := strings.TrimSpace(os.Getenv(cfg.SupabasePublishableKeyEnv))
		if anonKey == "" {
			anonKey = strings.TrimSpace(os.Getenv(cfg.SupabaseAnonKeyEnv))
		}
		controllerSecret := []byte(os.Getenv(cfg.SupabaseControllerKeyEnv))
		if projectURL == "" || anonKey == "" || len(controllerSecret) < 32 {
			return nil, "", fmt.Errorf("Supabase storage requires %s, %s (or legacy %s), and a 32-byte %s", cfg.SupabaseURLEnv, cfg.SupabasePublishableKeyEnv, cfg.SupabaseAnonKeyEnv, cfg.SupabaseControllerKeyEnv)
		}
		value, err := metastore.Open(metastore.Config{
			Provider:                 "supabase",
			SupabaseURL:              projectURL,
			SupabaseAnonKey:          anonKey,
			SupabaseControllerSecret: controllerSecret,
			AllowInsecure:            cfg.StorageAllowInsecure,
			Timeout:                  15 * time.Second,
		})
		return value, "", err
	default:
		return nil, "", fmt.Errorf("unsupported storage_provider %q", cfg.StorageProvider)
	}
}

func openArtifactBlobStore(cfg Config) (metastore.BlobStore, error) {
	if strings.ToLower(strings.TrimSpace(cfg.StorageProvider)) != "supabase" {
		return nil, nil
	}
	projectURL := strings.TrimSpace(os.Getenv(cfg.SupabaseURLEnv))
	secret := strings.TrimSpace(os.Getenv(cfg.SupabaseSecretKeyEnv))
	if secret == "" {
		return nil, nil
	}
	return metastore.NewSupabaseBlobStore(projectURL, secret, nil)
}

func (s *Server) Mux() http.Handler { return s.security.Wrap(s.mux) }
func (s *Server) Close() error {
	var errs []string
	if s.assuranceRuns != nil {
		s.assuranceRuns.Close()
	}
	if s.notificationWorkerCancel != nil {
		s.notificationWorkerCancel()
		<-s.notificationWorkerDone
	}
	if s.quarantine != nil && s.quarantine.store != nil {
		if err := s.quarantine.store.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if s.metadata != nil {
		if err := s.metadata.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if s.audit != nil {
		if err := s.audit.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
func (s *Server) ListenAndServe() error {
	server := &http.Server{Addr: s.cfg.ListenAddress, Handler: s.Mux(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 32 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if s.cfg.TLSCert != "" {
		return server.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
	}
	return server.ListenAndServe()
}

// PublishEvent publishes only a digest and bounded metadata. Full raw/event
// bodies are never sent to the browser's live metadata channel.
func (s *Server) PublishEvent(message []byte) {
	digest := sha256.Sum256(message)
	payload, _ := json.Marshal(map[string]any{"type": "event_metadata", "event_hash": hex.EncodeToString(digest[:]), "received_at": time.Now().UTC(), "payload_retained": false})
	s.hub.Publish(payload)
}

func (s *Server) registerRoutes() {
	guard := func(permission string, handler http.HandlerFunc) http.HandlerFunc {
		return s.security.Require(permission, handler)
	}
	s.mux.HandleFunc("/api/v1/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/v1/auth/logout", guard(middleware.PermissionSystemHealth, s.handleLogout))
	s.mux.HandleFunc("/api/v1/auth/me", guard(middleware.PermissionSystemHealth, s.handleMe))
	s.mux.HandleFunc("/api/v1/auth/csrf", guard(middleware.PermissionSystemHealth, s.handleCSRF))
	s.mux.HandleFunc("/api/v1/node/register", s.handleNodeRegister)
	s.mux.HandleFunc("/api/v1/node/heartbeat", s.handleNodeHeartbeat)
	s.mux.HandleFunc("/api/v1/node/stream-telemetry", s.handleNodeStreamTelemetry)
	s.mux.HandleFunc("/api/v1/node/commands/next", s.handleNodeNextCommand)
	s.mux.HandleFunc("/api/v1/node/commands/", s.handleNodeCommandResult)
	s.mux.HandleFunc("/api/v1/node/events", s.handleNodeEvents)
	s.mux.HandleFunc("/api/v1/node/state", s.handleNodeState)
	s.mux.HandleFunc("/api/v1/node/quarantine/records", s.handleNodeQuarantineRecords)
	s.mux.HandleFunc("/api/v1/telemetry", s.handleTelemetry)
	s.mux.HandleFunc("/api/v1/telemetry/query", guard(middleware.PermissionReadTelemetry, s.telemetry.GetTelemetry))
	s.mux.HandleFunc("/api/v1/telemetry/stats", guard(middleware.PermissionReadTelemetry, s.telemetry.GetTelemetryStats))
	s.mux.HandleFunc("/api/v1/telemetry/stream", guard(middleware.PermissionReadTelemetry, s.streamTelemetry.SSEHandlerWithEvents(s.hub)))
	s.mux.HandleFunc("/api/v1/telemetry/stream/snapshot", guard(middleware.PermissionReadTelemetry, s.streamTelemetry.SnapshotHandler))
	s.mux.HandleFunc("/api/v1/sync/snapshot", guard(middleware.PermissionReadTelemetry, s.handleSyncSnapshot))
	s.mux.HandleFunc("/api/v1/navigation/counts", guard(middleware.PermissionSystemHealth, s.handleNavigationCounts))
	s.mux.HandleFunc("/api/v1/communications/status", guard(middleware.PermissionReadTelemetry, s.handleCommunicationStatus))
	s.mux.HandleFunc("/api/v1/communications/browser-heartbeat", s.handleBrowserCommunicationHeartbeat)
	s.mux.HandleFunc("/api/v1/notifications", s.handleNotificationFeed)
	s.mux.HandleFunc("/api/v1/notifications/config", s.handleNotificationConfig)
	s.mux.HandleFunc("/api/v1/notifications/", s.handleNotificationAck)
	s.mux.HandleFunc("/api/v1/sync/stream", guard(middleware.PermissionReadTelemetry, s.streamTelemetry.CanonicalSSEHandlerWithSnapshot(s.hub, func(r *http.Request) any { return s.canonicalSyncSnapshot(r.Context()) })))
	s.mux.HandleFunc("/api/v1/sync/diagnostics", guard(middleware.PermissionReadTelemetry, s.streamTelemetry.DiagnosticsHandler))
	s.mux.HandleFunc("/api/v1/dashboard/snapshot", guard(middleware.PermissionReadTelemetry, s.handleDashboardSnapshot))
	s.mux.HandleFunc("/api/v1/quarantine/summary", guard(middleware.PermissionReadLogs, s.handleQuarantineSummary))
	s.mux.HandleFunc("/api/v1/quarantine/records", guard(middleware.PermissionReadLogs, s.handleQuarantineRecords))
	s.mux.HandleFunc("/api/v1/quarantine/records/", s.handleQuarantineRecord)
	s.mux.HandleFunc("/api/v1/quarantine/export", guard(middleware.PermissionReadLogs, s.handleQuarantineExport))
	s.mux.HandleFunc("/api/v1/quarantine/history", guard(middleware.PermissionReadLogs, s.handleQuarantineHistory))
	s.mux.HandleFunc("/api/v1/quarantine/reprocess", guard(middleware.PermissionTriggerRollout, s.handleQuarantineReprocess))
	s.mux.HandleFunc("/api/v1/metrics/timeseries", guard(middleware.PermissionReadTelemetry, s.handleMetricsTimeSeries))
	s.mux.HandleFunc("/api/v1/metrics/catalog", guard(middleware.PermissionReadTelemetry, s.handleMetricCatalog))
	s.mux.HandleFunc("/api/v1/metrics/openmetrics", guard(middleware.PermissionReadTelemetry, s.handleOpenMetrics))
	s.mux.HandleFunc("/api/v1/verification/sample-corpus", guard(middleware.PermissionReadParsers, s.handleSampleCorpusVerification))
	s.mux.HandleFunc("/api/v1/parser-sync", s.handleParserSync)
	s.mux.HandleFunc("/api/v1/parser-sync/", s.handleParserSync)
	s.mux.HandleFunc("/api/v1/metadata", guard(middleware.PermissionReadTelemetry, s.handleMetadata))
	s.mux.HandleFunc("/api/v1/stream", guard(middleware.PermissionReadTelemetry, s.handleStream))
	s.mux.HandleFunc("/api/v1/overview", guard(middleware.PermissionSystemHealth, s.handleOverview))
	s.mux.HandleFunc("/api/v1/fleet", guard(middleware.PermissionReadFleet, s.handleFleet))
	s.mux.HandleFunc("/api/v1/fleet/stats", guard(middleware.PermissionReadFleet, s.handleFleetStats))
	s.mux.HandleFunc("/api/v1/fleet/sync", guard(middleware.PermissionReadFleet, s.handleFleetSync))
	s.mux.HandleFunc("/api/v1/fleet/", s.handleFleetResource)
	s.mux.HandleFunc("/api/v1/parsers", guard(middleware.PermissionReadParsers, s.handleBuiltinParsers))
	s.mux.HandleFunc("/api/v1/parser-packs", s.handleParserPacks)
	s.mux.HandleFunc("/api/v1/parser-packs/", s.handleParserPack)
	for _, route := range []struct{ path, collection string }{{"/api/v1/config/kv", "kv-config"}, {"/api/v1/config/static-patterns", "static-patterns"}, {"/api/v1/config/projections", "projections"}, {"/api/v1/config/templates", "templates"}, {"/api/v1/config/regex", "regex-patterns"}, {"/api/v1/config/known-keys", "known-keys"}, {"/api/v1/config/semantic-extraction", "semantic-extraction"}, {"/api/v1/config/output-destinations", "output-destinations"}, {"/api/v1/config/input-paths", "input-paths"}} {
		collection := route.collection
		s.mux.HandleFunc(route.path, func(w http.ResponseWriter, r *http.Request) { s.handleConfiguration(w, r, collection) })
	}
	s.mux.HandleFunc("/api/v1/config/", s.handleConfigurationResource)
	s.mux.HandleFunc("/api/v1/assurance/run", guard(middleware.PermissionParsePreview, s.handleAssuranceRun))
	s.mux.HandleFunc("/api/v1/assurance/parser-helper", guard(middleware.PermissionParsePreview, s.handleParserCreationHelper))
	s.mux.HandleFunc("/api/v1/assurance/napi/synthesize", s.handleNAPISynthesize)
	s.mux.HandleFunc("/api/v1/nupire/", s.nupireAPI)
	s.mux.HandleFunc("/api/v1/nupire", s.nupireAPI)
	s.mux.HandleFunc("/api/v1/assurance/upload", s.handleAssuranceUpload)
	s.mux.HandleFunc("/api/v1/assurance/corpus", guard(middleware.PermissionParsePreview, s.handleAssuranceCorpus))
	s.mux.HandleFunc("/api/v1/assurance/cases", s.handleAssuranceCases)
	s.mux.HandleFunc("/api/v1/assurance/cases/", s.handleAssuranceCase)
	s.mux.HandleFunc("/api/v1/assurance/parsers", s.handleAssuranceParsers)
	s.mux.HandleFunc("/api/v1/assurance/parsers/", s.handleAssuranceParser)
	s.mux.HandleFunc("/api/v1/assurance/history", guard(middleware.PermissionReadParsers, s.handleAssuranceHistory))
	s.mux.HandleFunc("/api/v1/assurance/plans", s.handleAssurancePlans)
	s.mux.HandleFunc("/api/v1/assurance/plans/", s.handleAssurancePlan)
	s.mux.HandleFunc("/api/v1/assurance/runs", s.handleAssuranceRuns)
	s.mux.HandleFunc("/api/v1/assurance/runs/", s.handleAssuranceRunRecord)
	s.mux.HandleFunc("/api/v1/assurance/ai-proposal", guard(middleware.PermissionParsePreview, s.handleAssuranceAIProposal))
	s.mux.HandleFunc("/api/v1/assurance/ai-proposals", guard(middleware.PermissionReadParsers, s.handleAIProposals))
	s.mux.HandleFunc("/api/v1/assurance/ai-proposals/", guard(middleware.PermissionReadParsers, s.handleAIProposal))
	s.mux.HandleFunc("/api/v1/admin/ai-provider", s.handleAIProvider)
	s.mux.HandleFunc("/api/v1/admin/ai-provider/test", s.handleAIProviderTest)
	s.mux.HandleFunc("/api/v1/admin/ai-usage", guard(middleware.PermissionReadTelemetry, s.handleAIUsage))
	s.mux.HandleFunc("/api/v1/migration/import", guard(middleware.PermissionMigration, s.handleMigrationImport))
	s.mux.HandleFunc("/api/v1/migration/reports", guard(middleware.PermissionReadParsers, s.handleMigrationReports))
	s.mux.HandleFunc("/api/v1/migration/reports/", guard(middleware.PermissionReadParsers, s.handleMigrationReport))
	s.mux.HandleFunc("/api/v1/artifacts/upload", guard(middleware.PermissionUploadArtifact, s.handleArtifactUpload))
	s.mux.HandleFunc("/api/v1/artifacts", s.handleArtifacts)
	s.mux.HandleFunc("/api/v1/artifacts/", s.handleArtifact)
	s.mux.HandleFunc("/api/v1/upgrades", guard(middleware.PermissionTriggerRollout, s.handleUpgrade))
	s.mux.HandleFunc("/api/v1/rollouts", s.handleRollouts)
	s.mux.HandleFunc("/api/v1/rollouts/", s.handleRollout)
	s.mux.HandleFunc("/api/v1/users", s.handleUsers)
	s.mux.HandleFunc("/api/v1/users/", s.handleUser)
	s.mux.HandleFunc("/api/v1/rbac/roles", guard(middleware.PermissionSystemHealth, s.handleRoles))
	s.mux.HandleFunc("/api/v1/audit", guard(middleware.PermissionReadAudit, s.handleAudit))
	s.mux.HandleFunc("/api/v1/system", guard(middleware.PermissionSystemHealth, s.handleSystem))
	s.mux.HandleFunc("/api/v1/health", s.handleHealth)
	s.mux.Handle("/", http.FileServer(http.Dir(s.cfg.WebDir)))
}

func (s *Server) handleSampleCorpusVerification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(corpusevidence.JSON())
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, permission string) *middleware.SecurityContext {
	principal, err := s.security.AuthorizeRequest(r, permission)
	if err != nil {
		status := http.StatusForbidden
		if err.Error() == "unauthorized" {
			status = http.StatusUnauthorized
		}
		jsonError(w, err.Error(), status)
		return nil
	}
	return principal
}
func decodeJSON(r *http.Request, target any, max int64) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, max+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("body must contain one JSON object")
	}
	return nil
}
func extractToken(r *http.Request) string {
	if value := r.Header.Get("Authorization"); strings.HasPrefix(value, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	}
	if cookie, err := r.Cookie("nli_token"); err == nil {
		return cookie.Value
	}
	return ""
}
func jsonOK(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func jsonCreated(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(value)
}
func jsonError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-NLI-Controller", "v20")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Allow", "POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request, 4096); err != nil {
		jsonError(w, "invalid request", http.StatusBadRequest)
		return
	}
	token, user, err := s.auth.Authenticate(request.Username, request.Password)
	if err != nil {
		jsonError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "nli_token", Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.ForceSecureCookies || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 86400})
	_, _ = s.audit.Append(auditlog.Event{Action: "login", Actor: user.Username, Role: user.Role, Resource: "session", Outcome: "success", RemoteIP: r.RemoteAddr})
	jsonOK(w, map[string]any{"username": user.Username, "role": user.Role, "email": user.Email, "expires_in": 86400})
}
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "nli_token", Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.ForceSecureCookies || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: "nli_csrf", Value: "", Path: "/", MaxAge: -1})
	jsonOK(w, map[string]string{"status": "logged_out"})
}
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	principal := middleware.GetSecurityContext(r)
	if principal == nil {
		principal, _ = s.security.AuthorizeRequest(r, middleware.PermissionSystemHealth)
	}
	jsonOK(w, principal)
}
func (s *Server) handleCSRF(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	jsonOK(w, map[string]string{"csrf_token": s.security.IssueCSRF(w, r)})
}

func (s *Server) consumeEnrollmentToken(provided string) bool {
	s.enrollmentMu.Lock()
	defer s.enrollmentMu.Unlock()
	if subtle.ConstantTimeCompare([]byte(provided), []byte(s.enrollmentToken)) != 1 {
		return false
	}
	digest := sha256.Sum256([]byte(provided))
	fingerprint := hex.EncodeToString(digest[:])
	if data, err := os.ReadFile(s.enrollmentConsumedPath); err == nil {
		for _, consumed := range strings.Fields(string(data)) {
			if subtle.ConstantTimeCompare([]byte(consumed), []byte(fingerprint)) == 1 {
				return false
			}
		}
	} else if !os.IsNotExist(err) {
		return false
	}
	file, err := os.OpenFile(s.enrollmentConsumedPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return false
	}
	defer file.Close()
	if _, err := file.WriteString(fingerprint + "\n"); err != nil {
		return false
	}
	return file.Sync() == nil
}

func (s *Server) handleNodeRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var request protocol.RegisterRequest
	if err := decodeJSON(r, &request, 64<<10); err != nil || request.ID == "" || request.Hostname == "" || request.Arch == "" {
		jsonError(w, "valid node id, hostname and arch are required", http.StatusBadRequest)
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		jsonError(w, "credential generation failed", http.StatusInternalServerError)
		return
	}
	if !s.consumeEnrollmentToken(r.Header.Get("X-NLI-Enrollment")) {
		jsonError(w, "invalid or consumed enrollment token", http.StatusUnauthorized)
		return
	}
	node := &fleet.Node{ID: request.ID, Hostname: request.Hostname, IP: request.IP, OS: request.OS, Arch: request.Arch, Version: request.Version, ConfigHash: request.ConfigHash, MetricsURL: request.MetricsURL, Tags: request.Tags}
	s.fleet.Register(node)
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	if err := s.fleet.SetToken(request.ID, token); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Initial enrollment sync uses the same independent rollout approval gate
	// as every other parser bundle rollout.
	if bundle, err := s.buildActiveParserBundle(r.Context()); err == nil {
		_, _ = s.proposeParserBundleRollout(r.Context(), "nli-v20-auto-enrollment", "Initial parser asset synchronization", "Initial parser synchronization for a newly enrolled client requires independent rollout approval.", "selected", bundle, []string{request.ID})
	}
	jsonCreated(w, protocol.RegisterResponse{Status: "registered", NodeID: request.ID, NodeToken: token})
}
func (s *Server) authorizeNode(r *http.Request, nodeID string) bool {
	return nodeID != "" && s.fleet.Authorize(nodeID, extractToken(r))
}
func (s *Server) handleNodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var request protocol.HeartbeatRequest
	if err := decodeJSON(r, &request, 1<<20); err != nil {
		jsonError(w, "invalid heartbeat", http.StatusBadRequest)
		return
	}
	if !s.authorizeNode(r, request.NodeID) {
		jsonError(w, "unauthorized node", http.StatusUnauthorized)
		return
	}
	parsers := make([]fleet.ParserStatus, len(request.Parsers))
	for index, item := range request.Parsers {
		parsers[index] = fleet.ParserStatus{ID: item.ID, Version: item.Version, Description: item.Description, Source: item.Source, TestStatus: item.TestStatus}
	}
	for _, value := range []float64{request.Metrics.ParseRate, request.Metrics.ParseLatencyMs, request.Metrics.CPUPercent, request.Metrics.MemoryPercent, request.Metrics.Load1, request.Metrics.Load5, request.Metrics.Load15} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			jsonError(w, "heartbeat metrics must be finite and non-negative", http.StatusBadRequest)
			return
		}
	}
	if request.Metrics.CPUPercent > 100 || request.Metrics.MemoryPercent > 100 {
		jsonError(w, "cpu_percent and memory_percent must be at most 100", http.StatusBadRequest)
		return
	}
	if len(request.StreamFiles) > 256 {
		jsonError(w, "too many stream file samples", http.StatusBadRequest)
		return
	}
	for i := range request.StreamFiles {
		sample := &request.StreamFiles[i]
		if sample.Source == "" || len(sample.Source) > 4096 || sample.Phase == "" || sample.ReadLinesPerSecond < 0 || sample.NormalizedPerSecond < 0 || sample.QuarantinedPerSecond < 0 || sample.ValidationAttemptsPerSecond < 0 || sample.ValidationPassedPerSecond < 0 || sample.ValidationFailedPerSecond < 0 || sample.BytesPerSecond < 0 || sample.QueueDepth < 0 || sample.QueueCapacity < 0 || sample.ReadDuration < 0 || sample.ParseDuration < 0 || sample.OutputDuration < 0 || sample.TotalDuration < 0 || sample.ReadLines > 1<<60 || sample.Normalized > 1<<60 || sample.Quarantined > 1<<60 || sample.ValidationAttempts > 1<<60 || sample.ValidationPassed > 1<<60 || sample.ValidationFailed > 1<<60 || sample.Bytes > 1<<60 || sample.Errors > 1<<60 || math.IsNaN(sample.ReadLinesPerSecond) || math.IsInf(sample.ReadLinesPerSecond, 0) || math.IsNaN(sample.ValidationAttemptsPerSecond) || math.IsInf(sample.ValidationAttemptsPerSecond, 0) || math.IsNaN(sample.ValidationPassedPerSecond) || math.IsInf(sample.ValidationPassedPerSecond, 0) || math.IsNaN(sample.ValidationFailedPerSecond) || math.IsInf(sample.ValidationFailedPerSecond, 0) || math.IsNaN(sample.BytesPerSecond) || math.IsInf(sample.BytesPerSecond, 0) {
			jsonError(w, "invalid stream file sample", http.StatusBadRequest)
			return
		}
		sample.NodeID = request.NodeID
		s.streamTelemetry.Publish(*sample)
	}
	for _, value := range []uint64{request.Metrics.UPESUniqueFailureSignatures, request.Metrics.UPESObservedFailures, request.Metrics.UPESDiscoveryQueued, request.Metrics.UPESDiscoveryDropped, request.Metrics.UPESDiscoveryPending} {
		if value > 1<<60 {
			jsonError(w, "heartbeat UPES metrics exceed supported bounds", http.StatusBadRequest)
			return
		}
	}
	metrics := &fleet.NodeMetrics{Received: request.Metrics.Received, Parsed: request.Metrics.Parsed, Quarantined: request.Metrics.Quarantined, Errors: request.Metrics.Errors, ParseRate: request.Metrics.ParseRate, ParseLatencyMs: request.Metrics.ParseLatencyMs, CPUPercent: request.Metrics.CPUPercent, MemoryBytes: request.Metrics.MemoryBytes, MemoryPercent: request.Metrics.MemoryPercent, Load1: request.Metrics.Load1, Load5: request.Metrics.Load5, Load15: request.Metrics.Load15, HostMetricsAvailable: request.Metrics.HostMetricsAvailable, UPESMetricsAvailable: request.Metrics.UPESMetricsAvailable, UPESUniqueFailureSignatures: request.Metrics.UPESUniqueFailureSignatures, UPESObservedFailures: request.Metrics.UPESObservedFailures, UPESDiscoveryQueued: request.Metrics.UPESDiscoveryQueued, UPESDiscoveryDropped: request.Metrics.UPESDiscoveryDropped, UPESDiscoveryPending: request.Metrics.UPESDiscoveryPending}
	if err := s.fleet.UpdateHeartbeat(request.NodeID, request.Version, request.ConfigHash, request.Health, request.Issues, parsers, metrics); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	s.updateSyncFromHeartbeat(request)
	jsonOK(w, map[string]string{"status": "ok"})
}

func (s *Server) handleNodeStreamTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var request protocol.StreamTelemetryRequest
	if err := decodeJSON(r, &request, 256<<10); err != nil {
		jsonError(w, "invalid stream telemetry", http.StatusBadRequest)
		return
	}
	if !s.authorizeNode(r, request.NodeID) {
		jsonError(w, "unauthorized node", http.StatusUnauthorized)
		return
	}
	if len(request.StreamFiles) > 256 {
		jsonError(w, "too many stream file samples", http.StatusBadRequest)
		return
	}
	legacy := request.SchemaVersion == "" && request.ProcessGeneration == "" && request.Sequence == 0
	if !legacy {
		if request.SchemaVersion != telemetry.SyncSchemaVersion || len(request.ProcessGeneration) < 8 || len(request.ProcessGeneration) > 128 || request.Sequence == 0 || request.CollectionIntervalMS < 100 || request.CollectionIntervalMS > 60000 || request.PublicationIntervalMS < 100 || request.PublicationIntervalMS > 60000 || request.CollectedAt.IsZero() || request.PublishedAt.IsZero() {
			w.Header().Set("X-NLI-Diagnostic-Code", "sync_schema_invalid")
			jsonError(w, "invalid versioned synchronization envelope", http.StatusBadRequest)
			return
		}
	}
	for i := range request.StreamFiles {
		sample := &request.StreamFiles[i]
		values := []float64{sample.ReadLinesPerSecond, sample.NormalizedPerSecond, sample.QuarantinedPerSecond, sample.ValidationAttemptsPerSecond, sample.ValidationPassedPerSecond, sample.ValidationFailedPerSecond, sample.BytesPerSecond}
		invalid := sample.Source == "" || len(sample.Source) > 4096 || sample.Phase == "" || sample.QueueDepth < 0 || sample.QueueCapacity < 0 || sample.SourceWorkers < 0 || sample.ReadDuration < 0 || sample.ParseDuration < 0 || sample.OutputDuration < 0 || sample.TotalDuration < 0 || sample.ReadLines > 1<<60 || sample.Normalized > 1<<60 || sample.Quarantined > 1<<60 || sample.ValidationAttempts > 1<<60 || sample.ValidationPassed > 1<<60 || sample.ValidationFailed > 1<<60 || sample.Bytes > 1<<60 || sample.Errors > 1<<60
		for _, value := range values {
			if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				invalid = true
			}
		}
		if invalid {
			jsonError(w, "invalid stream file sample", http.StatusBadRequest)
			return
		}
		sample.NodeID = request.NodeID
	}
	if legacy {
		for _, sample := range request.StreamFiles {
			s.streamTelemetry.Publish(sample)
		}
		jsonOK(w, map[string]any{"status": "accepted_legacy", "schema_version": telemetry.SyncSchemaVersion, "upgrade_recommended": true})
		return
	}
	result := s.streamTelemetry.PublishBatch(request)
	if result.Status == "accepted" {
		// The canonical live state has already accepted this batch. Retention is
		// best-effort so a local history disk failure cannot trigger client retries
		// or turn an accepted publication into an apparent ingestion failure.
		_ = s.metricHistory.observe(s.streamTelemetry.Snapshot(), time.Now().UTC())
	}
	jsonOK(w, result)
}

func (s *Server) handleNodeNextCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	nodeID := r.URL.Query().Get("node_id")
	if !s.authorizeNode(r, nodeID) {
		jsonError(w, "unauthorized node", http.StatusUnauthorized)
		return
	}
	command, ok := s.rollouts.NextForNode(nodeID, time.Duration(s.cfg.CommandLeaseSeconds)*time.Second)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	jsonOK(w, command)
}
func (s *Server) handleNodeCommandResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	commandID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/node/commands/"), "/result")
	var result protocol.CommandResult
	if err := decodeJSON(r, &result, 64<<10); err != nil || commandID == "" || result.CommandID != commandID || !s.authorizeNode(r, result.NodeID) {
		jsonError(w, "unauthorized or inconsistent command result", http.StatusUnauthorized)
		return
	}
	if err := s.rollouts.RecordNodeResult(commandID, result.NodeID, result.Status, result.Message); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.recordInputPathRolloutResult(commandID, result.NodeID, result.Status, result.Message)
	s.recordQuarantineReplayResult(r.Context(), commandID, result.NodeID, result.Status, result.Message)
	jsonOK(w, map[string]string{"status": "recorded"})
}
func (s *Server) handleNodeEvents(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.EnableLegacyEventIngest {
		jsonError(w, "legacy event ingestion disabled; submit metadata telemetry", http.StatusGone)
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var batch protocol.EventBatch
	if err := decodeJSON(r, &batch, 8<<20); err != nil || !s.authorizeNode(r, batch.NodeID) {
		jsonError(w, "invalid or unauthorized event batch", http.StatusUnauthorized)
		return
	}
	if len(batch.Events) < 1 || len(batch.Events) > 500 {
		jsonError(w, "event batch must contain 1..500 records", http.StatusBadRequest)
		return
	}
	for _, raw := range batch.Events {
		digest := sha256.Sum256(raw)
		record, _ := s.events.Append(batch.NodeID, eventstore.TelemetryMetadata{EventHash: hex.EncodeToString(digest[:]), Service: "legacy", ParserID: "legacy", Route: "unknown", Result: "legacy_metadata_only"})
		message, _ := json.Marshal(map[string]any{"type": "event_metadata", "record": record})
		s.hub.Publish(message)
	}
	jsonOK(w, map[string]any{"status": "accepted_metadata_only", "count": len(batch.Events)})
}

func (s *Server) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.security.Require(middleware.PermissionReadTelemetry, s.telemetry.GetTelemetry)(w, r)
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}
	record, err := s.telemetry.Decode(r.Body)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	authorized := s.authorizeNode(r, record.NodeID)
	if !authorized {
		principal, authErr := s.security.AuthorizeRequest(r, middleware.PermissionWriteTelemetry)
		authorized = authErr == nil && principal != nil
	}
	if !authorized {
		jsonError(w, "node or telemetry-writer authorization required", http.StatusUnauthorized)
		return
	}
	stored, err := s.telemetry.Store(record)
	if err != nil {
		jsonError(w, "telemetry storage failed", http.StatusInternalServerError)
		return
	}
	message, _ := json.Marshal(map[string]any{"type": "telemetry", "record": stored})
	s.hub.Publish(message)
	jsonCreated(w, map[string]any{"status": "accepted", "event_id": stored.EventID, "payload_retained": false})
}
func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	jsonOK(w, map[string]any{"records": s.events.Query(eventQuery(r)), "stats": s.events.Stats(), "payload_retained": false})
}

func (s *Server) handleDashboardSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	if _, _, _, ok := queryTimeBounds(r.URL.Query(), s.metricHistory, time.Now().UTC()); !ok {
		jsonError(w, "range must be a valid duration or from/to RFC3339 pair", http.StatusBadRequest)
		return
	}
	query := r.URL.Query()
	call := func(handler http.HandlerFunc) (json.RawMessage, error) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1", nil).WithContext(r.Context())
		req.URL.RawQuery = query.Encode()
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		if recorder.Code < 200 || recorder.Code >= 300 {
			return nil, fmt.Errorf("dashboard component returned %d", recorder.Code)
		}
		var value json.RawMessage
		if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
			return nil, err
		}
		return value, nil
	}
	parts := map[string]json.RawMessage{}
	principal := middleware.GetSecurityContext(r)
	canRead := func(permission string) bool {
		if principal == nil {
			return false
		}
		for _, granted := range middleware.RolePermissions[principal.Role] {
			if granted == permission {
				return true
			}
		}
		return false
	}
	components := map[string]http.HandlerFunc{}
	if canRead(middleware.PermissionReadTelemetry) {
		components["metadata"] = s.handleMetadata
		components["timeseries"] = s.handleMetricsTimeSeries
	}
	if canRead(middleware.PermissionSystemHealth) {
		components["ai_usage"] = s.handleAIUsage
	}
	if canRead(middleware.PermissionSystemHealth) {
		components["system"] = s.handleSystem
		if canRead(middleware.PermissionReadFleet) {
			components["overview"] = s.handleOverview
		}
	}
	if canRead(middleware.PermissionReadFleet) {
		components["fleet_sync"] = s.handleFleetSync
	}
	for name, handler := range components {
		value, err := call(handler)
		if err != nil {
			jsonError(w, "dashboard snapshot temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		parts[name] = value
	}
	if canRead(middleware.PermissionReadTelemetry) {
		files, _ := json.Marshal(s.streamTelemetry.Snapshot())
		parts["stream_files"] = files
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-NLI-Dashboard-Bounded", "true")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{"))
	first := true
	for name, value := range parts {
		if !first {
			_, _ = w.Write([]byte(","))
		}
		first = false
		key, _ := json.Marshal(name)
		_, _ = w.Write(key)
		_, _ = w.Write([]byte(":"))
		_, _ = w.Write(value)
	}
	_, _ = w.Write([]byte("}"))
}
func eventQuery(r *http.Request) eventstore.Query {
	limit := 100
	if value, _ := strconv.Atoi(r.URL.Query().Get("limit")); value > 0 && value <= 1000 {
		limit = value
	}
	query := eventstore.Query{NodeID: r.URL.Query().Get("node_id"), Service: r.URL.Query().Get("service"), Parser: r.URL.Query().Get("parser"), Route: r.URL.Query().Get("route"), Search: r.URL.Query().Get("q"), Limit: limit}
	if value, err := time.Parse(time.RFC3339, r.URL.Query().Get("after")); err == nil {
		query.After = value
	}
	return query
}
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) { s.hub.ServeHTTP(w, r) }

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	jsonOK(w, s.fleet.All())
}
func (s *Server) handleFleetStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	jsonOK(w, s.fleet.Stats())
}
func (s *Server) handleBuiltinParsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	jsonOK(w, s.parsers.All())
}
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := contextWithTimeout(r, 2*time.Second)
	defer cancel()
	storageStatus := "ok"
	if err := s.metadata.Health(ctx); err != nil {
		storageStatus = "degraded"
	}
	jsonOK(w, map[string]any{"fleet": s.fleet.Stats(), "metadata": s.events.Stats(), "services": s.events.Services(), "active_rollouts": countActive(s.rollouts.List()), "storage": map[string]any{"mode": s.metadata.Mode(), "status": storageStatus, "warning": s.storageWarning}, "version": engine.Version})
}
func countActive(items []*rolloutRecord) int {
	count := 0
	for _, item := range items {
		if item.Status == rolloutStatusRunning {
			count++
		}
	}
	return count
}
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-NLI-Controller", "v20")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := contextWithTimeout(r, time.Second)
	defer cancel()
	storage := "ok"
	diagnostic := "ok"
	if err := s.metadata.Health(ctx); err != nil {
		storage = "degraded"
		diagnostic = storageDiagnostic(err.Error())
	}
	status := "ok"
	if storage != "ok" {
		status = "degraded"
	}
	jsonOK(w, map[string]any{"service": "nli-controller-agent", "api_version": "v1", "status": status, "version": engine.Version, "storage": storage, "storage_diagnostic": diagnostic, "time": time.Now().UTC()})
}

func storageDiagnostic(message string) string {
	message = strings.ToLower(message)
	switch {
	case strings.TrimSpace(message) == "":
		return "ok"
	case strings.Contains(message, "http 401"):
		return "invalid_public_api_key"
	case strings.Contains(message, "http 403"), strings.Contains(message, "controller authentication failed"):
		return "controller_secret_mismatch_or_rpc_permission"
	case strings.Contains(message, "schema version"), strings.Contains(message, "function") && strings.Contains(message, "does not exist"):
		return "schema_missing_or_outdated"
	default:
		return "storage_unavailable"
	}
}
func contextWithTimeout(r *http.Request, duration time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), duration)
}
