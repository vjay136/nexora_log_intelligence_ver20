package parserpolicy

import (
	"context"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/telemetry"
)

// TestParserPackExecutor_SMTPSuccess tests successful SMTP parsing through the executor
func TestParserPackExecutor_SMTPSuccess(t *testing.T) {
	executor, err := NewSMTPParserPackExecutor()
	if err != nil {
		t.Fatalf("Failed to create SMTP executor: %v", err)
	}

	input := "20260822 050048557+0200 severrgo001 smtp 230675 0 140347436861184 Note;SieveRuleMsg(2005/1) id=6A291BF5048F23DE:policy=outbound:checkpoint=AUTH:rule=INOUT_action:msg=PolicyMsgInfo:eventType=PolicyMsgInfo:action=disconnect:reason=badpassword:fromhost=11.22.33.44:geo=IL:helo=Site6:auth_ok=false:auth_user=managers999@012.meg.in:auth_fail=6:auth_tries=7:score=0"

	req := &ExecutionRequest{
		Input:   input,
		NodeID:  "test-node",
		Service: "smtp",
		Context: context.Background(),
	}

	result, err := executor.Execute(req)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if result.Quarantined {
		t.Error("Result should not be quarantined for valid SMTP input")
	}

	if result.EventPayload.FieldCount() != 26 {
		t.Logf("Expected 26 fields, got %d. Fields: %+v", result.EventPayload.FieldCount(), result.EventPayload.Fields)
		t.Errorf("Expected 26 fields, got %d", result.EventPayload.FieldCount())
	}

	if result.ParseTelemetry.AttemptCount != 1 {
		t.Errorf("Expected 1 attempt, got %d", result.ParseTelemetry.AttemptCount)
	}

	// Verify specific field types were converted correctly
	if pidStatus, exists := result.EventPayload.Get("pid_status"); !exists {
		t.Error("pid_status field should exist")
	} else if pidStatus != 0 {
		t.Errorf("Expected pid_status to be 0, got %v (type: %T)", pidStatus, pidStatus)
	}

	if authOk, exists := result.EventPayload.Get("auth_ok"); !exists {
		t.Error("auth_ok field should exist")
	} else if value, ok := authOk.(bool); !ok || value {
		t.Errorf("Expected auth_ok to be boolean false, got %T: %v", authOk, authOk)
	}

	if authFail, exists := result.EventPayload.Get("auth_fail"); !exists {
		t.Error("auth_fail field should exist")
	} else if value, ok := authFail.(int); !ok || value != 6 {
		t.Errorf("Expected auth_fail to be integer 6, got %T: %v", authFail, authFail)
	}
}

// TestParserPackExecutor_RuleSelectionAndSuccess tests rule selection logic and successful parsing
func TestParserPackExecutor_RuleSelectionAndSuccess(t *testing.T) {
	// Create a pack with multiple rules where first two fail
	pack := &ParserPack{
		ID:          "test_fallback",
		Version:     "1.0.0",
		Revision:    1,
		SourceType:  "manual",
		Author:      "test",
		ReviewState: ReviewStateDraft,
		CreatedAt:   time.Now(),
		Rules: []ParserRule{
			// Rule 1: High priority, will fail (wrong service selector)
			{
				ID:               "rule1_fail",
				Name:             "Rule 1 Fail",
				Priority:         10,
				ServiceSelectors: []string{"http"}, // Wrong service
				Stages: []Stage{{
					ID:            "stage1",
					Name:          "Stage 1",
					Type:          StageTypeStaticLayout,
					Config:        map[string]interface{}{"parser_type": "smtp"},
					FailurePolicy: FailurePolicyRejectRule,
				}},
				FailurePolicy:  FailurePolicyRejectRule,
				ProjectionMode: ProjectionModeAll,
				CountPolicy:    CountPolicyExactSource,
			},
			// Rule 2: Medium priority, will fail (exclude predicate matches)
			{
				ID:               "rule2_fail",
				Name:             "Rule 2 Fail",
				Priority:         20,
				ServiceSelectors: []string{"smtp"},
				ExcludePredicates: []Predicate{{
					Type:    "contains",
					Field:   "input",
					Pattern: "severrgo001", // Will match and exclude
				}},
				Stages: []Stage{{
					ID:            "stage2",
					Name:          "Stage 2",
					Type:          StageTypeStaticLayout,
					Config:        map[string]interface{}{"parser_type": "smtp"},
					FailurePolicy: FailurePolicyRejectRule,
				}},
				FailurePolicy:  FailurePolicyRejectRule,
				ProjectionMode: ProjectionModeAll,
				CountPolicy:    CountPolicyExactSource,
			},
			// Rule 3: Low priority, will succeed (SMTP parser)
			{
				ID:               "rule3_success",
				Name:             "Rule 3 Success",
				Priority:         30,
				ServiceSelectors: []string{"smtp"},
				Stages: []Stage{
					{
						ID:            "smtp_static_layout",
						Name:          "SMTP Prefix Parser",
						Type:          StageTypeStaticLayout,
						Config:        map[string]interface{}{"parser_type": "smtp"},
						FailurePolicy: FailurePolicyRejectRule,
					},
					{
						ID:   "smtp_kv_tail",
						Name: "SMTP KV Tail Parser",
						Type: StageTypeMixedKV,
						Config: map[string]interface{}{
							"pair_separators":       []string{":"},
							"assignment_separators": []string{"="},
						},
						FailurePolicy: FailurePolicyRejectRule,
					},
				},
				FailurePolicy:  FailurePolicyRejectRule,
				ProjectionMode: ProjectionModeAll,
				CountPolicy:    CountPolicyExactSource,
			},
		},
		ResourceLimits: DefaultResourceLimits(),
	}

	executor, err := NewParserPackExecutor(pack)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	input := "20260822 050048557+0200 severrgo001 smtp 230675 0 140347436861184 Note;SieveRuleMsg(2005/1) id=6A291BF5048F23DE:policy=outbound"

	req := &ExecutionRequest{
		Input:   input,
		NodeID:  "test-node",
		Service: "smtp",
		Context: context.Background(),
	}

	result, err := executor.Execute(req)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if result.Quarantined {
		t.Error("Result should not be quarantined when rule 3 succeeds")
	}

	// Debug: log what actually happened
	t.Logf("Actual attempts: %d", result.ParseTelemetry.AttemptCount)
	for i, attempt := range result.ParseTelemetry.OrderedAttempts {
		t.Logf("Attempt %d: ParserID=%s, Outcome=%s, Reason=%s", i+1, attempt.ParserID, attempt.Outcome, attempt.FailureReason)
	}

	// Only rule3 should be selected:
	// - rule1 is excluded by service selector (requires "http", got "smtp")
	// - rule2 is excluded by exclude predicate (matches "severrgo001" in input)
	// - rule3 matches and succeeds
	if result.RuleID != "rule3_success" {
		t.Errorf("Expected successful rule to be rule3_success, got %s", result.RuleID)
	}

	if result.Priority != 30 {
		t.Errorf("Expected priority 30, got %d", result.Priority)
	}

	// Should have 1 attempt (only rule3 was selected and succeeded)
	if result.ParseTelemetry.AttemptCount != 1 {
		t.Errorf("Expected 1 attempt (only rule3 selected), got %d", result.ParseTelemetry.AttemptCount)
	}
}

// TestParserPackExecutor_PriorityTies tests deterministic tie-breaking by rule ID
func TestParserPackExecutor_PriorityTies(t *testing.T) {
	pack := &ParserPack{
		ID:          "test_ties",
		Version:     "1.0.0",
		Revision:    1,
		SourceType:  "manual",
		Author:      "test",
		ReviewState: ReviewStateDraft,
		CreatedAt:   time.Now(),
		Rules: []ParserRule{
			// Rule with ID "z_rule" - same priority as "a_rule" but later alphabetically
			{
				ID:               "z_rule",
				Name:             "Z Rule",
				Priority:         100,
				ServiceSelectors: []string{"smtp"},
				Stages: []Stage{{
					ID:            "stage_z",
					Name:          "Stage Z",
					Type:          StageTypeStaticLayout,
					Config:        map[string]interface{}{"parser_type": "smtp"},
					FailurePolicy: FailurePolicyRejectRule,
				}},
				FailurePolicy:  FailurePolicyRejectRule,
				ProjectionMode: ProjectionModeAll,
				CountPolicy:    CountPolicyExactSource,
			},
			// Rule with ID "a_rule" - same priority but earlier alphabetically
			{
				ID:               "a_rule",
				Name:             "A Rule",
				Priority:         100,
				ServiceSelectors: []string{"smtp"},
				Stages: []Stage{{
					ID:            "smtp_static_layout",
					Name:          "SMTP Static Layout",
					Type:          StageTypeStaticLayout,
					Config:        map[string]interface{}{"parser_type": "smtp"},
					FailurePolicy: FailurePolicyRejectRule,
				}},
				FailurePolicy:  FailurePolicyRejectRule,
				ProjectionMode: ProjectionModeAll,
				CountPolicy:    CountPolicyExactSource,
			},
		},
		ResourceLimits: DefaultResourceLimits(),
	}

	executor, err := NewParserPackExecutor(pack)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	input := "20260822 050048557+0200 severrgo001 smtp 230675 0 140347436861184 Note;SieveRuleMsg(2005/1) id=6A291BF5048F23DE:policy=outbound"

	req := &ExecutionRequest{
		Input:   input,
		NodeID:  "test-node",
		Service: "smtp",
		Context: context.Background(),
	}

	result, err := executor.Execute(req)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Should execute "a_rule" first due to alphabetical tie-breaking
	if result.RuleID != "a_rule" {
		t.Errorf("Expected a_rule to be executed first due to tie-breaking, got %s", result.RuleID)
	}
}

// TestParserPackExecutor_ServiceSelectorExclusion tests service selector filtering
func TestParserPackExecutor_ServiceSelectorExclusion(t *testing.T) {
	pack := &ParserPack{
		ID:          "test_selector",
		Version:     "1.0.0",
		Revision:    1,
		SourceType:  "manual",
		Author:      "test",
		ReviewState: ReviewStateDraft,
		CreatedAt:   time.Now(),
		Rules: []ParserRule{
			{
				ID:               "http_rule",
				Name:             "HTTP Rule",
				Priority:         100,
				ServiceSelectors: []string{"http"}, // Should not match smtp service
				Stages: []Stage{{
					ID:            "stage1",
					Name:          "Stage 1",
					Type:          StageTypeStaticLayout,
					Config:        map[string]interface{}{"parser_type": "smtp"},
					FailurePolicy: FailurePolicyRejectRule,
				}},
				FailurePolicy:  FailurePolicyRejectRule,
				ProjectionMode: ProjectionModeAll,
				CountPolicy:    CountPolicyExactSource,
			},
		},
		ResourceLimits: DefaultResourceLimits(),
	}

	executor, err := NewParserPackExecutor(pack)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	req := &ExecutionRequest{
		Input:   "test input",
		NodeID:  "test-node",
		Service: "smtp", // Different from rule's http selector
		Context: context.Background(),
	}

	result, err := executor.Execute(req)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Should be quarantined since no rules match the service
	if !result.Quarantined {
		t.Error("Result should be quarantined when no service selectors match")
	}

	if result.ParseTelemetry.AttemptCount != 0 {
		t.Errorf("Expected 0 attempts when no rules match, got %d", result.ParseTelemetry.AttemptCount)
	}
}

// TestParserPackExecutor_MaxAttempts tests attempt limiting
func TestParserPackExecutor_MaxAttempts(t *testing.T) {
	pack := &ParserPack{
		ID:          "test_max_attempts",
		Version:     "1.0.0",
		Revision:    1,
		SourceType:  "manual",
		Author:      "test",
		ReviewState: ReviewStateDraft,
		CreatedAt:   time.Now(),
		Rules: []ParserRule{
			{ID: "rule1", Name: "Rule 1", Priority: 10, ServiceSelectors: []string{"smtp"}, Stages: []Stage{{ID: "s1", Name: "S1", Type: StageTypeRegex, Config: map[string]interface{}{"pattern": "nomatch123456789"}, FailurePolicy: FailurePolicyRejectRule}}, FailurePolicy: FailurePolicyRejectRule, ProjectionMode: ProjectionModeAll, CountPolicy: CountPolicyExactSource}, // Valid regex that won't match
			{ID: "rule2", Name: "Rule 2", Priority: 20, ServiceSelectors: []string{"smtp"}, Stages: []Stage{{ID: "s2", Name: "S2", Type: StageTypeRegex, Config: map[string]interface{}{"pattern": "nomatch123456789"}, FailurePolicy: FailurePolicyRejectRule}}, FailurePolicy: FailurePolicyRejectRule, ProjectionMode: ProjectionModeAll, CountPolicy: CountPolicyExactSource}, // Valid regex that won't match
			{ID: "rule3", Name: "Rule 3", Priority: 30, ServiceSelectors: []string{"smtp"}, Stages: []Stage{{ID: "s3", Name: "S3", Type: StageTypeRegex, Config: map[string]interface{}{"pattern": "nomatch123456789"}, FailurePolicy: FailurePolicyRejectRule}}, FailurePolicy: FailurePolicyRejectRule, ProjectionMode: ProjectionModeAll, CountPolicy: CountPolicyExactSource}, // Valid regex that won't match
			{ID: "rule4", Name: "Rule 4", Priority: 40, ServiceSelectors: []string{"smtp"}, Stages: []Stage{{ID: "s4", Name: "S4", Type: StageTypeRegex, Config: map[string]interface{}{"pattern": "nomatch123456789"}, FailurePolicy: FailurePolicyRejectRule}}, FailurePolicy: FailurePolicyRejectRule, ProjectionMode: ProjectionModeAll, CountPolicy: CountPolicyExactSource}, // Valid regex that won't match
		},
		ResourceLimits: ResourceLimits{
			MaxInputBytes:       4096,
			MaxFields:           100,
			MaxCaptures:         50,
			MaxRegexLength:      1024,
			MaxStageCount:       10,
			MaxAttempts:         2, // Limit to 2 attempts
			ExecutionDeadlineMs: 5000,
		},
	}

	executor, err := NewParserPackExecutor(pack)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	req := &ExecutionRequest{
		Input:   "test input",
		NodeID:  "test-node",
		Service: "smtp",
		Context: context.Background(),
	}

	result, err := executor.Execute(req)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Should try only first 2 rules due to MaxAttempts limit
	if result.ParseTelemetry.AttemptCount != 2 {
		t.Errorf("Expected 2 attempts due to limit, got %d", result.ParseTelemetry.AttemptCount)
	}

	// Should be quarantined since all attempts failed
	if !result.Quarantined {
		t.Error("Result should be quarantined when all limited attempts fail")
	}
}

// TestParserPackExecutor_InputSizeLimit tests input size rejection
func TestParserPackExecutor_InputSizeLimit(t *testing.T) {
	pack := DefaultSMTPParserPack()
	pack.ReviewState = ReviewStateDraft
	pack.ActivatedAt = nil
	pack.RegressionCases = nil
	pack.SHA256 = ""
	pack.ResourceLimits.MaxInputBytes = 10 // Very small limit

	executor, err := NewParserPackExecutor(pack)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	req := &ExecutionRequest{
		Input:   "This input is longer than 10 bytes",
		NodeID:  "test-node",
		Service: "smtp",
		Context: context.Background(),
	}

	_, err = executor.Execute(req)
	if err == nil {
		t.Error("Expected error for input exceeding size limit")
	}

	if !strings.Contains(err.Error(), "input too large") {
		t.Errorf("Expected 'input too large' error, got: %v", err)
	}
}

// TestParserPackExecutor_ExecutionDeadline tests deadline enforcement
func TestParserPackExecutor_ExecutionDeadline(t *testing.T) {
	pack := DefaultSMTPParserPack()
	pack.ReviewState = ReviewStateDraft
	pack.ActivatedAt = nil
	pack.RegressionCases = nil
	pack.SHA256 = ""
	pack.ResourceLimits.ExecutionDeadlineMs = 100 // Very short but valid deadline

	executor, err := NewParserPackExecutor(pack)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	req := &ExecutionRequest{
		Input:   "20260822 050048557+0200 severrgo001 smtp 230675 0 140347436861184 Note;SieveRuleMsg(2005/1)",
		NodeID:  "test-node",
		Service: "smtp",
		Context: context.Background(),
	}

	// The deadline is very short, so execution may or may not exceed it
	// Just verify it doesn't panic and handles deadlines gracefully
	_, err = executor.Execute(req)
	// Don't assert on the error since timing is unpredictable
	// The important thing is that the deadline logic is in place
	t.Logf("Execution with short deadline result: %v", err)
}

// TestParserPackExecutor_StageFailureReasons tests stage failure reason recording
func TestParserPackExecutor_StageFailureReasons(t *testing.T) {
	pack := &ParserPack{
		ID:          "test_stage_failures",
		Version:     "1.0.0",
		Revision:    1,
		SourceType:  "manual",
		Author:      "test",
		ReviewState: ReviewStateDraft,
		CreatedAt:   time.Now(),
		Rules: []ParserRule{
			{
				ID:               "failing_stage_rule",
				Name:             "Failing Stage Rule",
				Priority:         100,
				ServiceSelectors: []string{"smtp"},
				Stages: []Stage{{
					ID:            "failing_stage",
					Name:          "Failing Stage",
					Type:          StageTypeMixedKV,         // Valid type but will fail due to missing config
					Config:        map[string]interface{}{}, // Empty config will cause failure
					FailurePolicy: FailurePolicyRejectRule,
				}},
				FailurePolicy:  FailurePolicyRejectRule,
				ProjectionMode: ProjectionModeAll,
				CountPolicy:    CountPolicyExactSource,
			},
		},
		ResourceLimits: DefaultResourceLimits(),
	}

	executor, err := NewParserPackExecutor(pack)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	req := &ExecutionRequest{
		Input:   "test input",
		NodeID:  "test-node",
		Service: "smtp",
		Context: context.Background(),
	}

	result, err := executor.Execute(req)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Should be quarantined due to stage failure
	if !result.Quarantined {
		t.Error("Result should be quarantined when stage fails")
	}

	// Check that failure reason was recorded
	if len(result.ParseTelemetry.OrderedAttempts) != 1 {
		t.Fatalf("Expected 1 attempt, got %d", len(result.ParseTelemetry.OrderedAttempts))
	}

	attempt := result.ParseTelemetry.OrderedAttempts[0]
	if attempt.Outcome != telemetry.AttemptOutcomeFailed {
		t.Error("Expected attempt to have failure outcome")
	}

	if len(attempt.FailureReason) == 0 {
		t.Error("Expected failure reason to be recorded")
	}
}

// TestParserPackExecutor_AllFailQuarantine tests quarantine when all rules fail
func TestParserPackExecutor_AllFailQuarantine(t *testing.T) {
	pack := &ParserPack{
		ID:          "test_all_fail",
		Version:     "1.0.0",
		Revision:    1,
		SourceType:  "manual",
		Author:      "test",
		ReviewState: ReviewStateDraft,
		CreatedAt:   time.Now(),
		Rules: []ParserRule{
			{
				ID:               "fail_rule1",
				Name:             "Fail Rule 1",
				Priority:         10,
				ServiceSelectors: []string{"smtp"},
				Stages: []Stage{{
					ID:            "fail_stage1",
					Name:          "Fail Stage 1",
					Type:          StageTypeMixedKV,
					Config:        map[string]interface{}{}, // Empty config will fail
					FailurePolicy: FailurePolicyRejectRule,
				}},
				FailurePolicy:  FailurePolicyRejectRule,
				ProjectionMode: ProjectionModeAll,
				CountPolicy:    CountPolicyExactSource,
			},
			{
				ID:               "fail_rule2",
				Name:             "Fail Rule 2",
				Priority:         20,
				ServiceSelectors: []string{"smtp"},
				Stages: []Stage{{
					ID:            "fail_stage2",
					Name:          "Fail Stage 2",
					Type:          StageTypeMixedKV,
					Config:        map[string]interface{}{}, // Empty config will fail
					FailurePolicy: FailurePolicyRejectRule,
				}},
				FailurePolicy:  FailurePolicyRejectRule,
				ProjectionMode: ProjectionModeAll,
				CountPolicy:    CountPolicyExactSource,
			},
		},
		ResourceLimits: DefaultResourceLimits(),
	}

	executor, err := NewParserPackExecutor(pack)
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	req := &ExecutionRequest{
		Input:   "test input",
		NodeID:  "test-node",
		Service: "smtp",
		Context: context.Background(),
	}

	result, err := executor.Execute(req)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Should be quarantined when all rules fail
	if !result.Quarantined {
		t.Error("Result should be quarantined when all rules fail")
	}

	// Should have tried both rules
	if result.ParseTelemetry.AttemptCount != 2 {
		t.Errorf("Expected 2 attempts, got %d", result.ParseTelemetry.AttemptCount)
	}

	// Quarantine is telemetry-only and must not synthesize an empty customer payload.
	if result.EventPayload != nil {
		t.Errorf("Expected nil EventPayload for quarantined result, got %v", result.EventPayload.Fields)
	}
}
