// Package agent implements the NLI file tailer, config loader, and pipeline runner.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"nexora.local/nli-v20/internal/adapter"
	"nexora.local/nli-v20/internal/ai"
	"nexora.local/nli-v20/internal/drain"
	"nexora.local/nli-v20/internal/engine"
	"nexora.local/nli-v20/internal/enrichment"
	"nexora.local/nli-v20/internal/familyprofile"
	"nexora.local/nli-v20/internal/masker"
	"nexora.local/nli-v20/internal/multiline"
	"nexora.local/nli-v20/internal/output"
	"nexora.local/nli-v20/internal/quarantine"
	"nexora.local/nli-v20/internal/registry"
	"nexora.local/nli-v20/internal/routing"
	"nexora.local/nli-v20/internal/upes"
	"nexora.local/nli-v20/telemetry"
)

// InputConfig describes one monitored log file.
type InputConfig struct {
	Path      string           `json:"path"`
	Service   string           `json:"service"`
	Selection SelectionConfig  `json:"selection,omitempty"`
	Multiline multiline.Config `json:"multiline"`
	Mask      masker.Config    `json:"mask"`
}

// SelectionConfig applies bounded, literal metadata filters before parser execution.
type SelectionConfig struct {
	Mode         string   `json:"mode,omitempty"` // default, filtered, hybrid
	Events       []string `json:"events,omitempty"`
	Methods      []string `json:"methods,omitempty"`
	Services     []string `json:"services,omitempty"`
	Hostnames    []string `json:"hostnames,omitempty"`
	MatchService string   `json:"match_service,omitempty"` // parser family used for matched hybrid records
}

// Config is the NLI agent configuration (v2 schema).
type Config struct {
	Version             int                        `json:"version"`
	Inputs              []InputConfig              `json:"inputs"`
	StateDir            string                     `json:"state_dir"`
	ParserPackDir       string                     `json:"parser_pack_dir,omitempty"` // approved controller generation; defaults to state_dir/parsers
	Outputs             []output.Config            `json:"outputs"`
	TelemetryOutputs    []output.Config            `json:"telemetry_outputs,omitempty"`     // v5: separate ParseTelemetry destinations
	EnableV5Parser      bool                       `json:"enable_v5_parser,omitempty"`      // v5: enable ParserPack execution (deprecated - now default)
	DisableV5Parser     bool                       `json:"disable_v5_parser,omitempty"`     // v5: explicit opt-out of ParserPack (diagnostic only)
	EnableLegacyRouting bool                       `json:"enable_legacy_routing,omitempty"` // v5: explicit opt-in for legacy envelope routing
	MetricsAddress      string                     `json:"metrics_address"`
	PollMS              int                        `json:"poll_ms"`
	SourceWorkers       int                        `json:"source_workers,omitempty"`
	SourceBatchLines    int                        `json:"source_batch_lines,omitempty"`
	StreamThresholds    telemetry.StreamThresholds `json:"stream_thresholds,omitempty"`
	TailFromEnd         bool                       `json:"tail_from_end,omitempty"`
	Drain               drain.Config               `json:"drain"`
	AI                  ai.Config                  `json:"ai"`
	Enrichment          enrichment.Config          `json:"enrichment,omitempty"`

	// v1 compat fields (migrated on load)
	OutputFile     string `json:"output_file,omitempty"`
	QuarantineFile string `json:"quarantine_file,omitempty"`
	MaxOutputMB    int64  `json:"max_output_mb,omitempty"`
}

func sourceWorkerLimit(c Config) int {
	workers := c.SourceWorkers
	if workers < 1 {
		workers = runtime.NumCPU()
		if workers > 32 {
			workers = 32
		}
		if workers < 1 {
			workers = 1
		}
	}
	if workers > 64 {
		workers = 64
	}
	if workers > len(c.Inputs) {
		workers = len(c.Inputs)
	}
	return workers
}

func sourceBatchLineLimit(c Config) int {
	if c.SourceBatchLines <= 0 {
		return 1000
	}
	if c.SourceBatchLines > 10000 {
		return 10000
	}
	return c.SourceBatchLines
}

// finishPendingSources emits buffered multiline events before committing their
// source offsets. A failed emission leaves every affected offset uncommitted.
func finishPendingSources(tails []*tail, emit func(*tail, string, bool) error, commit func(*tail) error) error {
	var finishErr error
	for _, source := range tails {
		if result := source.ml.Flush(); result != nil {
			if err := emit(source, result.Line, result.Truncated); err != nil {
				finishErr = errors.Join(finishErr, fmt.Errorf("flush source %s: %w", source.input.Path, err))
			}
		}
	}
	if finishErr != nil {
		return finishErr
	}
	for _, source := range tails {
		if err := commit(source); err != nil {
			finishErr = errors.Join(finishErr, fmt.Errorf("checkpoint source %s: %w", source.input.Path, err))
		}
	}
	return finishErr
}

func effectiveStreamThresholds(configured telemetry.StreamThresholds) telemetry.StreamThresholds {
	if configured.ReadLinesPerSecond == 0 {
		configured.ReadLinesPerSecond = 100000
	}
	if configured.BytesPerSecond == 0 {
		configured.BytesPerSecond = 10 << 20
	}
	if configured.QuarantinedPerSecond == 0 {
		configured.QuarantinedPerSecond = 1
	}
	return configured
}

func runSourceWorkers(ctx context.Context, tails []*tail, limit int, process func(*tail) error) error {
	if limit < 1 {
		limit = 1
	}
	if limit > len(tails) {
		limit = len(tails)
	}
	if limit < 1 {
		return nil
	}
	type job struct {
		index  int
		source *tail
	}
	jobs := make(chan job, len(tails))
	var group sync.WaitGroup
	errs := make([]error, len(tails))
	for worker := 0; worker < limit; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for item := range jobs {
				if err := process(item.source); err != nil {
					errs[item.index] = err
				}
			}
		}()
	}
	for index, source := range tails {
		select {
		case jobs <- job{index: index, source: source}:
		case <-ctx.Done():
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	group.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// Load reads and validates a config file, migrating v1 configs automatically.
func Load(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()

	const maxConfigBytes = 1 << 20
	data, readErr := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if readErr != nil {
		return c, readErr
	}
	if len(data) > maxConfigBytes {
		return c, fmt.Errorf("config exceeds 1 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	var trailing interface{}
	if err = d.Decode(&trailing); err != io.EOF {
		if err == nil {
			return c, fmt.Errorf("config must contain exactly one JSON document")
		}
		return c, fmt.Errorf("invalid trailing config data: %w", err)
	}

	// Migrate v1 config
	if c.Version == 0 && (c.OutputFile != "" || c.QuarantineFile != "") {
		fmt.Fprintf(os.Stderr, "nli: migrating v1 config to v2 schema\n")
		c.Version = 2
		maxMB := c.MaxOutputMB
		if maxMB == 0 {
			maxMB = 100
		}
		if c.OutputFile != "" {
			c.Outputs = append(c.Outputs, output.Config{Type: "file", Path: c.OutputFile, MaxMB: maxMB})
		}
		if c.QuarantineFile != "" {
			c.Outputs = append(c.Outputs, output.Config{
				Type:   "file",
				Path:   c.QuarantineFile,
				MaxMB:  maxMB,
				Routes: []string{"quarantine", "unknown"},
			})
		}
	}

	return c, validate(c)
}

func validate(c Config) error {
	if len(c.Inputs) == 0 || len(c.Inputs) > 256 {
		return fmt.Errorf("configure 1..256 inputs")
	}
	if c.PollMS < 50 || c.PollMS > 60000 {
		return fmt.Errorf("poll_ms must be 50..60000")
	}
	if c.SourceWorkers < 0 || c.SourceWorkers > 64 {
		return fmt.Errorf("source_workers must be 0..64")
	}
	if c.SourceBatchLines < 0 || c.SourceBatchLines > 10000 {
		return fmt.Errorf("source_batch_lines must be 0..10000")
	}
	for _, value := range []float64{c.StreamThresholds.ReadLinesPerSecond, c.StreamThresholds.NormalizedPerSecond, c.StreamThresholds.QuarantinedPerSecond, c.StreamThresholds.BytesPerSecond} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("stream_thresholds must be finite and non-negative")
		}
	}
	if !filepath.IsAbs(c.StateDir) {
		return fmt.Errorf("state_dir must be absolute")
	}
	if c.ParserPackDir != "" && !filepath.IsAbs(c.ParserPackDir) {
		return fmt.Errorf("parser_pack_dir must be absolute")
	}
	for _, inp := range c.Inputs {
		if !filepath.IsAbs(inp.Path) || inp.Service == "" {
			return fmt.Errorf("inputs need absolute paths and services")
		}
		mode := strings.ToLower(strings.TrimSpace(inp.Selection.Mode))
		if mode != "" && mode != "default" && mode != "filtered" && mode != "hybrid" {
			return fmt.Errorf("input selection mode must be default, filtered, or hybrid")
		}
		if mode == "filtered" || mode == "hybrid" {
			if len(inp.Selection.Events)+len(inp.Selection.Methods)+len(inp.Selection.Services)+len(inp.Selection.Hostnames) == 0 {
				return fmt.Errorf("filtered and hybrid input selection need at least one event, method, service, or hostname")
			}
		}
		for _, values := range [][]string{inp.Selection.Events, inp.Selection.Methods, inp.Selection.Services, inp.Selection.Hostnames} {
			if len(values) > 128 {
				return fmt.Errorf("input selection allows at most 128 values per field")
			}
			for _, value := range values {
				if value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
					return fmt.Errorf("input selection values must be 1..256 bytes without control characters")
				}
			}
		}
		if len(inp.Selection.MatchService) > 128 || strings.ContainsAny(inp.Selection.MatchService, "\r\n\x00") {
			return fmt.Errorf("input selection match_service must be at most 128 bytes without control characters")
		}
	}
	if err := enrichment.ValidateConfig(c.Enrichment); err != nil {
		return fmt.Errorf("enrichment: %w", err)
	}
	if c.MetricsAddress != "" {
		host, _, e := net.SplitHostPort(c.MetricsAddress)
		ip := net.ParseIP(host)
		if e != nil || ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("metrics_address must use a loopback IP and port")
		}
	}
	return nil
}

func parserPackDirectory(c Config) string {
	if c.ParserPackDir != "" {
		return c.ParserPackDir
	}
	return filepath.Join(c.StateDir, "parsers")
}

func loadLegacyParsers(reg *registry.Registry, directory string) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 1<<20+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > 1<<20 {
			return fmt.Errorf("legacy parser %s exceeds 1 MiB", entry.Name())
		}
		if familyprofile.LooksLikePackDocument(data) {
			continue
		}
		if err := reg.LoadFile(path); err != nil {
			return err
		}
	}
	return nil
}

type cursor struct {
	Offset int64  `json:"offset"`
	Line   int64  `json:"line"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type tail struct {
	input                 InputConfig
	file                  *os.File
	state                 cursor
	hasCheckpoint         bool
	ml                    *multiline.Assembler
	oversizedPhysicalLine bool
	stream                *streamState
}

var (
	nativeSelectionHeader = regexp.MustCompile(`^\s*\d{8}\s+\d{9}[+-]\d{4}\s+(\S+)\s+(\S+)\s+`)
	webtopSelectionHeader = regexp.MustCompile(`^\s*\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}:\d{3}[+-]\d{4}\s+`)
	eventToken            = regexp.MustCompile(`(?:^|[;\s])([^;\s()]+)(?:\([^)]{0,64}\))?(?:\s|$)`)
	webtopMethod          = regexp.MustCompile(`(?:^|\s)method="([^"]{1,128})"`)
)

type lineMetadata struct{ event, method, service, hostname string }

func extractLineMetadata(line string) lineMetadata {
	var m lineMetadata
	if match := nativeSelectionHeader.FindStringSubmatch(line); len(match) == 3 {
		m.hostname, m.service = match[1], canonicalSelectionService(match[2])
		if i := strings.IndexByte(line, ';'); i >= 0 {
			if event := eventToken.FindStringSubmatch(line[i+1:]); len(event) == 2 {
				m.event = event[1]
			}
		}
		return m
	}
	if webtopSelectionHeader.MatchString(line) {
		if match := webtopMethod.FindStringSubmatch(line); len(match) == 2 {
			m.method, m.event = match[1], match[1]
		}
		// Webtop's hostname appears as a bounded quoted field after method.
		if i := strings.Index(line, `hostname="`); i >= 0 {
			tail := line[i+len(`hostname="`):]
			if end := strings.IndexByte(tail, '"'); end >= 0 && end <= 256 {
				m.hostname = tail[:end]
			}
		}
		m.service = "webtop"
		return m
	}
	return m
}

func canonicalSelectionService(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "imapserv":
		return "imap"
	case "popserv":
		return "pop"
	case "webmail":
		return "webtop"
	case "pss":
		return "pabd"
	case "mss":
		return "capd"
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}

func selectionMatches(s SelectionConfig, line string) (bool, string) {
	mode := strings.ToLower(strings.TrimSpace(s.Mode))
	if mode == "" || mode == "default" {
		return true, ""
	}
	m := extractLineMetadata(line)
	matched := true
	if len(s.Events) > 0 {
		found := false
		for _, v := range s.Events {
			if strings.EqualFold(v, m.event) {
				found = true
				break
			}
		}
		matched = matched && found
	}
	if len(s.Methods) > 0 {
		found := false
		for _, v := range s.Methods {
			if strings.EqualFold(v, m.method) {
				found = true
				break
			}
		}
		matched = matched && found
	}
	if len(s.Services) > 0 {
		found := false
		for _, v := range s.Services {
			if canonicalSelectionService(v) == m.service {
				found = true
				break
			}
		}
		matched = matched && found
	}
	if len(s.Hostnames) > 0 {
		found := false
		for _, v := range s.Hostnames {
			if strings.EqualFold(v, m.hostname) {
				found = true
				break
			}
		}
		matched = matched && found
	}
	if !matched {
		if mode == "hybrid" {
			return true, "auto"
		}
		return false, ""
	}
	service := strings.TrimSpace(s.MatchService)
	if service == "" {
		service = m.service
	}
	return true, service
}

type counters struct {
	received    uint64
	parsed      uint64
	quarantined uint64
	filtered    uint64
	failures    uint64
}

func acceptsRoute(routes []string, route string) bool {
	if len(routes) == 0 {
		return true
	}
	for _, accepted := range routes {
		if accepted == route {
			return true
		}
	}
	return false
}

func outputAcceptsFamily(families []string, family string) bool {
	if len(families) == 0 {
		return true
	}
	for _, candidate := range families {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(family)) {
			return true
		}
	}
	return false
}

func quarantinePathsForFamily(outputs []output.Config, family string) []string {
	seen := map[string]bool{}
	paths := make([]string, 0, 2)
	for _, destination := range outputs {
		if destination.Type != "controller" && (destination.Path != "" || destination.PathTemplate != "") &&
			acceptsRoute(destination.Routes, "quarantine") && outputAcceptsFamily(destination.Families, family) {
			path, err := output.ResolveFilePath(destination, time.Now().UTC())
			if err == nil && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	return paths
}

// ensureLocalQuarantineOutput keeps exact unparsed input durable even when a
// client sends fully parsed events only to remote destinations. The quarantine
// file is separate from optional normalized JSONL output and remains replayable.
func ensureLocalQuarantineOutput(c *Config) {
	for _, destination := range c.Outputs {
		if destination.Type == "file" && len(destination.Families) == 0 && acceptsRoute(destination.Routes, "quarantine") {
			return
		}
	}
	c.Outputs = append(c.Outputs, output.Config{
		Type: "file", Path: filepath.Join(c.StateDir, "quarantine", "records.jsonl"),
		Routes: []string{"quarantine", "unknown"}, MaxMB: 500, MaxBackups: 5,
	})
}

func checkpoint(path string, states map[string]cursor) error {
	data, err := json.Marshal(states)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func readRecord(reader *bufio.Reader) (string, int64, bool, bool, error) {
	data := []byte{}
	var consumed int64
	oversized := false
	for {
		chunk, err := reader.ReadSlice('\n')
		consumed += int64(len(chunk))
		available := engine.MaxLine - len(data)
		if available > 0 {
			n := len(chunk)
			if n > available {
				n = available
			}
			data = append(data, chunk[:n]...)
		}
		if consumed > engine.MaxLine {
			oversized = true
		}
		if err == bufio.ErrBufferFull {
			// Return a bounded segment instead of draining an arbitrarily long
			// unterminated physical line in one call. The tail loop carries the
			// oversized state across segments and routes each byte-preserving
			// segment as unknown input until the physical newline arrives.
			if len(data) >= engine.MaxLine {
				return string(data), consumed, false, true, nil
			}
			continue
		}
		complete := len(chunk) > 0 && chunk[len(chunk)-1] == '\n'
		if err != nil && err != io.EOF {
			return "", consumed, complete, oversized, err
		}
		return strings.TrimRight(string(data), "\r\n"), consumed, complete, oversized, nil
	}
}

func inputIntegrityErrors(value string) []string {
	errors := make([]string, 0, 2)
	if !utf8.ValidString(value) {
		errors = append(errors, "input contains invalid UTF-8")
	}
	for _, b := range []byte(value) {
		if b == 0 || (b < 0x20 && b != '\t' && b != '\n' && b != '\r') {
			errors = append(errors, "input contains NUL or unsupported control bytes")
			break
		}
	}
	return errors
}

// Run starts the agent loop with the full v4 pipeline.
func Run(ctx context.Context, c Config, once bool) (runErr error) {
	geo, err := enrichment.Open(c.Enrichment)
	if err != nil {
		return fmt.Errorf("GeoIP initialization: %w", err)
	}
	if geo != nil {
		defer func() { runErr = errors.Join(runErr, geo.Close()) }()
	}
	if err := os.MkdirAll(c.StateDir, 0700); err != nil {
		return err
	}
	legacyParserDir := filepath.Join(c.StateDir, "parsers")
	for _, dir := range []string{legacyParserDir, filepath.Join(legacyParserDir, "quarantine")} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return err
		}
	}

	// Initialize pipeline components
	deps := engine.Default()

	// Override drain config if provided
	if c.Drain.Depth > 0 || c.Drain.SimTh > 0 {
		deps.Miner = drain.New(c.Drain)
	}

	// Load template state
	persister := drain.NewPersister(deps.Miner, c.StateDir)
	if err := persister.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "nli: drain state load: %v (starting fresh)\n", err)
	}

	// Load legacy ParserDefs while leaving ParserPack documents for the strict
	// production loader below. Both formats may share the controller generation.
	if err := loadLegacyParsers(deps.Registry, legacyParserDir); err != nil {
		fmt.Fprintf(os.Stderr, "nli: parser registry load: %v\n", err)
	}

	// AI discoverer
	discoverer := ai.New(c.AI, deps.Registry, deps.Miner, c.StateDir)
	upesRuntime := upes.New(upes.DefaultSignatureLimit, upes.DefaultQueueLimit)

	// Default outputs if none configured
	if len(c.Outputs) == 0 {
		c.Outputs = []output.Config{
			{Type: "stdout"},
		}
	}
	ensureLocalQuarantineOutput(&c)

	// Initialize v5 parser - now default unless explicitly disabled
	var v5Adapter *adapter.ParserPackAdapter
	if !c.DisableV5Parser {
		v5Adapter, err = adapter.NewParserPackAdapterFromDirectory(parserPackDirectory(c))
		if err != nil {
			return fmt.Errorf("v5 parser initialization: %w", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "WARNING: v5 parser disabled - using legacy v4 parsing only\n")
	}

	// Set up routing - v5 separated routing is now the production default
	var v5Router *routing.V5Router
	var legacyRouter *output.Router

	// v5 separated routing is now the production default - legacy is explicit opt-in only
	if c.EnableLegacyRouting { // New explicit flag for legacy mode
		// Legacy single router only when explicitly requested
		legacyRouter, err = output.NewRouter(c.Outputs)
		if err != nil {
			return fmt.Errorf("output router: %w", err)
		}
		defer func() { runErr = errors.Join(runErr, legacyRouter.Close()) }()
	} else {
		// A controller destination in the client setup is a request to make
		// parsed activity visible to the controller. Route only the separate
		// bounded parser metadata there; customer EventPayloads must never be
		// sent to the legacy /node/events ingestion endpoint.
		controllerTelemetry := make([]output.Config, 0)
		customerOutputs := make([]output.Config, 0, len(c.Outputs))
		for _, destination := range c.Outputs {
			if destination.Type == "controller" {
				destination.Type = "telemetry"
				destination.Routes = []string{"telemetry"}
				controllerTelemetry = append(controllerTelemetry, destination)
				continue
			}
			customerOutputs = append(customerOutputs, destination)
		}
		c.Outputs = customerOutputs

		// Default telemetry outputs to the configured controller destination,
		// or discard metadata when no destination was requested.
		telemetryConfigs := c.TelemetryOutputs
		if len(telemetryConfigs) == 0 {
			telemetryConfigs = controllerTelemetry
			if len(telemetryConfigs) == 0 {
				telemetryConfigs = []output.Config{{Type: "discard"}}
			}
		} else {
			// Keep an explicitly configured telemetry sink and also honor any
			// legacy controller destination as a metadata sink. Avoid sending
			// duplicate telemetry when both describe the same controller.
			for _, destination := range controllerTelemetry {
				duplicate := false
				for _, configured := range telemetryConfigs {
					if configured.URL == destination.URL && configured.TokenFile == destination.TokenFile && configured.NodeID == destination.NodeID && configured.NodeIDFile == destination.NodeIDFile {
						duplicate = true
						break
					}
				}
				if !duplicate {
					telemetryConfigs = append(telemetryConfigs, destination)
				}
			}
		}

		// v5 separated routing (production default - always used unless legacy explicitly enabled)
		v5Router, err = routing.NewV5Router(c.Outputs, telemetryConfigs)
		if err != nil {
			return fmt.Errorf("v5 router: %w", err)
		}
		defer func() { runErr = errors.Join(runErr, v5Router.Close()) }()
	}

	statePath := filepath.Join(c.StateDir, "offsets.json")
	states := map[string]cursor{}
	if data, err := os.ReadFile(statePath); err == nil {
		if err = json.Unmarshal(data, &states); err != nil {
			return fmt.Errorf("invalid checkpoint: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	var runtimeMu sync.Mutex
	var quarantineMu sync.Mutex
	// Persist only acknowledged events. Buffered multiline input stays replayable
	// from the previous durable offset if the process stops before emission.
	// A completed event may precede a new, still-pending multiline record in the
	// same read batch. Persisting its safe prefix prevents a restart from
	// replaying already-written completed events while that final record waits
	// for the multiline timeout.
	persistCursor := func(t *tail, state cursor) error {
		if v5Router != nil {
			if err := v5Router.FlushCustomer(); err != nil {
				return err
			}
		} else if err := legacyRouter.Flush(); err != nil {
			return err
		}
		runtimeMu.Lock()
		states[t.input.Path] = state
		checkpointErr := checkpoint(statePath, states)
		runtimeMu.Unlock()
		if checkpointErr != nil {
			return checkpointErr
		}
		if v5Router != nil {
			// Parser telemetry is an independent observability channel. A temporary
			// controller outage must not replay already-durable customer records.
			// The generated production configuration also writes a hash-only
			// delivery-failure receipt for this optional destination.
			if err := v5Router.FlushTelemetry(); err != nil {
				fmt.Fprintf(os.Stderr, "nli: parser telemetry delivery pending: %v\n", err)
			}
		}
		return nil
	}
	commit := func(t *tail) error {
		if t.ml.Pending() {
			return nil
		}
		return persistCursor(t, t.state)
	}
	checkpointedOffset := func(path string) int64 {
		runtimeMu.Lock()
		defer runtimeMu.Unlock()
		return states[path].Offset
	}
	writeOutput := func(write func() error) error {
		return write()
	}
	outputQueueStats := func() (int, int) {
		if v5Router != nil {
			return v5Router.QueueStats()
		}
		if legacyRouter != nil {
			return legacyRouter.QueueStats()
		}
		return 0, 0
	}

	hostname, _ := os.Hostname()
	nodeID := hostname
	if data, readErr := os.ReadFile("/var/lib/nli-control-client/install.id"); readErr == nil && strings.TrimSpace(string(data)) != "" {
		nodeID = strings.TrimSpace(string(data))
	}
	metrics := &counters{}
	batchSize := sourceBatchLineLimit(c)
	sourceWorkers := sourceWorkerLimit(c)
	streamThresholds := effectiveStreamThresholds(c.StreamThresholds)
	streamSamples := newSampleSet()
	journal := newJournal(10 * time.Second)

	emit := func(t *tail, line string, truncated, oversized bool, parserService string) error {
		if strings.TrimSpace(line) == "" {
			return nil
		}
		service := t.input.Service
		if parserService != "" {
			service = parserService
		}
		started := time.Now()
		source := engine.Source{Hostname: hostname, Service: service, File: t.input.Path, LineNumber: t.state.Line + 1}
		quarantinePaths := quarantinePathsForFamily(c.Outputs, t.input.Service)
		integrityErrors := inputIntegrityErrors(line)

		// Try v5 parsing first if available.
		if v5Adapter != nil && v5Router != nil && !truncated && !oversized && len(integrityErrors) == 0 {
			parseStarted := time.Now()
			v5Result := v5Adapter.TryParseV5Production(line, source)
			t.stream.addParse(time.Since(parseStarted))
			if v5Result.Success && v5Result.EventPayload != nil {
				enrichment.Apply(v5Result.EventPayload.Fields, geo)
			}
			if v5Result.Success || v5Result.Quarantined {
				if v5Result.Quarantined {
					fingerprint, _ := engine.FormatFingerprint(line, "parserpack")
					reason := "parserpack_validation_failure"
					if len(v5Result.ParseTelemetry.ErrorCodes) > 0 {
						reason = v5Result.ParseTelemetry.ErrorCodes[0]
					}
					upesRuntime.Observe(fingerprint, service, "parserpack_validation_error", reason)
				}
				if v5Result.Quarantined && len(quarantinePaths) > 0 {
					metadata := v5Result.ParseTelemetry
					record := quarantine.New(nodeID, t.input.Service, t.input.Path, uint64(t.state.Line+1), line, metadata.PolicyHash, metadata.AttemptCount, metadata.ErrorCodes, time.Now().UTC())
					metadata.QuarantineID, metadata.QuarantineStatus, metadata.QuarantineAttempt = record.ID, record.Status, record.AttemptCount
					for _, quarantinePath := range quarantinePaths {
						quarantineMu.Lock()
						err := quarantine.Append(quarantinePath, record)
						quarantineMu.Unlock()
						if err != nil {
							return fmt.Errorf("persist raw quarantine record to %s: %w", quarantinePath, err)
						}
					}
				}
				routingResult := &routing.V5ParseResult{
					EventPayload: v5Result.EventPayload, ParseTelemetry: v5Result.ParseTelemetry,
					Success: v5Result.Success, Quarantined: v5Result.Quarantined,
				}
				persister.Mark()
				outputStarted := time.Now()
				if err := writeOutput(func() error { return v5Router.RouteV5ForFamily(routingResult, t.input.Service) }); err != nil {
					t.stream.addOutput(time.Since(outputStarted))
					journal.outputFailure(t.input.Path, err.Error())
					return err
				}
				t.stream.addOutput(time.Since(outputStarted))
				atomic.AddUint64(&metrics.received, 1)
				t.stream.validationAttempts.Add(1)
				if v5Result.Success {
					atomic.AddUint64(&metrics.parsed, 1)
					t.stream.normalized.Add(1)
					t.stream.validationPassed.Add(1)
				} else {
					atomic.AddUint64(&metrics.quarantined, 1)
					t.stream.quarantined.Add(1)
					t.stream.validationFailed.Add(1)
				}
				t.stream.addTotal(time.Since(started))
				// AI discovery is a last-resort recovery option. Successful records
				// must never consume provider capacity or create parser candidates;
				// only a failure already observed by UPES may enter this queue.
				if v5Result.Quarantined && v5Result.ParseTelemetry.TemplateID != "" {
					clusterID := 0
					fmt.Sscanf(v5Result.ParseTelemetry.TemplateID, "tpl-%d", &clusterID)
					if clusterID > 0 {
						discoverer.Submit(clusterID, line)
					}
				}
				return nil
			}
		}

		// Fallback to v4 parsing if v5 failed or not available
		parseStarted := time.Now()
		envelope := engine.ParseWithTruncated(line, source, deps, truncated || oversized || len(integrityErrors) > 0)
		t.stream.addParse(time.Since(parseStarted))
		if envelope.Meta.Route == "normal" || envelope.Meta.Route == "warning" {
			enrichment.Apply(envelope.Data, geo)
		}
		if oversized {
			envelope.NLI.Confidence = 0
			envelope.NLI.Parser = "unknown"
			envelope.Meta.Route = "unknown"
			envelope.Meta.Errors = append(envelope.Meta.Errors, "physical line exceeded 1 MiB")
		}
		if len(integrityErrors) > 0 {
			envelope.NLI.Confidence = 0
			envelope.NLI.Parser = "unknown"
			envelope.Meta.Route = "unknown"
			envelope.Meta.Errors = append(envelope.Meta.Errors, integrityErrors...)
		}
		if envelope.Meta.Route == "unknown" || envelope.Meta.Route == "quarantine" {
			failureClass := "no_parser_match"
			if len(envelope.Meta.Errors) > 0 {
				failureClass = "parser_or_validation_error"
			}
			if envelope.Meta.Assurance.Ambiguous {
				failureClass = "ambiguous_output"
			}
			if truncated || oversized {
				failureClass = "truncated_or_oversized_input"
			}
			reason := ""
			if len(envelope.Meta.Errors) > 0 {
				reason = envelope.Meta.Errors[0]
			}
			upesRuntime.Observe(envelope.Meta.Assurance.Fingerprint, service, failureClass, reason)
		}

		persister.Mark()
		var quarantineRecord *quarantine.Record
		if (envelope.Meta.Route == "quarantine" || envelope.Meta.Route == "unknown") && len(quarantinePaths) > 0 {
			record := quarantine.New(nodeID, t.input.Service, t.input.Path, uint64(t.state.Line+1), line, "", 1, envelope.Meta.Errors, time.Now().UTC())
			quarantineRecord = &record
			for _, quarantinePath := range quarantinePaths {
				quarantineMu.Lock()
				err := quarantine.Append(quarantinePath, record)
				quarantineMu.Unlock()
				if err != nil {
					return fmt.Errorf("persist raw quarantine record to %s: %w", quarantinePath, err)
				}
			}
		}

		// Route based on configuration
		if v5Router != nil {
			// Convert v4 envelope to separated EventPayload/ParseTelemetry
			telemetryMeta := map[string]interface{}{
				"parser_id":          envelope.NLI.Parser,
				"confidence":         envelope.NLI.Confidence,
				"evidence_coverage":  envelope.Meta.Assurance.EvidenceCoverage,
				"output_field_count": envelope.Meta.Extracted,
				"latency_ms":         envelope.Meta.Latency / 1000, // Convert from microseconds
				"route":              envelope.Meta.Route,
			}
			if quarantineRecord != nil {
				telemetryMeta["quarantine_id"] = quarantineRecord.ID
				telemetryMeta["quarantine_status"] = quarantineRecord.Status
				telemetryMeta["quarantine_attempt_count"] = quarantineRecord.AttemptCount
			}

			// Sanitize envelope.Data to remove forbidden fields recursively
			sanitizedData := sanitizeEventData(envelope.Data, envelope.Meta.Route)

			outputStarted := time.Now()
			if err := writeOutput(func() error {
				return v5Router.RouteLegacyForFamily(sanitizedData, telemetryMeta, envelope.Meta.Route, t.input.Service)
			}); err != nil {
				t.stream.addOutput(time.Since(outputStarted))
				journal.outputFailure(t.input.Path, err.Error())
				return err
			}
			t.stream.addOutput(time.Since(outputStarted))
		} else {
			// Legacy envelope routing
			encoded, err := json.Marshal(envelope)
			if err != nil {
				return err
			}
			outputStarted := time.Now()
			if err := writeOutput(func() error { return legacyRouter.WriteForFamily(encoded, envelope.Meta.Route, t.input.Service) }); err != nil {
				t.stream.addOutput(time.Since(outputStarted))
				journal.outputFailure(t.input.Path, err.Error())
				return err
			}
			t.stream.addOutput(time.Since(outputStarted))
		}

		if envelope.NLI.TemplateID != "" && envelope.NLI.Parser == "unknown" {
			clusterID := 0
			fmt.Sscanf(envelope.NLI.TemplateID, "tpl-%d", &clusterID)
			if clusterID > 0 {
				discoverer.Submit(clusterID, line)
			}
		}

		atomic.AddUint64(&metrics.received, 1)
		t.stream.validationAttempts.Add(1)
		if envelope.Meta.Route == "normal" || envelope.Meta.Route == "warning" {
			atomic.AddUint64(&metrics.parsed, 1)
			t.stream.normalized.Add(1)
			t.stream.validationPassed.Add(1)
		} else {
			atomic.AddUint64(&metrics.quarantined, 1)
			t.stream.quarantined.Add(1)
			t.stream.validationFailed.Add(1)
		}
		t.stream.addTotal(time.Since(started))
		return nil
	}
	emitSelected := func(t *tail, line string, truncated, oversized bool) error {
		keep, parserService := selectionMatches(t.input.Selection, line)
		if !keep {
			atomic.AddUint64(&metrics.filtered, 1)
			return nil
		}
		return emit(t, line, truncated, oversized, parserService)
	}

	tails := make([]*tail, 0, len(c.Inputs))
	for _, inp := range c.Inputs {
		state, hasCheckpoint := states[inp.Path]
		t := &tail{
			input:         inp,
			state:         state,
			hasCheckpoint: hasCheckpoint,
			ml:            multiline.New(inp.Multiline),
			stream:        &streamState{nodeID: nodeID, capacity: 0, sourceWorkers: sourceWorkers, thresholds: streamThresholds},
		}
		tails = append(tails, t)
	}
	defer func() {
		for _, t := range tails {
			if t.file != nil {
				t.file.Close()
			}
		}
	}()

	serverErrors := make(chan error, 1)
	if c.MetricsAddress != "" && !once {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":         "running",
				"version":        engine.Version,
				"inputs":         len(tails),
				"source_workers": sourceWorkers,
				"logs_received":  atomic.LoadUint64(&metrics.received),
			})
		})
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			aiStats := discoverer.Stats()
			upesStats := upesRuntime.Stats()
			fmt.Fprintf(w, "nli_logs_received_total %d\nnli_logs_parsed_total %d\nnli_logs_quarantined_total %d\nnli_logs_filtered_total %d\nnli_input_errors_total %d\nnli_ai_pending_clusters %d\nnli_ai_pending_dropped_total %d\nnli_ai_samples_truncated_total %d\nnli_upes_unique_failure_signatures_total %d\nnli_upes_observed_failures_total %d\nnli_upes_discovery_queued_total %d\nnli_upes_discovery_dropped_total %d\nnli_upes_discovery_pending %d\n",
				atomic.LoadUint64(&metrics.received),
				atomic.LoadUint64(&metrics.parsed),
				atomic.LoadUint64(&metrics.quarantined),
				atomic.LoadUint64(&metrics.filtered),
				atomic.LoadUint64(&metrics.failures),
				aiStats.PendingClusters,
				aiStats.DroppedPending,
				aiStats.TruncatedSamples,
				upesStats.UniqueSignatures,
				upesStats.ObservedFailures,
				upesStats.QueuedDiscoveries,
				upesStats.DroppedDiscoveries,
				upesStats.PendingDiscoveries)
		})
		mux.HandleFunc("/stream-snapshot", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "GET required", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"files": streamSamples.snapshot()})
		})
		listener, e := net.Listen("tcp", c.MetricsAddress)
		if e != nil {
			return e
		}
		server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
		go func() {
			if e := server.Serve(listener); e != nil && e != http.ErrServerClosed {
				serverErrors <- e
			}
		}()
		defer func() {
			stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			server.Shutdown(stop)
		}()
	}

	var warned sync.Map
	persistTick := time.NewTicker(60 * time.Second)
	defer persistTick.Stop()
	aiTick := time.NewTicker(30 * time.Second)
	defer aiTick.Stop()
	watcherWake, closeWatcher := watchInputDirectories(ctx, c.Inputs)
	defer closeWatcher()
	reconcileTick := time.NewTicker(time.Duration(c.PollMS) * time.Millisecond)
	defer reconcileTick.Stop()

	processTail := func(t *tail) error {
		if ctx.Err() != nil {
			return nil
		}
		if t.ml.TimedOut(time.Now()) {
			if result := t.ml.Flush(); result != nil {
				if err := emitSelected(t, result.Line, result.Truncated, false); err != nil {
					return err
				}
			}
		}
		if !t.ml.Pending() && t.state.Offset > checkpointedOffset(t.input.Path) {
			if err := commit(t); err != nil {
				return err
			}
		}
		if t.file == nil {
			f, e := os.Open(t.input.Path)
			if e != nil {
				depth, capacity := outputQueueStats()
				streamSamples.put(t.stream.publish(t.input.Path, "error", depth, capacity, time.Now()))
				if errors.Is(e, os.ErrPermission) {
					journal.event("permission_failure", t.input.Path, e.Error())
				} else {
					journal.event("read_failure", t.input.Path, e.Error())
				}
				if once {
					return e
				}
				atomic.AddUint64(&metrics.failures, 1)
				t.stream.errors.Add(1)
				if _, alreadyWarned := warned.LoadOrStore(t.input.Path, true); !alreadyWarned {
					fmt.Fprintf(os.Stderr, "input unavailable: %s: %v\n", t.input.Path, e)
				}
				return nil
			}
			info, e := f.Stat()
			if e != nil {
				f.Close()
				return e
			}
			if !info.Mode().IsRegular() {
				f.Close()
				return fmt.Errorf("input must be a regular file: %s", t.input.Path)
			}
			dev, ino, identityErr := identity(f, info)
			if identityErr != nil {
				f.Close()
				return fmt.Errorf("identify input %s: %w", t.input.Path, identityErr)
			}
			legacyWindowsCheckpoint := runtime.GOOS == "windows" && t.hasCheckpoint && t.state.Device == 0 && dev != 0 && t.state.Offset <= info.Size()
			if legacyWindowsCheckpoint {
				// Releases before V20 derived the Windows identity from modification
				// time. Preserve the acknowledged offset once while migrating that
				// checkpoint to the stable volume/file-index identity.
				t.state.Device = dev
				t.state.Inode = ino
			} else if t.state.Device != dev || t.state.Inode != ino || t.state.Offset > info.Size() {
				initialObservation := !t.hasCheckpoint && t.state.Device == 0 && t.state.Inode == 0
				t.state = cursor{Device: dev, Inode: ino}
				if initialObservation && c.TailFromEnd && !once {
					t.state.Offset = info.Size()
				}
				t.hasCheckpoint = true
			}
			t.file = f
			journal.event("source_opened", t.input.Path, "")
			warned.Delete(t.input.Path)
		}

		info, e := t.file.Stat()
		if e != nil {
			return e
		}
		if info.Size() < t.state.Offset {
			t.state.Offset = 0
			t.state.Line = 0
		}
		if _, e = t.file.Seek(t.state.Offset, io.SeekStart); e != nil {
			return e
		}
		reader := bufio.NewReaderSize(t.file, 64*1024)

		processed := 0
		for ; processed < batchSize; processed++ {
			if ctx.Err() != nil {
				return nil
			}
			readStarted := time.Now()
			recordStart := t.state.Offset
			raw, n, complete, oversized, e := readRecord(reader)
			if e != nil {
				return e
			}
			recordOversized := oversized || t.oversizedPhysicalLine
			if n == 0 || (!complete && !once && !oversized) {
				break
			}
			lineEnded := complete || (once && !oversized)
			if lineEnded {
				t.stream.readLines.Add(1)
			}
			t.stream.bytes.Add(uint64(n))
			t.stream.readDuration.Add(int64(time.Since(readStarted)))
			if oversized && !complete {
				t.oversizedPhysicalLine = true
			} else if lineEnded {
				t.oversizedPhysicalLine = false
			}
			if strings.TrimSpace(raw) == "" {
				t.state.Offset += n
				if lineEnded {
					t.state.Line++
				}
				continue
			}
			if result := t.ml.Feed(raw, time.Now()); result != nil {
				resultOversized := recordOversized && !t.ml.Pending()
				if recordOversized && t.ml.Pending() {
					t.ml.MarkTruncated()
				}
				if err := emitSelected(t, result.Line, result.Truncated, resultOversized); err != nil {
					return err
				}
				if t.ml.Pending() {
					// Feed emitted the completed event and retained this physical
					// record as the start of the next multiline event. t.state is
					// still at recordStart here, so it is the acknowledged prefix.
					prefix := t.state
					prefix.Offset = recordStart
					if e = persistCursor(t, prefix); e != nil {
						return e
					}
				}
			} else if recordOversized && t.ml.Pending() {
				t.ml.MarkTruncated()
			}

			t.state.Offset += n
			if lineEnded {
				t.state.Line++
			}
		}
		if !t.ml.Pending() && t.state.Offset > checkpointedOffset(t.input.Path) {
			if e = commit(t); e != nil {
				return e
			}
		}

		if processed < batchSize {
			phase := "eof_wait"
			latest, e := os.Stat(t.input.Path)
			if (e == nil && !os.SameFile(info, latest)) || errors.Is(e, os.ErrNotExist) {
				if result := t.ml.Flush(); result != nil {
					if err := emitSelected(t, result.Line, result.Truncated, false); err != nil {
						return err
					}
				}
				if err := commit(t); err != nil {
					return err
				}
				t.file.Close()
				t.file = nil
				t.state = cursor{}
				journal.event("source_resume", t.input.Path, "file rotated")
			} else if e == nil && latest.Size() > t.state.Offset {
				phase = "reading"
				journal.event("source_resume", t.input.Path, "appended bytes")
			}
			journal.progress(t.input.Path, phase, journalCounts{read: t.stream.readLines.Load(), parsed: t.stream.normalized.Load(), quarantined: t.stream.quarantined.Load(), errors: t.stream.errors.Load(), bytes: t.stream.bytes.Load()})
			journal.outputRecovered(t.input.Path)
			depth, capacity := outputQueueStats()
			streamSamples.put(t.stream.publish(t.input.Path, phase, depth, capacity, time.Now()))
		} else {
			journal.progress(t.input.Path, "reading", journalCounts{read: t.stream.readLines.Load(), parsed: t.stream.normalized.Load(), quarantined: t.stream.quarantined.Load(), errors: t.stream.errors.Load(), bytes: t.stream.bytes.Load()})
			journal.outputRecovered(t.input.Path)
			depth, capacity := outputQueueStats()
			streamSamples.put(t.stream.publish(t.input.Path, "reading", depth, capacity, time.Now()))
		}
		return nil
	}

	for {
		if err := runSourceWorkers(ctx, tails, sourceWorkers, func(source *tail) error {
			err := processTail(source)
			if err != nil {
				atomic.AddUint64(&metrics.failures, 1)
				source.stream.errors.Add(1)
			}
			return err
		}); err != nil {
			return err
		}

		if once {
			more := false
			for _, t := range tails {
				if t.file != nil {
					info, e := t.file.Stat()
					if e != nil {
						return e
					}
					if info.Size() > t.state.Offset {
						more = true
					}
				}
			}
			if !more {
				for _, t := range tails {
					if result := t.ml.Flush(); result != nil {
						if err := emitSelected(t, result.Line, result.Truncated, false); err != nil {
							return err
						}
					}
				}
				for _, t := range tails {
					if err := commit(t); err != nil {
						return err
					}
				}
				if err := discoverer.RunBatch(ctx, clusterSizes(deps.Miner)); err != nil {
					fmt.Fprintf(os.Stderr, "nli: AI discovery: %v\n", err)
				}
				if err := persister.Save(); err != nil {
					return err
				}
				return nil
			}
			continue
		}

		select {
		case <-ctx.Done():
			// Finish already-read multiline events before closing the routers. Their
			// source offsets are committed only after output Flush succeeds, so an
			// interrupted write remains replayable from the previous checkpoint.
			shutdownErr := finishPendingSources(tails,
				func(t *tail, line string, truncated bool) error {
					return emitSelected(t, line, truncated, false)
				}, commit)
			if err := persister.Save(); err != nil {
				shutdownErr = errors.Join(shutdownErr, fmt.Errorf("save AI discovery state: %w", err))
			}
			return shutdownErr
		case e := <-serverErrors:
			return e
		case <-persistTick.C:
			persister.SaveIfDue()
		case <-aiTick.C:
			if err := discoverer.RunBatch(ctx, clusterSizes(deps.Miner)); err != nil {
				fmt.Fprintf(os.Stderr, "nli: AI discovery: %v\n", err)
			}
		case <-watcherWake:
		case <-reconcileTick.C:
		}
	}
}

func clusterSizes(miner *drain.Miner) map[int]int {
	sizes := make(map[int]int)
	if miner == nil {
		return sizes
	}
	for _, cluster := range miner.Clusters() {
		sizes[cluster.ID] = cluster.Size
	}
	return sizes
}

// sanitizeEventData recursively removes forbidden fields from event data
// For unknown/quarantine routes, returns metadata only
func sanitizeEventData(data map[string]interface{}, route string) map[string]interface{} {
	// Unknown/quarantine outcomes are telemetry-only in normal operation.
	if route == "quarantine" || route == "unknown" {
		return map[string]interface{}{}
	}

	// For normal routes, recursively sanitize to remove forbidden fields
	result := recursiveSanitize(data)
	if sanitizedMap, ok := result.(map[string]interface{}); ok {
		return sanitizedMap
	}
	// Fallback if sanitization changed the type
	return map[string]interface{}{"_sanitized": true}
}

// recursiveSanitize removes forbidden fields at any nesting level
func recursiveSanitize(obj interface{}) interface{} {
	switch v := obj.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, value := range v {
			// Remove forbidden keys
			if key == "raw" || key == "_raw" {
				continue
			}
			// Special case: _unknown may contain raw, so sanitize it
			if key == "_unknown" {
				if unknownMap, ok := value.(map[string]interface{}); ok {
					sanitized := recursiveSanitize(unknownMap)
					if sanitizedMap, ok := sanitized.(map[string]interface{}); ok && len(sanitizedMap) > 0 {
						result[key] = sanitizedMap
					}
				}
				continue
			}
			// Recursively sanitize nested structures
			result[key] = recursiveSanitize(value)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = recursiveSanitize(item)
		}
		return result
	default:
		return v
	}
}
