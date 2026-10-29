package controlplane

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/auditlog"
	"nexora.local/nli-v20/internal/engine"
	"nexora.local/nli-v20/internal/middleware"
	metastore "nexora.local/nli-v20/internal/store"
)

type assuranceVault struct {
	mu           sync.Mutex
	enabled      bool
	manual       bool
	aead         cipher.AEAD
	providerAEAD cipher.AEAD
	store        metastore.Store
	audit        *auditlog.ChainLog
	max          int
	ttl          time.Duration
}
type sealedAssuranceCase struct {
	Name                     string         `json:"name"`
	Description              string         `json:"description,omitempty"`
	Version                  int            `json:"version"`
	ParserID                 string         `json:"parser_id"`
	Passed                   bool           `json:"passed"`
	CreatedBy                string         `json:"created_by"`
	CreatedAt                time.Time      `json:"created_at"`
	ExpiresAt                time.Time      `json:"expires_at"`
	Nonce                    string         `json:"nonce"`
	Ciphertext               string         `json:"ciphertext"`
	CiphertextSHA256         string         `json:"ciphertext_sha256,omitempty"`
	CiphertextObjectPath     string         `json:"ciphertext_object_path,omitempty"`
	CiphertextStorageBackend string         `json:"ciphertext_storage_backend,omitempty"`
	OriginalFilename         string         `json:"original_filename,omitempty"`
	MediaType                string         `json:"media_type,omitempty"`
	ArchiveType              string         `json:"archive_type,omitempty"`
	CompressedBytes          int64          `json:"compressed_bytes,omitempty"`
	ExpandedBytes            int64          `json:"expanded_bytes,omitempty"`
	SHA256                   string         `json:"sha256,omitempty"`
	Service                  string         `json:"service,omitempty"`
	ParseSummary             map[string]any `json:"parse_summary,omitempty"`
}
type assuranceSecret struct {
	Raw          string            `json:"raw"`
	Desired      map[string]any    `json:"desired"`
	Actual       map[string]any    `json:"actual"`
	Service      string            `json:"service"`
	Source       string            `json:"source"`
	FieldMapping map[string]string `json:"field_mapping,omitempty"`
}
type assuranceRequest struct {
	Raw          string            `json:"raw"`
	Desired      map[string]any    `json:"desired,omitempty"`
	Service      string            `json:"service"`
	Source       string            `json:"source,omitempty"`
	ParserID     string            `json:"parser_id,omitempty"`
	FieldMapping map[string]string `json:"field_mapping,omitempty"`
	Template     map[string]any    `json:"template,omitempty"`
}

func (s *Server) storeAssuranceCiphertext(ctx context.Context, ciphertext []byte) (string, string, error) {
	if s.metadata.Mode() != "supabase" {
		return base64.StdEncoding.EncodeToString(ciphertext), "", nil
	}
	if s.artifactStorage == nil {
		return "", "", errors.New("private encrypted Assurance Storage is unavailable")
	}
	digest := sha256.Sum256(ciphertext)
	digestText := hex.EncodeToString(digest[:])
	objectPath := "sha256/" + digestText + ".enc"
	if err := s.artifactStorage.PutVerified(ctx, "nli-assurance-fixtures", objectPath, bytes.NewReader(ciphertext), int64(len(ciphertext)), "application/octet-stream", digestText); err != nil {
		return "", "", err
	}
	return "", objectPath, nil
}

func newAssuranceVault(cfg Config, store metastore.Store, audit *auditlog.ChainLog) (*assuranceVault, error) {
	vault := &assuranceVault{enabled: cfg.AssuranceRetentionEnabled, manual: cfg.AssuranceRetentionManual, store: store, audit: audit, max: cfg.AssuranceRetentionMax, ttl: time.Duration(cfg.AssuranceRetentionHours) * time.Hour}
	key := cfg.AssuranceKey
	if len(key) == 0 {
		key, _ = decodeKey(os.Getenv(cfg.AssuranceKeyEnv))
	}
	if len(key) != 32 {
		if vault.enabled {
			return nil, fmt.Errorf("%s must provide exactly 32 bytes when Assurance retention is enabled", cfg.AssuranceKeyEnv)
		}
		return vault, nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	vault.providerAEAD, err = cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if vault.enabled {
		vault.aead = vault.providerAEAD
	}
	return vault, nil
}

func (s *Server) handleAssuranceRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var request assuranceRequest
	if err := decodeJSON(r, &request, 2<<20); err != nil {
		jsonError(w, "raw input is required", http.StatusBadRequest)
		return
	}
	lineEndings := assuranceLineEndingCounts(request.Raw)
	request.Raw = normalizeAssuranceRaw(request.Raw)
	if request.Raw == "" {
		jsonError(w, "raw input is required", http.StatusBadRequest)
		return
	}
	result := s.runAssurance(request)
	result["line_endings"] = lineEndings
	actor := "anonymous"
	if principal := middleware.GetSecurityContext(r); principal != nil {
		actor = principal.Username
	}
	s.recordAssuranceHistory(result, actor)
	jsonOK(w, result)
}

// handleParserCreationHelper returns the complete deterministic helper payload
// without requiring clients to unpack the larger Assurance run response.
func (s *Server) handleParserCreationHelper(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var request assuranceRequest
	if err := decodeJSON(r, &request, 2<<20); err != nil || strings.TrimSpace(request.Raw) == "" {
		jsonError(w, "raw input is required", http.StatusBadRequest)
		return
	}
	request.Raw = normalizeAssuranceRaw(request.Raw)
	result := s.runAssurance(request)
	helper, _ := result["parser_creation_helper"].(map[string]any)
	jsonOK(w, map[string]any{"raw": result["raw"], "raw_sha256": result["raw_sha256"], "parsed_json": result["actual"], "family_decision": helper["family_decision"], "parser_creation_helper": helper, "validation": helper["validation_accounting"], "categorized_results": helper["categorized_results"], "requires_human_approval": true})
}

func (s *Server) runAssurance(request assuranceRequest) map[string]any {
	request.Raw = normalizeAssuranceRaw(request.Raw)
	if request.Service == "" {
		request.Service = "auto"
	}
	start := time.Now()
	source := engine.Source{Hostname: "assurance-lab", Service: request.Service, File: request.Source}
	envelope, selectedParserError := engine.ParseWithSelectedParser(request.Raw, source, s.assuranceDeps, request.ParserID)
	if selectedParserError != nil {
		result := map[string]any{
			"raw": request.Raw, "raw_sha256": hex.EncodeToString(hashBytes([]byte(request.Raw))), "desired": request.Desired,
			"actual": map[string]any{}, "diff": []map[string]any{}, "matches": 0, "passed": false,
			"comparison_enabled": len(request.Desired) > 0, "counts": map[string]int{"desired": len(request.Desired), "actual": 0, "matches": 0, "differences": 0, "attempts": 0, "fallbacks": 0},
			"evidence_spans": []map[string]any{}, "trace": []map[string]any{}, "parser": map[string]any{"name": request.ParserID, "version": 0, "confidence": 0, "strategy": "selected"},
			"requested_parser": request.ParserID, "requested_family": request.Service, "route": "quarantine", "errors": []string{selectedParserError.Error()}, "latency_us": time.Since(start).Microseconds(), "retained": false, "score": 0,
			"coverage":               map[string]any{"expected": len(request.Desired), "found": 0, "percent": 0, "missing": sortedMapKeys(request.Desired)},
			"parser_creation_helper": buildParserCreationHelper(request.Raw, request.Service, engine.Envelope{}, map[string]any{}),
		}
		enrichParserCreationHelper(result)
		return result
	}
	actual := envelope.Data
	if actual == nil {
		actual = map[string]any{}
	}
	differences := []map[string]any{}
	matches := 0
	comparisonEnabled := len(request.Desired) > 0
	if comparisonEnabled {
		differences, matches = structuralDiff(actual, request.Desired)
	}
	evidence := envelope.Meta.FieldEvidence
	if len(evidence) == 0 {
		evidence = evidenceSpans(request.Raw, actual)
	}
	trace := []map[string]any{{"stage": "format_detection", "outcome": envelope.Meta.Format, "success": envelope.Meta.Format != "unknown"}}
	trace = append(trace, envelope.Meta.ParserTrace...)
	if len(envelope.Meta.ParserTrace) == 0 {
		for index, candidate := range envelope.Meta.Assurance.Candidates {
			trace = append(trace, map[string]any{"stage": "parser_attempt", "attempt": index + 1, "strategy": candidate.Strategy, "parser": candidate.Parser, "confidence": candidate.Confidence, "evidence_coverage": candidate.EvidenceCoverage, "fields": candidate.FieldsExtracted, "validation_errors": candidate.ValidationErrors, "selected": candidate.Parser == envelope.NLI.Parser, "success": candidate.Parser == envelope.NLI.Parser})
		}
	}
	trace = append(trace, map[string]any{"stage": "routing", "outcome": envelope.Meta.Route, "success": envelope.Meta.Route == "normal"})
	attempts := envelope.Meta.ParserAttempts
	if attempts == 0 {
		attempts = len(envelope.Meta.Assurance.Candidates)
	}
	fallbacks := envelope.Meta.FallbackDepth
	if envelope.Meta.ParserAttempts == 0 {
		fallbacks = maxInt(0, attempts-1)
	}
	passed := envelope.Meta.Route == "normal" && len(actual) > 0 && (!comparisonEnabled || len(differences) == 0)
	expected := map[string]any{}
	for key, value := range request.Desired {
		expected[key] = value
	}
	for key, value := range request.Template {
		if _, exists := expected[key]; !exists {
			expected[key] = value
		}
	}
	found, missing := 0, []string{}
	for key := range expected {
		if _, ok := actual[key]; ok {
			found++
		} else {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	coverage := 100.0
	if len(expected) > 0 {
		coverage = float64(found) * 100 / float64(len(expected))
	}
	confidence := envelope.NLI.Confidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	score := int((confidence*0.45+coverage/100*0.45+boolFloat(passed)*0.10)*100 + 0.5)
	var candidate any
	if envelope.Meta.Route != "normal" || confidence < 0.65 || len(missing) > 0 {
		candidate = map[string]any{"name": "candidate-" + hex.EncodeToString(hashBytes([]byte(request.Raw))[:4]), "source_parser": envelope.NLI.Parser, "detected_format": envelope.Meta.Format, "fields": sortedMapKeys(actual), "sample_values": actual, "suggested_strategy": "key_value_or_format_specific", "review_state": "draft", "requires_human_approval": true}
	}
	result := map[string]any{"raw": request.Raw, "raw_sha256": hex.EncodeToString(hashBytes([]byte(request.Raw))), "desired": request.Desired, "actual": actual, "diff": differences, "matches": matches, "passed": passed, "comparison_enabled": comparisonEnabled, "counts": map[string]int{"desired": len(request.Desired), "actual": len(actual), "matches": matches, "differences": len(differences), "attempts": attempts, "fallbacks": fallbacks}, "evidence_spans": evidence, "trace": trace, "parser": map[string]any{"name": envelope.NLI.Parser, "version": envelope.NLI.ParserVersion, "confidence": envelope.NLI.Confidence, "strategy": envelope.Meta.Assurance.SelectedStrategy}, "requested_parser": request.ParserID, "requested_family": request.Service, "route": envelope.Meta.Route, "errors": envelope.Meta.Errors, "latency_us": time.Since(start).Microseconds(), "retained": false, "score": score, "coverage": map[string]any{"expected": len(expected), "found": found, "percent": coverage, "missing": missing}, "parser_creation_helper": buildParserCreationHelper(request.Raw, request.Service, envelope, actual)}
	if candidate != nil {
		result["candidate_parser"] = candidate
	}
	enrichParserCreationHelper(result)
	return result
}

// normalizeAssuranceRaw keeps multiline boundaries intact while removing CRLF
// variance, trailing horizontal whitespace on every line, and empty trailing
// lines. The normalized sample is the one hashed, analysed, and retained.
func normalizeAssuranceRaw(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// assuranceLineEndingCounts reports the submitted representation before
// normalization, so mixed LF/CRLF samples remain diagnosable in the lab.
func assuranceLineEndingCounts(raw string) map[string]int {
	counts := map[string]int{"crlf": 0, "lf": 0, "cr": 0}
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '\r':
			if i+1 < len(raw) && raw[i+1] == '\n' {
				counts["crlf"]++
				i++
			} else {
				counts["cr"]++
			}
		case '\n':
			counts["lf"]++
		}
	}
	return counts
}

func buildParserCreationHelper(raw, requestedFamily string, envelope engine.Envelope, actual map[string]any) map[string]any {
	fieldNames := sortedMapKeys(actual)
	fields := make([]map[string]any, 0, len(fieldNames))
	for _, name := range fieldNames {
		fields = append(fields, map[string]any{
			"name":     name,
			"type":     jsonType(actual[name]),
			"source":   "detected_field:" + name,
			"required": false,
		})
	}

	delimiters := make([]string, 0, 4)
	for _, candidate := range []string{"=", ":", ";", "|"} {
		if strings.Contains(raw, candidate) {
			delimiters = append(delimiters, candidate)
		}
	}

	matchField, matchValue := "", ""
	for _, name := range []string{"event", "method", "service", "event_type"} {
		if value := strings.TrimSpace(fmt.Sprint(actual[name])); value != "" && value != "<nil>" {
			matchField, matchValue = name, value
			break
		}
	}
	suggestedRegex := ""
	if matchValue != "" {
		quoted := regexp.QuoteMeta(matchValue)
		if matchField == "event" {
			suggestedRegex = `(?:^|[;[:space:]])` + quoted + `(?:\(|[;:=[:space:]]|$)`
		} else {
			suggestedRegex = `(?:^|[[:space:]])` + regexp.QuoteMeta(matchField) + `=["']?` + quoted + `["']?(?:[[:space:]]|$)`
		}
	}

	lines := 0
	if raw != "" {
		lines = strings.Count(raw, "\n") + 1
	}
	regexFields := make([]map[string]any, 0, len(fields))
	regexParts := make([]string, 0, len(fields))
	for _, field := range fields {
		name := fmt.Sprint(field["name"])
		for _, separator := range []string{"=", ":"} {
			needle := name + separator
			if strings.Contains(raw, needle) {
				groupName := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
				if groupName.MatchString(name) {
					regexParts = append(regexParts, regexp.QuoteMeta(needle)+`(?P<`+name+`>[^[:space:]]+)`)
					regexFields = append(regexFields, map[string]any{"name": name, "source": "group:" + name, "type": field["type"], "required": false})
				}
				break
			}
		}
	}
	keyValueRegex := strings.Join(regexParts, `[[:space:]]+`)
	if keyValueRegex != "" {
		keyValueRegex = `(?:^|[[:space:]])` + keyValueRegex + `(?:[[:space:]]|$)`
	}
	family := strings.TrimSpace(requestedFamily)
	if family == "" {
		family = "auto"
	}
	return map[string]any{
		"normalized_raw_input": raw,
		"normalized_sample":    raw,
		"sample_lines":         lines,
		"parsed_json":          actual,
		"detected_format":      envelope.Meta.Format,
		"requested_family":     requestedFamily,
		"family_decision":      map[string]any{"requested": family, "selected_parser": envelope.NLI.Parser, "detected_format": envelope.Meta.Format, "decision": envelope.Meta.Assurance.SelectedStrategy},
		"selected_parser":      envelope.NLI.Parser,
		"selected_strategy":    envelope.Meta.Assurance.SelectedStrategy,
		"match_scope":          map[string]any{"field": matchField, "value": matchValue, "exact": matchValue != ""},
		"suggested_regex": func() string {
			if keyValueRegex != "" {
				return keyValueRegex
			}
			return suggestedRegex
		}(),
		"regex_fields":  regexFields,
		"kv_delimiters": delimiters,
		"fields":        fields,
		"projection":    fieldNames,
		"template":      map[string]any{"pattern_type": "review_required", "format": envelope.Meta.Format, "field_count": len(fieldNames), "fields": fields},
		"field_mapping": func() map[string]any {
			mapping := map[string]any{}
			for _, field := range fields {
				mapping[fmt.Sprint(field["name"])] = field["source"]
			}
			return mapping
		}(),
		"review_state":            "draft",
		"requires_human_approval": true,
	}
}

func enrichParserCreationHelper(result map[string]any) {
	helper, _ := result["parser_creation_helper"].(map[string]any)
	if helper == nil {
		return
	}
	actual, _ := result["actual"].(map[string]any)
	fields, _ := helper["fields"].([]map[string]any)
	if fields == nil {
		if values, ok := helper["fields"].([]any); ok {
			for _, value := range values {
				if field, ok := value.(map[string]any); ok {
					fields = append(fields, field)
				}
			}
		}
	}
	regexPattern := fmt.Sprint(helper["suggested_regex"])
	regexArtifact := map[string]any{"pattern": regexPattern, "fields": helper["regex_fields"]}
	templateArtifact := helper["template"]
	kvArtifact := map[string]any{"delimiters": helper["kv_delimiters"], "fields": fields, "format": helper["detected_format"]}
	mappingArtifact := helper["field_mapping"]
	projectionArtifact := map[string]any{"fields": helper["projection"]}
	parserArtifact := map[string]any{"name": "Assurance generated parser", "parser": map[string]any{"pattern_type": "regex", "pattern": regexPattern, "fields": helper["regex_fields"]}}
	artifacts := []map[string]any{
		{"category": "parser", "artifact": parserArtifact},
		{"category": "regex", "artifact": regexArtifact},
		{"category": "template", "artifact": templateArtifact},
		{"category": "kv", "artifact": kvArtifact},
		{"category": "field_mapping", "artifact": mappingArtifact},
		{"category": "projection", "artifact": projectionArtifact},
	}
	for _, item := range artifacts {
		encoded, err := json.MarshalIndent(item["artifact"], "", "  ")
		if err == nil {
			item["copy_json"] = string(encoded)
		}
	}
	counts, _ := result["counts"].(map[string]int)
	if counts == nil {
		counts = map[string]int{}
		if rawCounts, ok := result["counts"].(map[string]any); ok {
			for key, value := range rawCounts {
				if number, ok := value.(int); ok {
					counts[key] = number
				}
			}
		}
	}
	helper["validation_accounting"] = map[string]any{"passed": result["passed"], "score": result["score"], "counts": counts, "coverage": result["coverage"], "diff": result["diff"], "errors": result["errors"]}
	helper["categorized_results"] = artifacts
	helper["copyable_results"] = artifacts
	helper["parsed_json"] = actual
}
func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
func sortedMapKeys(value map[string]any) []string {
	out := make([]string, 0, len(value))
	for key := range value {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
func hashBytes(data []byte) []byte { sum := sha256.Sum256(data); return sum[:] }
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func jsonType(value any) string {
	if value == nil {
		return "null"
	}
	kind := reflect.TypeOf(value).Kind()
	switch kind {
	case reflect.Bool:
		return "boolean"
	case reflect.Float32, reflect.Float64, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "number"
	case reflect.Map:
		return "object"
	case reflect.Slice, reflect.Array:
		return "array"
	default:
		return "string"
	}
}
func equivalent(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
func structuralDiff(actual, desired map[string]any) ([]map[string]any, int) {
	keys := map[string]bool{}
	for key := range actual {
		keys[key] = true
	}
	for key := range desired {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	diff := []map[string]any{}
	matches := 0
	for _, key := range ordered {
		a, hasA := actual[key]
		d, hasD := desired[key]
		status := "match"
		switch {
		case !hasA:
			status = "missing"
		case !hasD:
			status = "extra"
		case jsonType(a) != jsonType(d):
			status = "type"
		case !equivalent(a, d):
			status = "value"
		default:
			matches++
			continue
		}
		diff = append(diff, map[string]any{"field": key, "status": status, "actual": a, "desired": d, "actual_type": jsonType(a), "desired_type": jsonType(d)})
	}
	return diff, matches
}
func evidenceSpans(raw string, actual map[string]any) []map[string]any {
	fields := make([]string, 0, len(actual))
	for field := range actual {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	out := make([]map[string]any, 0, len(fields))
	for _, field := range fields {
		text := fmt.Sprint(actual[field])
		start := strings.Index(raw, text)
		entry := map[string]any{"field": field, "source_backed": start >= 0}
		if start >= 0 {
			entry["start"] = start
			entry["end"] = start + len(text)
		} else {
			entry["derived_rule"] = "active parser output; source span unavailable"
		}
		out = append(out, entry)
	}
	return out
}

func (s *Server) handleAssuranceCorpus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Cases []assuranceRequest `json:"cases"`
	}
	if err := decodeJSON(r, &request, 8<<20); err != nil || len(request.Cases) < 1 || len(request.Cases) > 100 {
		jsonError(w, "corpus must contain 1..100 cases", http.StatusBadRequest)
		return
	}
	results := make([]map[string]any, 0, len(request.Cases))
	passed := 0
	for _, item := range request.Cases {
		if strings.TrimSpace(item.Raw) == "" {
			jsonError(w, "every corpus case requires raw input", http.StatusBadRequest)
			return
		}
		result := s.runAssurance(item)
		if value, _ := result["passed"].(bool); value {
			passed++
		}
		result["raw"] = ""
		results = append(results, result)
	}
	jsonOK(w, map[string]any{"total": len(results), "passed": passed, "failed": len(results) - passed, "results": results, "retained": false})
}

func (s *Server) handleAssuranceCases(w http.ResponseWriter, r *http.Request) {
	permission := middleware.PermissionReadParsers
	if r.Method != http.MethodGet {
		permission = middleware.PermissionAssuranceSave
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	if !s.assurance.enabled {
		if r.Method == http.MethodGet {
			jsonOK(w, map[string]any{"cases": []any{}, "count": 0, "encrypted": false, "retention_enabled": false, "max_cases": 0})
			return
		}
		jsonError(w, "Assurance fixture retention is disabled", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.listAssuranceCases(w, r)
	case http.MethodPost:
		s.saveAssuranceCase(w, r, principal)
	default:
		jsonError(w, "GET or POST required", http.StatusMethodNotAllowed)
	}
}
func (s *Server) listAssuranceCases(w http.ResponseWriter, r *http.Request) {
	s.assurance.mu.Lock()
	defer s.assurance.mu.Unlock()
	documents, err := s.activeAssuranceCases(r.Context())
	if err != nil {
		jsonError(w, "fixture store unavailable", http.StatusServiceUnavailable)
		return
	}
	out := []map[string]any{}
	for _, doc := range documents {
		var sealed sealedAssuranceCase
		if json.Unmarshal(doc.Payload, &sealed) != nil {
			continue
		}
		out = append(out, map[string]any{"id": doc.ID, "revision": doc.Revision, "name": sealed.Name, "description": sealed.Description, "version": sealed.Version, "parser_id": sealed.ParserID, "passed": sealed.Passed, "created_by": sealed.CreatedBy, "created_at": sealed.CreatedAt, "expires_at": sealed.ExpiresAt, "manual_retention": s.assurance.manual})
	}
	jsonOK(w, map[string]any{"cases": out, "count": len(out), "encrypted": true, "manual_retention": s.assurance.manual, "max_cases": s.assurance.max})
}

// Page fixtures so large encrypted samples cannot exceed the RPC response
// bound. Drop ciphertext from summaries and reclaim expired slots before saves.
// Callers hold assurance.mu to keep capacity checks and writes atomic.
func (s *Server) activeAssuranceCases(ctx context.Context) ([]metastore.Document, error) {
	var active []metastore.Document
	for offset := 0; ; {
		page, err := s.metadata.List(ctx, "assurance-cases", metastore.Query{Limit: 10, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, doc := range page {
			var sealed sealedAssuranceCase
			if err := json.Unmarshal(doc.Payload, &sealed); err != nil {
				return nil, fmt.Errorf("invalid retained fixture %s: %w", doc.ID, err)
			}
			if !s.assurance.manual && !sealed.ExpiresAt.After(time.Now().UTC()) {
				if err := s.deleteAssuranceObject(ctx, doc.Payload); err != nil {
					return nil, err
				}
				if err := s.metadata.Delete(ctx, "assurance-cases", doc.ID, true, "retention-policy"); err != nil {
					return nil, err
				}
				continue
			}
			sealed.Nonce, sealed.Ciphertext = "", ""
			doc.Payload, _ = json.Marshal(sealed)
			active = append(active, doc)
			offset++
		}
		if len(page) < 10 {
			return active, nil
		}
	}
}
func safeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			b.WriteRune(char)
		} else if char == ' ' || char == '.' {
			b.WriteByte('-')
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) > 80 {
		result = result[:80]
	}
	return result
}
func (s *Server) saveAssuranceCase(w http.ResponseWriter, r *http.Request, principal *middleware.SecurityContext) {
	var request struct {
		ID          string           `json:"id,omitempty"`
		Name        string           `json:"name"`
		Description string           `json:"description,omitempty"`
		Retain      bool             `json:"retain"`
		Case        assuranceRequest `json:"case"`
	}
	if err := decodeJSON(r, &request, 3<<20); err != nil || !request.Retain || request.Name == "" || strings.TrimSpace(request.Case.Raw) == "" {
		jsonError(w, "name, retain=true and case.raw are required", http.StatusBadRequest)
		return
	}
	s.assurance.mu.Lock()
	defer s.assurance.mu.Unlock()
	id := safeID(request.ID)
	if request.ID != "" && id != request.ID {
		jsonError(w, "case id must use lowercase letters, digits, hyphens or underscores", http.StatusBadRequest)
		return
	}
	documents, err := s.activeAssuranceCases(r.Context())
	if err != nil {
		jsonError(w, "fixture store unavailable", http.StatusServiceUnavailable)
		return
	}
	version := 1
	var previousPayload []byte
	for _, existing := range documents {
		if existing.ID == id {
			version = existing.Revision + 1
			previousPayload = append([]byte(nil), existing.Payload...)
			break
		}
	}
	if version == 1 && len(documents) >= s.assurance.max {
		jsonError(w, "Assurance fixture retention limit reached", http.StatusConflict)
		return
	}
	result := s.runAssurance(request.Case)
	actual, _ := result["actual"].(map[string]any)
	secret := assuranceSecret{Raw: request.Case.Raw, Desired: request.Case.Desired, Actual: actual, Service: request.Case.Service, Source: request.Case.Source, FieldMapping: request.Case.FieldMapping}
	plaintext, _ := json.Marshal(secret)
	if id == "" {
		prefix := safeID(request.Name)
		if prefix == "" {
			prefix = "case"
		}
		id = prefix + "-" + fmt.Sprintf("%x", hashBytes([]byte(fmt.Sprintf("%s-%d", request.Name, time.Now().UnixNano())))[:8])
	}
	nonce := make([]byte, s.assurance.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		jsonError(w, "encryption failed", http.StatusInternalServerError)
		return
	}
	ciphertext := s.assurance.aead.Seal(nil, nonce, plaintext, []byte(id))
	parser := result["parser"].(map[string]any)
	expiresAt := time.Time{}
	if !s.assurance.manual {
		expiresAt = time.Now().UTC().Add(s.assurance.ttl)
	}
	storedCiphertext, objectPath, err := s.storeAssuranceCiphertext(r.Context(), ciphertext)
	if err != nil {
		jsonError(w, "encrypted fixture storage failed", http.StatusServiceUnavailable)
		return
	}
	sealed := sealedAssuranceCase{Name: request.Name, Description: request.Description, Version: version, ParserID: fmt.Sprint(parser["name"]), Passed: result["passed"].(bool), CreatedBy: principal.Username, CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt, Nonce: base64.StdEncoding.EncodeToString(nonce), Ciphertext: storedCiphertext, CiphertextObjectPath: objectPath}
	if objectPath != "" {
		digest := sha256.Sum256(ciphertext)
		sealed.CiphertextSHA256 = hex.EncodeToString(digest[:])
		sealed.CiphertextStorageBackend = "supabase-private-storage"
	}
	payload, _ := json.Marshal(sealed)
	saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "assurance-cases", ID: id, State: "retained", Payload: payload, CreatedBy: principal.Username})
	if err != nil {
		if objectPath != "" {
			_ = s.artifactStorage.Delete(r.Context(), "nli-assurance-fixtures", objectPath)
		}
		jsonError(w, "fixture persistence failed", http.StatusInternalServerError)
		return
	}
	if len(previousPayload) > 0 {
		_ = s.deleteAssuranceObject(r.Context(), previousPayload)
	}
	_, _ = s.audit.Append(auditlog.Event{Action: "assurance.save", Actor: principal.Username, Role: principal.Role, Resource: "assurance-cases/" + id, Outcome: "success", Details: map[string]string{"revision": fmt.Sprint(saved.Revision)}})
	jsonCreated(w, map[string]any{"id": id, "revision": saved.Revision, "encrypted": true, "manual_retention": s.assurance.manual, "expires_at": sealed.ExpiresAt})
}

func (s *Server) handleAssuranceCase(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/assurance/cases/"), "/")
	parts := strings.Split(path, "/")
	id := parts[0]
	if id == "" {
		jsonError(w, "case id required", http.StatusBadRequest)
		return
	}
	permission := middleware.PermissionReadParsers
	if r.Method != http.MethodGet {
		permission = middleware.PermissionAssuranceSave
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	if !s.assurance.enabled {
		jsonError(w, "Assurance fixture retention is disabled", http.StatusServiceUnavailable)
		return
	}
	if len(parts) > 1 && parts[1] == "download" {
		if r.Method != http.MethodGet {
			jsonError(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		_, secret, err := s.decryptAssurance(r, id)
		if err != nil {
			jsonError(w, "case not found", http.StatusNotFound)
			return
		}
		filename := safeID(id)
		if filename == "" {
			filename = "assurance-case"
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.log\"", filename))
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, secret.Raw)
		if !strings.HasSuffix(secret.Raw, "\n") {
			_, _ = io.WriteString(w, "\n")
		}
		return
	}
	if len(parts) > 1 && parts[1] == "promote" {
		s.promoteAssuranceCase(w, r, id, principal)
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc, secret, err := s.decryptAssurance(r, id)
		if err != nil {
			jsonError(w, "case not found", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]any{"id": id, "revision": doc.Revision, "fixture": secret, "explicit_retention": true})
	case http.MethodDelete:
		s.assurance.mu.Lock()
		defer s.assurance.mu.Unlock()
		doc, getErr := s.metadata.Get(r.Context(), "assurance-cases", id, 0)
		if getErr != nil {
			jsonError(w, "case not found", http.StatusNotFound)
			return
		}
		if err := s.deleteAssuranceObject(r.Context(), doc.Payload); err != nil {
			jsonError(w, "encrypted fixture object purge failed", http.StatusServiceUnavailable)
			return
		}
		if err := s.metadata.Delete(r.Context(), "assurance-cases", id, true, principal.Username); err != nil {
			jsonError(w, "purge failed", http.StatusInternalServerError)
			return
		}
		_, _ = s.audit.Append(auditlog.Event{Action: "assurance.purge", Actor: principal.Username, Role: principal.Role, Resource: "assurance-cases/" + id, Outcome: "success"})
		jsonOK(w, map[string]any{"status": "purged", "id": id})
	default:
		jsonError(w, "GET or DELETE required", http.StatusMethodNotAllowed)
	}
}
func (s *Server) decryptAssurance(r *http.Request, id string) (metastore.Document, assuranceSecret, error) {
	return s.decryptAssuranceContext(r.Context(), id)
}

func (s *Server) decryptAssuranceContext(ctx context.Context, id string) (metastore.Document, assuranceSecret, error) {
	s.assurance.mu.Lock()
	defer s.assurance.mu.Unlock()
	doc, err := s.metadata.Get(ctx, "assurance-cases", id, 0)
	if err != nil {
		return doc, assuranceSecret{}, err
	}
	var sealed sealedAssuranceCase
	if err = json.Unmarshal(doc.Payload, &sealed); err != nil {
		return doc, assuranceSecret{}, err
	}
	if !s.assurance.manual && !sealed.ExpiresAt.After(time.Now().UTC()) {
		if err := s.deleteAssuranceObject(ctx, doc.Payload); err != nil {
			return doc, assuranceSecret{}, err
		}
		err := s.metadata.Delete(ctx, "assurance-cases", id, true, "retention-policy")
		if err != nil {
			return doc, assuranceSecret{}, err
		}
		return doc, assuranceSecret{}, os.ErrNotExist
	}
	nonce, err := base64.StdEncoding.DecodeString(sealed.Nonce)
	if err != nil {
		return doc, assuranceSecret{}, err
	}
	if len(nonce) != s.assurance.aead.NonceSize() {
		return doc, assuranceSecret{}, fmt.Errorf("invalid fixture encryption nonce")
	}
	var ciphertext []byte
	if sealed.CiphertextObjectPath != "" {
		if sealed.CiphertextStorageBackend != "supabase-private-storage" || s.artifactStorage == nil {
			return doc, assuranceSecret{}, fmt.Errorf("encrypted fixture storage is unavailable")
		}
		reader, size, openErr := s.artifactStorage.Open(ctx, "nli-assurance-fixtures", sealed.CiphertextObjectPath)
		if openErr != nil {
			return doc, assuranceSecret{}, openErr
		}
		ciphertext, err = io.ReadAll(io.LimitReader(reader, (1<<20)+(64<<10)+1))
		closeErr := reader.Close()
		if err != nil {
			return doc, assuranceSecret{}, err
		}
		if closeErr != nil {
			return doc, assuranceSecret{}, closeErr
		}
		if len(ciphertext) > (1<<20)+(64<<10) || size > int64((1<<20)+(64<<10)) {
			return doc, assuranceSecret{}, fmt.Errorf("encrypted fixture object exceeds bound")
		}
		digest := sha256.Sum256(ciphertext)
		if !strings.EqualFold(hex.EncodeToString(digest[:]), sealed.CiphertextSHA256) {
			return doc, assuranceSecret{}, fmt.Errorf("encrypted fixture checksum mismatch")
		}
	} else {
		ciphertext, err = base64.StdEncoding.DecodeString(sealed.Ciphertext)
		if err != nil {
			return doc, assuranceSecret{}, err
		}
	}
	plaintext, err := s.assurance.aead.Open(nil, nonce, ciphertext, []byte(id))
	if err != nil {
		return doc, assuranceSecret{}, err
	}
	var secret assuranceSecret
	err = json.Unmarshal(plaintext, &secret)
	return doc, secret, err
}

func (s *Server) deleteAssuranceObject(ctx context.Context, payload []byte) error {
	var sealed sealedAssuranceCase
	if err := json.Unmarshal(payload, &sealed); err != nil {
		return err
	}
	if sealed.CiphertextObjectPath == "" {
		return nil
	}
	if sealed.CiphertextStorageBackend != "supabase-private-storage" || s.artifactStorage == nil {
		return errors.New("encrypted fixture storage unavailable")
	}
	return s.artifactStorage.Delete(ctx, "nli-assurance-fixtures", sealed.CiphertextObjectPath)
}
func (s *Server) promoteAssuranceCase(w http.ResponseWriter, r *http.Request, id string, principal *middleware.SecurityContext) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	_, _, err := s.decryptAssurance(r, id)
	if err != nil {
		jsonError(w, "case not found", http.StatusNotFound)
		return
	}
	var request struct {
		Name    string             `json:"name"`
		Version string             `json:"version,omitempty"`
		Parser  map[string]any     `json:"parser"`
		Cases   []assuranceRequest `json:"cases"`
	}
	if err := decodeJSON(r, &request, 2<<20); err != nil || strings.TrimSpace(request.Name) == "" || request.Parser == nil || len(request.Cases) == 0 || len(request.Cases) > 100 {
		jsonError(w, "name, declarative parser and 1..100 validation cases are required", http.StatusBadRequest)
		return
	}
	if request.Version == "" {
		request.Version = "0.1.0"
	}
	if !semverPattern.MatchString(request.Version) {
		jsonError(w, "semantic parser version is invalid", http.StatusBadRequest)
		return
	}
	if err := unsafeDeclarative(request.Parser); err != nil {
		jsonError(w, "unsafe declarative parser", http.StatusBadRequest)
		return
	}
	for _, item := range request.Cases {
		if strings.TrimSpace(item.Raw) == "" {
			jsonError(w, "every validation case requires raw input", http.StatusBadRequest)
			return
		}
	}
	docID := safeID(request.Name)
	if docID == "" {
		jsonError(w, "parser draft name is invalid", http.StatusBadRequest)
		return
	}
	request.Parser["id"] = docID
	if _, ok := request.Parser["version"]; !ok {
		request.Parser["version"] = 1
	}
	validation := validateParserCandidate(request.Parser, request.Cases)
	provenance := make([]map[string]any, 0, len(request.Cases))
	for _, item := range request.Cases {
		provenance = append(provenance, map[string]any{"raw_sha256": hex.EncodeToString(hashBytes([]byte(item.Raw))), "desired": item.Desired})
	}
	// Keep raw samples out of parser-pack metadata; the retained source fixture
	// remains the only encrypted copy and can be fetched through its guarded API.
	payload, _ := json.Marshal(map[string]any{"id": docID, "name": request.Name, "version": request.Version, "parser": request.Parser, "source_case": id, "regression_cases": provenance, "validation": validation, "validation_passed": validation["failed"] == 0 && validation["total"] == len(request.Cases), "review_state": "draft", "activated": false})
	saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "parser-packs", ID: docID, State: "draft", Payload: payload, CreatedBy: principal.Username})
	if err != nil {
		jsonError(w, "draft creation failed", http.StatusInternalServerError)
		return
	}
	_, _ = s.audit.Append(auditlog.Event{Action: "parser.promote_draft", Actor: principal.Username, Role: principal.Role, Resource: "parser-packs/" + docID, Outcome: "success", Details: map[string]string{"source_case": id}})
	jsonCreated(w, map[string]any{"id": docID, "revision": saved.Revision, "review_state": "draft"})
}
