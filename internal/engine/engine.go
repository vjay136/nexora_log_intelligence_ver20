// Package engine is the NLI parse pipeline orchestrator.
// It composes all pipeline stages: multiline → detect → timestamp → mask →
// tokenize → drain template mine → deterministic parse → type infer →
// schema validate → confidence → output route.
package engine

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/confidence"
	"nexora.local/nli-v20/internal/detector"
	"nexora.local/nli-v20/internal/drain"
	"nexora.local/nli-v20/internal/masker"
	"nexora.local/nli-v20/internal/registry"
	ts "nexora.local/nli-v20/internal/timestamp"
	"nexora.local/nli-v20/internal/typeinfer"
)

// Version is the NLI engine version.
const Version = "20.0.0"

// MaxLine is the maximum line size processed (1 MiB).
const MaxLine = 1024 * 1024

// V5ParseResult contains separated v5 parsing output
type V5ParseResult struct {
	EventPayload     map[string]interface{} // Customer fields only
	ParseTelemetry   map[string]interface{} // Metadata only
	Success          bool
	Quarantined      bool
	ParserID         string
	Confidence       float64
	EvidenceCoverage float64
	LatencyMs        int64
	AttemptCount     int
	FallbackDepth    int
	Trace            []map[string]interface{}
	FieldEvidence    []map[string]interface{}
}

// V5Parser interface for ParserPack integration (avoids import cycles)
type V5Parser interface {
	TryParseV5(input string, source Source) *V5ParseResult
}

type NLI struct {
	Version       string  `json:"version"`
	Parser        string  `json:"parser"`
	ParserVersion int     `json:"parser_version"`
	Confidence    float64 `json:"confidence"`
	TemplateID    string  `json:"template_id,omitempty"`
}

type Source struct {
	Hostname   string `json:"hostname"`
	Service    string `json:"service"`
	File       string `json:"file"`
	LineNumber int64  `json:"line_number"`
	IngestTime string `json:"ingest_time"`
}

type Event struct {
	Name     string `json:"name"`
	Code     string `json:"code,omitempty"`
	Level    string `json:"level"`
	Category string `json:"category"`
}

type CandidateEvidence struct {
	Strategy         string  `json:"strategy"`
	Parser           string  `json:"parser"`
	Score            float64 `json:"score"`
	Confidence       float64 `json:"confidence"`
	EvidenceCoverage float64 `json:"evidence_coverage"`
	FieldsExtracted  int     `json:"fields_extracted"`
	ValidationErrors int     `json:"validation_errors"`
}

type Assurance struct {
	RawSHA256        string              `json:"raw_sha256"`
	Fingerprint      string              `json:"fingerprint,omitempty"`
	ProfileEvidence  []string            `json:"profile_evidence,omitempty"`
	SelectedStrategy string              `json:"selected_strategy"`
	EvidenceCoverage float64             `json:"evidence_coverage"`
	Ambiguous        bool                `json:"ambiguous"`
	Verified         bool                `json:"verified"`
	Candidates       []CandidateEvidence `json:"candidates"`
}

type Meta struct {
	Latency            int64                    `json:"parse_latency_us"`
	Matches            int                      `json:"parser_matches"`
	Extracted          int                      `json:"fields_extracted"`
	Unknown            int                      `json:"fields_unknown"`
	Timezone           string                   `json:"original_timezone,omitempty"`
	Format             string                   `json:"detected_format"`
	Route              string                   `json:"route"`
	TimestampInferred  bool                     `json:"timestamp_inferred,omitempty"`
	MultilineTruncated bool                     `json:"multiline_truncated,omitempty"`
	RawRedacted        bool                     `json:"raw_redacted,omitempty"`
	Errors             []string                 `json:"validation_errors,omitempty"`
	ParserAttempts     int                      `json:"parser_attempts,omitempty"`
	FallbackDepth      int                      `json:"fallback_depth,omitempty"`
	ParserTrace        []map[string]interface{} `json:"parser_trace,omitempty"`
	FieldEvidence      []map[string]interface{} `json:"field_evidence,omitempty"`
	Assurance          Assurance                `json:"assurance"`
}

// Envelope is the normalized JSON output record.
type Envelope struct {
	NLI       NLI                    `json:"nli"`
	Timestamp string                 `json:"timestamp"`
	Source    Source                 `json:"source"`
	Event     Event                  `json:"event"`
	Data      map[string]interface{} `json:"data"`
	Raw       string                 `json:"raw"`
	Meta      Meta                   `json:"meta"`
}

// --- Pipeline components (injected at startup) ---

// Deps holds the pipeline dependencies.
type Deps struct {
	Miner    *drain.Miner
	Registry *registry.Registry
	Masker   *masker.Masker
	V5Parser V5Parser
}

// Default creates a Deps with sensible defaults (no AI, in-memory state).
func Default() *Deps {
	m := masker.New(nil)
	miner := drain.New(drain.Config{})
	reg := registry.New()
	registry.LoadBuiltin(reg)

	// V5Parser can be set separately to avoid import cycles
	return &Deps{Miner: miner, Registry: reg, Masker: m, V5Parser: nil}
}

// SetV5Parser configures the v5 ParserPack parser (called from main/agent)
func (d *Deps) SetV5Parser(parser V5Parser) {
	d.V5Parser = parser
}

// --- Regex patterns ---

var (
	// SMTP native header (preserved from v0.1.0)
	smtpHeader = regexp.MustCompile(`^(\d{8})\s+(\d{9})([+-]\d{4})\s+(\S+)\s+(\S+)\s+\d+\s+\d+\s+\d+\s+(\w+);([^ (]+)\(([^)]+)\)\s*`)
	// key=value tokenizer lookahead
	nextKeyRE  = regexp.MustCompile(`^\s*[A-Za-z_][A-Za-z0-9_.\-]*\s*[:=]`)
	validKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.\-]*$`)
	// RFC5424: <PRI>VERSION TIMESTAMP HOSTNAME APP-NAME PROCID MSGID [SD] MSG
	syslog5424RE = regexp.MustCompile(`^<(\d{1,3})>(\d) (\S+) (\S+) (\S+) (\S+) (\S+) (.*)$`)
	// RFC3164: <PRI>Mon DD HH:MM:SS HOST PROG[PID]: MSG  or without PRI
	syslog3164RE = regexp.MustCompile(`^(?:<\d{1,3}>)?(\w{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s+(\S+)\s+([^\[:]+?)(?:\[(\d+)\])?: (.*)$`)
	// CLF/Apache
	clfRE = regexp.MustCompile(`^(\S+)\s+(\S+)\s+(\S+)\s+\[([^\]]+)\]\s+"(\S+)\s+(\S+)\s+HTTP/([\d.]+)"\s+(\d+)\s+(\S+)(?:\s+"([^"]*)"\s+"([^"]*)")?`)
	// CEF
	cefRE                 = regexp.MustCompile(`^CEF:(\d+)\|([^|]*)\|([^|]*)\|([^|]*)\|([^|]*)\|([^|]*)\|([^|]*)\|(.*)$`)
	plainTimestampLevelRE = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:?\d{2})?)\s+(?:\[([^\]]+)\]\s+)?(?:\[)?(TRACE|DEBUG|INFO|NOTICE|WARN(?:ING)?|ERROR|ERR|CRITICAL|CRIT|FATAL)(?:\])?\s+(.*)$`)
	plainLevelRE          = regexp.MustCompile(`^(?:\[)?(TRACE|DEBUG|INFO|NOTICE|WARN(?:ING)?|ERROR|ERR|CRITICAL|CRIT|FATAL)(?:\])?[:\s-]+(.*)$`)
	exceptionRE           = regexp.MustCompile(`(?s)^([A-Za-z_$][\w.$]*(?:Exception|Error))(?::\s*)?(.*)$`)
	sshdAcceptedRE        = regexp.MustCompile(`^(Accepted|Failed)\s+(\S+)\s+for\s+(?:invalid user\s+)?(\S+)\s+from\s+(\S+)\s+port\s+(\d+)`)
	sshdConnectionRE      = regexp.MustCompile(`^(Connection (?:closed|reset)|Disconnected)\s+(?:by|from)\s+(?:authenticating user\s+\S+\s+)?(\S+)\s+port\s+(\d+)`)
	postfixRE             = regexp.MustCompile(`^([A-F0-9]{5,}):\s+(.*)$`)
	sudoRE                = regexp.MustCompile(`^\s*(\S+)\s*:\s*(.*?);\s*COMMAND=(.*)$`)
)

// Parse processes a single raw log line through the full pipeline.
func Parse(raw string, source Source, deps *Deps) Envelope {
	return ParseWithTruncated(raw, source, deps, false)
}

// ParseWithSelectedParser runs one named declarative runtime parser. It is
// intentionally separate from production auto-detection and is used by the
// Assurance Lab when an operator selects an exact parser from the live
// registry. ParserPack family selection remains controlled by Source.Service.
func ParseWithSelectedParser(raw string, source Source, deps *Deps, parserID string) (Envelope, error) {
	parserID = strings.TrimSpace(parserID)
	if parserID == "" || parserID == "auto" {
		return Parse(raw, source, deps), nil
	}
	if deps == nil {
		deps = Default()
	}
	if deps.Registry == nil {
		return Envelope{}, fmt.Errorf("selected parser %q cannot run because the runtime registry is unavailable", parserID)
	}
	parserDef, ok := deps.Registry.Get(parserID)
	if !ok {
		return Envelope{}, fmt.Errorf("selected parser %q is not available in the active runtime registry", parserID)
	}

	envelope := Parse(raw, source, deps)
	fields, failures := registry.Apply(parserDef, strings.TrimSpace(raw))
	envelope.Data = fields
	envelope.NLI.Parser = parserDef.ID
	envelope.NLI.ParserVersion = parserDef.Version
	envelope.Meta.Errors = failures
	envelope.Meta.Extracted = len(fields)
	envelope.Meta.Matches = 1
	envelope.Meta.ParserAttempts = 1
	envelope.Meta.FallbackDepth = 0
	envelope.Meta.FieldEvidence = nil
	coverage := evidenceCoverage(strings.TrimSpace(raw), fields)
	envelope.Meta.Assurance = Assurance{
		RawSHA256:        fmt.Sprintf("%x", sha256Sum(raw)),
		SelectedStrategy: "selected:" + parserDef.ID,
		EvidenceCoverage: coverage,
		Verified:         len(fields) > 0 && len(failures) == 0,
		Candidates: []CandidateEvidence{{
			Strategy: "selected:" + parserDef.ID, Parser: parserDef.ID,
			Confidence: 0.99, EvidenceCoverage: coverage,
			FieldsExtracted: len(fields), ValidationErrors: len(failures),
		}},
	}
	envelope.Meta.ParserTrace = []map[string]any{{
		"stage": "parser_attempt", "attempt": 1, "strategy": "selected:" + parserDef.ID,
		"parser": parserDef.ID, "fields": len(fields), "validation_errors": len(failures),
		"selected": true, "success": len(fields) > 0 && len(failures) == 0,
	}}
	if len(fields) == 0 || len(failures) > 0 {
		envelope.NLI.Confidence = 0
		envelope.Meta.Unknown = 1
		envelope.Meta.Route = confidence.RouteQuarantine
	} else {
		envelope.NLI.Confidence = 0.99
		envelope.Meta.Unknown = 0
		envelope.Meta.Route = confidence.RouteNormal
	}
	return envelope, nil
}

// ParseWithTruncated processes a line with optional multiline truncation flag.
func ParseWithTruncated(raw string, source Source, deps *Deps, multilineTruncated bool) Envelope {
	start := time.Now()
	now := start.UTC().Format(time.RFC3339Nano)
	source.IngestTime = now
	if deps == nil {
		deps = Default()
	}

	redactedRaw := masker.MaskSecrets(raw)
	base := Envelope{
		NLI:    NLI{Version: Version, Parser: "unknown", ParserVersion: 1},
		Source: source,
		Event:  Event{Name: "Unclassified", Level: "info", Category: "unknown"},
		Data:   make(map[string]interface{}),
		Raw:    redactedRaw,
		Meta: Meta{
			Format:             "unknown",
			MultilineTruncated: multilineTruncated,
			RawRedacted:        redactedRaw != raw,
			Assurance: Assurance{
				RawSHA256: fmt.Sprintf("%x", sha256Sum(raw)),
			},
		},
	}

	text := strings.TrimSpace(raw)
	if text == "" {
		base.Timestamp = now
		base.Meta.Route = confidence.RouteUnknown
		base.Meta.Latency = time.Since(start).Microseconds()
		return base
	}

	primaryFormat := detector.Detect(text)
	base.Meta.Format = primaryFormat
	base.Meta.Assurance.Fingerprint, base.Meta.Assurance.ProfileEvidence = FormatFingerprint(text, primaryFormat)
	timestampResult := ts.Extract(text, start)
	base.Timestamp = timestampResult.Time.UTC().Format(time.RFC3339Nano)
	base.Meta.Timezone = timestampResult.OriginalTimezone
	base.Meta.TimestampInferred = timestampResult.Inferred

	// A valid JSON object is structurally unambiguous. Return it immediately
	// instead of running template mining and every text-format fallback. This
	// keeps the production JSONL path proportional to the record size while
	// preserving masking, type inference, assurance metadata, and routing.
	if primaryFormat == detector.FormatJSON {
		candidate := freshCandidate(base)
		parseJSON(text, &candidate)
		if candidate.NLI.Parser != "unknown" && candidate.Meta.Extracted > 0 && len(candidate.Meta.Errors) == 0 {
			candidate.Meta.Assurance.SelectedStrategy = detector.FormatJSON
			candidate.Meta.Assurance.EvidenceCoverage = 1
			candidate.Meta.Assurance.Verified = true
			candidate.Meta.Assurance.Candidates = []CandidateEvidence{{
				Strategy: detector.FormatJSON, Parser: candidate.NLI.Parser,
				Confidence: candidate.NLI.Confidence, EvidenceCoverage: 1,
				FieldsExtracted: candidate.Meta.Extracted,
			}}
			inferTypes(candidate.Data)
			if deps.Masker != nil {
				masker.MaskValue(candidate.Data)
			}
			if multilineTruncated {
				candidate.NLI.Confidence = minConfidence(candidate.NLI.Confidence, 0.79)
				candidate.Meta.Assurance.Verified = false
				candidate.Meta.Errors = append(candidate.Meta.Errors, "input record was truncated")
				candidate.Meta.Route = confidence.RouteQuarantine
			} else {
				candidate.Meta.Route = confidence.RouteNormal
			}
			candidate.Meta.Latency = time.Since(start).Microseconds()
			return candidate
		}
	}

	masked := text
	if deps.Masker != nil {
		masked = deps.Masker.MaskForTemplate(text)
	}
	var drainMatch drain.Match
	if deps.Miner != nil {
		drainMatch = deps.Miner.Process(masked)
		if drainMatch.ClusterID > 0 {
			base.NLI.TemplateID = fmt.Sprintf("tpl-%d", drainMatch.ClusterID)
		}
	}

	type scoredCandidate struct {
		envelope Envelope
		evidence CandidateEvidence
		score    float64
	}
	candidates := make([]scoredCandidate, 0, 6)
	attempted := make([]CandidateEvidence, 0, 6)

	consider := func(candidate Envelope, strategy string, preferred bool) {
		coverage := evidenceCoverage(text, candidate.Data)
		evidence := CandidateEvidence{
			Strategy: strategy, Parser: candidate.NLI.Parser,
			Confidence: candidate.NLI.Confidence, EvidenceCoverage: coverage,
			FieldsExtracted:  candidate.Meta.Extracted,
			ValidationErrors: len(candidate.Meta.Errors),
		}
		if candidate.NLI.Parser == "unknown" || candidate.Meta.Extracted == 0 {
			if preferred {
				evidence.Score = 0.025
			}
			attempted = append(attempted, evidence)
			return
		}
		score := candidate.NLI.Confidence
		if preferred {
			score += 0.025
		}
		if strings.HasPrefix(strategy, "learned:") {
			score += 0.04
		}
		if coverage < 0.8 {
			score -= (0.8 - coverage) * 0.35
		}
		score -= float64(len(candidate.Meta.Errors)) * 0.05
		evidence.Score = score
		candidate.Meta.Assurance.EvidenceCoverage = coverage
		candidate.Meta.Assurance.SelectedStrategy = strategy
		candidates = append(candidates, scoredCandidate{envelope: candidate, evidence: evidence, score: score})
	}

	// Try v5 ParserPack execution first.
	if deps.V5Parser != nil {
		v5Result := deps.V5Parser.TryParseV5(text, source)
		if v5Result.Quarantined {
			base.NLI.Parser = v5Result.ParserID
			base.NLI.Confidence = 0
			base.Meta.Route = confidence.RouteQuarantine
			base.Meta.Assurance.SelectedStrategy = "parserpack:" + v5Result.ParserID
			base.Meta.Assurance.EvidenceCoverage = v5Result.EvidenceCoverage
			base.Meta.ParserAttempts = v5Result.AttemptCount
			base.Meta.FallbackDepth = v5Result.FallbackDepth
			base.Meta.ParserTrace = v5Result.Trace
			base.Meta.FieldEvidence = v5Result.FieldEvidence
			base.Meta.Latency = time.Since(start).Microseconds()
			return base
		}
		if v5Result.Success && len(v5Result.EventPayload) > 0 {
			// Production ParserPack output retains the complete static prefix,
			// typed KV tail, stage traversal, and field-level source evidence.
			envelope := base
			envelope.Data = v5Result.EventPayload
			envelope.NLI.Parser = v5Result.ParserID
			envelope.NLI.ParserVersion = 5
			envelope.NLI.Confidence = v5Result.Confidence
			envelope.Meta.Extracted = len(v5Result.EventPayload)
			envelope.Meta.Matches = v5Result.AttemptCount
			envelope.Meta.ParserAttempts = v5Result.AttemptCount
			envelope.Meta.FallbackDepth = v5Result.FallbackDepth
			envelope.Meta.ParserTrace = v5Result.Trace
			envelope.Meta.FieldEvidence = v5Result.FieldEvidence
			envelope.Meta.Assurance.EvidenceCoverage = v5Result.EvidenceCoverage
			envelope.Meta.Assurance.SelectedStrategy = "parserpack:" + v5Result.ParserID
			envelope.Meta.Assurance.Verified = v5Result.EvidenceCoverage >= 0.8
			envelope.Meta.Route = confidence.RouteNormal
			envelope.Meta.Latency = time.Since(start).Microseconds()
			return envelope
		}
		// v5 parsing failed or quarantined - fall back to legacy v4 parsing
	}

	// A parser learned for this exact Drain template gets the first opportunity.
	if drainMatch.ParserID != "" && deps.Registry != nil {
		if parserDef, ok := deps.Registry.Get(drainMatch.ParserID); ok {
			candidate := freshCandidate(base)
			fields, failures := registry.Apply(parserDef, text)
			candidate.Data = fields
			candidate.Meta.Errors = append(candidate.Meta.Errors, failures...)
			candidate.Meta.Extracted = len(fields)
			candidate.NLI.Parser = parserDef.ID
			candidate.NLI.ParserVersion = parserDef.Version
			candidate.NLI.Confidence = 0.96 - float64(len(failures))*0.08
			consider(candidate, "learned:"+parserDef.ID, true)
		}
	}

	for index, strategy := range detector.Candidates(text) {
		if strategy == detector.FormatNginx && primaryFormat != detector.FormatNginx {
			continue
		}
		candidate := freshCandidate(base)
		parseByStrategy(strategy, text, &candidate, deps)
		consider(candidate, strategy, index == 0)
	}

	if len(candidates) == 0 {
		base.Data["_unknown"] = map[string]interface{}{"raw": redactedRaw}
		base.Meta.Unknown = 1
		base.Meta.Route = confidence.RouteUnknown
		base.Meta.Assurance.SelectedStrategy = primaryFormat
		base.Meta.Assurance.Candidates = rankEvidence(attempted)
		base.Meta.Latency = time.Since(start).Microseconds()
		return base
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].evidence.Parser < candidates[j].evidence.Parser
		}
		return candidates[i].score > candidates[j].score
	})
	e := candidates[0].envelope
	maxEvidence := 5
	if len(candidates) < maxEvidence {
		maxEvidence = len(candidates)
	}
	e.Meta.Assurance.Candidates = make([]CandidateEvidence, 0, maxEvidence)
	for _, candidate := range candidates[:maxEvidence] {
		e.Meta.Assurance.Candidates = append(e.Meta.Assurance.Candidates, candidate.evidence)
	}
	e.Meta.Assurance.Candidates = rankEvidence(e.Meta.Assurance.Candidates)
	e.Meta.Matches = len(candidates)
	e.Meta.Assurance.EvidenceCoverage = candidates[0].evidence.EvidenceCoverage
	e.Meta.Assurance.SelectedStrategy = candidates[0].evidence.Strategy

	if len(candidates) > 1 && candidates[0].evidence.Parser != candidates[1].evidence.Parser && candidates[0].score-candidates[1].score < 0.03 {
		e.Meta.Assurance.Ambiguous = true
		e.Meta.Errors = append(e.Meta.Errors, "ambiguous parser candidates")
		if e.NLI.Confidence > 0.79 {
			e.NLI.Confidence = 0.79
		}
	}
	if e.Meta.Assurance.EvidenceCoverage < 0.8 {
		e.Meta.Errors = append(e.Meta.Errors, "extracted values have insufficient raw evidence")
		if e.NLI.Confidence > 0.79 {
			e.NLI.Confidence = 0.79
		}
	}
	if multilineTruncated && e.NLI.Confidence > 0.79 {
		e.NLI.Confidence = 0.79
	}
	if len(e.Meta.Errors) > 0 && e.NLI.Confidence > 0.85 {
		e.NLI.Confidence = 0.85
	}
	e.Meta.Assurance.Verified = !e.Meta.Assurance.Ambiguous && e.Meta.Assurance.EvidenceCoverage >= 0.8 && len(e.Meta.Errors) == 0

	inferTypes(e.Data)
	if deps.Masker != nil {
		masker.MaskValue(e.Data)
	}
	e.Meta.Route = confidence.Route(e.NLI.Confidence)
	if e.Meta.Assurance.Ambiguous || multilineTruncated {
		e.Meta.Route = confidence.RouteQuarantine
	}
	e.Meta.Latency = time.Since(start).Microseconds()
	return e
}

func minConfidence(value, ceiling float64) float64 {
	if value > ceiling {
		return ceiling
	}
	return value
}

var (
	fingerprintIPRE     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	fingerprintUUIDRE   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	fingerprintKeyRE    = regexp.MustCompile(`(?:^|\s)([A-Za-z_][A-Za-z0-9_.-]*)\s*[:=]`)
	fingerprintNumberRE = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	fingerprintQuotedRE = regexp.MustCompile(`"[^"\\]*(?:\\.[^"\\]*)*"|'[^']*'`)
	fingerprintWordRE   = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_./-]*`)
	fingerprintSpaceRE  = regexp.MustCompile(`\s+`)
)

const fingerprintInputLimit = 4096

// FormatFingerprint describes bounded structure only. It deliberately excludes
// field values so equivalent records group together without exposing secrets.
func FormatFingerprint(line, format string) (string, []string) {
	if len(line) > fingerprintInputLimit {
		line = line[:fingerprintInputLimit]
	}
	profile := []string{"format:" + format}
	if strings.Contains(line, "=") {
		profile = append(profile, "has_equals")
	}
	if strings.Contains(line, ":") {
		profile = append(profile, "has_colon")
	}
	if strings.Contains(line, "|") {
		profile = append(profile, "has_pipe_delimiter")
	}
	if strings.Contains(line, `"`) {
		profile = append(profile, "has_quoted_fields")
	}
	keys := fingerprintKeyRE.FindAllStringSubmatch(line, 17)
	if len(keys) > 0 {
		profile = append(profile, "key_value_fields")
		for index, match := range keys {
			if index == 16 {
				profile = append(profile, "field_count:16+")
				break
			}
			profile = append(profile, "field:"+strings.ToLower(match[1]))
		}
	}
	shape := strings.ToLower(line)
	shape = fingerprintUUIDRE.ReplaceAllString(shape, "<uuid>")
	shape = fingerprintIPRE.ReplaceAllString(shape, "<ip>")
	shape = fingerprintKeyRE.ReplaceAllString(shape, " $1=<value>")
	shape = fingerprintNumberRE.ReplaceAllString(shape, "<n>")
	shape = fingerprintQuotedRE.ReplaceAllString(shape, "<quoted>")
	shape = fingerprintWordRE.ReplaceAllString(shape, "<word>")
	shape = fingerprintSpaceRE.ReplaceAllString(strings.TrimSpace(shape), " ")
	shape = format + "\x00" + strings.Join(profile, "\x00") + "\x00" + shape
	digest := sha256.Sum256([]byte(shape))
	return fmt.Sprintf("%x", digest[:12]), profile
}

func rankEvidence(candidates []CandidateEvidence) []CandidateEvidence {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].EvidenceCoverage != candidates[j].EvidenceCoverage {
			return candidates[i].EvidenceCoverage > candidates[j].EvidenceCoverage
		}
		if candidates[i].FieldsExtracted != candidates[j].FieldsExtracted {
			return candidates[i].FieldsExtracted > candidates[j].FieldsExtracted
		}
		return candidates[i].Strategy < candidates[j].Strategy
	})
	if len(candidates) > 5 {
		return candidates[:5]
	}
	return candidates
}

func freshCandidate(base Envelope) Envelope {
	candidate := base
	candidate.Data = make(map[string]interface{})
	candidate.Meta.Errors = nil
	candidate.Meta.Extracted = 0
	candidate.Meta.Unknown = 0
	candidate.Meta.Matches = 0
	candidate.NLI.Parser = "unknown"
	candidate.NLI.ParserVersion = 1
	candidate.NLI.Confidence = 0
	return candidate
}

func parseByStrategy(strategy, text string, candidate *Envelope, deps *Deps) {
	switch strategy {
	case detector.FormatJSON:
		parseJSON(text, candidate)
	case detector.FormatSyslogRFC5424:
		parseSyslog5424(text, candidate)
	case detector.FormatSyslogRFC3164:
		parseSyslog3164(text, candidate)
	case detector.FormatCLF:
		parseCLF(text, candidate)
	case detector.FormatNginx:
		parseCLF(text, candidate)
		if candidate.NLI.Parser != "unknown" {
			candidate.NLI.Parser = "web.nginx_access"
		}
	case detector.FormatCEF:
		parseCEF(text, candidate)
	case detector.FormatSMTPNative:
		parseSMTPNative(text, candidate, deps)
	case detector.FormatLogfmt, detector.FormatKVPairs:
		parseKV(text, candidate)
	case detector.FormatPlain:
		if !parsePlain(text, candidate) && !parseKVFallback(text, candidate) {
			candidate.Data["_unknown"] = map[string]interface{}{"raw": candidate.Raw}
			candidate.Meta.Unknown = 1
		}
	}
}

func evidenceCoverage(raw string, data map[string]interface{}) float64 {
	var values []string
	var collect func(interface{})
	collect = func(value interface{}) {
		switch typed := value.(type) {
		case map[string]interface{}:
			for key, item := range typed {
				if key != "raw" {
					collect(item)
				}
			}
		case []interface{}:
			for _, item := range typed {
				collect(item)
			}
		case nil:
			return
		default:
			text := strings.TrimSpace(fmt.Sprint(typed))
			if text != "" {
				values = append(values, text)
			}
		}
	}
	collect(data)
	if len(values) == 0 {
		return 0
	}
	lowerRaw := strings.ToLower(raw)
	matched := 0
	for _, value := range values {
		if strings.Contains(lowerRaw, strings.ToLower(value)) {
			matched++
		}
	}
	return float64(matched) / float64(len(values))
}

func sha256Sum(value string) [32]byte {
	return sha256.Sum256([]byte(value))
}

// --- Format-specific parsers ---

func parsePlain(text string, envelope *Envelope) bool {
	if match := plainTimestampLevelRE.FindStringSubmatch(text); match != nil {
		envelope.NLI.Parser = "application.timestamped"
		envelope.NLI.Confidence = 0.93
		envelope.Data["timestamp"] = match[1]
		if match[2] != "" {
			envelope.Data["context"] = match[2]
		}
		level := normalizeLevel(match[3])
		envelope.Data["level"] = level
		envelope.Data["message"] = match[4]
		envelope.Event.Name = "ApplicationLog"
		envelope.Event.Level = level
		envelope.Event.Category = "application"
		envelope.Meta.Extracted = len(envelope.Data)
		return true
	}
	if match := exceptionRE.FindStringSubmatch(text); match != nil {
		envelope.NLI.Parser = "application.exception"
		envelope.NLI.Confidence = 0.92
		envelope.Data["exception_type"] = match[1]
		envelope.Data["message"] = strings.SplitN(match[2], "\n", 2)[0]
		if strings.Contains(text, "\n") {
			envelope.Data["stack_trace"] = text
		}
		envelope.Event.Name = match[1]
		envelope.Event.Level = "error"
		envelope.Event.Category = "application"
		envelope.Meta.Extracted = len(envelope.Data)
		return true
	}
	if match := plainLevelRE.FindStringSubmatch(text); match != nil {
		level := normalizeLevel(match[1])
		envelope.NLI.Parser = "application.levelled"
		envelope.NLI.Confidence = 0.88
		envelope.Data["level"] = level
		envelope.Data["message"] = match[2]
		envelope.Event.Name = "ApplicationLog"
		envelope.Event.Level = level
		envelope.Event.Category = "application"
		envelope.Meta.Extracted = 2
		return true
	}
	return false
}

func normalizeLevel(value string) string {
	switch strings.ToUpper(value) {
	case "TRACE":
		return "trace"
	case "DEBUG":
		return "debug"
	case "INFO", "NOTICE":
		return "info"
	case "WARN", "WARNING":
		return "warn"
	case "ERROR", "ERR":
		return "error"
	case "CRITICAL", "CRIT", "FATAL":
		return "critical"
	default:
		return strings.ToLower(value)
	}
}

func parseJSON(text string, e *Envelope) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var values map[string]interface{}
	if err := dec.Decode(&values); err != nil {
		e.Meta.Errors = append(e.Meta.Errors, "invalid JSON: "+err.Error())
		e.Data["_unknown"] = map[string]interface{}{"raw": text}
		return
	}
	if values == nil {
		e.Meta.Errors = append(e.Meta.Errors, "JSON log must be an object")
		e.Data["_unknown"] = map[string]interface{}{"raw": text}
		return
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		e.Meta.Errors = append(e.Meta.Errors, "trailing JSON content")
		e.Data["_unknown"] = map[string]interface{}{"raw": text}
		return
	}
	e.NLI.Parser = "generic.json"
	e.NLI.Confidence = 0.99
	e.Meta.Matches = 1
	for k, v := range values {
		e.Data[k] = v
		e.Meta.Extracted++
	}
	if _, hasLog := values["log"]; hasLog {
		if _, hasStream := values["stream"]; hasStream {
			if _, hasTime := values["time"]; hasTime {
				e.NLI.Parser = "docker.json"
				e.Event.Name = "ContainerLog"
				e.Event.Category = "container"
			}
		}
	}
}

func parseSyslog5424(text string, e *Envelope) {
	m := syslog5424RE.FindStringSubmatch(text)
	if m == nil {
		e.Data["_unknown"] = map[string]interface{}{"raw": text}
		return
	}
	e.NLI.Parser = "syslog.rfc5424"
	e.NLI.Confidence = 0.98
	e.Meta.Matches = 1
	if m[3] != "-" {
		e.Data["timestamp"] = m[3]
	}
	if m[4] != "-" {
		e.Data["host"] = m[4]
	}
	if m[5] != "-" {
		e.Data["app"] = m[5]
	}
	if m[6] != "-" {
		e.Data["procid"] = m[6]
	}
	if m[7] != "-" {
		e.Data["msgid"] = m[7]
	}
	e.Data["message"] = m[8]
	e.Meta.Extracted = 5
	e.Event.Category = "system"
}

func parseSyslog3164(text string, e *Envelope) {
	m := syslog3164RE.FindStringSubmatch(text)
	if m == nil {
		e.Data["_unknown"] = map[string]interface{}{"raw": text}
		return
	}
	e.NLI.Parser = "syslog.rfc3164"
	e.NLI.Confidence = 0.97
	e.Meta.Matches = 1
	e.Data["timestamp"] = m[1]
	e.Data["host"] = m[2]
	e.Data["program"] = strings.TrimSpace(m[3])
	if m[4] != "" {
		e.Data["pid"] = m[4]
	}
	e.Data["message"] = m[5]
	e.Meta.Extracted = len(e.Data)
	e.Event.Category = "system"
	specializeSyslog(e, strings.TrimSpace(m[3]), m[5])
}

func specializeSyslog(envelope *Envelope, program, message string) {
	switch {
	case strings.HasPrefix(program, "sshd"):
		if match := sshdAcceptedRE.FindStringSubmatch(message); match != nil {
			envelope.NLI.Parser = "security.sshd"
			envelope.NLI.Confidence = 0.99
			envelope.Data["event_type"] = strings.ToLower(match[1])
			envelope.Data["auth_method"] = match[2]
			envelope.Data["user"] = match[3]
			envelope.Data["source_ip"] = match[4]
			envelope.Data["source_port"] = match[5]
			envelope.Event.Name = "SSHAuthentication"
			envelope.Event.Level = map[bool]string{true: "error", false: "info"}[match[1] == "Failed"]
			envelope.Event.Category = "authentication"
		} else if match := sshdConnectionRE.FindStringSubmatch(message); match != nil {
			envelope.NLI.Parser = "security.sshd"
			envelope.NLI.Confidence = 0.97
			envelope.Data["event_type"] = strings.ToLower(match[1])
			envelope.Data["source_ip"] = match[2]
			envelope.Data["source_port"] = match[3]
			envelope.Event.Name = "SSHConnection"
			envelope.Event.Category = "network"
		}
	case strings.HasPrefix(program, "sudo"):
		if match := sudoRE.FindStringSubmatch(message); match != nil {
			envelope.NLI.Parser = "security.sudo"
			envelope.NLI.Confidence = 0.97
			envelope.Data["user"] = match[1]
			envelope.Data["attributes"] = match[2]
			envelope.Data["command"] = match[3]
			envelope.Event.Name = "SudoCommand"
			envelope.Event.Category = "process"
		}
	case strings.HasPrefix(program, "postfix"):
		if match := postfixRE.FindStringSubmatch(message); match != nil {
			envelope.NLI.Parser = "smtp.postfix"
			envelope.NLI.Confidence = 0.97
			envelope.Data["queue_id"] = match[1]
			envelope.Data["smtp_message"] = match[2]
			envelope.Event.Name = "PostfixMessage"
			envelope.Event.Category = "email"
		}
	}
	envelope.Meta.Extracted = len(envelope.Data)
}

func parseCLF(text string, e *Envelope) {
	m := clfRE.FindStringSubmatch(text)
	if m == nil {
		e.Data["_unknown"] = map[string]interface{}{"raw": text}
		return
	}
	e.NLI.Parser = "web.apache_clf"
	e.NLI.Confidence = 0.98
	e.Meta.Matches = 1
	e.Data["client_ip"] = m[1]
	e.Data["timestamp"] = m[4]
	e.Data["method"] = m[5]
	e.Data["uri"] = m[6]
	e.Data["http_version"] = m[7]
	e.Data["status"] = m[8]
	if m[9] != "-" {
		e.Data["bytes"] = m[9]
	}
	if len(m) > 10 && m[10] != "" {
		e.Data["referrer"] = m[10]
	}
	if len(m) > 11 && m[11] != "" {
		e.Data["user_agent"] = m[11]
	}
	e.Meta.Extracted = 7
	e.Event.Category = "web"
}

func parseCEF(text string, e *Envelope) {
	m := cefRE.FindStringSubmatch(text)
	if m == nil {
		e.Data["_unknown"] = map[string]interface{}{"raw": text}
		return
	}
	e.NLI.Parser = "cef.generic"
	e.NLI.Confidence = 0.97
	e.Meta.Matches = 1
	e.Data["cef_version"] = m[1]
	e.Data["device_vendor"] = m[2]
	e.Data["device_product"] = m[3]
	e.Data["device_version"] = m[4]
	e.Data["signature_id"] = m[5]
	e.Data["name"] = m[6]
	e.Data["severity"] = m[7]
	// Parse extension kv pairs
	if ext := parseExtensionKV(m[8]); len(ext) > 0 {
		for k, v := range ext {
			e.Data[k] = v
		}
		e.Meta.Extracted = 7 + len(ext)
	} else {
		e.Data["extension"] = m[8]
		e.Meta.Extracted = 8
	}
	e.Event.Category = "security"
}

func parseExtensionKV(s string) map[string]interface{} {
	// CEF extension: key=value key2=value2 (value ends at next key=)
	result := make(map[string]interface{})
	reKey := regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)=`)
	locs := reKey.FindAllStringIndex(s, -1)
	for i, loc := range locs {
		key := s[loc[0] : loc[1]-1]
		start := loc[1]
		end := len(s)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		result[key] = strings.TrimSpace(s[start:end])
	}
	return result
}

// parseSMTPNative handles the existing SMTP native format (preserved from v0.1.0).
var (
	smtpSecretKey   = regexp.MustCompile(`(?i)password|secret|token|api_?key|credential`)
	smtpSecretValue = regexp.MustCompile(`(?i)((?:password|secret|token|api_?key|credential)\s*[:=]\s*)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s:,;]+)`)
	smtpEmailRE     = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	smtpValidKey    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.\-]*$`)
	smtpNextKey     = regexp.MustCompile(`^\s*[A-Za-z_][A-Za-z0-9_.\-]*\s*[:=]`)
	dmarcFields     = map[string]string{"id": "string", "adkim": "string", "aspf": "string", "p": "string", "pct": "integer", "fo": "string", "rua": "email_list", "ruf": "email_list", "sp": "string"}
)

func parseSMTPNative(text string, e *Envelope, deps *Deps) {
	m := smtpHeader.FindStringSubmatch(text)
	if m == nil {
		parseKV(text, e)
		return
	}
	e.Source.Hostname = m[4]
	e.Source.Service = m[5]
	payload := text[len(m[0]):]

	e.Event = Event{Name: m[7], Code: m[8], Level: strings.ToLower(m[6]), Category: "application"}
	if level, ok := map[string]string{"Note": "info", "Warning": "warn", "Error": "error"}[m[6]]; ok {
		e.Event.Level = level
	}

	t, err := time.Parse("20060102 150405.000-0700", m[1]+" "+m[2][:6]+"."+m[2][6:]+m[3])
	if err != nil {
		e.Meta.Errors = append(e.Meta.Errors, "invalid native timestamp")
	} else {
		e.Timestamp = t.UTC().Format(time.RFC3339Nano)
		e.Meta.Timezone = m[3]
	}

	isDMARC := m[5] == "smtp" && m[7] == "AntiAbuseDmarcAggregateReportRecord" && strings.HasPrefix(m[8], "2004/")
	values, kvErr := tokenizeKV(payload)

	if kvErr != nil || len(values) == 0 {
		e.Data["_unknown"] = map[string]interface{}{"raw": text}
		e.NLI.Parser = "smtp.native.unclassified"
		e.NLI.Confidence = 0.75
		return
	}

	if isDMARC {
		e.NLI.Parser = "smtp.dmarc.aggregate"
		e.NLI.ParserVersion = 4
		e.NLI.Confidence = 0.9987
		e.NLI.TemplateID = "tpl-smtp-dmarc"
		e.Event.Category = "authentication"
		e.Meta.Format = "smtp_native"
		unknown := map[string]interface{}{}
		for key, value := range values {
			kind, known := dmarcFields[key]
			if !known {
				unknown[key] = value
				continue
			}
			str := fmt.Sprint(value)
			typed := value.(string)
			switch kind {
			case "integer":
				n, parseErr := strconv.ParseInt(str, 10, 64)
				if parseErr != nil || n < 0 || n > 100 {
					e.Meta.Errors = append(e.Meta.Errors, key+": expected integer 0..100")
				} else {
					e.Data[key] = n
					e.Meta.Extracted++
					continue
				}
			case "email_list":
				list := []interface{}{}
				valid := true
				for _, addr := range strings.Split(str, ",") {
					addr = strings.TrimPrefix(strings.TrimSpace(addr), "mailto:")
					if !smtpEmailRE.MatchString(addr) {
						valid = false
					}
					list = append(list, addr)
				}
				if valid {
					e.Data[key] = list
				} else {
					e.Meta.Errors = append(e.Meta.Errors, key+": invalid email list")
					e.Data[key] = typed
				}
				e.Meta.Extracted++
				continue
			}
			if key == "adkim" || key == "aspf" {
				lower := strings.ToLower(str)
				if lower != "r" && lower != "s" {
					e.Meta.Errors = append(e.Meta.Errors, key+": expected r or s")
				}
				e.Data[key] = lower
			} else if key == "p" || key == "sp" {
				lower := strings.ToLower(str)
				if lower != "none" && lower != "quarantine" && lower != "reject" {
					e.Meta.Errors = append(e.Meta.Errors, key+": invalid policy")
				}
				e.Data[key] = lower
			} else {
				e.Data[key] = strings.TrimSpace(str)
			}
			e.Meta.Extracted++
		}
		if id, ok := values["id"]; !ok || strings.TrimSpace(fmt.Sprint(id)) == "" {
			e.Meta.Errors = append(e.Meta.Errors, "id: required field missing")
		}
		if len(unknown) > 0 {
			e.Data["_unknown"] = unknown
		}
		e.Meta.Unknown = len(unknown)
	} else {
		e.NLI.Parser = "smtp.native.mixed_kv"
		e.NLI.ParserVersion = 5
		e.NLI.Confidence = 0.97
		e.Meta.Format = "smtp_native"
		for key, value := range values {
			e.Data[key] = value
		}
		e.Meta.Extracted = len(values)
		e.Meta.Unknown = 0
	}
}

func parseKV(text string, e *Envelope) {
	values, err := tokenizeKV(text)
	if err != nil || len(values) < 1 {
		e.Data["_unknown"] = map[string]interface{}{"raw": text}
		e.NLI.Parser = "unknown"
		e.NLI.Confidence = 0
		e.Meta.Unknown = 1
		return
	}
	e.NLI.Parser = "generic.kv"
	e.NLI.Confidence = 0.96
	e.Meta.Matches = 1
	for key, value := range values {
		e.Data[key] = value
		e.Meta.Extracted++
	}
}

func parseKVFallback(text string, e *Envelope) bool {
	values, err := tokenizeKV(text)
	if err != nil || len(values) < 2 {
		return false
	}
	e.NLI.Parser = "generic.kv"
	e.NLI.Confidence = 0.90
	e.Meta.Matches = 1
	e.Meta.Format = detector.FormatKVPairs
	for key, value := range values {
		e.Data[key] = value
		e.Meta.Extracted++
	}
	return true
}

// tokenizeKV uses the compiled mixed-delimiter stage shared by generic and
// SMTP-native parsing. The stage preserves strings unless a parser policy
// explicitly declares a value type.
func tokenizeKV(s string) (map[string]interface{}, error) {
	return parseMixedKVFields(s)
}

// inferTypes walks a data map and coerces string values to their inferred types.
func inferTypes(data map[string]interface{}) {
	for k, v := range data {
		switch val := v.(type) {
		case string:
			data[k] = typeinfer.Coerce(val)
		case map[string]interface{}:
			inferTypes(val)
		}
	}
}
