package parserpolicy

import (
	"context"
	"fmt"
	"time"

	"nexora.local/nli-v20/internal/mixedkv"
	"nexora.local/nli-v20/static-layout"
	"nexora.local/nli-v20/telemetry"
)

const SMTPGoldenInput = "20260822 050048557+0200 severrgo001 smtp 230675 0 140347436861184 Note;SieveRuleMsg(2005/1) id=6A291BF5048F23DE:policy=outbound:checkpoint=AUTH:rule=INOUT_action:msg=PolicyMsgInfo:eventType=PolicyMsgInfo:action=disconnect:reason=badpassword:fromhost=11.22.33.44:geo=IL:helo=Site6:auth_ok=false:auth_user=managers999@012.meg.in:auth_fail=6:auth_tries=7:score=0"

func SMTPGoldenPayload() map[string]interface{} {
	return map[string]interface{}{
		"date": "20260822", "time": "050048557+0200", "hostname": "severrgo001", "service": "smtp",
		"major_pid": "230675", "pid_status": 0, "minor_pid": "140347436861184",
		"event_type": "Note", "event": "SieveRuleMsg", "code": "2005/1",
		"id": "6A291BF5048F23DE", "policy": "outbound", "checkpoint": "AUTH",
		"rule": "INOUT_action", "msg": "PolicyMsgInfo", "eventType": "PolicyMsgInfo",
		"action": "disconnect", "reason": "badpassword", "fromhost": "11.22.33.44",
		"geo": "IL", "helo": "Site6", "auth_ok": false,
		"auth_user": "managers999@012.meg.in", "auth_fail": 6, "auth_tries": 7, "score": 0,
	}
}

type SMTPParser struct {
	executor *ParserPackExecutor
}

func NewSMTPParser() (*SMTPParser, error) {
	executor, err := NewSMTPParserPackExecutor()
	if err != nil {
		return nil, err
	}
	return &SMTPParser{executor: executor}, nil
}

func (parser *SMTPParser) Parse(input, nodeID string) (*telemetry.EventPayload, *telemetry.ParseTelemetry, error) {
	result, err := parser.executor.Execute(&ExecutionRequest{
		Input: input, NodeID: nodeID, Service: "smtp", SourceType: "file", Context: context.Background(),
	})
	if err != nil {
		return nil, nil, err
	}
	if result.Quarantined || result.EventPayload == nil {
		return nil, result.ParseTelemetry, fmt.Errorf("SMTP input did not satisfy the activated parser policy")
	}
	return result.EventPayload, result.ParseTelemetry, nil
}

func DefaultSMTPParserPack() *ParserPack {
	activated := time.Date(2026, time.October, 5, 0, 0, 0, 0, time.UTC)
	stringField := func(required bool) FieldDef {
		return FieldDef{Type: FieldTypeString, Required: required, CollisionPolicy: CollisionPolicyError}
	}
	intField := func(required bool) FieldDef {
		return FieldDef{Type: FieldTypeInt, Required: required, CollisionPolicy: CollisionPolicyError}
	}
	boolField := func(required bool) FieldDef {
		return FieldDef{Type: FieldTypeBool, Required: required, CollisionPolicy: CollisionPolicyError}
	}
	pack := &ParserPack{
		ID: "smtp_native_v5", Version: "5.0.0", Revision: 1,
		SourceType: "manual", Author: "nli_v5_core", ReviewState: ReviewStateActivated,
		CreatedAt: activated, ActivatedAt: &activated,
		Rules: []ParserRule{{
			ID: "smtp_sieve_rule", Name: "SMTP Sieve Rule Parser", Priority: 100,
			ServiceSelectors:  []string{"smtp"},
			IncludePredicates: []Predicate{{Type: "contains", Field: "input", Pattern: ";SieveRuleMsg("}},
			Stages: []Stage{
				{
					ID: "smtp_static_layout", Name: "SMTP Prefix Parser", Type: StageTypeStaticLayout,
					Config: staticlayout.CreateSMTPConfig(), FailurePolicy: FailurePolicyRejectRule,
				},
				{
					ID: "smtp_kv_tail", Name: "SMTP KV Tail Parser", Type: StageTypeMixedKV,
					Config: mixedKVStageConfig{
						InputField: "_kv_tail", PairSeparators: []string{":"}, AssignmentSeparators: []string{"="},
						QuoteCharacters: `"'`, EscapeCharacter: `\`, BracketPairs: map[string]string{"[": "]", "{": "}", "(": ")"},
						KeyPattern: `[A-Za-z_][A-Za-z0-9_]*`, DuplicatePolicy: mixedkv.DuplicateError,
						ValueTypes: map[string]string{"auth_ok": "bool", "auth_fail": "int", "auth_tries": "int", "score": "int"},
					},
					FailurePolicy: FailurePolicyRejectRule,
				},
			},
			FailurePolicy: FailurePolicyRejectRule,
			FieldSchema: map[string]FieldDef{
				"date": stringField(true), "time": stringField(true), "hostname": stringField(true), "service": stringField(true),
				"major_pid": stringField(true), "pid_status": intField(true), "minor_pid": stringField(true),
				"event_type": stringField(true), "event": stringField(true), "code": stringField(true),
				"id": stringField(true), "policy": stringField(true), "checkpoint": stringField(true), "rule": stringField(true),
				"msg": stringField(true), "eventType": stringField(true), "action": stringField(true), "reason": stringField(true),
				"fromhost": {Type: FieldTypeIP, Required: true, CollisionPolicy: CollisionPolicyError},
				"geo":      stringField(true), "helo": stringField(true), "auth_ok": boolField(true),
				"auth_user": {Type: FieldTypeEmail, Required: true, CollisionPolicy: CollisionPolicyError},
				"auth_fail": intField(true), "auth_tries": intField(true), "score": intField(true),
			},
			ProjectionMode: ProjectionModeAll, CountPolicy: CountPolicyExactSource,
		}},
		ResourceLimits: DefaultResourceLimits(),
		RegressionCases: []RegressionCase{{
			Name: "smtp_sieve_auth_disconnect", Input: SMTPGoldenInput, Service: "smtp", SourceType: "file",
			ExpectedPayload: SMTPGoldenPayload(), ExpectedParser: "smtp_native_v5/smtp_sieve_rule", ExpectedRoute: "normal",
			ExpectedTelemetry: &ExpectedTelemetry{
				ParserAttempts: []string{"smtp_native_v5/smtp_sieve_rule"}, FallbackDepth: 0, SourceFields: 26, OutputFields: 26,
			},
		}},
	}
	if err := SealParserPack(pack); err != nil {
		panic(err)
	}
	return pack
}

func NewSMTPParserPackExecutor() (*ParserPackExecutor, error) {
	return NewParserPackExecutor(DefaultSMTPParserPack())
}
