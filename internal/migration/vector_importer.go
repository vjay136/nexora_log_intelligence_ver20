package migration

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	parserpolicy "nexora.local/nli-v20/parser-policy"
)

type vectorConfig struct {
	Sources    map[string]vectorSource    `toml:"sources"`
	Transforms map[string]vectorTransform `toml:"transforms"`
	Sinks      map[string]vectorSink      `toml:"sinks"`
}

type vectorSource struct {
	Type    string   `toml:"type"`
	Include []string `toml:"include"`
	Exclude []string `toml:"exclude"`
}

type vectorTransform struct {
	Type      string   `toml:"type"`
	Inputs    []string `toml:"inputs"`
	Source    string   `toml:"source"`
	Condition string   `toml:"condition"`
}

type vectorSink struct {
	Type   string   `toml:"type"`
	Inputs []string `toml:"inputs"`
}

type vectorImporter struct {
	file      UploadedFile
	config    vectorConfig
	metadata  toml.MetaData
	result    MigrationResult
	stageSeq  int
	ruleSeq   int
	sections  map[string]SourceRange
	lineStart []int
}

func translateVectorSource(file UploadedFile) MigrationResult {
	result := MigrationResult{SourceFile: file.Name, SourceFormat: "vector", SourceSHA256: file.Checksum, ParityStatus: ParityUnverified}
	var config vectorConfig
	metadata, err := toml.Decode(string(file.Content), &config)
	if err != nil {
		result.Errors = []string{"parse Vector TOML: " + err.Error()}
		return result
	}
	importer := &vectorImporter{
		file: file, config: config, metadata: metadata, result: result,
		sections: sectionRanges(file.Content), lineStart: lineOffsets(file.Content),
	}
	importer.result.Parsed = true
	importer.recordSourcesAndSinks()
	rules := importer.translateTransforms()
	if len(rules) == 0 {
		importer.result.Success = true
		importer.result.Warnings = append(importer.result.Warnings, "Vector source contains no executable declarative transform")
		return importer.result
	}
	pack := &parserpolicy.ParserPack{
		ID:      identifier("vector_" + strings.TrimSuffix(baseName(file.Name), extension(file.Name))),
		Version: "20.0.0", Revision: 1, SourceType: "migrated", SourceHash: file.Checksum,
		Author: "nli-migrate", ReviewState: parserpolicy.ReviewStateDraft, Rules: rules,
		ResourceLimits: parserpolicy.ResourceLimits{
			MaxInputBytes: 1 << 20, MaxFields: 512, MaxCaptures: 256, MaxRegexLength: 4096,
			MaxStageCount: parserpolicy.MaxStages, MaxAttempts: 20, ExecutionDeadlineMs: 5000,
		}, Metadata: map[string]string{"source_file": file.Name, "source_format": "vector-toml-vrl"},
	}
	importer.result.ParserPack = pack
	importer.result.Success = true
	return importer.result
}

func (importer *vectorImporter) recordSourcesAndSinks() {
	for _, name := range sortedMapKeys(importer.config.Sources) {
		rng := importer.sectionRange("sources." + name)
		importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: "vector.source", Name: name, Range: rng, Status: "metadata"})
		importer.result.LineMappings = append(importer.result.LineMappings, LineMapping{SourceFile: importer.file.Name, Construct: "vector.source", Range: rng, Status: "metadata"})
	}
	for _, name := range sortedMapKeys(importer.config.Sinks) {
		rng := importer.sectionRange("sinks." + name)
		importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: "vector.sink", Name: name, Range: rng, Status: "metadata"})
		importer.result.LineMappings = append(importer.result.LineMappings, LineMapping{SourceFile: importer.file.Name, Construct: "vector.sink", Range: rng, Status: "metadata"})
	}
}

func (importer *vectorImporter) translateTransforms() []parserpolicy.ParserRule {
	ordered := importer.transformOrder()
	rules := make([]parserpolicy.ParserRule, 0, len(ordered))
	for _, name := range ordered {
		transform := importer.config.Transforms[name]
		importer.ruleSeq++
		ruleID := identifier(fmt.Sprintf("vector_%s_%d", name, importer.ruleSeq))
		rng := importer.sectionRange("transforms." + name)
		stages := importer.translateTransform(name, transform, ruleID, rng)
		if len(stages) == 0 {
			continue
		}
		if len(stages) > parserpolicy.MaxStages {
			importer.addUnsupported("resource.stage_count", fmt.Sprintf("transform generated %d stages; maximum is %d", len(stages), parserpolicy.MaxStages), rng, "split the VRL transform at reviewed semantic boundaries")
			stages = stages[:parserpolicy.MaxStages]
		}
		service := serviceFromName(name)
		if service == "*" && len(transform.Inputs) > 0 {
			service = serviceFromName(transform.Inputs[0])
		}
		rules = append(rules, parserpolicy.ParserRule{
			ID: ruleID, Name: "Migrated Vector transform " + name, Priority: importer.ruleSeq * 100,
			ServiceSelectors: []string{service}, Stages: stages, FailurePolicy: parserpolicy.FailurePolicyRejectRule,
			ProjectionMode: parserpolicy.ProjectionModeAll, CountPolicy: parserpolicy.CountPolicyAllowDerived,
		})
	}
	return rules
}

func (importer *vectorImporter) transformOrder() []string {
	seen := map[string]bool{}
	ordered := make([]string, 0, len(importer.config.Transforms))
	for _, key := range importer.metadata.Keys() {
		if len(key) >= 2 && key[0] == "transforms" && !seen[key[1]] {
			if _, exists := importer.config.Transforms[key[1]]; exists {
				seen[key[1]] = true
				ordered = append(ordered, key[1])
			}
		}
	}
	for _, name := range sortedMapKeys(importer.config.Transforms) {
		if !seen[name] {
			ordered = append(ordered, name)
		}
	}
	return ordered
}

func (importer *vectorImporter) translateTransform(name string, transform vectorTransform, ruleID string, section SourceRange) []parserpolicy.Stage {
	kind := "vector.transform." + strings.ToLower(transform.Type)
	importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: kind, Name: name, Range: section, IRRuleID: ruleID, Status: "metadata"})
	importer.result.LineMappings = append(importer.result.LineMappings, LineMapping{SourceFile: importer.file.Name, Construct: kind, Range: section, IRRuleID: ruleID, Status: "metadata"})
	switch strings.ToLower(transform.Type) {
	case "remap":
		baseLine, baseOffset := importer.sourceStart(name)
		program, err := parseVRL(transform.Source, baseLine, baseOffset)
		if err != nil {
			importer.addUnsupported("vrl.syntax", err.Error(), section, "repair the VRL syntax before import")
			return nil
		}
		return importer.translateVRL(program, ruleID)
	case "filter":
		return importer.translateFilter(transform.Condition, ruleID, section)
	default:
		importer.addUnsupported(kind, "Vector transform type is not supported", section, "add a reviewed declarative translation for this transform")
		return nil
	}
}

func (importer *vectorImporter) translateFilter(condition, ruleID string, rng SourceRange) []parserpolicy.Stage {
	program, err := parseVRL(condition, rng.Start.Line, rng.Start.Offset)
	if err != nil {
		importer.addUnsupported("vector.filter", "invalid filter condition: "+err.Error(), rng, "repair the condition before import")
		return nil
	}
	field, operator, value, ok := simpleCondition(program.tokens)
	if !ok {
		importer.addUnsupported("vector.filter", "compound filter condition cannot be represented by one flat conditional stage", rng, "extend parser-policy with boolean conditional expressions")
		return nil
	}
	stageID := importer.nextStageID()
	stage := parserpolicy.Stage{ID: stageID, Name: "Vector filter", Type: parserpolicy.StageTypeConditionalMatch, Config: map[string]interface{}{"field": field, "operator": operator, "value": value}, FailurePolicy: parserpolicy.FailurePolicyRejectRule}
	importer.recordTranslated("vrl.filter", ruleID, stageID, rng)
	return []parserpolicy.Stage{stage}
}

func (importer *vectorImporter) translateVRL(program *vrlProgram, ruleID string) []parserpolicy.Stage {
	stages := make([]parserpolicy.Stage, 0)
	variables := program.literalAssignments()
	for _, operation := range program.operations {
		rng := operation.Range
		stageID := importer.nextStageID()
		appendStage := func(kind string, stage parserpolicy.Stage) {
			stage.ID = stageID
			stages = append(stages, stage)
			importer.recordTranslated(kind, ruleID, stageID, rng)
		}
		unsupported := func(kind, description, repair string) {
			importer.addUnsupported(kind, description, rng, repair)
			importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: kind, Range: rng, IRRuleID: ruleID, Status: "unsupported"})
			importer.result.LineMappings = append(importer.result.LineMappings, LineMapping{SourceFile: importer.file.Name, Construct: kind, Range: rng, IRRuleID: ruleID, Status: "unsupported"})
		}

		switch operation.Name {
		case "parse_regex":
			if len(operation.Args) < 2 {
				unsupported("vrl.parse_regex", "parse_regex requires input and pattern", "supply a literal RE2 pattern")
				continue
			}
			input := normalizeVRLField(operation.Args[0].Text)
			pattern, ok := argumentLiteral(operation.Args[1], variables)
			if !ok {
				unsupported("vrl.parse_regex", "dynamic regex patterns are not imported", "replace the dynamic pattern with a reviewed literal")
				continue
			}
			compiled, err := regexp.Compile(pattern)
			if err != nil || len(pattern) > parserpolicy.MaxRegexLength {
				message := "regex exceeds limits"
				if err != nil {
					message = "regex is not RE2-compatible: " + err.Error()
				}
				unsupported("vrl.parse_regex", message, "rewrite lookarounds/backreferences without semantic approximation")
				continue
			}
			groups := map[string]string{}
			for _, group := range compiled.SubexpNames() {
				if group != "" {
					groups[group] = group
				}
			}
			appendStage("vrl.parse_regex", parserpolicy.Stage{Name: "Vector parse_regex", Type: parserpolicy.StageTypeRegex, Config: map[string]interface{}{"input_field": input, "pattern": pattern, "groups": groups}, FailurePolicy: parserpolicy.FailurePolicyRejectRule})
		case "parse_key_value":
			if len(operation.Args) == 0 {
				unsupported("vrl.parse_key_value", "parse_key_value requires an input", "supply an explicit input field")
				continue
			}
			assignment, pair := "=", " "
			for _, argument := range operation.Args[1:] {
				switch argument.Name {
				case "key_value_delimiter":
					if value, ok := argumentLiteral(argument, variables); ok {
						assignment = value
					}
				case "field_delimiter":
					if value, ok := argumentLiteral(argument, variables); ok {
						pair = value
					}
				}
			}
			appendStage("vrl.parse_key_value", parserpolicy.Stage{Name: "Vector parse_key_value", Type: parserpolicy.StageTypeMixedKV, Config: map[string]interface{}{
				"input_field": normalizeVRLField(operation.Args[0].Text), "pair_separators": []string{pair}, "assignment_separators": []string{assignment},
				"quote_characters": "\"'", "escape_character": "\\", "key_pattern": "^[A-Za-z_][A-Za-z0-9_.-]*$", "duplicate_policy": "last",
			}, FailurePolicy: parserpolicy.FailurePolicyRejectRule})
		case "parse_timestamp":
			if len(operation.Args) < 2 || operation.Target == "" {
				unsupported("vrl.parse_timestamp", "timestamp conversion requires an assigned target and literal format", "declare the input, output, and format explicitly")
				continue
			}
			layout, ok := argumentLiteral(operation.Args[1], variables)
			if !ok {
				unsupported("vrl.parse_timestamp", "dynamic timestamp format is unsupported", "use an explicit reviewed timestamp layout")
				continue
			}
			appendStage("vrl.parse_timestamp", parserpolicy.Stage{Name: "Vector parse_timestamp", Type: parserpolicy.StageTypeTimestamp, Config: map[string]interface{}{
				"input_field": normalizeVRLField(operation.Args[0].Text), "output_field": normalizeVRLField(operation.Target), "layouts": []string{vectorTimeLayout(layout)},
			}, FailurePolicy: parserpolicy.FailurePolicyRejectRule})
		case "to_string", "to_int":
			target := normalizeVRLField(operation.Target)
			if target == "" || !safeConversionField(target) {
				unsupported("vrl."+operation.Name, "conversion target is not a parser-policy field", "assign the conversion directly to a safe event field")
				continue
			}
			conversion := "string"
			if operation.Name == "to_int" {
				conversion = "int"
			}
			appendStage("vrl."+operation.Name, parserpolicy.Stage{Name: "Vector " + operation.Name, Type: parserpolicy.StageTypeTypeConversion, Config: map[string]interface{}{"conversions": map[string]string{target: conversion}}, FailurePolicy: parserpolicy.FailurePolicyContinue})
		case "replace":
			if len(operation.Args) < 3 {
				unsupported("vrl.replace", "replace requires value, pattern, and replacement", "supply literal bounded replacement arguments")
				continue
			}
			field := normalizeVRLField(operation.Args[0].Text)
			pattern, patternOK := argumentLiteral(operation.Args[1], variables)
			replacement, replacementOK := argumentLiteral(operation.Args[2], variables)
			if field == "" || !patternOK || !replacementOK {
				unsupported("vrl.replace", "dynamic replace arguments cannot be represented exactly", "use an event field and literal pattern/replacement")
				continue
			}
			isRegex := operation.Args[1].RawRegex
			if isRegex {
				if _, err := regexp.Compile(pattern); err != nil {
					unsupported("vrl.replace", "replacement regex is not RE2-compatible: "+err.Error(), "rewrite the expression for RE2")
					continue
				}
			}
			appendStage("vrl.replace", parserpolicy.Stage{Name: "Vector replace", Type: parserpolicy.StageTypeReplace, Config: map[string]interface{}{"field": field, "pattern": pattern, "replacement": replacement, "regex": isRegex}, FailurePolicy: parserpolicy.FailurePolicyContinue})
		case "del":
			if len(operation.Args) == 0 {
				unsupported("vrl.del", "del requires a field", "supply a static event field")
				continue
			}
			field := normalizeVRLField(operation.Args[0].Text)
			appendStage("vrl.del", parserpolicy.Stage{Name: "Vector del", Type: parserpolicy.StageTypeFieldProjection, Config: map[string]interface{}{"mode": "denylist", "denied_fields": []string{field}}, FailurePolicy: parserpolicy.FailurePolicyContinue})
		case "format_timestamp", "merge":
			unsupported("vrl."+operation.Name, "operation has no exact executable parser-policy equivalent", "extend parser-policy with this operation before activation")
		case "if":
			unsupported("vrl.conditional", "nested conditional blocks cannot be flattened safely", "extend parser-policy with scoped conditional blocks")
		case "null_coalescing":
			unsupported("vrl.null_coalescing", "null coalescing cannot be represented by flat stages", "extend parser-policy with typed null-coalescing semantics")
		default:
			unsupported("vrl.function."+operation.Name, "VRL function is outside the supported translation subset", "implement and review a declarative equivalent")
		}
	}
	return stages
}

func (importer *vectorImporter) recordTranslated(kind, ruleID, stageID string, rng SourceRange) {
	importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: kind, Range: rng, IRRuleID: ruleID, IRStageID: stageID, Status: "translated"})
	importer.result.LineMappings = append(importer.result.LineMappings, LineMapping{SourceFile: importer.file.Name, Construct: kind, Range: rng, IRRuleID: ruleID, IRStageID: stageID, Status: "translated"})
}

func (importer *vectorImporter) addUnsupported(kind, description string, rng SourceRange, repair string) {
	importer.result.UnsupportedConstructs = append(importer.result.UnsupportedConstructs, UnsupportedConstruct{Type: kind, Description: description, Range: rng, Severity: "error", Repair: repair})
}

func (importer *vectorImporter) nextStageID() string {
	importer.stageSeq++
	return fmt.Sprintf("stage_%d", importer.stageSeq)
}

func (importer *vectorImporter) sectionRange(name string) SourceRange {
	if value, exists := importer.sections[name]; exists {
		return value
	}
	return SourceRange{Start: SourcePosition{Line: 1, Column: 1}, End: SourcePosition{Line: 1, Column: 1}}
}

func (importer *vectorImporter) sourceStart(name string) (int, int) {
	lines := strings.Split(string(importer.file.Content), "\n")
	section := "[transforms." + name + "]"
	inside := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inside = trimmed == section
			continue
		}
		if inside && strings.HasPrefix(trimmed, "source") && strings.Contains(trimmed, "'''") {
			lineNumber := index + 2
			offset := 0
			if lineNumber-1 < len(importer.lineStart) {
				offset = importer.lineStart[lineNumber-1]
			}
			return lineNumber, offset
		}
	}
	rng := importer.sectionRange("transforms." + name)
	return rng.Start.Line, rng.Start.Offset
}

func sectionRanges(data []byte) map[string]SourceRange {
	lines := strings.Split(string(data), "\n")
	offsets := lineOffsets(data)
	type section struct {
		name string
		line int
	}
	sections := make([]section, 0)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") && !strings.HasPrefix(trimmed, "[[") {
			sections = append(sections, section{name: strings.TrimSpace(trimmed[1 : len(trimmed)-1]), line: index})
		}
	}
	result := map[string]SourceRange{}
	for index, current := range sections {
		endLine := len(lines) - 1
		if index+1 < len(sections) {
			endLine = sections[index+1].line - 1
		}
		startOffset := offsets[current.line]
		endOffset := len(data)
		if endLine+1 < len(offsets) {
			endOffset = offsets[endLine+1]
		}
		result[current.name] = SourceRange{
			Start: SourcePosition{Line: current.line + 1, Column: 1, Offset: startOffset},
			End:   SourcePosition{Line: endLine + 1, Column: len(lines[endLine]) + 1, Offset: endOffset},
		}
	}
	return result
}

func lineOffsets(data []byte) []int {
	offsets := []int{0}
	for index, value := range data {
		if value == '\n' {
			offsets = append(offsets, index+1)
		}
	}
	return offsets
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func normalizeVRLField(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, ".")
	value = strings.Trim(value, "()")
	return value
}

func safeConversionField(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if !(unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_') {
			return false
		}
	}
	return true
}

func vectorTimeLayout(value string) string {
	replacer := strings.NewReplacer("%Y", "2006", "%m", "01", "%d", "02", "%H", "15", "%M", "04", "%S", "05", "%z", "-0700", "%f", "000000", "%.3f", "000")
	return replacer.Replace(value)
}

type vrlTokenKind uint8

const (
	vrlIdentifier vrlTokenKind = iota
	vrlField
	vrlString
	vrlNumber
	vrlOperator
	vrlPunctuation
	vrlNewline
)

type vrlToken struct {
	Kind     vrlTokenKind
	Text     string
	Line     int
	Column   int
	Offset   int
	RawRegex bool
}

type vrlArgument struct {
	Name     string
	Text     string
	Tokens   []vrlToken
	RawRegex bool
}

type vrlOperation struct {
	Name   string
	Target string
	Args   []vrlArgument
	Range  SourceRange
}

type vrlProgram struct {
	tokens     []vrlToken
	operations []vrlOperation
}

func parseVRL(source string, baseLine, baseOffset int) (*vrlProgram, error) {
	tokens, err := lexVRL(source, baseLine, baseOffset)
	if err != nil {
		return nil, err
	}
	program := &vrlProgram{tokens: tokens}
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		if token.Kind == vrlIdentifier && token.Text == "if" {
			program.operations = append(program.operations, vrlOperation{Name: "if", Range: tokenRange(token, token)})
			continue
		}
		if token.Kind == vrlOperator && token.Text == "??" {
			program.operations = append(program.operations, vrlOperation{Name: "null_coalescing", Range: tokenRange(token, token)})
			continue
		}
		if token.Kind != vrlIdentifier {
			continue
		}
		open := index + 1
		if open < len(tokens) && tokens[open].Kind == vrlOperator && tokens[open].Text == "!" {
			open++
		}
		if open >= len(tokens) || tokens[open].Text != "(" {
			continue
		}
		close, ok := matchingToken(tokens, open, "(", ")")
		if !ok {
			return nil, fmt.Errorf("unclosed call %s at line %d, column %d", token.Text, token.Line, token.Column)
		}
		program.operations = append(program.operations, vrlOperation{
			Name: token.Text, Target: assignmentTarget(tokens, index), Args: splitArguments(tokens[open+1 : close]), Range: tokenRange(token, tokens[close]),
		})
	}
	return program, nil
}

func lexVRL(source string, baseLine, baseOffset int) ([]vrlToken, error) {
	line, column, offset := baseLine, 1, baseOffset
	tokens := make([]vrlToken, 0, len(source)/4)
	for index := 0; index < len(source); {
		start, startLine, startColumn, startOffset := index, line, column, offset
		char := source[index]
		if char == '\n' {
			tokens = append(tokens, vrlToken{Kind: vrlNewline, Text: "\n", Line: line, Column: column, Offset: offset})
			index, line, column, offset = index+1, line+1, 1, offset+1
			continue
		}
		if char == ' ' || char == '\t' || char == '\r' {
			index, column, offset = index+1, column+1, offset+1
			continue
		}
		if char == '#' {
			for index < len(source) && source[index] != '\n' {
				index, column, offset = index+1, column+1, offset+1
			}
			continue
		}
		rawRegex := char == 'r' && index+1 < len(source) && (source[index+1] == '\'' || source[index+1] == '"')
		if rawRegex {
			index, column, offset = index+1, column+1, offset+1
			char = source[index]
			start = index
		}
		if char == '\'' || char == '"' {
			quote := char
			index, column, offset = index+1, column+1, offset+1
			var builder strings.Builder
			escaped := false
			for index < len(source) {
				current := source[index]
				if current == '\n' && quote != '`' {
					return nil, fmt.Errorf("unterminated string at line %d, column %d", startLine, startColumn)
				}
				if !rawRegex && escaped {
					builder.WriteByte(current)
					escaped = false
					index, column, offset = index+1, column+1, offset+1
					continue
				}
				if !rawRegex && current == '\\' {
					escaped = true
					builder.WriteByte(current)
					index, column, offset = index+1, column+1, offset+1
					continue
				}
				if current == quote {
					index, column, offset = index+1, column+1, offset+1
					tokens = append(tokens, vrlToken{Kind: vrlString, Text: decodeVRLString(builder.String(), rawRegex), Line: startLine, Column: startColumn, Offset: startOffset, RawRegex: rawRegex})
					break
				}
				builder.WriteByte(current)
				index, column, offset = index+1, column+1, offset+1
			}
			if index >= len(source) && (len(tokens) == 0 || tokens[len(tokens)-1].Offset != startOffset) {
				return nil, fmt.Errorf("unterminated string at line %d, column %d", startLine, startColumn)
			}
			continue
		}
		if char == '.' && index+1 < len(source) && isVRLIdentifierStart(rune(source[index+1])) {
			index++
			for index < len(source) && isVRLFieldRune(rune(source[index])) {
				index++
			}
			text := source[start:index]
			tokens = append(tokens, vrlToken{Kind: vrlField, Text: text, Line: startLine, Column: startColumn, Offset: startOffset})
			width := index - start
			column, offset = column+width, offset+width
			continue
		}
		r, width := utf8.DecodeRuneInString(source[index:])
		if isVRLIdentifierStart(r) {
			index += width
			for index < len(source) {
				next, nextWidth := utf8.DecodeRuneInString(source[index:])
				if !isVRLIdentifierRune(next) {
					break
				}
				index += nextWidth
			}
			text := source[start:index]
			tokens = append(tokens, vrlToken{Kind: vrlIdentifier, Text: text, Line: startLine, Column: startColumn, Offset: startOffset})
			column, offset = column+(index-start), offset+(index-start)
			continue
		}
		if unicode.IsDigit(r) {
			index += width
			for index < len(source) && (source[index] == '.' || (source[index] >= '0' && source[index] <= '9')) {
				index++
			}
			text := source[start:index]
			tokens = append(tokens, vrlToken{Kind: vrlNumber, Text: text, Line: startLine, Column: startColumn, Offset: startOffset})
			column, offset = column+(index-start), offset+(index-start)
			continue
		}
		text := string(char)
		if index+1 < len(source) {
			pair := source[index : index+2]
			if pair == "??" || pair == "==" || pair == "!=" || pair == "||" || pair == "&&" || pair == ">=" || pair == "<=" {
				text = pair
				index++
			}
		}
		kind := vrlPunctuation
		if strings.Contains("=!?+-*/<>|&", string(text[0])) {
			kind = vrlOperator
		}
		tokens = append(tokens, vrlToken{Kind: kind, Text: text, Line: startLine, Column: startColumn, Offset: startOffset})
		index, column, offset = index+1, column+len(text), offset+len(text)
	}
	if err := validateVRLBalance(tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

func decodeVRLString(value string, raw bool) string {
	if raw {
		return value
	}
	decoded, err := strconv.Unquote("\"" + strings.ReplaceAll(value, "\"", "\\\"") + "\"")
	if err == nil {
		return decoded
	}
	return value
}

func validateVRLBalance(tokens []vrlToken) error {
	stack := make([]vrlToken, 0)
	closing := map[string]string{"(": ")", "[": "]", "{": "}"}
	for _, token := range tokens {
		if _, opens := closing[token.Text]; opens {
			stack = append(stack, token)
			continue
		}
		if token.Text == ")" || token.Text == "]" || token.Text == "}" {
			if len(stack) == 0 || closing[stack[len(stack)-1].Text] != token.Text {
				return fmt.Errorf("unbalanced %s at line %d, column %d", token.Text, token.Line, token.Column)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		token := stack[len(stack)-1]
		return fmt.Errorf("unclosed %s at line %d, column %d", token.Text, token.Line, token.Column)
	}
	return nil
}

func matchingToken(tokens []vrlToken, open int, left, right string) (int, bool) {
	depth := 0
	for index := open; index < len(tokens); index++ {
		if tokens[index].Text == left {
			depth++
		} else if tokens[index].Text == right {
			depth--
			if depth == 0 {
				return index, true
			}
		}
	}
	return 0, false
}

func splitArguments(tokens []vrlToken) []vrlArgument {
	parts := make([][]vrlToken, 0)
	start, depth := 0, 0
	for index, token := range tokens {
		switch token.Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
		case ",":
			if depth == 0 {
				parts = append(parts, tokens[start:index])
				start = index + 1
			}
		}
	}
	if start < len(tokens) {
		parts = append(parts, tokens[start:])
	}
	arguments := make([]vrlArgument, 0, len(parts))
	for _, part := range parts {
		part = trimVRLTokens(part)
		if len(part) == 0 {
			continue
		}
		argument := vrlArgument{Tokens: part, Text: joinVRLTokens(part)}
		for index, token := range part {
			if token.Text == ":" && index > 0 {
				argument.Name = part[0].Text
				argument.Tokens = trimVRLTokens(part[index+1:])
				argument.Text = joinVRLTokens(argument.Tokens)
				break
			}
		}
		if len(argument.Tokens) == 1 && argument.Tokens[0].Kind == vrlString {
			argument.Text = argument.Tokens[0].Text
			argument.RawRegex = argument.Tokens[0].RawRegex
		}
		arguments = append(arguments, argument)
	}
	return arguments
}

func assignmentTarget(tokens []vrlToken, callIndex int) string {
	start := callIndex - 1
	for start >= 0 && tokens[start].Kind != vrlNewline && tokens[start].Text != ";" && tokens[start].Text != "{" && tokens[start].Text != "}" {
		start--
	}
	segment := tokens[start+1 : callIndex]
	assignment := -1
	depth := 0
	for index, token := range segment {
		switch token.Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
		case "=":
			if depth == 0 {
				assignment = index
			}
		}
	}
	if assignment < 0 {
		return ""
	}
	left := trimVRLTokens(segment[:assignment])
	if len(left) == 0 {
		return ""
	}
	for _, token := range left {
		if token.Kind == vrlField {
			return token.Text
		}
	}
	return left[0].Text
}

func (program *vrlProgram) literalAssignments() map[string]string {
	values := map[string]string{}
	for index, token := range program.tokens {
		if token.Text != "=" || index == 0 || index+1 >= len(program.tokens) {
			continue
		}
		left, right := program.tokens[index-1], program.tokens[index+1]
		if left.Kind == vrlIdentifier && right.Kind == vrlString {
			values[left.Text] = right.Text
		}
	}
	return values
}

func argumentLiteral(argument vrlArgument, variables map[string]string) (string, bool) {
	if len(argument.Tokens) == 1 {
		token := argument.Tokens[0]
		if token.Kind == vrlString {
			return token.Text, true
		}
		if token.Kind == vrlIdentifier {
			value, ok := variables[token.Text]
			return value, ok
		}
	}
	return "", false
}

func simpleCondition(tokens []vrlToken) (string, string, string, bool) {
	filtered := make([]vrlToken, 0, len(tokens))
	for _, token := range tokens {
		if token.Kind != vrlNewline {
			filtered = append(filtered, token)
		}
	}
	if len(filtered) != 3 || filtered[0].Kind != vrlField || filtered[2].Kind != vrlString {
		return "", "", "", false
	}
	operator := "equals"
	if filtered[1].Text != "==" {
		return "", "", "", false
	}
	return normalizeVRLField(filtered[0].Text), operator, filtered[2].Text, true
}

func tokenRange(first, last vrlToken) SourceRange {
	return SourceRange{
		Start: SourcePosition{Line: first.Line, Column: first.Column, Offset: first.Offset},
		End:   SourcePosition{Line: last.Line, Column: last.Column + len(last.Text), Offset: last.Offset + len(last.Text)},
	}
}

func trimVRLTokens(tokens []vrlToken) []vrlToken {
	for len(tokens) > 0 && tokens[0].Kind == vrlNewline {
		tokens = tokens[1:]
	}
	for len(tokens) > 0 && tokens[len(tokens)-1].Kind == vrlNewline {
		tokens = tokens[:len(tokens)-1]
	}
	return tokens
}

func joinVRLTokens(tokens []vrlToken) string {
	var builder strings.Builder
	for _, token := range tokens {
		if token.Kind == vrlNewline {
			builder.WriteByte(' ')
		} else {
			builder.WriteString(token.Text)
		}
	}
	return builder.String()
}

func isVRLIdentifierStart(value rune) bool {
	return value == '_' || unicode.IsLetter(value)
}

func isVRLIdentifierRune(value rune) bool {
	return value == '_' || value == '-' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

func isVRLFieldRune(value rune) bool {
	return value == '.' || isVRLIdentifierRune(value)
}
