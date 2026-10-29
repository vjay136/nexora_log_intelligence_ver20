// Package ai implements selective AI parser discovery.
// AI is called ONLY for unknown Drain clusters with ≥MinClusterSize samples.
// AI output MUST be a ParserDef JSON — never executable code.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nexora.local/nli-v20/internal/drain"
	"nexora.local/nli-v20/internal/registry"
)

// Config for AI provider integration.
type Config struct {
	Enabled            bool   `json:"enabled"`
	Provider           string `json:"provider"` // openai, anthropic, ollama, none
	Model              string `json:"model"`
	APIKeyEnv          string `json:"api_key_env"`
	Endpoint           string `json:"endpoint"`
	MaxCallsPerHour    int    `json:"max_calls_per_hour"`
	BatchSize          int    `json:"batch_size"`
	MinClusterSize     int    `json:"min_cluster_size"`
	MaxPendingClusters int    `json:"max_pending_clusters,omitempty"`
	MaxSampleBytes     int    `json:"max_sample_bytes,omitempty"`
}

const (
	defaultMaxPendingClusters = 1000
	defaultMaxSampleBytes     = 16 * 1024
	hardMaxSampleBytes        = 64 * 1024
)

// PendingCluster holds samples awaiting AI parser discovery.
type PendingCluster struct {
	ClusterID int
	Samples   []string
}

// Discoverer manages AI-assisted parser discovery.
type Discoverer struct {
	cfg              Config
	pending          []PendingCluster
	hourly           []time.Time // timestamps of recent calls for rate limiting
	mu               sync.Mutex
	reg              *registry.Registry
	miner            *drain.Miner
	stateDir         string
	droppedPending   uint64
	truncatedSamples uint64
}

// QueueStats exposes bounded-queue pressure without revealing log contents.
type QueueStats struct {
	PendingClusters  int    `json:"pending_clusters"`
	DroppedPending   uint64 `json:"dropped_pending"`
	TruncatedSamples uint64 `json:"truncated_samples"`
}

// New creates a Discoverer. stateDir is where AI parser candidates are staged.
func New(cfg Config, reg *registry.Registry, miner *drain.Miner, stateDir string) *Discoverer {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 20
	}
	if cfg.MinClusterSize <= 0 {
		cfg.MinClusterSize = 10
	}
	if cfg.MaxCallsPerHour <= 0 {
		cfg.MaxCallsPerHour = 100
	}
	if cfg.MaxPendingClusters <= 0 {
		cfg.MaxPendingClusters = defaultMaxPendingClusters
	}
	if cfg.MaxSampleBytes <= 0 {
		cfg.MaxSampleBytes = defaultMaxSampleBytes
	} else if cfg.MaxSampleBytes > hardMaxSampleBytes {
		cfg.MaxSampleBytes = hardMaxSampleBytes
	}
	return &Discoverer{cfg: cfg, reg: reg, miner: miner, stateDir: stateDir}
}

// Submit queues a cluster for AI discovery when it reaches MinClusterSize.
func (d *Discoverer) Submit(clusterID int, sample string) {
	if !d.cfg.Enabled {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	sample = strings.ToValidUTF8(sample, "\uFFFD")
	if len(sample) > d.cfg.MaxSampleBytes {
		sample = truncateUTF8(sample, d.cfg.MaxSampleBytes)
		d.truncatedSamples++
	}

	// Find existing pending entry or create one.
	for i := range d.pending {
		if d.pending[i].ClusterID == clusterID {
			if len(d.pending[i].Samples) < 5 {
				d.pending[i].Samples = append(d.pending[i].Samples, sample)
			}
			return
		}
	}
	if len(d.pending) >= d.cfg.MaxPendingClusters {
		d.droppedPending++
		return
	}
	d.pending = append(d.pending, PendingCluster{
		ClusterID: clusterID,
		Samples:   []string{sample},
	})
}

// Stats returns a concurrency-safe snapshot of the bounded pending queue.
func (d *Discoverer) Stats() QueueStats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return QueueStats{
		PendingClusters:  len(d.pending),
		DroppedPending:   d.droppedPending,
		TruncatedSamples: d.truncatedSamples,
	}
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

// RunBatch processes pending clusters that have reached MinClusterSize.
func (d *Discoverer) RunBatch(ctx context.Context, clusterSizes map[int]int) error {
	if !d.cfg.Enabled {
		return nil
	}
	d.mu.Lock()
	ready := make([]PendingCluster, 0, d.cfg.BatchSize)
	remaining := make([]PendingCluster, 0)
	for _, pc := range d.pending {
		if clusterSizes[pc.ClusterID] >= d.cfg.MinClusterSize && len(ready) < d.cfg.BatchSize {
			ready = append(ready, pc)
		} else {
			remaining = append(remaining, pc)
		}
	}
	d.pending = remaining
	d.mu.Unlock()

	if len(ready) == 0 {
		return nil
	}

	for _, pending := range ready {
		if !d.checkRateLimit() {
			d.mu.Lock()
			d.pending = append([]PendingCluster{pending}, d.pending...)
			d.mu.Unlock()
			continue
		}
		parserDef, err := d.discoverParser(ctx, pending)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ai: cluster %d discovery failed: %v\n", pending.ClusterID, err)
			d.mu.Lock()
			d.pending = append(d.pending, pending)
			d.mu.Unlock()
			continue
		}
		if err := registry.Validate(parserDef); err != nil {
			_ = d.reg.SaveParser(d.stateDir+"/parsers/quarantine", parserDef)
			fmt.Fprintf(os.Stderr, "ai: parser for cluster %d failed schema validation: %v\n", pending.ClusterID, err)
			continue
		}
		failures := registry.RunRegressionTests(parserDef)
		if len(failures) > 0 {
			_ = d.reg.SaveParser(d.stateDir+"/parsers/quarantine", parserDef)
			fmt.Fprintf(os.Stderr, "ai: parser %s failed regression (%d failures), quarantined\n", parserDef.ID, len(failures))
			continue
		}
		// A valid AI response is evidence for a candidate, never authorization to
		// change the active parser registry. Activation must travel through the
		// controller's checksum-bound review, independent approval, signed bundle,
		// and client acknowledgement workflow.
		if err := d.reg.SaveParser(d.stateDir+"/parsers/candidates", parserDef); err != nil {
			fmt.Fprintf(os.Stderr, "ai: failed to stage parser candidate %s: %v\n", parserDef.ID, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "ai: parser candidate %s staged for managed review; cluster %d remains unassigned\n", parserDef.ID, pending.ClusterID)
	}
	return nil
}

func (d *Discoverer) checkRateLimit() bool {
	now := time.Now()
	cutoff := now.Add(-time.Hour)
	fresh := d.hourly[:0]
	for _, t := range d.hourly {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	d.hourly = fresh
	if len(d.hourly) >= d.cfg.MaxCallsPerHour {
		return false
	}
	d.hourly = append(d.hourly, now)
	return true
}

func (d *Discoverer) discoverParser(ctx context.Context, pc PendingCluster) (*registry.ParserDef, error) {
	switch d.cfg.Provider {
	case "ollama":
		return d.callOllama(ctx, pc)
	case "openai":
		return d.callOpenAI(ctx, pc)
	case "anthropic":
		return d.callAnthropic(ctx, pc)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", d.cfg.Provider)
	}
}

// prompt builds the AI prompt for parser discovery.
func buildPrompt(pc PendingCluster) string {
	var b bytes.Buffer
	b.WriteString("You are a log parser expert. Given these log sample lines, produce a ParserDef JSON object that describes how to extract structured fields.\n\n")
	b.WriteString("IMPORTANT: Return ONLY valid JSON matching this exact schema:\n")
	b.WriteString(`{"id":"service.event","version":1,"description":"...","format":"plain|json|kvpairs|syslog_rfc3164|clf","pattern_type":"grok|regex","pattern":"anchored full-record pattern with named fields","fields":[{"name":"fieldname","source":"group:fieldname or kv:key or json:key or token:N","type":"string|integer|float|boolean|ip|timestamp","required":false}],"tests":[{"input":"exact sample","expected":{"field":"value"}}]}`)
	b.WriteString("\n\nLog samples:\n")
	for i, s := range pc.Samples {
		fmt.Fprintf(&b, "%d: %s\n", i+1, s)
	}
	b.WriteString("\nReturn ONLY the JSON object. No explanation, no code, no shell commands.")
	return b.String()
}

func (d *Discoverer) callOllama(ctx context.Context, pc PendingCluster) (*registry.ParserDef, error) {
	endpoint := d.cfg.Endpoint
	if endpoint == "" {
		endpoint = "http://127.0.0.1:11434"
	}
	model := d.cfg.Model
	if model == "" {
		model = "llama3.2"
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":  model,
		"prompt": buildPrompt(pc),
		"stream": false,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/api/generate", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return parseParserDefJSON(result.Response)
}

func (d *Discoverer) callOpenAI(ctx context.Context, pc PendingCluster) (*registry.ParserDef, error) {
	apiKey := os.Getenv(d.cfg.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key env %q not set", d.cfg.APIKeyEnv)
	}
	endpoint := d.cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/chat/completions"
	}
	model := d.cfg.Model
	if model == "" {
		model = "gpt-4o-mini"
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": buildPrompt(pc)},
		},
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices in OpenAI response")
	}
	return parseParserDefJSON(result.Choices[0].Message.Content)
}

func (d *Discoverer) callAnthropic(ctx context.Context, pc PendingCluster) (*registry.ParserDef, error) {
	apiKey := os.Getenv(d.cfg.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key env %q not set", d.cfg.APIKeyEnv)
	}
	endpoint := d.cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1/messages"
	}
	model := d.cfg.Model
	if model == "" {
		model = "claude-3-5-haiku-latest"
	}
	body, _ := json.Marshal(map[string]interface{}{
		"model": model, "max_tokens": 2048, "temperature": 0,
		"messages": []map[string]string{{"role": "user", "content": buildPrompt(pc)}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Anthropic HTTP %d", resp.StatusCode)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
			Type string `json:"type"`
		} `json:"content"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	for _, block := range result.Content {
		if block.Type == "text" && block.Text != "" {
			return parseParserDefJSON(block.Text)
		}
	}
	return nil, fmt.Errorf("Anthropic response contained no text")
}

func parseParserDefJSON(value string) (*registry.ParserDef, error) {
	trimmed := strings.TrimSpace(value)
	var parser registry.ParserDef
	if err := json.Unmarshal([]byte(trimmed), &parser); err != nil {
		return nil, fmt.Errorf("AI response must be a single ParserDef JSON object: %w", err)
	}
	if parser.Version == 0 {
		parser.Version = 1
	}
	if !strings.HasPrefix(parser.ID, "ai.") {
		parser.ID = "ai." + parser.ID
	}
	if len(parser.Tests) == 0 {
		return nil, fmt.Errorf("AI parser must include regression tests")
	}
	if parser.Pattern == "" && parser.Format != "json" && parser.Format != "kvpairs" && parser.Format != "logfmt" {
		return nil, fmt.Errorf("AI parser must include a declarative pattern")
	}
	if err := registry.Validate(&parser); err != nil {
		return nil, err
	}
	return &parser, nil
}
