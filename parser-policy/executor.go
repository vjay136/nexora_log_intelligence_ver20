package parserpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"nexora.local/nli-v20/projection"
	"nexora.local/nli-v20/telemetry"
)

type ParserPackExecutor struct {
	pack       *ParserPack
	compiled   bool
	validators map[string]*regexp.Regexp
}

type ExecutionRequest struct {
	Input          string
	NodeID         string
	Service        string
	SourceType     string
	Context        context.Context
	SourceMetadata map[string]string
}

type ExecutionResult struct {
	EventPayload   *telemetry.EventPayload
	ParseTelemetry *telemetry.ParseTelemetry
	ParserID       string
	RuleID         string
	Priority       int
	Quarantined    bool
}

type capturedField struct {
	Value       interface{}
	Source      bool
	Start       int
	End         int
	DerivedRule string
}

type workingSet map[string]capturedField

type ruleStats struct {
	InputFields  int
	SourceFields int
	Derived      int
	Coverage     float64
	Evidence     []telemetry.FieldEvidence
}

type executionError struct {
	code       string
	message    string
	quarantine bool
}

func (e *executionError) Error() string { return e.message }

func fail(code, message string) error {
	return &executionError{code: code, message: message}
}

func quarantineFailure(code, message string) error {
	return &executionError{code: code, message: message, quarantine: true}
}

func errorCode(err error) string {
	if typed, ok := err.(*executionError); ok && typed.code != "" {
		return typed.code
	}
	return "parser_execution_failed"
}

func shouldQuarantine(err error) bool {
	typed, ok := err.(*executionError)
	return ok && typed.quarantine
}

func NewParserPackExecutor(pack *ParserPack) (*ParserPackExecutor, error) {
	if pack == nil {
		return nil, fmt.Errorf("parser pack cannot be nil")
	}
	validation := ValidateParserPack(pack)
	if !validation.Valid {
		return nil, fmt.Errorf("invalid parser pack: %s", strings.Join(validation.Errors, "; "))
	}
	executor := &ParserPackExecutor{pack: pack, validators: map[string]*regexp.Regexp{}}
	if err := executor.compile(); err != nil {
		return nil, fmt.Errorf("compile parser pack: %w", err)
	}
	if pack.ReviewState == ReviewStateActivated {
		if err := executor.runRequiredRegressions(); err != nil {
			return nil, fmt.Errorf("activation regression failed: %w", err)
		}
	}
	return executor, nil
}

func (e *ParserPackExecutor) compile() error {
	for ruleIndex, rule := range e.pack.Rules {
		for stageIndex, stage := range rule.Stages {
			if stage.ResourceLimits != (ResourceLimits{}) && !limitsWithin(stage.ResourceLimits, e.pack.ResourceLimits) {
				return fmt.Errorf("rule[%d].stage[%d]: stage resource limits exceed pack limits", ruleIndex, stageIndex)
			}
		}
		for predicateIndex, predicate := range rule.IncludePredicates {
			if predicate.Type != "regex" {
				continue
			}
			compiled, err := e.compileRegex(predicate.Pattern)
			if err != nil {
				return fmt.Errorf("rule[%d].include_predicates[%d]: %w", ruleIndex, predicateIndex, err)
			}
			e.validators[predicateKey(rule.ID, true, predicateIndex)] = compiled
		}
		for predicateIndex, predicate := range rule.ExcludePredicates {
			if predicate.Type != "regex" {
				continue
			}
			compiled, err := e.compileRegex(predicate.Pattern)
			if err != nil {
				return fmt.Errorf("rule[%d].exclude_predicates[%d]: %w", ruleIndex, predicateIndex, err)
			}
			e.validators[predicateKey(rule.ID, false, predicateIndex)] = compiled
		}
	}
	e.compiled = true
	return nil
}

func (e *ParserPackExecutor) compileRegex(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > e.pack.ResourceLimits.MaxRegexLength {
		return nil, fmt.Errorf("regex exceeds configured maximum")
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid RE2 expression: %w", err)
	}
	return compiled, nil
}

func predicateKey(ruleID string, include bool, index int) string {
	kind := "exclude"
	if include {
		kind = "include"
	}
	return fmt.Sprintf("%s/%s/%d", ruleID, kind, index)
}

func (e *ParserPackExecutor) Execute(request *ExecutionRequest) (*ExecutionResult, error) {
	started := time.Now()
	if !e.compiled {
		return nil, fmt.Errorf("executor is not compiled")
	}
	if request == nil {
		return nil, fmt.Errorf("execution request cannot be nil")
	}
	if len(request.Input) > e.pack.ResourceLimits.MaxInputBytes {
		return nil, fmt.Errorf("input too large: %d > %d bytes", len(request.Input), e.pack.ResourceLimits.MaxInputBytes)
	}

	metadata := telemetry.NewParseTelemetry(request.Input, request.NodeID)
	metadata.PackID = e.pack.ID
	metadata.ParserID = e.pack.ID
	metadata.ParserVersion = e.pack.Version
	metadata.PolicyHash = e.pack.SHA256
	if metadata.PolicyHash == "" {
		metadata.PolicyHash, _ = CanonicalParserPackHash(e.pack)
	}

	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(e.pack.ResourceLimits.ExecutionDeadlineMs)*time.Millisecond)
	defer cancel()

	rules := SortRulesByPriority(e.selectRules(request))
	if len(rules) == 0 {
		return e.quarantinedResult(metadata, started, "no_eligible_rule"), nil
	}
	attemptLimit := e.pack.ResourceLimits.MaxAttempts
	if attemptLimit > len(rules) {
		attemptLimit = len(rules)
	}

	for index := 0; index < attemptLimit; index++ {
		select {
		case <-ctx.Done():
			metadata.ErrorCodes = appendUnique(metadata.ErrorCodes, "execution_deadline")
			return e.quarantinedResult(metadata, started, "execution_deadline"), nil
		default:
		}

		rule := rules[index]
		payload, attempt, stats, err := e.executeRule(ctx, request, rule)
		metadata.OrderedAttempts = append(metadata.OrderedAttempts, attempt)
		metadata.AttemptCount++
		metadata.StageCount += len(attempt.StageResults)
		for _, stageResult := range attempt.StageResults {
			if stageResult.ErrorCode != "" {
				metadata.ErrorCodes = appendUnique(metadata.ErrorCodes, stageResult.ErrorCode)
			}
		}
		metadata.FallbackDepth = metadata.AttemptCount - 1
		if err != nil {
			metadata.InputFieldCount = stats.InputFields
			metadata.SourceFieldCount = stats.SourceFields
			metadata.DerivedFieldCount = stats.Derived
			metadata.EvidenceCoverage = stats.Coverage
			metadata.FieldEvidence = stats.Evidence
			code := errorCode(err)
			metadata.ErrorCodes = appendUnique(metadata.ErrorCodes, code)
			if rule.FailurePolicy == FailurePolicyQuarantine || shouldQuarantine(err) {
				return e.quarantinedResult(metadata, started, code), nil
			}
			continue
		}

		identity := e.pack.ID + "/" + rule.ID
		metadata.ParserID = identity
		metadata.RuleID = rule.ID
		metadata.ParserPriority = rule.Priority
		metadata.InputFieldCount = stats.InputFields
		metadata.OutputFieldCount = payload.FieldCount()
		metadata.SourceFieldCount = stats.SourceFields
		metadata.DerivedFieldCount = stats.Derived
		metadata.EvidenceCoverage = stats.Coverage
		metadata.FieldEvidence = stats.Evidence
		metadata.Confidence = 1
		metadata.Route = "normal"
		metadata.Result = "success"
		metadata.LatencyMs = elapsedMillis(started)
		return &ExecutionResult{
			EventPayload: payload, ParseTelemetry: metadata,
			ParserID: identity, RuleID: rule.ID, Priority: rule.Priority,
		}, nil
	}
	return e.quarantinedResult(metadata, started, "all_rules_failed"), nil
}

func (e *ParserPackExecutor) quarantinedResult(metadata *telemetry.ParseTelemetry, started time.Time, code string) *ExecutionResult {
	metadata.Route = "quarantine"
	metadata.Result = "quarantined"
	metadata.Confidence = 0
	metadata.LatencyMs = elapsedMillis(started)
	metadata.ErrorCodes = appendUnique(metadata.ErrorCodes, code)
	return &ExecutionResult{ParseTelemetry: metadata, ParserID: metadata.ParserID, Quarantined: true}
}

func (e *ParserPackExecutor) executeRule(ctx context.Context, request *ExecutionRequest, rule ParserRule) (*telemetry.EventPayload, telemetry.ParserAttempt, ruleStats, error) {
	started := time.Now()
	attempt := telemetry.ParserAttempt{
		ParserID: e.pack.ID + "/" + rule.ID, Priority: rule.Priority,
		Outcome: telemetry.AttemptOutcomeFailed, StageResults: []telemetry.StageResult{},
	}
	fields := workingSet{}
	if len(rule.Stages) > e.pack.ResourceLimits.MaxStageCount {
		err := fail("stage_limit", "rule exceeds configured stage limit")
		attempt.FailureReason = errorCode(err)
		attempt.LatencyMs = elapsedMillis(started)
		return nil, attempt, snapshotRuleStats(fields), err
	}

	for _, stage := range rule.Stages {
		select {
		case <-ctx.Done():
			err := fail("execution_deadline", "stage execution deadline exceeded")
			attempt.FailureReason = errorCode(err)
			attempt.LatencyMs = elapsedMillis(started)
			return nil, attempt, snapshotRuleStats(fields), err
		default:
		}
		stageResult, err := e.executeStage(ctx, request, rule, stage, fields)
		if err != nil {
			stageResult.ErrorCode = errorCode(err)
			if stage.FailurePolicy == FailurePolicyContinue {
				stageResult.Outcome = telemetry.StageOutcomeContinued
				attempt.StageResults = append(attempt.StageResults, stageResult)
				continue
			}
			if stage.FailurePolicy == FailurePolicyQuarantine {
				err = quarantineFailure(errorCode(err), "stage requested quarantine")
				stageResult.Outcome = telemetry.StageOutcomeFailed
			}
			attempt.StageResults = append(attempt.StageResults, stageResult)
			attempt.FailureReason = errorCode(err)
			attempt.LatencyMs = elapsedMillis(started)
			return nil, attempt, snapshotRuleStats(fields), err
		}
		attempt.StageResults = append(attempt.StageResults, stageResult)
	}

	if err := enforceSchema(rule, fields); err != nil {
		attempt.FailureReason = errorCode(err)
		attempt.LatencyMs = elapsedMillis(started)
		return nil, attempt, snapshotRuleStats(fields), err
	}

	metadata := make([]projection.FieldMetadata, 0, len(fields))
	inputSourceCount := 0
	for name, field := range fields {
		if strings.HasPrefix(name, "_") {
			continue
		}
		if isForbiddenOutputField(name) {
			typed := fail("forbidden_output_field", "rule emitted a reserved output field")
			attempt.FailureReason = errorCode(typed)
			attempt.LatencyMs = elapsedMillis(started)
			return nil, attempt, snapshotRuleStats(fields), typed
		}
		if field.Source {
			inputSourceCount++
		}
		metadata = append(metadata, projection.FieldMetadata{
			Name: name, Value: field.Value, IsSource: field.Source,
			IsDerived: !field.Source, Evidence: evidenceLabel(field),
		})
	}
	if len(metadata) == 0 {
		err := fail("no_fields", "rule emitted no customer fields")
		attempt.FailureReason = errorCode(err)
		attempt.LatencyMs = elapsedMillis(started)
		return nil, attempt, snapshotRuleStats(fields), err
	}
	sort.Slice(metadata, func(i, j int) bool { return metadata[i].Name < metadata[j].Name })
	projector, err := e.createProjector(rule)
	if err != nil {
		typed := fail("projection_config", "invalid projection configuration")
		attempt.FailureReason = errorCode(typed)
		attempt.LatencyMs = elapsedMillis(started)
		return nil, attempt, snapshotRuleStats(fields), typed
	}
	projected, err := projector.Project(metadata, inputSourceCount)
	if err != nil || len(projected.Errors) > 0 {
		typed := fail("field_count_policy", "field projection or count policy failed")
		attempt.FailureReason = errorCode(typed)
		attempt.LatencyMs = elapsedMillis(started)
		return nil, attempt, snapshotRuleStats(fields), typed
	}
	if len(projected.Fields) > e.pack.ResourceLimits.MaxFields {
		typed := fail("field_limit", "rule exceeded configured field limit")
		attempt.FailureReason = errorCode(typed)
		attempt.LatencyMs = elapsedMillis(started)
		return nil, attempt, snapshotRuleStats(fields), typed
	}

	payload := &telemetry.EventPayload{Fields: projected.Fields}
	stats := buildRuleStats(projected.Fields, fields, inputSourceCount)
	if stats.SourceFields > 0 && stats.Coverage < 1 {
		typed := fail("evidence_incomplete", "one or more source fields lack evidence")
		attempt.FailureReason = errorCode(typed)
		attempt.LatencyMs = elapsedMillis(started)
		return nil, attempt, snapshotRuleStats(fields), typed
	}
	attempt.Outcome = telemetry.AttemptOutcomeSuccess
	attempt.FieldsEmitted = payload.FieldCount()
	attempt.LatencyMs = elapsedMillis(started)
	return payload, attempt, stats, nil
}

func snapshotRuleStats(fields workingSet) ruleStats {
	output := make(map[string]interface{})
	inputCount := 0
	for name, field := range fields {
		if strings.HasPrefix(name, "_") {
			continue
		}
		output[name] = field.Value
		if field.Source {
			inputCount++
		}
	}
	if len(output) == 0 {
		return ruleStats{Evidence: []telemetry.FieldEvidence{}}
	}
	return buildRuleStats(output, fields, inputCount)
}

func buildRuleStats(output map[string]interface{}, fields workingSet, inputCount int) ruleStats {
	stats := ruleStats{InputFields: inputCount, Evidence: []telemetry.FieldEvidence{}}
	evidenced := 0
	names := make([]string, 0, len(output))
	for name := range output {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		field := fields[name]
		record := telemetry.FieldEvidence{Field: name, Source: field.Source, Start: field.Start, End: field.End, DerivedRule: field.DerivedRule}
		stats.Evidence = append(stats.Evidence, record)
		if field.Source {
			stats.SourceFields++
			if field.End > field.Start && field.Start >= 0 {
				evidenced++
			}
		} else {
			stats.Derived++
		}
	}
	if stats.SourceFields == 0 {
		stats.Coverage = 1
	} else {
		stats.Coverage = float64(evidenced) / float64(stats.SourceFields)
	}
	return stats
}

func evidenceLabel(field capturedField) string {
	if !field.Source {
		return "derived:" + field.DerivedRule
	}
	return fmt.Sprintf("bytes:%d-%d", field.Start, field.End)
}

func (e *ParserPackExecutor) selectRules(request *ExecutionRequest) []ParserRule {
	selected := make([]ParserRule, 0, len(e.pack.Rules))
	for _, rule := range e.pack.Rules {
		if len(rule.ServiceSelectors) > 0 {
			matched := false
			for _, selector := range rule.ServiceSelectors {
				if selector == "*" || selector == request.Service {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		eligible := true
		for index, predicate := range rule.IncludePredicates {
			if !e.evaluatePredicate(predicate, request, predicateKey(rule.ID, true, index)) {
				eligible = false
				break
			}
		}
		if !eligible {
			continue
		}
		for index, predicate := range rule.ExcludePredicates {
			if e.evaluatePredicate(predicate, request, predicateKey(rule.ID, false, index)) {
				eligible = false
				break
			}
		}
		if eligible {
			selected = append(selected, rule)
		}
	}
	return selected
}

func (e *ParserPackExecutor) evaluatePredicate(predicate Predicate, request *ExecutionRequest, key string) bool {
	var value string
	switch predicate.Field {
	case "service":
		value = request.Service
	case "source_type":
		value = request.SourceType
	case "input":
		value = request.Input
	default:
		value = request.SourceMetadata[predicate.Field]
	}
	matched := false
	switch predicate.Type {
	case "exact":
		matched = value == predicate.Pattern
	case "contains":
		matched = strings.Contains(value, predicate.Pattern)
	case "glob":
		matched, _ = path.Match(predicate.Pattern, value)
	case "regex":
		matched = e.validators[key] != nil && e.validators[key].MatchString(value)
	}
	if predicate.Negate {
		return !matched
	}
	return matched
}

func (e *ParserPackExecutor) createProjector(rule ParserRule) (*projection.FieldProjector, error) {
	return projection.NewFieldProjector(
		projection.ProjectionMode(rule.ProjectionMode), rule.AllowedFields,
		rule.DeniedFields, projection.CountPolicy(rule.CountPolicy),
	)
}

func (e *ParserPackExecutor) runRequiredRegressions() error {
	for _, testCase := range e.pack.RegressionCases {
		result, err := e.Execute(&ExecutionRequest{
			Input: testCase.Input, Service: testCase.Service,
			SourceType: testCase.SourceType, NodeID: "regression",
			Context: context.Background(),
		})
		if err != nil {
			return fmt.Errorf("%s: execution error: %w", testCase.Name, err)
		}
		if result.Quarantined || result.EventPayload == nil {
			return fmt.Errorf("%s: result was quarantined", testCase.Name)
		}
		if result.ParserID != testCase.ExpectedParser {
			return fmt.Errorf("%s: parser mismatch: got %s", testCase.Name, result.ParserID)
		}
		if testCase.ExpectedRoute != "" && result.ParseTelemetry.Route != testCase.ExpectedRoute {
			return fmt.Errorf("%s: route mismatch: got %s", testCase.Name, result.ParseTelemetry.Route)
		}
		actual, _ := json.Marshal(result.EventPayload.Fields)
		expected, _ := json.Marshal(testCase.ExpectedPayload)
		if !bytes.Equal(actual, expected) {
			return fmt.Errorf("%s: payload mismatch", testCase.Name)
		}
		if expectedTelemetry := testCase.ExpectedTelemetry; expectedTelemetry != nil {
			if result.ParseTelemetry.FallbackDepth != expectedTelemetry.FallbackDepth ||
				result.ParseTelemetry.SourceFieldCount != expectedTelemetry.SourceFields ||
				result.ParseTelemetry.OutputFieldCount != expectedTelemetry.OutputFields {
				return fmt.Errorf("%s: telemetry count mismatch", testCase.Name)
			}
			if len(expectedTelemetry.ParserAttempts) > 0 {
				if len(expectedTelemetry.ParserAttempts) != len(result.ParseTelemetry.OrderedAttempts) {
					return fmt.Errorf("%s: attempt count mismatch", testCase.Name)
				}
				for index, parserID := range expectedTelemetry.ParserAttempts {
					if result.ParseTelemetry.OrderedAttempts[index].ParserID != parserID {
						return fmt.Errorf("%s: attempt identity mismatch", testCase.Name)
					}
				}
			}
		}
	}
	return nil
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
	// Successful parser work must remain observable even on hosts whose clock
	// resolution is coarser than the operation. A zero duration makes completed
	// attempts indistinguishable from missing telemetry in the controller UI.
	if elapsed <= 0 || elapsed.Milliseconds() == 0 {
		return 1
	}
	return elapsed.Milliseconds()
}

func (e *ParserPackExecutor) limitsFor(stage Stage) ResourceLimits {
	if stage.ResourceLimits == (ResourceLimits{}) {
		return e.pack.ResourceLimits
	}
	return stage.ResourceLimits
}

func limitsWithin(child, parent ResourceLimits) bool {
	return child.MaxInputBytes <= parent.MaxInputBytes &&
		child.MaxFields <= parent.MaxFields &&
		child.MaxCaptures <= parent.MaxCaptures &&
		child.MaxRegexLength <= parent.MaxRegexLength &&
		child.MaxStageCount <= parent.MaxStageCount &&
		child.MaxAttempts <= parent.MaxAttempts &&
		child.ExecutionDeadlineMs <= parent.ExecutionDeadlineMs
}
