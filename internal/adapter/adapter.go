package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/engine"
	"nexora.local/nli-v20/internal/familyprofile"
	parserpolicy "nexora.local/nli-v20/parser-policy"
	"nexora.local/nli-v20/telemetry"
)

const maxProfileExecutions = 8

var (
	nativeFamilyHeader = regexp.MustCompile(`^\s*\d{8}\s+\d{9}[+-]\d{4}\s+(\S+)\s+(\S+)\s+`)
	webtopFamilyHeader = regexp.MustCompile(`^\s*\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}:\d{3}[+-]\d{4}\s+`)
)

type ParserPackAdapter struct {
	profiles          []familyprofile.Profile
	smtpCompatibility familyprofile.Profile
	policyHash        string
}

// NewParserPackAdapter creates the default runtime from the embedded,
// product-reviewed family profiles.
func NewParserPackAdapter() (*ParserPackAdapter, error) {
	return NewParserPackAdapterFromDirectory("")
}

// NewParserPackAdapterFromDirectory merges the embedded profiles with one
// bounded controller-managed active generation. The directory may be absent.
func NewParserPackAdapterFromDirectory(directory string) (*ParserPackAdapter, error) {
	profiles, err := familyprofile.Resolve(directory)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, &configurationError{message: "no approved parser profiles"}
	}
	compatibilityPack := parserpolicy.DefaultSMTPParserPack()
	compatibilityExecutor, err := parserpolicy.NewParserPackExecutor(compatibilityPack)
	if err != nil {
		return nil, err
	}
	compatibility := familyprofile.Profile{
		Pack: compatibilityPack, Executor: compatibilityExecutor,
		Order: 90, Name: "embedded-smtp-golden-compatibility", Managed: false,
	}
	policyProfiles := append(append([]familyprofile.Profile(nil), profiles...), compatibility)
	return &ParserPackAdapter{
		profiles: profiles, smtpCompatibility: compatibility,
		policyHash: aggregatePolicyHash(policyProfiles),
	}, nil
}

type configurationError struct{ message string }

func (err *configurationError) Error() string { return err.message }

type V5ParseResult struct {
	EventPayload   *telemetry.EventPayload
	ParseTelemetry *telemetry.ParseTelemetry
	Success        bool
	Quarantined    bool
}

type profileCandidate struct {
	profile     familyprofile.Profile
	specificity int
}

type profileExecution struct {
	candidate profileCandidate
	result    *parserpolicy.ExecutionResult
	err       error
}

func (adapter *ParserPackAdapter) TryParseV5Production(input string, source engine.Source) *V5ParseResult {
	started := time.Now()
	trimmed := strings.TrimSpace(input)
	service := runtimeService(source.Service, trimmed)
	if service == "" {
		return &V5ParseResult{Success: false}
	}
	candidates := adapter.candidates(service)
	if service == "smtp" || service == "smtp-in" || service == "smtp-out" {
		candidates = append([]profileCandidate{{profile: adapter.smtpCompatibility, specificity: 1}}, candidates...)
	}
	if len(candidates) == 0 {
		return &V5ParseResult{Success: false}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	limitExceeded := len(candidates) > maxProfileExecutions
	if len(candidates) > maxProfileExecutions {
		candidates = candidates[:maxProfileExecutions]
	}

	// Candidate packs are immutable after activation. Execute the small, bounded
	// candidate set concurrently, but store results by original index so the
	// telemetry attempt order and tie-breaking remain identical to serial runs.
	executions := make([]profileExecution, len(candidates))
	execute := func(index int, candidate profileCandidate) {
		executionService := service
		if candidate.profile.Pack.ID == adapter.smtpCompatibility.Pack.ID {
			executionService = "smtp"
		}
		result, err := candidate.profile.Executor.Execute(&parserpolicy.ExecutionRequest{
			Input: trimmed, NodeID: source.Hostname, Service: executionService, SourceType: "file", Context: ctx,
			SourceMetadata: map[string]string{
				"hostname": source.Hostname,
				"service":  service,
				"file":     source.File,
			},
		})
		executions[index] = profileExecution{candidate: candidate, result: result, err: err}
	}
	if len(candidates) == 1 {
		execute(0, candidates[0])
	} else {
		var group sync.WaitGroup
		for index, candidate := range candidates {
			group.Add(1)
			go func(index int, candidate profileCandidate) {
				defer group.Done()
				execute(index, candidate)
			}(index, candidate)
		}
		group.Wait()
	}
	selectedIndex := -1
	for index := range executions {
		if successfulExecution(executions[index].result, executions[index].err) && (selectedIndex < 0 || betterExecution(executions[index], executions[selectedIndex])) {
			selectedIndex = index
		}
	}

	if limitExceeded {
		selectedIndex = -1
	}
	metadata := adapter.aggregateTelemetry(trimmed, source.Hostname, executions, selectedIndex, started)
	if limitExceeded {
		metadata.ErrorCodes = appendUnique(metadata.ErrorCodes, "pack_attempt_limit")
	}
	if selectedIndex < 0 {
		metadata.Route = "quarantine"
		metadata.Result = "quarantined"
		metadata.Confidence = 0
		metadata.ErrorCodes = appendUnique(metadata.ErrorCodes, "all_packs_failed")
		return &V5ParseResult{ParseTelemetry: metadata, Success: false, Quarantined: true}
	}

	selected := executions[selectedIndex].result
	if selected.EventPayload == nil || selected.EventPayload.FieldCount() == 0 {
		metadata.Route = "quarantine"
		metadata.Result = "quarantined"
		metadata.Confidence = 0
		metadata.ErrorCodes = appendUnique(metadata.ErrorCodes, "empty_event_payload")
		return &V5ParseResult{ParseTelemetry: metadata, Success: false, Quarantined: true}
	}
	return &V5ParseResult{EventPayload: selected.EventPayload, ParseTelemetry: metadata, Success: true}
}

func (adapter *ParserPackAdapter) candidates(service string) []profileCandidate {
	candidates := make([]profileCandidate, 0, len(adapter.profiles))
	bestSpecificity := 3
	for _, profile := range adapter.profiles {
		specificity, eligible := profileSpecificity(profile.Pack, service)
		if !eligible {
			continue
		}
		if specificity < bestSpecificity {
			bestSpecificity = specificity
			candidates = candidates[:0]
		}
		if specificity == bestSpecificity {
			candidates = append(candidates, profileCandidate{profile: profile, specificity: specificity})
		}
	}
	return candidates
}

func profileSpecificity(pack *parserpolicy.ParserPack, service string) (int, bool) {
	best := 3
	for _, rule := range pack.Rules {
		for _, selector := range rule.ServiceSelectors {
			normalized := canonicalService(selector)
			switch {
			case normalized == service:
				best = 0
			case (service == "smtp-in" || service == "smtp-out") && normalized == "smtp" && best > 1:
				best = 1
			case selector == "*" && best > 2:
				best = 2
			}
		}
	}
	return best, best < 3
}

func successfulExecution(result *parserpolicy.ExecutionResult, err error) bool {
	return err == nil && result != nil && !result.Quarantined && result.EventPayload != nil && result.EventPayload.FieldCount() > 0
}

func betterExecution(candidate, current profileExecution) bool {
	candidateTelemetry := candidate.result.ParseTelemetry
	currentTelemetry := current.result.ParseTelemetry
	if candidateTelemetry.SourceFieldCount != currentTelemetry.SourceFieldCount {
		return candidateTelemetry.SourceFieldCount > currentTelemetry.SourceFieldCount
	}
	if candidateTelemetry.OutputFieldCount != currentTelemetry.OutputFieldCount {
		return candidateTelemetry.OutputFieldCount > currentTelemetry.OutputFieldCount
	}
	if candidate.candidate.specificity != current.candidate.specificity {
		return candidate.candidate.specificity < current.candidate.specificity
	}
	if candidate.candidate.profile.Order != current.candidate.profile.Order {
		return candidate.candidate.profile.Order < current.candidate.profile.Order
	}
	if candidate.candidate.profile.Pack.Revision != current.candidate.profile.Pack.Revision {
		return candidate.candidate.profile.Pack.Revision > current.candidate.profile.Pack.Revision
	}
	return candidate.candidate.profile.Pack.ID < current.candidate.profile.Pack.ID
}

func (adapter *ParserPackAdapter) aggregateTelemetry(input, nodeID string, executions []profileExecution, selectedIndex int, started time.Time) *telemetry.ParseTelemetry {
	var metadata *telemetry.ParseTelemetry
	if selectedIndex >= 0 && executions[selectedIndex].result != nil && executions[selectedIndex].result.ParseTelemetry != nil {
		copyOfSelected := *executions[selectedIndex].result.ParseTelemetry
		metadata = &copyOfSelected
	} else {
		metadata = telemetry.NewParseTelemetry(input, nodeID)
		metadata.PackID = "runtime_family_router"
		metadata.ParserID = "runtime_family_router"
		metadata.ParserVersion = parserpolicy.CurrentSchemaVersion
		metadata.PolicyHash = adapter.policyHash
	}

	metadata.OrderedAttempts = make([]telemetry.ParserAttempt, 0, familyprofile.MaxAttemptsPerPack*len(executions))
	metadata.ErrorCodes = make([]string, 0)
	metadata.AttemptCount = 0
	metadata.StageCount = 0
	metadata.FallbackDepth = 0
	attemptsBeforeSelected := 0
	for index, execution := range executions {
		attempts := executionAttempts(execution)
		if index < selectedIndex {
			attemptsBeforeSelected += len(attempts)
		}
		metadata.OrderedAttempts = append(metadata.OrderedAttempts, attempts...)
		metadata.AttemptCount += len(attempts)
		if execution.result != nil && execution.result.ParseTelemetry != nil {
			metadata.StageCount += execution.result.ParseTelemetry.StageCount
			for _, code := range execution.result.ParseTelemetry.ErrorCodes {
				metadata.ErrorCodes = appendUnique(metadata.ErrorCodes, code)
			}
		}
		if execution.err != nil {
			metadata.ErrorCodes = appendUnique(metadata.ErrorCodes, "parser_execution_failed")
		}
	}
	if selectedIndex >= 0 {
		metadata.FallbackDepth = attemptsBeforeSelected
	}
	metadata.LatencyMs = elapsedMillis(started)
	return metadata
}

func executionAttempts(execution profileExecution) []telemetry.ParserAttempt {
	if execution.result != nil && execution.result.ParseTelemetry != nil && len(execution.result.ParseTelemetry.OrderedAttempts) > 0 {
		return append([]telemetry.ParserAttempt(nil), execution.result.ParseTelemetry.OrderedAttempts...)
	}
	reason := "no_eligible_rule"
	outcome := telemetry.AttemptOutcomeRejected
	if execution.err != nil {
		reason = "parser_execution_failed"
		outcome = telemetry.AttemptOutcomeFailed
	} else if execution.result != nil && execution.result.ParseTelemetry != nil && len(execution.result.ParseTelemetry.ErrorCodes) > 0 {
		reason = execution.result.ParseTelemetry.ErrorCodes[len(execution.result.ParseTelemetry.ErrorCodes)-1]
	}
	return []telemetry.ParserAttempt{{
		ParserID: execution.candidate.profile.Pack.ID,
		Priority: execution.candidate.profile.Order,
		Outcome:  outcome, FailureReason: reason,
	}}
}

func runtimeService(configured, input string) string {
	configured = canonicalService(configured)
	inferred := inferService(input)
	switch configured {
	case "", "auto", "generic", "nli":
		return inferred
	case "smtp":
		if inferred == "smtp-in" || inferred == "smtp-out" {
			return inferred
		}
	}
	return configured
}

func inferService(input string) string {
	if webtopFamilyHeader.MatchString(input) {
		return "webtop"
	}
	match := nativeFamilyHeader.FindStringSubmatch(input)
	if len(match) != 3 {
		return ""
	}
	hostname := strings.ToLower(match[1])
	service := canonicalService(match[2])
	if service != "smtp" {
		return service
	}
	lowerInput := strings.ToLower(input)
	switch {
	case strings.Contains(lowerInput, "policy=inbound"), strings.Contains(hostname, "rgi"):
		return "smtp-in"
	case strings.Contains(lowerInput, "policy=outbound"), strings.Contains(hostname, "rgo"):
		return "smtp-out"
	default:
		return "smtp"
	}
}

func canonicalService(service string) string {
	switch strings.ToLower(strings.TrimSpace(service)) {
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
		return strings.ToLower(strings.TrimSpace(service))
	}
}

func aggregatePolicyHash(profiles []familyprofile.Profile) string {
	var material strings.Builder
	for _, profile := range profiles {
		material.WriteString(profile.Pack.ID)
		material.WriteByte(0)
		material.WriteString(profile.Pack.SHA256)
		material.WriteByte('\n')
	}
	digest := sha256.Sum256([]byte(material.String()))
	return hex.EncodeToString(digest[:])
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func elapsedMillis(started time.Time) int64 {
	elapsed := time.Since(started)
	if elapsed <= 0 {
		return 0
	}
	milliseconds := elapsed.Milliseconds()
	if milliseconds == 0 {
		return 1
	}
	return milliseconds
}

func (adapter *ParserPackAdapter) TryParseV5(input string, source engine.Source) *engine.V5ParseResult {
	result := adapter.TryParseV5Production(input, source)
	metadata := map[string]interface{}{}
	parserID := ""
	confidence, coverage, latency := 0.0, 0.0, int64(0)
	attemptCount, fallbackDepth := 0, 0
	trace := []map[string]interface{}{}
	fieldEvidence := []map[string]interface{}{}
	if result.ParseTelemetry != nil {
		telemetryResult := result.ParseTelemetry
		metadata["event_id"] = telemetryResult.EventID
		metadata["parser_id"] = telemetryResult.ParserID
		metadata["confidence"] = telemetryResult.Confidence
		metadata["evidence_coverage"] = telemetryResult.EvidenceCoverage
		metadata["route"] = telemetryResult.Route
		metadata["attempt_count"] = telemetryResult.AttemptCount
		metadata["fallback_depth"] = telemetryResult.FallbackDepth
		metadata["stage_count"] = telemetryResult.StageCount
		parserID = telemetryResult.ParserID
		confidence = telemetryResult.Confidence
		coverage = telemetryResult.EvidenceCoverage
		latency = telemetryResult.LatencyMs
		attemptCount = telemetryResult.AttemptCount
		fallbackDepth = telemetryResult.FallbackDepth
		for attemptIndex, attempt := range telemetryResult.OrderedAttempts {
			trace = append(trace, map[string]interface{}{
				"stage": "parser_attempt", "attempt": attemptIndex + 1,
				"parser": attempt.ParserID, "priority": attempt.Priority,
				"outcome": string(attempt.Outcome), "success": attempt.Outcome == telemetry.AttemptOutcomeSuccess,
				"failure_reason": attempt.FailureReason, "latency_ms": attempt.LatencyMs,
				"fields": attempt.FieldsEmitted, "selected": attempt.ParserID == telemetryResult.ParserID,
			})
			for stageIndex, stage := range attempt.StageResults {
				trace = append(trace, map[string]interface{}{
					"stage": "stage_execution", "attempt": attemptIndex + 1, "step": stageIndex + 1,
					"parser": attempt.ParserID, "stage_id": stage.StageID, "stage_name": stage.StageName,
					"stage_type": stage.StageType, "outcome": string(stage.Outcome),
					"success": stage.Outcome == telemetry.StageOutcomeSuccess,
					"fields":  stage.FieldsAdded, "fields_modified": stage.FieldsModified,
					"latency_ms": stage.LatencyMs, "failure_reason": stage.ErrorCode,
				})
			}
		}
		for _, evidence := range telemetryResult.FieldEvidence {
			fieldEvidence = append(fieldEvidence, map[string]interface{}{
				"field": evidence.Field, "source_backed": evidence.Source,
				"start": evidence.Start, "end": evidence.End, "derived_rule": evidence.DerivedRule,
			})
		}
	}
	payload := map[string]interface{}{}
	if result.EventPayload != nil {
		payload = result.EventPayload.Fields
	}
	return &engine.V5ParseResult{
		EventPayload: payload, ParseTelemetry: metadata,
		Success: result.Success, Quarantined: result.Quarantined,
		ParserID: parserID, Confidence: confidence, EvidenceCoverage: coverage, LatencyMs: latency,
		AttemptCount: attemptCount, FallbackDepth: fallbackDepth, Trace: trace, FieldEvidence: fieldEvidence,
	}
}
