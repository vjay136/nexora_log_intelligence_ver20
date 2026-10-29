// Package controlclient implements the privileged, allowlisted client sidecar
// used for fleet enrollment and controller-driven configuration rollouts.
package controlclient

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nexora.local/nli-v20/internal/agent"
	"nexora.local/nli-v20/internal/engine"
	"nexora.local/nli-v20/internal/protocol"
	"nexora.local/nli-v20/internal/registry"
	"nexora.local/nli-v20/internal/rollout"
	"nexora.local/nli-v20/parser-policy"
	"nexora.local/nli-v20/telemetry"
)

const maxPackageBytes = 512 * 1024 * 1024

type Config struct {
	ControllerURL        string            `json:"controller_url"`
	CAFile               string            `json:"ca_file,omitempty"`
	EnrollmentTokenEnv   string            `json:"enrollment_token_env"`
	NodeTokenFile        string            `json:"node_token_file"`
	InstallIDFile        string            `json:"install_id_file"`
	JournalFile          string            `json:"journal_file"`
	AgentConfigPath      string            `json:"agent_config_path"`
	ParserDir            string            `json:"parser_dir"`
	AssetDir             string            `json:"asset_dir,omitempty"`
	MetricsURL           string            `json:"metrics_url"`
	PollSeconds          int               `json:"poll_seconds"`
	TelemetryPollSeconds int               `json:"telemetry_poll_seconds"`
	Tags                 map[string]string `json:"tags"`
	AllowInsecureHTTP    bool              `json:"allow_insecure_http"`
	AllowPackageUpdates  bool              `json:"allow_package_updates"`
	AllowedPackageHosts  []string          `json:"allowed_package_hosts"`
	PackageSpoolDir      string            `json:"package_spool_dir"`
	QuarantinePath       string            `json:"quarantine_path"`
	QuarantineRawUpload  bool              `json:"quarantine_raw_upload_enabled"`
	QuarantineMaxRecords int               `json:"quarantine_raw_upload_max_records"`
	QuarantineMaxBytes   int               `json:"quarantine_raw_upload_max_bytes"`
}

func Load(path string) (Config, error) {
	var cfg Config
	file, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, err
	}
	cfg.setDefaults()
	return cfg, cfg.validate()
}

func (c *Config) setDefaults() {
	if c.EnrollmentTokenEnv == "" {
		c.EnrollmentTokenEnv = "NLI_ENROLLMENT_TOKEN"
	}
	if c.NodeTokenFile == "" {
		c.NodeTokenFile = "/var/lib/nli-control-client/node.token"
	}
	if c.InstallIDFile == "" {
		c.InstallIDFile = "/var/lib/nli-control-client/install.id"
	}
	if c.JournalFile == "" {
		c.JournalFile = "/var/lib/nli-control-client/journal.json"
	}
	if c.AgentConfigPath == "" {
		c.AgentConfigPath = "/etc/nli/nli.json"
	}
	if c.ParserDir == "" {
		c.ParserDir = "/var/lib/nli/parsers"
	}
	if c.AssetDir == "" {
		c.AssetDir = "/var/lib/nli/parser-assets"
	}
	if c.MetricsURL == "" {
		c.MetricsURL = "http://127.0.0.1:12380/metrics"
	}
	if c.PollSeconds <= 0 {
		c.PollSeconds = 15
	}
	if c.TelemetryPollSeconds <= 0 {
		c.TelemetryPollSeconds = 1
	}
	if c.PackageSpoolDir == "" {
		c.PackageSpoolDir = "/var/cache/nli/packages"
	}
	if c.QuarantinePath == "" {
		c.QuarantinePath = "/var/log/nli/quarantine.jsonl"
	}
	if c.QuarantineMaxRecords == 0 {
		c.QuarantineMaxRecords = 100
	}
	if c.QuarantineMaxBytes == 0 {
		c.QuarantineMaxBytes = 1 << 20
	}
}

func (c Config) validate() error {
	endpoint, err := url.Parse(c.ControllerURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
		return fmt.Errorf("controller_url must be an absolute http(s) URL")
	}
	hostname := strings.ToLower(strings.TrimSuffix(endpoint.Hostname(), "."))
	if hostname == "example.com" || strings.HasSuffix(hostname, ".example.com") || hostname == "example" || strings.HasSuffix(hostname, ".example") {
		return fmt.Errorf("controller_url uses a documentation-only placeholder host: %s", endpoint.Hostname())
	}
	if endpoint.Scheme != "https" && !c.AllowInsecureHTTP {
		return fmt.Errorf("controller_url must use HTTPS unless allow_insecure_http is explicitly enabled")
	}
	if c.CAFile != "" {
		if endpoint.Scheme != "https" {
			return fmt.Errorf("ca_file is valid only with an HTTPS controller_url")
		}
		if !filepath.IsAbs(c.CAFile) {
			return fmt.Errorf("ca_file must be absolute: %s", c.CAFile)
		}
	}
	for _, path := range []string{c.NodeTokenFile, c.InstallIDFile, c.JournalFile, c.AgentConfigPath, c.ParserDir, c.PackageSpoolDir} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("managed paths must be absolute: %s", path)
		}
	}
	if c.PollSeconds < 5 || c.PollSeconds > 3600 {
		return fmt.Errorf("poll_seconds must be 5..3600")
	}
	if c.TelemetryPollSeconds < 1 || c.TelemetryPollSeconds > 60 {
		return fmt.Errorf("telemetry_poll_seconds must be 1..60")
	}
	if c.QuarantineMaxRecords < 1 || c.QuarantineMaxRecords > 1000 || c.QuarantineMaxBytes < 1 || c.QuarantineMaxBytes > 4<<20 {
		return fmt.Errorf("quarantine raw upload bounds are invalid")
	}
	return nil
}

func controllerHTTPClient(cfg Config) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Keep controller connections pooled per client. Across a 1,000-node fleet
	// this avoids a TLS handshake for every one-second telemetry publication,
	// while the per-process cap prevents a failed controller from accumulating
	// unbounded connections.
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 8
	transport.MaxConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	if cfg.CAFile != "" {
		pemData, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read controller ca_file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("controller ca_file contains no valid PEM certificates")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}, nil
}

type Executor interface {
	RestartAgent(context.Context) error
	Replay(context.Context, string, []string) (string, error)
	UpgradePackage(context.Context, string) error
}

type SystemExecutor struct{}

func (SystemExecutor) RestartAgent(ctx context.Context) error {
	return exec.CommandContext(ctx, "/usr/bin/systemctl", "restart", "nli-agent.service").Run()
}
func (SystemExecutor) Replay(ctx context.Context, path string, recordIDs []string) (string, error) {
	args := []string{"replay", "--config", "/etc/nli/nli.json", "--quarantine", path}
	if len(recordIDs) > 0 {
		args = append(args, "--record-ids", strings.Join(recordIDs, ","))
	}
	output, err := exec.CommandContext(ctx, "/usr/bin/nli", args...).Output()
	return strings.TrimSpace(string(output)), err
}
func (SystemExecutor) UpgradePackage(ctx context.Context, path string) error {
	return exec.CommandContext(ctx, "/usr/bin/dnf", "-y", "upgrade", path).Run()
}

type Client struct {
	cfg               Config
	http              *http.Client
	executor          Executor
	pollInterval      time.Duration
	mu                sync.Mutex
	nodeID            string
	token             string
	journal           map[string]protocol.CommandResult
	processGeneration string
	syncSequence      atomic.Uint64
}

func New(cfg Config, executor Executor) (*Client, error) {
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	httpClient, err := controllerHTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	if executor == nil {
		executor = SystemExecutor{}
	}
	client := &Client{
		cfg: cfg, executor: executor, journal: make(map[string]protocol.CommandResult),
		http: httpClient, pollInterval: time.Duration(cfg.PollSeconds) * time.Second,
		processGeneration: newProcessGeneration(),
	}
	client.loadJournal()
	return client, nil
}

func (c *Client) Run(ctx context.Context) error {
	var streamReady atomic.Bool
	go func() {
		interval := time.Duration(c.cfg.TelemetryPollSeconds) * time.Second
		timer := time.NewTimer(telemetryJitter(interval))
		defer timer.Stop()
		failures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				next := interval
				if streamReady.Load() {
					if err := c.sendStreamTelemetry(ctx); err != nil && !errors.Is(err, context.Canceled) {
						failures++
						fmt.Fprintf(os.Stderr, "nli-client-control: stream telemetry pending: %v\n", err)
						next = telemetryRetryDelay(interval, failures)
					} else {
						failures = 0
					}
				}
				timer.Reset(next)
			}
		}
	}()
	interval := c.pollInterval
	if interval <= 0 {
		interval = time.Duration(c.cfg.PollSeconds) * time.Second
	}
	for {
		if err := c.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "nli-client-control: %v\n", err)
		} else {
			streamReady.Store(true)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func telemetryJitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return limit / 2
	}
	var value uint64
	for _, octet := range random {
		value = value<<8 | uint64(octet)
	}
	return time.Duration(value % uint64(limit))
}

func telemetryRetryDelay(interval time.Duration, failures int) time.Duration {
	if interval <= 0 {
		interval = time.Second
	}
	if failures < 1 {
		return interval
	}
	shift := failures - 1
	if shift > 5 {
		shift = 5
	}
	delay := interval * time.Duration(1<<shift)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	// Add up to one publication interval of jitter so a recovered controller is
	// not hit by every enrolled client in the same scheduler tick.
	return delay + telemetryJitter(interval)
}

// RunOnce sends one heartbeat and applies at most one leased command.
func (c *Client) RunOnce(ctx context.Context) error {
	if err := c.EnsureEnrollment(ctx); err != nil {
		return err
	}
	if err := c.heartbeat(ctx); err != nil {
		return err
	}
	if err := c.uploadQuarantineRecords(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "nli-client-control: quarantine queue sync pending: %v\n", err)
	}
	command, err := c.nextCommand(ctx)
	if err != nil || command == nil {
		return err
	}
	if previous, ok := c.journal[command.ID]; ok {
		return c.postResult(ctx, previous)
	}
	result := protocol.CommandResult{
		NodeID: c.nodeID, CommandID: command.ID, Status: rollout.StatusCompleted,
		StartedAt: time.Now().UTC(),
	}
	message, applyErr := c.apply(ctx, command)
	result.FinishedAt = time.Now().UTC()
	result.Message = message
	result.ConfigHash = fileSHA256(c.cfg.AgentConfigPath)
	if applyErr != nil {
		result.Status = rollout.StatusFailed
		result.Message = applyErr.Error()
	}
	c.journal[command.ID] = result
	if err := c.saveJournal(); err != nil {
		return err
	}
	return c.postResult(ctx, result)
}

func (c *Client) uploadQuarantineRecords(ctx context.Context) error {
	file, err := os.Open(c.cfg.QuarantinePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	maxBytes := c.cfg.QuarantineMaxBytes
	if maxBytes < 1 || maxBytes > 4<<20 {
		return fmt.Errorf("quarantine raw upload max bytes must be 1..4194304")
	}
	maxRecords := c.cfg.QuarantineMaxRecords
	if maxRecords < 1 || maxRecords > 1000 {
		return fmt.Errorf("quarantine raw upload max records must be 1..1000")
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	var records []json.RawMessage
	used := 0
	for scanner.Scan() {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		status := "waiting"
		_ = json.Unmarshal(record["status"], &status)
		if status != "waiting" {
			continue
		}
		if !c.cfg.QuarantineRawUpload {
			delete(record, "raw")
		}
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if len(line) > maxBytes && c.cfg.QuarantineRawUpload {
			delete(record, "raw")
			record["raw_omitted"] = json.RawMessage(`"record_exceeds_upload_batch_limit"`)
			line, err = json.Marshal(record)
			if err != nil {
				return err
			}
		}
		if len(line)+used > maxBytes {
			continue
		}
		records = append(records, line)
		used += len(line)
		if len(records) >= maxRecords {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	return c.requestJSON(ctx, http.MethodPost, "/api/v1/node/quarantine/records", map[string]any{"records": records, "raw_included": c.cfg.QuarantineRawUpload}, nil, nil)
}

func (c *Client) EnsureEnrollment(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nodeID == "" {
		id, err := c.loadOrCreateNodeID()
		if err != nil {
			return err
		}
		c.nodeID = id
	}
	if c.token == "" {
		if data, err := os.ReadFile(c.cfg.NodeTokenFile); err == nil {
			c.token = strings.TrimSpace(string(data))
		}
	}
	if c.token != "" {
		return nil
	}

	enrollment := os.Getenv(c.cfg.EnrollmentTokenEnv)
	if len(enrollment) < 24 {
		return fmt.Errorf("%s must contain at least 24 characters", c.cfg.EnrollmentTokenEnv)
	}
	hostname, _ := os.Hostname()
	request := protocol.RegisterRequest{
		ID: c.nodeID, Hostname: hostname, IP: primaryIP(), OS: osRelease(), Arch: normalizedArch(),
		Version: engine.Version, ConfigHash: fileSHA256(c.cfg.AgentConfigPath), MetricsURL: c.cfg.MetricsURL,
		Tags: c.cfg.Tags,
	}
	var response protocol.RegisterResponse
	if err := c.requestJSON(ctx, http.MethodPost, "/api/v1/node/register", request, &response, map[string]string{"X-NLI-Enrollment": enrollment}); err != nil {
		return err
	}
	if response.NodeToken == "" {
		return fmt.Errorf("controller returned an empty node token")
	}
	if err := atomicWrite(c.cfg.NodeTokenFile, []byte(response.NodeToken+"\n"), 0640); err != nil {
		return err
	}
	c.token = response.NodeToken
	return nil
}

func (c *Client) heartbeat(ctx context.Context) error {
	parsers := parserInventory(c.cfg.ParserDir)
	metrics := readMetrics(c.cfg.MetricsURL)
	bundleHash, assetRevisions := activeAssetBundleStatus(filepath.Join(c.cfg.AssetDir, "bundle.json"))
	request := protocol.HeartbeatRequest{
		NodeID: c.nodeID, Version: engine.Version, ConfigHash: fileSHA256(c.cfg.AgentConfigPath),
		Metrics: metrics, Health: "online", Parsers: parsers, ActiveAssetBundleHash: bundleHash, ActiveAssetRevisions: assetRevisions,
	}
	return c.requestJSON(ctx, http.MethodPost, "/api/v1/node/heartbeat", request, nil, nil)
}

func (c *Client) sendStreamTelemetry(ctx context.Context) error {
	collectedAt := time.Now().UTC()
	files, err := readStreamFiles(c.cfg.MetricsURL)
	if err != nil {
		return fmt.Errorf("read agent stream snapshot: %w", err)
	}
	request := protocol.StreamTelemetryRequest{SchemaVersion: telemetry.SyncSchemaVersion, NodeID: c.nodeID, ProcessGeneration: c.processGeneration, Sequence: c.syncSequence.Add(1), CollectedAt: collectedAt, PublishedAt: time.Now().UTC(), CollectionIntervalMS: int64(c.cfg.TelemetryPollSeconds) * 1000, PublicationIntervalMS: int64(c.cfg.TelemetryPollSeconds) * 1000, FullSnapshot: true, StreamFiles: files}
	return c.requestJSON(ctx, http.MethodPost, "/api/v1/node/stream-telemetry", request, nil, nil)
}

func newProcessGeneration() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err == nil {
		return hex.EncodeToString(value)
	}
	return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
}

func readStreamFiles(metricsURL string) ([]telemetry.StreamFileTelemetry, error) {
	parsed, err := url.Parse(metricsURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("metrics_url must be an absolute HTTP URL")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/metrics") + "/stream-snapshot"
	if strings.HasSuffix(parsed.Path, "/") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(parsed.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent stream snapshot returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Files []telemetry.StreamFileTelemetry `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode agent stream snapshot: %w", err)
	}
	if len(payload.Files) > 256 {
		payload.Files = payload.Files[:256]
	}
	return payload.Files, nil
}

func (c *Client) nextCommand(ctx context.Context) (*protocol.Command, error) {
	var command protocol.Command
	path := "/api/v1/node/commands/next?node_id=" + url.QueryEscape(c.nodeID)
	status, err := c.requestJSONStatus(ctx, http.MethodGet, path, nil, &command, nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	return &command, nil
}

func (c *Client) postResult(ctx context.Context, result protocol.CommandResult) error {
	path := "/api/v1/node/commands/" + url.PathEscape(result.CommandID) + "/result"
	return c.requestJSON(ctx, http.MethodPost, path, result, nil, nil)
}

func (c *Client) apply(ctx context.Context, command *protocol.Command) (string, error) {
	switch command.Type {
	case rollout.TypeConfigPush:
		return c.applyConfig(ctx, command.Payload)
	case rollout.TypeParserSync:
		return c.applyParsers(ctx, command.Payload)
	case rollout.TypeRestart:
		return "nli-agent restarted", c.executor.RestartAgent(ctx)
	case rollout.TypeReplay:
		var request struct {
			RecordIDs []string `json:"record_ids"`
		}
		if len(command.Payload) > 0 {
			if err := json.Unmarshal(command.Payload, &request); err != nil {
				return "", fmt.Errorf("invalid quarantine replay payload: %w", err)
			}
		}
		if len(request.RecordIDs) > 1000 {
			return "", fmt.Errorf("quarantine replay is limited to 1000 records")
		}
		return c.executor.Replay(ctx, c.cfg.QuarantinePath, request.RecordIDs)
	case rollout.TypePackageUpdate:
		return c.applyPackage(ctx, command.Payload)
	default:
		return "", fmt.Errorf("unsupported command type %q", command.Type)
	}
}

func (c *Client) applyConfig(ctx context.Context, payload json.RawMessage) (string, error) {
	var wrapper struct {
		Config      json.RawMessage `json:"config"`
		ConfigPatch json.RawMessage `json:"config_patch"`
	}
	if err := json.Unmarshal(payload, &wrapper); err != nil {
		return "", fmt.Errorf("config payload is not valid JSON: %w", err)
	}
	configBytes := wrapper.Config
	if len(wrapper.ConfigPatch) > 0 {
		if len(wrapper.Config) > 0 {
			return "", fmt.Errorf("config payload cannot contain both config and config_patch")
		}
		current, err := os.ReadFile(c.cfg.AgentConfigPath)
		if err != nil {
			return "", fmt.Errorf("read current agent config for patch: %w", err)
		}
		configBytes, err = mergeTopLevelConfig(current, wrapper.ConfigPatch)
		if err != nil {
			return "", err
		}
	}
	if len(configBytes) == 0 {
		configBytes = payload
	}
	if !json.Valid(configBytes) {
		return "", fmt.Errorf("config payload is not valid JSON")
	}
	validationPath := c.cfg.AgentConfigPath + ".validate"
	if err := atomicWrite(validationPath, configBytes, 0640); err != nil {
		return "", err
	}
	_, validationErr := agent.Load(validationPath)
	_ = os.Remove(validationPath)
	if validationErr != nil {
		return "", fmt.Errorf("config validation: %w", validationErr)
	}
	if err := backupAndWrite(c.cfg.AgentConfigPath, configBytes, 0640); err != nil {
		return "", err
	}
	if err := c.executor.RestartAgent(ctx); err != nil {
		_ = restoreBackup(c.cfg.AgentConfigPath)
		return "", fmt.Errorf("restart failed; config restored: %w", err)
	}
	return "configuration validated, applied, and agent restarted", nil
}

func mergeTopLevelConfig(current, patch json.RawMessage) ([]byte, error) {
	var base map[string]json.RawMessage
	var updates map[string]json.RawMessage
	if err := json.Unmarshal(current, &base); err != nil || base == nil {
		return nil, fmt.Errorf("current agent config is not a JSON object")
	}
	if err := json.Unmarshal(patch, &updates); err != nil || len(updates) == 0 {
		return nil, fmt.Errorf("config_patch must be a non-empty JSON object")
	}
	for key, value := range updates {
		if strings.TrimSpace(key) == "" || len(value) == 0 || string(value) == "null" {
			return nil, fmt.Errorf("config_patch key %q must have a non-null value", key)
		}
		base[key] = append(json.RawMessage(nil), value...)
	}
	merged, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode patched agent config: %w", err)
	}
	return append(merged, '\n'), nil
}

func (c *Client) applyParsers(ctx context.Context, payload json.RawMessage) (string, error) {
	var wrapper struct {
		Parsers     []*registry.ParserDef `json:"parsers"`
		AssetBundle json.RawMessage       `json:"asset_bundle,omitempty"`
	}
	if err := json.Unmarshal(payload, &wrapper); err != nil {
		return "", err
	}
	if len(wrapper.AssetBundle) > 0 {
		return c.applyAssetBundle(ctx, wrapper.AssetBundle)
	}
	if len(wrapper.Parsers) == 0 {
		return "", fmt.Errorf("parser_sync requires parsers")
	}
	staging := c.cfg.ParserDir + ".staging"
	_ = os.RemoveAll(staging)
	if err := prepareParserRuntimeDirectory(staging); err != nil {
		return "", err
	}
	reg := registry.New()
	for _, parser := range wrapper.Parsers {
		if failures := registry.RunRegressionTests(parser); len(failures) > 0 {
			_ = os.RemoveAll(staging)
			return "", fmt.Errorf("parser %s regression failed: %s", parser.ID, strings.Join(failures, "; "))
		}
		if err := reg.SaveParser(staging, parser); err != nil {
			_ = os.RemoveAll(staging)
			return "", err
		}
	}
	parserEntries, err := os.ReadDir(staging)
	if err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}
	for _, entry := range parserEntries {
		if entry.IsDir() {
			continue
		}
		if err := ensureNLIGroupReadable(filepath.Join(staging, entry.Name())); err != nil {
			_ = os.RemoveAll(staging)
			return "", err
		}
	}
	backup := c.cfg.ParserDir + ".backup"
	_ = os.RemoveAll(backup)
	if _, err := os.Stat(c.cfg.ParserDir); err == nil {
		if err := os.Rename(c.cfg.ParserDir, backup); err != nil {
			return "", err
		}
	}
	if err := os.Rename(staging, c.cfg.ParserDir); err != nil {
		_ = os.Rename(backup, c.cfg.ParserDir)
		return "", err
	}
	if err := c.executor.RestartAgent(ctx); err != nil {
		_ = os.RemoveAll(c.cfg.ParserDir)
		_ = os.Rename(backup, c.cfg.ParserDir)
		return "", fmt.Errorf("restart failed; parser set restored: %w", err)
	}
	return fmt.Sprintf("%d parsers validated and synchronized", len(wrapper.Parsers)), nil
}

type synchronizedAsset struct {
	AssetClass string          `json:"asset_class"`
	AssetID    string          `json:"asset_id"`
	Name       string          `json:"name"`
	Revision   int             `json:"revision"`
	SHA256     string          `json:"sha256"`
	Payload    json.RawMessage `json:"payload"`
}
type synchronizedAssetBundle struct {
	SchemaVersion int                 `json:"schema_version"`
	Assets        []synchronizedAsset `json:"assets"`
	BundleHash    string              `json:"bundle_hash"`
}

func safeSynchronizedAssetID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || (i > 0 && (char == '-' || char == '_' || char == '.')) {
			continue
		}
		return false
	}
	return true
}
func synchronizedBundleHash(bundle synchronizedAssetBundle) (string, error) {
	unsigned, err := json.Marshal(struct {
		SchemaVersion int                 `json:"schema_version"`
		Assets        []synchronizedAsset `json:"assets"`
	}{bundle.SchemaVersion, bundle.Assets})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(unsigned)
	return hex.EncodeToString(sum[:]), nil
}

func (c *Client) applyAssetBundle(ctx context.Context, data []byte) (string, error) {
	if len(data) == 0 || len(data) > 16<<20 {
		return "", errors.New("parser asset bundle exceeds 16 MiB bound")
	}
	var bundle synchronizedAssetBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return "", err
	}
	if bundle.SchemaVersion != 1 || len(bundle.Assets) > 10000 {
		return "", errors.New("unsupported parser asset bundle schema or size")
	}
	computed, err := synchronizedBundleHash(bundle)
	if err != nil {
		return "", err
	}
	if computed != bundle.BundleHash {
		return "", errors.New("parser asset bundle hash verification failed")
	}
	allowed := map[string]string{"kv-config": "kv", "static-patterns": "static-patterns", "projections": "projections", "templates": "templates", "regex-patterns": "regex", "known-keys": "known-keys", "semantic-extraction": "semantic-extraction", "parser-packs": ""}
	parent := filepath.Dir(c.cfg.AssetDir)
	if err := os.MkdirAll(parent, 0750); err != nil {
		return "", err
	}
	staging := c.cfg.AssetDir + ".staging"
	backup := c.cfg.AssetDir + ".backup"
	parserStaging := c.cfg.ParserDir + ".bundle-staging"
	parserBackup := c.cfg.ParserDir + ".bundle-backup"
	_ = os.RemoveAll(staging)
	_ = os.RemoveAll(parserStaging)
	if err := os.MkdirAll(staging, 0750); err != nil {
		return "", err
	}
	if err := copyParserDirectory(c.cfg.ParserDir, parserStaging); err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}
	seen := map[string]bool{}
	for _, asset := range bundle.Assets {
		dir, ok := allowed[asset.AssetClass]
		if !ok || !safeSynchronizedAssetID(asset.AssetID) || asset.Revision < 1 || len(asset.SHA256) != 64 || !json.Valid(asset.Payload) {
			_ = os.RemoveAll(staging)
			return "", errors.New("parser asset has invalid class, ID, revision, hash or payload")
		}
		sum := sha256.Sum256(asset.Payload)
		if hex.EncodeToString(sum[:]) != asset.SHA256 {
			_ = os.RemoveAll(staging)
			return "", fmt.Errorf("parser asset %s/%s revision hash mismatch", asset.AssetClass, asset.AssetID)
		}
		key := asset.AssetClass + "/" + asset.AssetID
		if seen[key] {
			_ = os.RemoveAll(staging)
			return "", errors.New("duplicate parser asset in bundle")
		}
		seen[key] = true
		if asset.AssetClass == "parser-packs" {
			pack, err := parserpolicy.LoadParserPackBytes(asset.Payload)
			if err != nil || pack.ID != asset.AssetID {
				_ = os.RemoveAll(staging)
				_ = os.RemoveAll(parserStaging)
				return "", errors.New("published parser pack failed strict runtime validation")
			}
			if err := writeSynchronizedAsset(filepath.Join(parserStaging, asset.AssetID+".json"), asset.Payload); err != nil {
				_ = os.RemoveAll(staging)
				_ = os.RemoveAll(parserStaging)
				return "", err
			}
			continue
		}
		assetDir := filepath.Join(staging, dir)
		if err := os.MkdirAll(assetDir, 0750); err != nil {
			_ = os.RemoveAll(staging)
			_ = os.RemoveAll(parserStaging)
			return "", err
		}
		encoded, err := json.Marshal(asset)
		if err != nil {
			_ = os.RemoveAll(staging)
			return "", err
		}
		if err := writeSynchronizedAsset(filepath.Join(assetDir, asset.AssetID+".json"), encoded); err != nil {
			_ = os.RemoveAll(staging)
			return "", err
		}
	}
	if err := writeSynchronizedAsset(filepath.Join(staging, "bundle.json"), data); err != nil {
		_ = os.RemoveAll(staging)
		_ = os.RemoveAll(parserStaging)
		return "", err
	}
	_ = os.RemoveAll(backup)
	_ = os.RemoveAll(parserBackup)
	if _, err := os.Stat(c.cfg.AssetDir); err == nil {
		if err := os.Rename(c.cfg.AssetDir, backup); err != nil {
			_ = os.RemoveAll(staging)
			return "", err
		}
	}
	if _, err := os.Stat(c.cfg.ParserDir); err == nil {
		if err := os.Rename(c.cfg.ParserDir, parserBackup); err != nil {
			_ = os.Rename(backup, c.cfg.AssetDir)
			_ = os.RemoveAll(staging)
			_ = os.RemoveAll(parserStaging)
			return "", err
		}
	}
	if err := os.Rename(parserStaging, c.cfg.ParserDir); err != nil {
		_ = os.Rename(parserBackup, c.cfg.ParserDir)
		_ = os.Rename(backup, c.cfg.AssetDir)
		_ = os.RemoveAll(staging)
		return "", err
	}
	if err := os.Rename(staging, c.cfg.AssetDir); err != nil {
		_ = os.RemoveAll(c.cfg.ParserDir)
		_ = os.Rename(parserBackup, c.cfg.ParserDir)
		_ = os.Rename(backup, c.cfg.AssetDir)
		return "", err
	}
	if err := c.executor.RestartAgent(ctx); err != nil {
		_ = os.RemoveAll(c.cfg.AssetDir)
		_ = os.RemoveAll(c.cfg.ParserDir)
		_ = os.Rename(parserBackup, c.cfg.ParserDir)
		_ = os.Rename(backup, c.cfg.AssetDir)
		return "", fmt.Errorf("restart failed; parser assets and runtime packs restored: %w", err)
	}
	_ = os.RemoveAll(backup)
	_ = os.RemoveAll(parserBackup)
	return fmt.Sprintf("%d revisioned parser assets validated and synchronized (bundle %s)", len(bundle.Assets), bundle.BundleHash), nil
}

func copyParserDirectory(source, destination string) error {
	if err := prepareParserRuntimeDirectory(destination); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return err
		}
		if err := writeSynchronizedAsset(filepath.Join(destination, entry.Name()), data); err != nil {
			return err
		}
	}
	return nil
}

// prepareParserRuntimeDirectory builds the directory contract needed after an
// atomic parser swap. The fleet-control service runs as root:nli, while the
// parser agent runs as nli:nli. A staging directory created only with 0750 is
// therefore readable by the agent but prevents it from creating its runtime
// quarantine directory after the staging directory is renamed into place.
// Applying the RPM's root:nli / nli:nli ownership contract before rename keeps
// the atomic swap and least-privilege service split intact.
func prepareParserRuntimeDirectory(path string) error {
	if err := os.MkdirAll(path, 0o750); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o750); err != nil {
		return err
	}
	if err := ensureNLIGroupOwnership(path); err != nil {
		return err
	}
	quarantine := filepath.Join(path, "quarantine")
	if err := os.MkdirAll(quarantine, 0o750); err != nil {
		return err
	}
	if err := os.Chmod(quarantine, 0o750); err != nil {
		return err
	}
	return ensureNLIOwnership(quarantine)
}

// writeSynchronizedAsset reapplies the final mode after creation so a
// restrictive process umask cannot silently remove the nli group's read bit.
// Windows accepts Chmod but exposes only its read-only attribute through
// FileMode; Rocky/Linux lifecycle tests verify the complete 0640 contract.
func writeSynchronizedAsset(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return err
	}
	return ensureNLIGroupReadable(path)
}

func activeAssetBundleStatus(path string) (string, map[string]int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	var bundle synchronizedAssetBundle
	if json.Unmarshal(data, &bundle) != nil {
		return "", nil
	}
	hash, err := synchronizedBundleHash(bundle)
	if err != nil || hash != bundle.BundleHash {
		return "", nil
	}
	revisions := map[string]int{}
	for _, asset := range bundle.Assets {
		revisions[asset.AssetClass+"/"+asset.AssetID] = asset.Revision
	}
	return hash, revisions
}

func (c *Client) applyPackage(ctx context.Context, payload json.RawMessage) (string, error) {
	if !c.cfg.AllowPackageUpdates {
		return "", fmt.Errorf("package updates are disabled on this client")
	}
	var request rollout.PackagePayload
	if err := json.Unmarshal(payload, &request); err != nil {
		return "", err
	}
	if request.DownloadURL == "" || len(request.Checksum) != 64 {
		return "", fmt.Errorf("package URL and SHA-256 checksum are required")
	}
	endpoint, err := url.Parse(request.DownloadURL)
	if err != nil || endpoint.Hostname() == "" {
		return "", fmt.Errorf("invalid package URL")
	}
	if endpoint.Scheme != "https" && !c.cfg.AllowInsecureHTTP {
		return "", fmt.Errorf("package URL must use HTTPS")
	}
	if !hostAllowed(endpoint.Hostname(), c.cfg.AllowedPackageHosts) {
		return "", fmt.Errorf("package host is not allowlisted")
	}
	if err := os.MkdirAll(c.cfg.PackageSpoolDir, 0750); err != nil {
		return "", err
	}
	path := filepath.Join(c.cfg.PackageSpoolDir, "nli-client-"+sanitizeVersion(request.Version)+".rpm")
	if err := c.downloadPackage(ctx, request.DownloadURL, path, request.Checksum); err != nil {
		return "", err
	}
	if err := c.executor.UpgradePackage(ctx, path); err != nil {
		return "", err
	}
	return "checksummed package upgrade completed", nil
}

func (c *Client) downloadPackage(ctx context.Context, source, destination, checksum string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("package download HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxPackageBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > maxPackageBytes {
		return fmt.Errorf("package exceeds %d bytes", maxPackageBytes)
	}
	digest := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), checksum) {
		return fmt.Errorf("package checksum mismatch")
	}
	return atomicWrite(destination, data, 0600)
}

func (c *Client) requestJSON(ctx context.Context, method, path string, input, output interface{}, headers map[string]string) error {
	_, err := c.requestJSONStatus(ctx, method, path, input, output, headers)
	return err
}

func (c *Client) requestJSONStatus(ctx context.Context, method, path string, input, output interface{}, headers map[string]string) (int, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.ControllerURL, "/")+path, body)
	if err != nil {
		return 0, err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, fmt.Errorf("controller HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(output); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (c *Client) loadOrCreateNodeID() (string, error) {
	if data, err := os.ReadFile(c.cfg.InstallIDFile); err == nil && strings.TrimSpace(string(data)) != "" {
		if err := ensureNLIGroupReadable(c.cfg.InstallIDFile); err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	hostname, _ := os.Hostname()
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	id := hostname + "-" + hex.EncodeToString(random)
	// nli-agent runs as the unprivileged nli account and reads this stable node
	// identity from its telemetry output configuration. Keep the identifier
	// group-readable; the controller sidecar runs with primary group nli.
	return id, atomicWrite(c.cfg.InstallIDFile, []byte(id+"\n"), 0640)
}

func ensureNLIGroupReadable(path string) error {
	if err := os.Chmod(path, 0640); err != nil {
		return err
	}
	return ensureNLIGroupOwnership(path)
}

func ensureNLIGroupOwnership(path string) error {
	group, err := user.LookupGroup("nli")
	if err != nil {
		return nil // RPM environments create this group; dev/test hosts may not.
	}
	groupID, err := strconv.Atoi(group.Gid)
	if err != nil {
		return fmt.Errorf("invalid nli group id: %w", err)
	}
	if err := os.Chown(path, -1, groupID); err != nil && !errors.Is(err, os.ErrPermission) {
		return err
	}
	return nil
}

func ensureNLIOwnership(path string) error {
	account, err := user.Lookup("nli")
	if err != nil {
		return nil // RPM environments create this account; dev/test hosts may not.
	}
	group, err := user.LookupGroup("nli")
	if err != nil {
		return nil
	}
	userID, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("invalid nli user id: %w", err)
	}
	groupID, err := strconv.Atoi(group.Gid)
	if err != nil {
		return fmt.Errorf("invalid nli group id: %w", err)
	}
	if err := os.Chown(path, userID, groupID); err != nil && !errors.Is(err, os.ErrPermission) {
		return err
	}
	return nil
}

func (c *Client) loadJournal() {
	data, err := os.ReadFile(c.cfg.JournalFile)
	if err == nil {
		_ = json.Unmarshal(data, &c.journal)
	}
}
func (c *Client) saveJournal() error {
	if len(c.journal) > 500 {
		keys := make([]string, 0, len(c.journal))
		for key := range c.journal {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys[:len(keys)-500] {
			delete(c.journal, key)
		}
	}
	data, _ := json.MarshalIndent(c.journal, "", "  ")
	return atomicWrite(c.cfg.JournalFile, data, 0600)
}

func readMetrics(endpoint string) protocol.Metrics {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return protocol.Metrics{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return protocol.Metrics{}
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	metrics := protocol.Metrics{}
	var upesMetricsSeen uint8
	for _, line := range strings.Split(string(data), "\n") {
		var value uint64
		switch {
		case strings.HasPrefix(line, "nli_logs_received_total "):
			fmt.Sscanf(line, "nli_logs_received_total %d", &value)
			metrics.Received = value
		case strings.HasPrefix(line, "nli_logs_parsed_total "):
			fmt.Sscanf(line, "nli_logs_parsed_total %d", &value)
			metrics.Parsed = value
		case strings.HasPrefix(line, "nli_logs_quarantined_total "):
			fmt.Sscanf(line, "nli_logs_quarantined_total %d", &value)
			metrics.Quarantined = value
		case strings.HasPrefix(line, "nli_input_errors_total "):
			fmt.Sscanf(line, "nli_input_errors_total %d", &value)
			metrics.Errors = value
		case strings.HasPrefix(line, "nli_upes_unique_failure_signatures_total "):
			value, ok := parseMetricUint(line, "nli_upes_unique_failure_signatures_total")
			metrics.UPESUniqueFailureSignatures = value
			if ok {
				upesMetricsSeen |= 1
			}
		case strings.HasPrefix(line, "nli_upes_observed_failures_total "):
			value, ok := parseMetricUint(line, "nli_upes_observed_failures_total")
			metrics.UPESObservedFailures = value
			if ok {
				upesMetricsSeen |= 2
			}
		case strings.HasPrefix(line, "nli_upes_discovery_queued_total "):
			value, ok := parseMetricUint(line, "nli_upes_discovery_queued_total")
			metrics.UPESDiscoveryQueued = value
			if ok {
				upesMetricsSeen |= 4
			}
		case strings.HasPrefix(line, "nli_upes_discovery_dropped_total "):
			value, ok := parseMetricUint(line, "nli_upes_discovery_dropped_total")
			metrics.UPESDiscoveryDropped = value
			if ok {
				upesMetricsSeen |= 8
			}
		case strings.HasPrefix(line, "nli_upes_discovery_pending "):
			value, ok := parseMetricUint(line, "nli_upes_discovery_pending")
			metrics.UPESDiscoveryPending = value
			if ok {
				upesMetricsSeen |= 16
			}
		}
	}
	metrics.UPESMetricsAvailable = upesMetricsSeen == 31
	if metrics.Received > 0 {
		metrics.ParseRate = float64(metrics.Parsed) / float64(metrics.Received) * 100
	}
	if host, ok := readHostMetrics(); ok {
		metrics.CPUPercent = host.cpu
		metrics.MemoryBytes = host.usedBytes
		metrics.MemoryPercent = host.memoryPercent
		metrics.Load1, metrics.Load5, metrics.Load15 = host.load1, host.load5, host.load15
		metrics.HostMetricsAvailable = true
	}
	return metrics
}

func parseMetricUint(line, name string) (uint64, bool) {
	value, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, name)), 10, 64)
	return value, err == nil
}

type hostMetrics struct {
	cpu, memoryPercent, load1, load5, load15 float64
	usedBytes                                uint64
}

var hostCPUState struct {
	sync.Mutex
	total, idle uint64
	at          time.Time
}

// readHostMetrics reports Linux host values only when all required kernel
// counters are readable. The first CPU sample is a real zero baseline; later
// samples use deltas from /proc/stat. Other platforms remain explicitly
// unavailable instead of presenting fabricated zeros.
func readHostMetrics() (hostMetrics, bool) {
	if runtime.GOOS != "linux" {
		return hostMetrics{}, false
	}
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return hostMetrics{}, false
	}
	var label string
	var user, nice, system, idle, iowait, irq, softirq, steal uint64
	if _, err := fmt.Sscanf(strings.SplitN(string(stat), "\n", 2)[0], "%s %d %d %d %d %d %d %d %d", &label, &user, &nice, &system, &idle, &iowait, &irq, &softirq, &steal); err != nil {
		return hostMetrics{}, false
	}
	total := user + nice + system + idle + iowait + irq + softirq + steal
	idleTotal := idle + iowait
	hostCPUState.Lock()
	cpu := 0.0
	if hostCPUState.at.IsZero() {
		hostCPUState.total, hostCPUState.idle = total, idleTotal
	} else if total > hostCPUState.total && idleTotal >= hostCPUState.idle {
		delta, idleDelta := total-hostCPUState.total, idleTotal-hostCPUState.idle
		if delta > 0 {
			cpu = 100 * float64(delta-idleDelta) / float64(delta)
		}
	}
	hostCPUState.total, hostCPUState.idle, hostCPUState.at = total, idleTotal, time.Now()
	hostCPUState.Unlock()
	mem, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return hostMetrics{}, false
	}
	var totalKB, availableKB uint64
	for _, line := range strings.Split(string(mem), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			_, _ = fmt.Sscanf(line, "MemTotal: %d kB", &totalKB)
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			_, _ = fmt.Sscanf(line, "MemAvailable: %d kB", &availableKB)
		}
	}
	if totalKB == 0 || availableKB > totalKB {
		return hostMetrics{}, false
	}
	loadData, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return hostMetrics{}, false
	}
	var out hostMetrics
	if _, err := fmt.Sscanf(string(loadData), "%f %f %f", &out.load1, &out.load5, &out.load15); err != nil {
		return hostMetrics{}, false
	}
	out.cpu, out.usedBytes = cpu, (totalKB-availableKB)*1024
	out.memoryPercent = 100 * float64(totalKB-availableKB) / float64(totalKB)
	return out, true
}

func parserInventory(dir string) []protocol.ParserInventory {
	entries, _ := os.ReadDir(dir)
	out := make([]protocol.ParserInventory, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var parser registry.ParserDef
		if json.Unmarshal(data, &parser) == nil && registry.Validate(&parser) == nil {
			out = append(out, protocol.ParserInventory{ID: parser.ID, Version: parser.Version, Description: parser.Description, Source: "managed", TestStatus: "passed"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	file, err := os.OpenFile(tmp, os.O_RDWR, mode)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func backupAndWrite(path string, data []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil {
		if err := atomicWrite(path+".backup", existing, mode); err != nil {
			return err
		}
	}
	return atomicWrite(path, data, mode)
}
func restoreBackup(path string) error {
	data, err := os.ReadFile(path + ".backup")
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0640)
}
func fileSHA256(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func primaryIP() string {
	addresses, _ := net.InterfaceAddrs()
	for _, address := range addresses {
		if ipNet, ok := address.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return ""
}
func normalizedArch() string {
	if runtime.GOARCH == "amd64" {
		return "x86_64"
	}
	return runtime.GOARCH
}
func osRelease() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
		}
	}
	return runtime.GOOS
}
func hostAllowed(host string, allowed []string) bool {
	for _, candidate := range allowed {
		if strings.EqualFold(host, candidate) {
			return true
		}
	}
	return false
}
func sanitizeVersion(version string) string {
	var out strings.Builder
	for _, char := range version {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '-' {
			out.WriteRune(char)
		}
	}
	if out.Len() == 0 {
		return "unknown"
	}
	return out.String()
}
