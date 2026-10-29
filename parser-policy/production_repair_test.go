package parserpolicy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/static-layout"
)

func productionTestPack(stages []Stage) *ParserPack {
	return &ParserPack{
		ID: "production_test", Version: "1.0.0", Revision: 1,
		SourceType: "manual", Author: "test", ReviewState: ReviewStateDraft,
		CreatedAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		Rules: []ParserRule{{
			ID: "rule", Name: "Rule", Priority: 10, ServiceSelectors: []string{"smtp"},
			Stages: stages, FailurePolicy: FailurePolicyRejectRule,
			ProjectionMode: ProjectionModeAll, CountPolicy: CountPolicyExactSource,
		}},
		ResourceLimits: DefaultResourceLimits(),
	}
}

func executeProductionTestPack(t *testing.T, pack *ParserPack, input string) *ExecutionResult {
	t.Helper()
	executor, err := NewParserPackExecutor(pack)
	if err != nil {
		t.Fatalf("create executor: %v", err)
	}
	result, err := executor.Execute(&ExecutionRequest{Input: input, Service: "smtp", NodeID: "test", Context: context.Background()})
	if err != nil {
		t.Fatalf("execute pack: %v", err)
	}
	return result
}

func TestLoadParserPackStrictAndHashed(t *testing.T) {
	pack := productionTestPack([]Stage{{
		ID: "regex", Name: "Regex", Type: StageTypeRegex,
		Config: map[string]interface{}{"pattern": `^(?P<id>\w+)$`}, FailurePolicy: FailurePolicyRejectRule,
	}})
	if err := SealParserPack(pack); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadParserPackBytes(encoded); err != nil {
		t.Fatalf("valid sealed pack rejected: %v", err)
	}
	unknown := strings.Replace(string(encoded), `"id":"production_test"`, `"id":"production_test","unknown":true`, 1)
	if _, err := LoadParserPackBytes([]byte(unknown)); err == nil {
		t.Fatal("unknown top-level field was accepted")
	}
	pack.Rules[0].Stages[0].Config = map[string]interface{}{"pattern": `^(?P<id>\w+)$`, "code": "forbidden"}
	if err := SealParserPack(pack); err != nil {
		t.Fatal(err)
	}
	unknownStage, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadParserPackBytes(unknownStage); err == nil {
		t.Fatal("unknown stage-config field was accepted")
	}
	tampered := strings.Replace(string(encoded), `"version":"1.0.0"`, `"version":"1.0.1"`, 1)
	if _, err := LoadParserPackBytes([]byte(tampered)); err == nil {
		t.Fatal("tampered canonical hash was accepted")
	}
}

func TestActivatedPackRequiresPassingRegressions(t *testing.T) {
	missing := DefaultSMTPParserPack()
	missing.RegressionCases = nil
	if err := SealParserPack(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := NewParserPackExecutor(missing); err == nil {
		t.Fatal("activated pack without regressions was accepted")
	}

	failing := DefaultSMTPParserPack()
	failing.RegressionCases[0].ExpectedPayload["score"] = 99
	if err := SealParserPack(failing); err != nil {
		t.Fatal(err)
	}
	if _, err := NewParserPackExecutor(failing); err == nil {
		t.Fatal("activated pack with a failing exact regression was accepted")
	}
}

func TestDeclarativeStaticLayoutDoesNotDependOnStageID(t *testing.T) {
	pack := productionTestPack([]Stage{{
		ID: "custom_name", Name: "Static", Type: StageTypeStaticLayout,
		Config: staticlayout.CreateSMTPConfig(), FailurePolicy: FailurePolicyRejectRule,
	}})
	result := executeProductionTestPack(t, pack, SMTPGoldenInput)
	if result.Quarantined || result.EventPayload == nil || result.EventPayload.FieldCount() != 10 {
		t.Fatalf("declarative static stage failed: %+v", result.ParseTelemetry)
	}
}

func TestParserPackMixedKVBridgeHonorsFullConfig(t *testing.T) {
	pack := productionTestPack([]Stage{{
		ID: "kv", Name: "KV", Type: StageTypeMixedKV,
		Config: map[string]interface{}{
			"pair_separators": []string{";"}, "assignment_separators": []string{"="},
			"quote_characters": `"'`, "escape_character": `\`,
			"bracket_pairs":      map[string]string{"[": "]"},
			"key_allow_patterns": []string{`^old$`},
			"key_aliases":        map[string]string{"old": "new"},
			"value_types":        map[string]string{"new": "int"},
			"duplicate_policy":   "error", "max_fields": 4,
		},
		FailurePolicy: FailurePolicyRejectRule,
	}})
	pack.Rules[0].FieldSchema = map[string]FieldDef{
		"new": {Type: FieldTypeInt, Required: true, CollisionPolicy: CollisionPolicyError},
	}
	result := executeProductionTestPack(t, pack, "old=7")
	value, exists := result.EventPayload.Get("new")
	if result.Quarantined || !exists || value != 7 {
		t.Fatalf("complete mixed-KV config was not honored: %#v", result)
	}
}

func TestParserPackMixedKVRejectsUnconsumedRemainder(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		config map[string]interface{}
	}{
		{
			name:  "stop key remainder",
			input: "id=7;stop=yes;later=no",
			config: map[string]interface{}{
				"pair_separators": []string{";"}, "assignment_separators": []string{"="},
				"key_allow_patterns": []string{`^(id|stop|later)$`}, "stop_keys": []string{"stop"},
				"value_types": map[string]string{"id": "int"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pack := productionTestPack([]Stage{{
				ID: "kv", Name: "KV", Type: StageTypeMixedKV,
				Config: test.config, FailurePolicy: FailurePolicyRejectRule,
			}})
			pack.Rules[0].FieldSchema = map[string]FieldDef{
				"id": {Type: FieldTypeInt, Required: true, CollisionPolicy: CollisionPolicyError},
			}
			result := executeProductionTestPack(t, pack, test.input)
			if !result.Quarantined || !contains(result.ParseTelemetry.ErrorCodes, "mixed_kv_unconsumed_remainder") {
				t.Fatalf("unconsumed mixed-KV data was accepted: %+v", result.ParseTelemetry)
			}
		})
	}
}

func TestParserPackMixedKVAllowsConfiguredLeadingText(t *testing.T) {
	pack := productionTestPack([]Stage{{
		ID: "kv", Name: "KV", Type: StageTypeMixedKV,
		Config: map[string]interface{}{
			"pair_separators": []string{";"}, "assignment_separators": []string{"="},
			"key_allow_patterns": []string{`^id$`}, "value_types": map[string]string{"id": "int"},
			"allow_leading_text": true,
		},
		FailurePolicy: FailurePolicyRejectRule,
	}})
	pack.Rules[0].FieldSchema = map[string]FieldDef{
		"id": {Type: FieldTypeInt, Required: true, CollisionPolicy: CollisionPolicyError},
	}
	result := executeProductionTestPack(t, pack, "diagnostic text;id=7")
	value, exists := result.EventPayload.Get("id")
	if result.Quarantined || !exists || value != 7 {
		t.Fatalf("configured leading text was not preserved: %+v", result.ParseTelemetry)
	}
}

func TestCountSchemaAndCollisionFailuresRejectRules(t *testing.T) {
	t.Run("exact count", func(t *testing.T) {
		pack := productionTestPack([]Stage{{
			ID: "static", Name: "Static", Type: StageTypeStaticLayout,
			Config: staticlayout.CreateSMTPConfig(), FailurePolicy: FailurePolicyRejectRule,
		}})
		pack.Rules[0].ProjectionMode = ProjectionModeAllowlist
		pack.Rules[0].AllowedFields = []string{"date"}
		result := executeProductionTestPack(t, pack, SMTPGoldenInput)
		if !result.Quarantined || !contains(result.ParseTelemetry.ErrorCodes, "field_count_policy") {
			t.Fatalf("exact-count violation succeeded: %+v", result)
		}
	})

	t.Run("required schema", func(t *testing.T) {
		pack := productionTestPack([]Stage{{
			ID: "regex", Name: "Regex", Type: StageTypeRegex,
			Config: map[string]interface{}{"pattern": `^(?P<id>\w+)$`}, FailurePolicy: FailurePolicyRejectRule,
		}})
		pack.Rules[0].FieldSchema = map[string]FieldDef{
			"id":             {Type: FieldTypeString, Required: true},
			"required_count": {Type: FieldTypeInt, Required: true},
		}
		result := executeProductionTestPack(t, pack, "abc")
		if !result.Quarantined || !contains(result.ParseTelemetry.ErrorCodes, "schema_required") {
			t.Fatalf("missing required field succeeded: %+v", result)
		}
	})

	t.Run("collision", func(t *testing.T) {
		pack := productionTestPack([]Stage{
			{ID: "static", Name: "Static", Type: StageTypeStaticLayout, Config: staticlayout.CreateSMTPConfig(), FailurePolicy: FailurePolicyRejectRule},
			{ID: "kv", Name: "KV", Type: StageTypeMixedKV, Config: map[string]interface{}{
				"input_field": "_kv_tail", "pair_separators": []string{":"}, "assignment_separators": []string{"="},
			}, FailurePolicy: FailurePolicyRejectRule},
		})
		result := executeProductionTestPack(t, pack, SMTPGoldenInput+":service=spoofed")
		if !result.Quarantined || !contains(result.ParseTelemetry.ErrorCodes, "field_collision") {
			t.Fatalf("collision succeeded: %+v", result)
		}
	})
}

func TestFallbackPreservesStageTraceAndCompletesTelemetry(t *testing.T) {
	pack := productionTestPack(nil)
	pack.Rules = []ParserRule{
		{
			ID: "first", Name: "First", Priority: 1, ServiceSelectors: []string{"smtp"},
			Stages:        []Stage{{ID: "reject", Name: "Reject", Type: StageTypeRegex, Config: map[string]interface{}{"pattern": "^NEVER$"}, FailurePolicy: FailurePolicyRejectRule}},
			FailurePolicy: FailurePolicyRejectRule, ProjectionMode: ProjectionModeAll, CountPolicy: CountPolicyExactSource,
		},
		{
			ID: "second", Name: "Second", Priority: 2, ServiceSelectors: []string{"smtp"},
			Stages:        []Stage{{ID: "static", Name: "Static", Type: StageTypeStaticLayout, Config: staticlayout.CreateSMTPConfig(), FailurePolicy: FailurePolicyRejectRule}},
			FailurePolicy: FailurePolicyRejectRule, ProjectionMode: ProjectionModeAll, CountPolicy: CountPolicyExactSource,
		},
	}
	result := executeProductionTestPack(t, pack, SMTPGoldenInput)
	metadata := result.ParseTelemetry
	if result.Quarantined || result.RuleID != "second" || metadata.AttemptCount != 2 || metadata.FallbackDepth != 1 {
		t.Fatalf("fallback did not reach second rule: %+v", result)
	}
	if len(metadata.OrderedAttempts[0].StageResults) != 1 || metadata.OrderedAttempts[0].StageResults[0].ErrorCode != "regex_no_match" {
		t.Fatalf("failed stage trace was lost: %+v", metadata.OrderedAttempts[0])
	}
	if metadata.ParserID != "production_test/second" || metadata.RuleID != "second" || metadata.PolicyHash == "" ||
		metadata.InputFieldCount == 0 || metadata.SourceFieldCount == 0 || metadata.EvidenceCoverage != 1 ||
		metadata.StageCount != 2 || metadata.LatencyMs < 1 || metadata.Result != "success" || metadata.Route != "normal" {
		t.Fatalf("telemetry is incomplete: %+v", metadata)
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestCollisionPoliciesAndSchemaConstraints(t *testing.T) {
	for _, testCase := range []struct {
		policy   CollisionPolicy
		expected interface{}
	}{
		{CollisionPolicyFirst, "one"},
		{CollisionPolicyLast, "two"},
		{CollisionPolicyArray, []interface{}{"one", "two"}},
	} {
		t.Run(string(testCase.policy), func(t *testing.T) {
			pack := productionTestPack([]Stage{
				{ID: "first", Name: "First", Type: StageTypeRegex, Config: map[string]interface{}{"pattern": `^(?P<a>\w+)`, "groups": map[string]string{"a": "a"}}, FailurePolicy: FailurePolicyRejectRule},
				{ID: "second", Name: "Second", Type: StageTypeRegex, Config: map[string]interface{}{"pattern": `(?P<b>\w+)$`, "groups": map[string]string{"b": "b"}}, FailurePolicy: FailurePolicyRejectRule},
			})
			pack.Rules[0].FieldSchema = map[string]FieldDef{
				"value": {Type: FieldTypeString, Required: true, Aliases: []string{"a", "b"}, CollisionPolicy: testCase.policy},
			}
			result := executeProductionTestPack(t, pack, "one two")
			if result.Quarantined {
				t.Fatalf("collision policy %s quarantined: %+v", testCase.policy, result.ParseTelemetry)
			}
			actual, _ := json.Marshal(result.EventPayload.Fields["value"])
			expected, _ := json.Marshal(testCase.expected)
			if string(actual) != string(expected) {
				t.Fatalf("collision policy %s got %s, expected %s", testCase.policy, actual, expected)
			}
		})
	}

	for _, testCase := range []struct {
		name  string
		input string
		field string
		def   FieldDef
		code  string
	}{
		{"enum", "blocked", "state", FieldDef{Type: FieldTypeString, Required: true, Enum: []string{"allowed"}}, "schema_enum"},
		{"range", "9", "count", FieldDef{Type: FieldTypeInt, Required: true, Range: &RangeConstraint{Max: floatPointer(8)}}, "schema_range"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pack := productionTestPack([]Stage{{ID: "capture", Name: "Capture", Type: StageTypeRegex, Config: map[string]interface{}{"pattern": `^(?P<value>.+)$`, "groups": map[string]string{"value": testCase.field}}, FailurePolicy: FailurePolicyRejectRule}})
			pack.Rules[0].FieldSchema = map[string]FieldDef{testCase.field: testCase.def}
			result := executeProductionTestPack(t, pack, testCase.input)
			if !result.Quarantined || !contains(result.ParseTelemetry.ErrorCodes, testCase.code) {
				t.Fatalf("schema %s constraint did not reject: %+v", testCase.name, result)
			}
		})
	}
}

func floatPointer(value float64) *float64 { return &value }

func TestParserPackStaticLayoutIRExecutesDirectly(t *testing.T) {
	pack := productionTestPack([]Stage{{
		ID: "layout", Name: "Layout", Type: StageTypeStaticLayout,
		Config: StaticLayoutConfig{Tokens: []TokenDef{
			{Index: 0, Name: "level", Type: TokenTypeWhitespace, Required: true},
			{Index: 1, Name: "message", Type: TokenTypeRestOfLine, Required: true, Transform: []TransformConfig{{Type: "trim"}}},
		}},
		FailurePolicy: FailurePolicyRejectRule,
	}})
	pack.Rules[0].FieldSchema = map[string]FieldDef{
		"level":   {Type: FieldTypeString, Required: true},
		"message": {Type: FieldTypeString, Required: true},
	}
	result := executeProductionTestPack(t, pack, "INFO   preserved  message")
	if result.Quarantined || result.EventPayload.Fields["level"] != "INFO" || result.EventPayload.Fields["message"] != "preserved  message" {
		t.Fatalf("ParserPack static-layout IR failed: %#v", result)
	}
}

func TestReservedRawOutputFieldIsRejected(t *testing.T) {
	pack := productionTestPack([]Stage{{
		ID: "json", Name: "JSON", Type: StageTypeJSON,
		Config: map[string]interface{}{}, FailurePolicy: FailurePolicyRejectRule,
	}})
	result := executeProductionTestPack(t, pack, `{"raw":"must-not-leave"}`)
	if !result.Quarantined || !contains(result.ParseTelemetry.ErrorCodes, "forbidden_output_field") || result.EventPayload != nil {
		t.Fatalf("reserved raw field escaped parser policy: %+v", result)
	}
}

func TestPerStageResourceLimitsAreEnforced(t *testing.T) {
	limits := DefaultResourceLimits()
	limits.MaxCaptures = 1
	pack := productionTestPack([]Stage{{
		ID: "regex", Name: "Regex", Type: StageTypeRegex,
		Config:        map[string]interface{}{"pattern": `^(?P<a>\w+)\s+(?P<b>\w+)$`},
		FailurePolicy: FailurePolicyRejectRule, ResourceLimits: limits,
	}})
	result := executeProductionTestPack(t, pack, "one two")
	if !result.Quarantined || !contains(result.ParseTelemetry.ErrorCodes, "capture_limit") {
		t.Fatalf("per-stage capture limit was not enforced: %+v", result)
	}
}
