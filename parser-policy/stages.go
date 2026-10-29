package parserpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/grok"
	"nexora.local/nli-v20/internal/mixedkv"
	"nexora.local/nli-v20/internal/semanticextract"
	"nexora.local/nli-v20/static-layout"
	"nexora.local/nli-v20/telemetry"
)

func (e *ParserPackExecutor) executeStage(ctx context.Context, request *ExecutionRequest, rule ParserRule, stage Stage, fields workingSet) (telemetry.StageResult, error) {
	started := time.Now()
	result := telemetry.StageResult{StageID: stage.ID, StageName: stage.Name, StageType: string(stage.Type), Outcome: telemetry.StageOutcomeFailed}
	stageContext := ctx
	if stage.ResourceLimits != (ResourceLimits{}) {
		var cancel context.CancelFunc
		stageContext, cancel = context.WithTimeout(ctx, time.Duration(stage.ResourceLimits.ExecutionDeadlineMs)*time.Millisecond)
		defer cancel()
	}
	select {
	case <-stageContext.Done():
		result.ErrorCode = "execution_deadline"
		return result, fail("execution_deadline", "stage execution deadline exceeded")
	default:
	}
	var added, modified int
	var err error
	switch stage.Type {
	case StageTypeStaticLayout, StageTypeDelimited:
		added, modified, err = e.executeStaticLayoutStage(request, rule, stage, fields)
	case StageTypeMixedKV, StageTypeKV, StageTypeLogfmt:
		added, modified, err = e.executeMixedKVStage(request, rule, stage, fields)
	case StageTypeRegex:
		added, modified, err = e.executeRegexStage(request, rule, stage, fields)
	case StageTypeGrok:
		added, modified, err = e.executeGrokStage(request, rule, stage, fields)
	case StageTypeJSON:
		added, modified, err = e.executeJSONStage(request, rule, stage, fields)
	case StageTypeTimestamp:
		modified, err = e.executeTimestampStage(stage, fields)
	case StageTypeReplace:
		modified, err = e.executeReplaceStage(stage, fields)
	case StageTypeRename:
		added, modified, err = e.executeRenameStage(rule, stage, fields)
	case StageTypeTypeConversion:
		modified, err = e.executeTypeConversionStage(stage, fields)
	case StageTypeFieldProjection:
		modified, err = e.executeProjectionStage(stage, fields)
	case StageTypeConditionalMatch:
		err = e.executeConditionalStage(request, stage, fields)
	case StageTypeDrop:
		err = quarantineFailure("stage_drop", "drop stage requested quarantine")
	case StageTypeOutputMapping:
		added, modified, err = e.executeOutputMappingStage(rule, stage, fields)
	case StageTypeSemanticExtract:
		added, modified, err = e.executeSemanticExtractStage(request, rule, stage, fields)
	default:
		err = fail("unsupported_stage", "stage type is not executable")
	}
	select {
	case <-stageContext.Done():
		err = fail("execution_deadline", "stage execution deadline exceeded")
	default:
	}
	if err == nil && len(fields) > e.limitsFor(stage).MaxFields {
		err = fail("field_limit", "stage exceeded its effective field limit")
	}
	result.FieldsAdded = added
	result.FieldsModified = modified
	result.LatencyMs = elapsedMillis(started)
	if err != nil {
		result.ErrorCode = errorCode(err)
		return result, err
	}
	result.Outcome = telemetry.StageOutcomeSuccess
	return result, nil
}

func (e *ParserPackExecutor) executeSemanticExtractStage(request *ExecutionRequest, rule ParserRule, stage Stage, fields workingSet) (int, int, error) {
	var config semanticExtractStageConfig
	if err := decodeStrictConfig(stage.Config, &config); err != nil {
		return 0, 0, fail("semantic_config", "invalid semantic extraction configuration")
	}
	input, baseStart := request.Input, 0
	if config.InputField != "" {
		field, exists := fields[config.InputField]
		if !exists {
			return 0, 0, fail("semantic_input", "configured semantic input field is missing")
		}
		value, ok := field.Value.(string)
		if !ok {
			return 0, 0, fail("semantic_input", "configured semantic input field is not a string")
		}
		input, baseStart = value, field.Start
	}
	limits := e.limitsFor(stage)
	maxInput := config.MaxInputBytes
	if maxInput == 0 || maxInput > limits.MaxInputBytes {
		maxInput = limits.MaxInputBytes
	}
	if maxInput > 1<<20 {
		maxInput = 1 << 20
	}
	maxHeader := config.MaxHeaderBytes
	if maxHeader == 0 {
		maxHeader = maxInput
		if maxHeader > 64<<10 {
			maxHeader = 64 << 10
		}
	}
	// Raw-record extraction expects RFC 5322 headers and lets Extract locate the
	// Subject header. When input_field is set, the selected field is already the
	// header value (for example SMTP value==?UTF-8?B?...?=), so decode that value
	// directly instead of requiring a synthetic "Subject:" prefix.
	directMIMEValue := config.DecodeMIMEHeaders && config.InputField != ""
	settings := semanticextract.Config{MaxInputBytes: maxInput, MaxHeaderBytes: maxHeader, DecodeMIMEHeaders: config.DecodeMIMEHeaders && !directMIMEValue}
	for _, entity := range config.Entities {
		switch entity {
		case "ipv4":
			settings.IPv4 = true
		case "ipv6":
			settings.IPv6 = true
		case "domain":
			settings.Domain = true
		case "url":
			settings.URL = true
		case "email":
			settings.Email = true
		}
	}
	result := semanticextract.Extract(input, settings)
	if directMIMEValue {
		decoded, decodeErr := semanticextract.DecodeMIMEHeader(input, maxHeader)
		if decodeErr != nil {
			result.Errors = append(result.Errors, "mime_decode_failed")
		} else if decoded != "" {
			result.Entities["email_subject_decoded"] = []semanticextract.Entity{{Value: decoded, Start: 0, End: len(input)}}
		}
	}
	added, modified := 0, 0
	for entity, matches := range result.Entities {
		if len(matches) == 0 {
			continue
		}
		fieldName := config.OutputFields[entity]
		if fieldName == "" {
			fieldName = defaultSemanticOutputField(entity)
		}
		values := make([]interface{}, 0, len(matches))
		start, end := -1, -1
		for _, match := range matches {
			values = append(values, match.Value)
			if start < 0 || match.Start < start {
				start = match.Start
			}
			if match.End > end {
				end = match.End
			}
		}
		one, two, err := mergeCaptured(rule, fields, map[string]capturedField{fieldName: {Value: values, Source: false, Start: baseStart + start, End: baseStart + end, DerivedRule: "semantic_extract:" + entity}}, false)
		if err != nil {
			return added, modified, err
		}
		added += one
		modified += two
	}
	if len(result.Errors) > 0 {
		return added, modified, fail("semantic_extraction_partial", "semantic extraction skipped malformed or oversized input")
	}
	return added, modified, nil
}

func defaultSemanticOutputField(entity string) string {
	switch entity {
	case "ipv4":
		return "network_ipv4"
	case "ipv6":
		return "network_ipv6"
	case "domain":
		return "network_domain"
	case "email":
		return "email_address"
	case "email_subject_decoded":
		return "email_subject_decoded"
	default:
		return entity
	}
}

func (e *ParserPackExecutor) executeStaticLayoutStage(request *ExecutionRequest, rule ParserRule, stage Stage, fields workingSet) (int, int, error) {
	config, err := decodeStaticStageConfig(stage.Config)
	if err != nil {
		return 0, 0, fail("static_config", "invalid static layout configuration")
	}
	limits := e.limitsFor(stage)
	if config.MaxInputBytes == 0 {
		config.MaxInputBytes = limits.MaxInputBytes
	} else if config.MaxInputBytes > limits.MaxInputBytes {
		return 0, 0, fail("static_config", "static input limit exceeds stage limit")
	}
	if err := validateStaticRuntimeLimits(config, limits); err != nil {
		return 0, 0, err
	}
	parser, err := staticlayout.NewParser(config)
	if err != nil {
		return 0, 0, fail("static_config", "invalid static layout configuration")
	}
	parsed, err := parser.Parse(request.Input)
	if err != nil {
		return 0, 0, fail("static_parse", "static layout did not match")
	}
	if len(parsed.Errors) > 0 {
		return 0, 0, fail("static_parse", "static layout reported token errors")
	}
	if len(parsed.Evidence) > limits.MaxCaptures {
		return 0, 0, fail("capture_limit", "static layout exceeded the pack capture limit")
	}
	evidence := make(map[string]staticlayout.TokenEvidence, len(parsed.Evidence))
	for _, record := range parsed.Evidence {
		evidence[record.FieldName] = record
	}
	captured := make(map[string]capturedField, len(parsed.Fields))
	for name, value := range parsed.Fields {
		record, exists := evidence[name]
		if !exists {
			return 0, 0, fail("evidence_missing", "static field has no source span")
		}
		captured[name] = capturedField{Value: value, Source: true, Start: record.Start, End: record.End}
	}
	return mergeCaptured(rule, fields, captured, false)
}

func (e *ParserPackExecutor) executeMixedKVStage(request *ExecutionRequest, rule ParserRule, stage Stage, fields workingSet) (int, int, error) {
	stageConfig, config, err := decodeMixedKVStageConfig(stage.Config)
	if err != nil {
		return 0, 0, fail("mixed_kv_config", "invalid mixed-KV configuration")
	}
	if stage.Type == StageTypeLogfmt {
		if len(config.PairSeparators) == 0 {
			config.PairSeparators = []string{" ", "\t"}
		}
		if len(config.AssignmentSeparators) == 0 {
			config.AssignmentSeparators = []string{"="}
		}
	}
	limits := e.limitsFor(stage)
	if config.MaxInputBytes == 0 {
		config.MaxInputBytes = limits.MaxInputBytes
	} else if config.MaxInputBytes > limits.MaxInputBytes {
		return 0, 0, fail("mixed_kv_config", "mixed-KV input limit exceeds stage limit")
	}
	if config.MaxFields == 0 {
		config.MaxFields = limits.MaxFields
	} else if config.MaxFields > limits.MaxFields {
		return 0, 0, fail("mixed_kv_config", "mixed-KV field limit exceeds stage limit")
	}
	patterns := append([]string{config.KeyPattern}, config.KeyAllowPatterns...)
	patterns = append(patterns, config.KeyDenyPatterns...)
	for _, pattern := range patterns {
		if len(pattern) > limits.MaxRegexLength {
			return 0, 0, fail("regex_limit", "mixed-KV pattern exceeds pack regex limit")
		}
	}

	input := request.Input
	baseStart := 0
	inputField := stageConfig.InputField
	if inputField == "" {
		if _, exists := fields["_kv_tail"]; exists {
			inputField = "_kv_tail"
		}
	}
	if inputField != "" {
		field, exists := fields[inputField]
		if !exists {
			return 0, 0, fail("mixed_kv_input", "configured mixed-KV input field is missing")
		}
		text, ok := field.Value.(string)
		if !ok {
			return 0, 0, fail("mixed_kv_input", "configured mixed-KV input field is not a string")
		}
		input, baseStart = text, field.Start
	}
	parsed, err := mixedkv.Parse(input, config)
	if err != nil {
		if typed, ok := err.(*mixedkv.Error); ok {
			return 0, 0, fail("mixed_kv_"+typed.Code, "mixed-KV parsing failed")
		}
		return 0, 0, fail("mixed_kv_parse", "mixed-KV parsing failed")
	}
	if parsed.RemainderSpan != nil && parsed.RemainderSpan.Start < parsed.RemainderSpan.End {
		return 0, 0, fail("mixed_kv_unconsumed_remainder", "mixed-KV input contains unconsumed data")
	}
	captured := make(map[string]capturedField, len(parsed.Fields))
	for name, value := range parsed.Fields {
		start, end, found := evidenceForMixedField(parsed, name)
		if !found {
			return 0, 0, fail("evidence_missing", "mixed-KV field has no source span")
		}
		captured[name] = capturedField{Value: value, Source: true, Start: baseStart + start, End: baseStart + end}
	}
	return mergeCaptured(rule, fields, captured, false)
}

func evidenceForMixedField(result mixedkv.Result, name string) (int, int, bool) {
	start, end, found := 0, 0, false
	for _, entry := range result.Entries {
		if entry.Key != name || !entry.Emitted {
			continue
		}
		if !found || entry.PairSpan.Start < start {
			start = entry.PairSpan.Start
		}
		if !found || entry.PairSpan.End > end {
			end = entry.PairSpan.End
		}
		found = true
	}
	return start, end, found
}

func (e *ParserPackExecutor) executeRegexStage(request *ExecutionRequest, rule ParserRule, stage Stage, fields workingSet) (int, int, error) {
	var config regexStageConfig
	if err := decodeStrictConfig(stage.Config, &config); err != nil {
		return 0, 0, fail("regex_config", "invalid regex configuration")
	}
	limits := e.limitsFor(stage)
	if len(config.Pattern) > limits.MaxRegexLength {
		return 0, 0, fail("regex_limit", "regex exceeded the stage regex limit")
	}
	compiled, err := e.compileRegex(config.Pattern)
	if err != nil {
		return 0, 0, fail("regex_config", "invalid regex configuration")
	}
	if compiled.NumSubexp() > limits.MaxCaptures {
		return 0, 0, fail("capture_limit", "regex exceeded the pack capture limit")
	}
	input, base, err := stageInput(request.Input, config.InputField, fields)
	if err != nil {
		return 0, 0, err
	}
	indexes := compiled.FindStringSubmatchIndex(input)
	if indexes == nil {
		return 0, 0, fail("regex_no_match", "regex did not match")
	}
	captured := map[string]capturedField{}
	for index, name := range compiled.SubexpNames() {
		if index == 0 || name == "" || index*2+1 >= len(indexes) || indexes[index*2] < 0 {
			continue
		}
		output := name
		if alias := config.Groups[name]; alias != "" {
			output = alias
		}
		start, end := indexes[index*2], indexes[index*2+1]
		captured[output] = capturedField{Value: input[start:end], Source: true, Start: base + start, End: base + end}
	}
	return mergeCaptured(rule, fields, captured, false)
}

func (e *ParserPackExecutor) executeGrokStage(request *ExecutionRequest, rule ParserRule, stage Stage, fields workingSet) (int, int, error) {
	var config grokStageConfig
	if err := decodeStrictConfig(stage.Config, &config); err != nil {
		return 0, 0, fail("grok_config", "invalid grok configuration")
	}
	limits := e.limitsFor(stage)
	compiled, err := grok.New(config.Patterns).Compile(config.Pattern)
	if err != nil {
		return 0, 0, fail("grok_config", "invalid grok configuration")
	}
	if compiled.RE.NumSubexp() > limits.MaxCaptures || len(compiled.RE.String()) > limits.MaxRegexLength {
		return 0, 0, fail("capture_limit", "grok expansion exceeded pack limits")
	}
	input, base, err := stageInput(request.Input, config.InputField, fields)
	if err != nil {
		return 0, 0, err
	}
	indexes := compiled.RE.FindStringSubmatchIndex(input)
	if indexes == nil {
		return 0, 0, fail("grok_no_match", "grok did not match")
	}
	types := map[string]string{}
	for _, field := range compiled.Fields {
		types[field.Name] = field.Type
	}
	captured := map[string]capturedField{}
	for index, name := range compiled.RE.SubexpNames() {
		if index == 0 || name == "" || index*2+1 >= len(indexes) || indexes[index*2] < 0 {
			continue
		}
		start, end := indexes[index*2], indexes[index*2+1]
		value, conversionErr := convertDeclaredValue(input[start:end], FieldType(types[name]))
		if types[name] == "" {
			value, conversionErr = input[start:end], nil
		}
		if conversionErr != nil {
			return 0, 0, fail("grok_type", "grok capture type conversion failed")
		}
		captured[name] = capturedField{Value: value, Source: true, Start: base + start, End: base + end}
	}
	return mergeCaptured(rule, fields, captured, false)
}

func (e *ParserPackExecutor) executeJSONStage(request *ExecutionRequest, rule ParserRule, stage Stage, fields workingSet) (int, int, error) {
	var config jsonStageConfig
	if err := decodeStrictConfig(stage.Config, &config); err != nil {
		return 0, 0, fail("json_config", "invalid JSON stage configuration")
	}
	input, base, err := stageInput(request.Input, config.InputField, fields)
	if err != nil {
		return 0, 0, err
	}
	var object map[string]interface{}
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return 0, 0, fail("json_invalid", "JSON stage input is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return 0, 0, fail("json_trailing", "JSON stage input contains trailing data")
	}
	if len(object) > e.limitsFor(stage).MaxFields {
		return 0, 0, fail("field_limit", "JSON object exceeds field limit")
	}
	captured := make(map[string]capturedField, len(object))
	for name, value := range object {
		captured[name] = capturedField{Value: value, Source: true, Start: base, End: base + len(input)}
	}
	return mergeCaptured(rule, fields, captured, false)
}

func (e *ParserPackExecutor) executeTimestampStage(stage Stage, fields workingSet) (int, error) {
	var config timestampStageConfig
	if err := decodeStrictConfig(stage.Config, &config); err != nil {
		return 0, fail("timestamp_config", "invalid timestamp configuration")
	}
	field, exists := fields[config.InputField]
	if !exists {
		return 0, fail("timestamp_missing", "timestamp input field is missing")
	}
	text, ok := field.Value.(string)
	if !ok {
		return 0, fail("timestamp_type", "timestamp input field is not a string")
	}
	var parsed time.Time
	var parseErr error
	for _, layout := range config.Layouts {
		parsed, parseErr = time.Parse(layout, text)
		if parseErr == nil {
			break
		}
	}
	if parseErr != nil {
		return 0, fail("timestamp_invalid", "timestamp value did not match configured layouts")
	}
	output := config.OutputField
	if output == "" {
		output = config.InputField
	}
	if output != config.InputField {
		delete(fields, config.InputField)
	}
	field.Value = parsed.UTC().Format(time.RFC3339Nano)
	fields[output] = field
	return 1, nil
}

func (e *ParserPackExecutor) executeReplaceStage(stage Stage, fields workingSet) (int, error) {
	var config replaceStageConfig
	if err := decodeStrictConfig(stage.Config, &config); err != nil {
		return 0, fail("replace_config", "invalid replace configuration")
	}
	field, exists := fields[config.Field]
	if !exists {
		return 0, fail("replace_missing", "replace input field is missing")
	}
	text, ok := field.Value.(string)
	if !ok {
		return 0, fail("replace_type", "replace input field is not a string")
	}
	if config.Regex {
		compiled, err := regexp.Compile(config.Pattern)
		if err != nil {
			return 0, fail("replace_config", "invalid replacement regex")
		}
		field.Value = compiled.ReplaceAllString(text, config.Replacement)
	} else {
		field.Value = strings.ReplaceAll(text, config.Pattern, config.Replacement)
	}
	fields[config.Field] = field
	return 1, nil
}

func (e *ParserPackExecutor) executeRenameStage(rule ParserRule, stage Stage, fields workingSet) (int, int, error) {
	var config renameStageConfig
	if err := decodeStrictConfig(stage.Config, &config); err != nil {
		return 0, 0, fail("rename_config", "invalid rename configuration")
	}
	sources := make([]string, 0, len(config.Fields))
	for source := range config.Fields {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	added, modified := 0, 0
	for _, source := range sources {
		field, exists := fields[source]
		if !exists {
			return added, modified, fail("rename_missing", "rename source field is missing")
		}
		delete(fields, source)
		a, m, err := mergeCaptured(rule, fields, map[string]capturedField{config.Fields[source]: field}, false)
		added, modified = added+a, modified+m+1
		if err != nil {
			return added, modified, err
		}
	}
	return added, modified, nil
}

func (e *ParserPackExecutor) executeTypeConversionStage(stage Stage, fields workingSet) (int, error) {
	var config conversionStageConfig
	if err := decodeStrictConfig(stage.Config, &config); err != nil {
		return 0, fail("type_config", "invalid type conversion configuration")
	}
	modified := 0
	for name, target := range config.Conversions {
		field, exists := fields[name]
		if !exists {
			continue
		}
		converted, err := convertDeclaredValue(field.Value, normalizeFieldType(target))
		if err != nil {
			return modified, fail("type_conversion", "declared field type conversion failed")
		}
		field.Value = converted
		fields[name] = field
		modified++
	}
	return modified, nil
}

func (e *ParserPackExecutor) executeProjectionStage(stage Stage, fields workingSet) (int, error) {
	var config projectionStageConfig
	if err := decodeStrictConfig(stage.Config, &config); err != nil {
		return 0, fail("projection_config", "invalid stage projection configuration")
	}
	mode := config.Mode
	if mode == "" {
		mode = ProjectionModeAll
	}
	allowed, denied := stringSet(config.AllowedFields), stringSet(config.DeniedFields)
	removed := 0
	for name := range fields {
		if strings.HasPrefix(name, "_") {
			continue
		}
		remove := mode == ProjectionModeAllowlist && !allowed[name]
		remove = remove || (mode == ProjectionModeDenylist && denied[name])
		if remove {
			delete(fields, name)
			removed++
		}
	}
	return removed, nil
}

func (e *ParserPackExecutor) executeConditionalStage(request *ExecutionRequest, stage Stage, fields workingSet) error {
	var config conditionalStageConfig
	if err := decodeStrictConfig(stage.Config, &config); err != nil {
		return fail("condition_config", "invalid conditional configuration")
	}
	value := request.Input
	exists := true
	if config.Field != "" && config.Field != "input" {
		field, found := fields[config.Field]
		exists = found
		if found {
			value = fmt.Sprint(field.Value)
		}
	}
	matched := false
	switch config.Operator {
	case "exists":
		matched = exists
	case "equals":
		matched = exists && value == config.Value
	case "contains":
		matched = exists && strings.Contains(value, config.Value)
	case "regex":
		compiled, err := regexp.Compile(config.Pattern)
		matched = err == nil && exists && compiled.MatchString(value)
	}
	if !matched {
		return fail("condition_no_match", "conditional stage did not match")
	}
	return nil
}

func (e *ParserPackExecutor) executeOutputMappingStage(rule ParserRule, stage Stage, fields workingSet) (int, int, error) {
	var config outputMappingStageConfig
	if err := decodeStrictConfig(stage.Config, &config); err != nil {
		return 0, 0, fail("mapping_config", "invalid output mapping configuration")
	}
	captured := map[string]capturedField{}
	for target, source := range config.Fields {
		field, exists := fields[source]
		if !exists {
			return 0, 0, fail("mapping_missing", "output mapping source field is missing")
		}
		captured[target] = field
	}
	for target, value := range config.Constants {
		captured[target] = capturedField{Value: value, Source: false, Start: -1, End: -1, DerivedRule: stage.ID}
	}
	return mergeCaptured(rule, fields, captured, false)
}

func stageInput(raw, fieldName string, fields workingSet) (string, int, error) {
	if fieldName == "" || fieldName == "input" {
		return raw, 0, nil
	}
	field, exists := fields[fieldName]
	if !exists {
		return "", 0, fail("stage_input_missing", "configured stage input field is missing")
	}
	text, ok := field.Value.(string)
	if !ok {
		return "", 0, fail("stage_input_type", "configured stage input field is not a string")
	}
	return text, field.Start, nil
}

func mergeCaptured(rule ParserRule, fields workingSet, captured map[string]capturedField, update bool) (int, int, error) {
	aliases := schemaAliases(rule)
	names := make([]string, 0, len(captured))
	for name := range captured {
		names = append(names, name)
	}
	sort.Strings(names)
	added, modified := 0, 0
	for _, sourceName := range names {
		name := sourceName
		if canonical := aliases[name]; canonical != "" {
			name = canonical
		}
		incoming := captured[sourceName]
		existing, collision := fields[name]
		if !collision {
			fields[name] = incoming
			added++
			continue
		}
		if update {
			fields[name] = incoming
			modified++
			continue
		}
		policy := CollisionPolicyError
		if definition, exists := rule.FieldSchema[name]; exists && definition.CollisionPolicy != "" {
			policy = definition.CollisionPolicy
		}
		switch policy {
		case CollisionPolicyFirst:
			continue
		case CollisionPolicyLast:
			fields[name] = incoming
			modified++
		case CollisionPolicyArray:
			values, ok := existing.Value.([]interface{})
			if !ok {
				values = []interface{}{existing.Value}
			}
			values = append(values, incoming.Value)
			existing.Value = values
			if incoming.Source {
				existing.Source = true
				if incoming.Start < existing.Start || existing.Start < 0 {
					existing.Start = incoming.Start
				}
				if incoming.End > existing.End {
					existing.End = incoming.End
				}
			}
			fields[name] = existing
			modified++
		default:
			return added, modified, fail("field_collision", "field collision rejected by policy")
		}
	}
	return added, modified, nil
}

func schemaAliases(rule ParserRule) map[string]string {
	aliases := map[string]string{}
	for canonical, definition := range rule.FieldSchema {
		for _, alias := range definition.Aliases {
			aliases[alias] = canonical
		}
	}
	return aliases
}

func enforceSchema(rule ParserRule, fields workingSet) error {
	if len(rule.FieldSchema) == 0 {
		return nil
	}
	for name := range fields {
		if strings.HasPrefix(name, "_") {
			continue
		}
		if _, exists := rule.FieldSchema[name]; !exists {
			return fail("schema_unknown_field", "parser emitted a field absent from its schema")
		}
	}
	for name, definition := range rule.FieldSchema {
		field, exists := fields[name]
		if !exists {
			if definition.Required {
				return fail("schema_required", "required schema field is missing")
			}
			continue
		}
		value, err := enforceFieldDefinition(field.Value, definition)
		if err != nil {
			return err
		}
		field.Value = value
		fields[name] = field
	}
	return nil
}

func enforceFieldDefinition(value interface{}, definition FieldDef) (interface{}, error) {
	if values, ok := value.([]interface{}); ok {
		if definition.CollisionPolicy != CollisionPolicyArray {
			return nil, fail("schema_type", "array value requires collision_policy=array")
		}
		converted := make([]interface{}, len(values))
		for index, item := range values {
			validated, err := enforceScalarDefinition(item, definition)
			if err != nil {
				return nil, err
			}
			converted[index] = validated
		}
		return converted, nil
	}
	return enforceScalarDefinition(value, definition)
}

func enforceScalarDefinition(value interface{}, definition FieldDef) (interface{}, error) {
	converted, err := convertDeclaredValue(value, definition.Type)
	if err != nil {
		return nil, fail("schema_type", "field does not satisfy its declared type")
	}
	if definition.Sensitive {
		return "[REDACTED]", nil
	}
	if len(definition.Enum) > 0 {
		matched := false
		for _, allowed := range definition.Enum {
			if fmt.Sprint(converted) == allowed {
				matched = true
				break
			}
		}
		if !matched {
			return nil, fail("schema_enum", "field is outside its configured enum")
		}
	}
	if definition.Range != nil {
		number, ok := numericValue(converted)
		if !ok {
			return nil, fail("schema_range", "range-constrained field is not numeric")
		}
		if definition.Range.Min != nil && number < *definition.Range.Min {
			return nil, fail("schema_range", "field is below its configured range")
		}
		if definition.Range.Max != nil && number > *definition.Range.Max {
			return nil, fail("schema_range", "field is above its configured range")
		}
	}
	return converted, nil
}

func convertDeclaredValue(value interface{}, target FieldType) (interface{}, error) {
	target = normalizeFieldType(string(target))
	if target == "" || target == FieldTypeString {
		text, ok := value.(string)
		if ok {
			return text, nil
		}
		return fmt.Sprint(value), nil
	}
	switch target {
	case FieldTypeInt:
		switch typed := value.(type) {
		case int:
			return typed, nil
		case int8:
			return int(typed), nil
		case int16:
			return int(typed), nil
		case int32:
			return int(typed), nil
		case int64:
			return int(typed), nil
		case json.Number:
			number, err := typed.Int64()
			return int(number), err
		case float64:
			if typed != float64(int64(typed)) {
				return nil, fmt.Errorf("not an integer")
			}
			return int(typed), nil
		case string:
			number, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
			return int(number), err
		}
	case FieldTypeFloat:
		switch typed := value.(type) {
		case float64:
			return typed, nil
		case float32:
			return float64(typed), nil
		case int:
			return float64(typed), nil
		case int64:
			return float64(typed), nil
		case json.Number:
			return typed.Float64()
		case string:
			return strconv.ParseFloat(strings.TrimSpace(typed), 64)
		}
	case FieldTypeBool:
		if typed, ok := value.(bool); ok {
			return typed, nil
		}
		if typed, ok := value.(string); ok {
			return strconv.ParseBool(strings.TrimSpace(typed))
		}
	case FieldTypeTime:
		text, ok := value.(string)
		if !ok {
			break
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "20060102 150405.000-0700", "20060102"} {
			if _, err := time.Parse(layout, text); err == nil {
				return text, nil
			}
		}
	case FieldTypeIP:
		text, ok := value.(string)
		if ok && net.ParseIP(text) != nil {
			return text, nil
		}
	case FieldTypeURL:
		text, ok := value.(string)
		if ok {
			parsed, err := url.ParseRequestURI(text)
			if err == nil && parsed.Scheme != "" {
				return text, nil
			}
		}
	case FieldTypeEmail:
		text, ok := value.(string)
		if ok {
			address, err := mail.ParseAddress(text)
			if err == nil && address.Address == text {
				return text, nil
			}
		}
	}
	return nil, fmt.Errorf("value does not match declared type")
}

func normalizeFieldType(value string) FieldType {
	switch strings.ToLower(value) {
	case "int", "integer":
		return FieldTypeInt
	case "float", "number":
		return FieldTypeFloat
	case "bool", "boolean":
		return FieldTypeBool
	case "time", "timestamp":
		return FieldTypeTime
	case "ip":
		return FieldTypeIP
	case "url":
		return FieldTypeURL
	case "email":
		return FieldTypeEmail
	case "string":
		return FieldTypeString
	default:
		return FieldType(value)
	}
}

func numericValue(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	default:
		return 0, false
	}
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func validateStaticRuntimeLimits(config staticlayout.Config, limits ResourceLimits) error {
	for _, token := range config.Tokens {
		switch token.Type {
		case staticlayout.TokenTypeRegex:
			var regexConfig staticlayout.RegexConfig
			if err := decodeStrictConfig(token.Config, &regexConfig); err != nil {
				return fail("static_config", "invalid static regex token configuration")
			}
			compiled, err := regexp.Compile(regexConfig.Pattern)
			if err != nil || len(regexConfig.Pattern) > limits.MaxRegexLength || compiled.NumSubexp() > limits.MaxCaptures {
				return fail("capture_limit", "static regex token exceeded pack limits")
			}
		case staticlayout.TokenTypeGrok:
			var grokConfig staticlayout.GrokConfig
			if err := decodeStrictConfig(token.Config, &grokConfig); err != nil {
				return fail("static_config", "invalid static grok token configuration")
			}
			compiled, err := grok.New(grokConfig.Patterns).Compile(grokConfig.Pattern)
			if err != nil || len(compiled.RE.String()) > limits.MaxRegexLength || compiled.RE.NumSubexp() > limits.MaxCaptures {
				return fail("capture_limit", "static grok token exceeded pack limits")
			}
		}
	}
	return nil
}
