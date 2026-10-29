package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/grafana/river/ast"
	riverparser "github.com/grafana/river/parser"
	"github.com/grafana/river/token"
	parserpolicy "nexora.local/nli-v20/parser-policy"
)

type alloyImporter struct {
	fileName     string
	source       []byte
	result       MigrationResult
	rules        []parserpolicy.ParserRule
	ruleSequence int
	stageSeq     int
}

func translateAlloySource(file UploadedFile) MigrationResult {
	result := MigrationResult{SourceFile: file.Name, SourceFormat: "alloy", SourceSHA256: file.Checksum, ParityStatus: ParityUnverified}
	parsed, err := riverparser.ParseFile(file.Name, file.Content)
	if err != nil {
		result.Errors = []string{"parse Alloy/River HCL: " + err.Error()}
		return result
	}
	importer := &alloyImporter{fileName: file.Name, source: file.Content, result: result}
	importer.result.Parsed = true
	importer.walkBody(parsed.Body, false)
	if len(importer.generatedRules()) == 0 {
		importer.result.Success = true
		importer.result.Warnings = append(importer.result.Warnings, "source is orchestration/metadata only; no parser-policy rule was generated")
		return importer.result
	}
	pack := &parserpolicy.ParserPack{
		ID:      identifier("alloy_" + strings.TrimSuffix(baseName(file.Name), extension(file.Name))),
		Version: "20.0.0", Revision: 1, SourceType: "migrated", SourceHash: file.Checksum,
		Author: "nli-migrate", ReviewState: parserpolicy.ReviewStateDraft,
		Rules: importer.generatedRules(), ResourceLimits: parserpolicy.ResourceLimits{
			MaxInputBytes: 1 << 20, MaxFields: 512, MaxCaptures: 256, MaxRegexLength: 4096,
			MaxStageCount: parserpolicy.MaxStages, MaxAttempts: 20, ExecutionDeadlineMs: 5000,
		}, Metadata: map[string]string{"source_file": file.Name, "source_format": "alloy-river"},
	}
	importer.result.ParserPack = pack
	importer.result.Success = true
	return importer.result
}

func (importer *alloyImporter) generatedRules() []parserpolicy.ParserRule {
	return append([]parserpolicy.ParserRule(nil), importer.rules...)
}

func (importer *alloyImporter) addRule(rule parserpolicy.ParserRule) {
	importer.rules = append(importer.rules, rule)
}

func (importer *alloyImporter) walkBody(body ast.Body, insideProcess bool) {
	for _, statement := range body {
		block, ok := statement.(*ast.BlockStmt)
		if !ok {
			continue
		}
		name := block.GetBlockName()
		rng := importer.nodeRange(block)
		switch name {
		case "declare":
			importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: name, Name: block.Label, Range: rng, Status: "metadata"})
			importer.result.LineMappings = append(importer.result.LineMappings, LineMapping{SourceFile: importer.fileName, Construct: name, Range: rng, Status: "metadata"})
			importer.walkBody(block.Body, false)
		case "loki.process":
			importer.translateProcess(block)
		case "loki.source.file":
			importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: name, Name: block.Label, Range: rng, Status: "metadata"})
			importer.result.LineMappings = append(importer.result.LineMappings, LineMapping{SourceFile: importer.fileName, Construct: name, Range: rng, Status: "metadata"})
		case "import.file":
			importer.unsupported(name, "external module reference is preserved but not resolved inside a single-file import", block, "import the referenced processor in the same bounded batch")
			importer.walkBody(block.Body, false)
		case "loki.relabel", "loki.write", "export":
			importer.unsupported(name, "transport/relabel metadata is not executable parser-policy behavior", block, "persist this metadata in the controller routing catalog")
		case "logging", "argument":
			importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: name, Name: block.Label, Range: rng, Status: "metadata"})
		default:
			if strings.HasPrefix(name, "stage.") && !insideProcess {
				importer.unsupported(name, "stage occurs outside a loki.process block", block, "move the stage into an explicit process pipeline")
			} else if !insideProcess {
				importer.unsupported(name, "component invocation is preserved as orchestration metadata", block, "resolve the imported component and migrate its source together")
			}
		}
	}
}

func (importer *alloyImporter) translateProcess(block *ast.BlockStmt) {
	importer.ruleSequence++
	service := serviceFromName(block.Label)
	processRange := importer.nodeRange(block)
	processID := identifier(fmt.Sprintf("alloy_%s_%d", block.Label, importer.ruleSequence))
	importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: "loki.process", Name: block.Label, Range: processRange, IRRuleID: processID, Status: "metadata"})
	importer.result.LineMappings = append(importer.result.LineMappings, LineMapping{SourceFile: importer.fileName, Construct: "loki.process", IRRuleID: processID, Range: processRange, Status: "metadata"})

	stages := make([]parserpolicy.Stage, 0)
	for _, statement := range block.Body {
		stageBlock, ok := statement.(*ast.BlockStmt)
		if !ok || !strings.HasPrefix(stageBlock.GetBlockName(), "stage.") {
			continue
		}
		translated := importer.translateStage(stageBlock, processID)
		stages = append(stages, translated...)
	}
	if len(stages) == 0 {
		importer.result.Warnings = append(importer.result.Warnings, fmt.Sprintf("process %q produced no executable source-derived stages", block.Label))
		return
	}
	if len(stages) > parserpolicy.MaxStages {
		importer.result.UnsupportedConstructs = append(importer.result.UnsupportedConstructs, UnsupportedConstruct{
			Type: "resource.stage_count", Description: fmt.Sprintf("process has %d translated stages; parser-policy maximum is %d", len(stages), parserpolicy.MaxStages),
			Range: processRange, Severity: "error", Repair: "split the pipeline at reviewed semantic boundaries before activation",
		})
		stages = stages[:parserpolicy.MaxStages]
	}
	priority := importer.ruleSequence*100 + 50
	importer.addRule(parserpolicy.ParserRule{
		ID: processID, Name: "Migrated Alloy process " + block.Label, Priority: priority,
		ServiceSelectors: []string{service}, Stages: stages, FailurePolicy: parserpolicy.FailurePolicyRejectRule,
		ProjectionMode: parserpolicy.ProjectionModeAll, CountPolicy: parserpolicy.CountPolicyAllowDerived,
	})
}

func (importer *alloyImporter) translateStage(block *ast.BlockStmt, ruleID string) []parserpolicy.Stage {
	kind := strings.TrimPrefix(block.GetBlockName(), "stage.")
	rng := importer.nodeRange(block)
	attrs := importer.attributes(block.Body)
	stageID := importer.nextStageID()
	operation := OperationRecord{Kind: "stage." + kind, Range: rng, IRRuleID: ruleID, IRStageID: stageID}
	mapping := LineMapping{SourceFile: importer.fileName, Construct: operation.Kind, IRRuleID: ruleID, IRStageID: stageID, Range: rng}
	translated := func(stage parserpolicy.Stage) []parserpolicy.Stage {
		operation.Status, mapping.Status = "translated", "translated"
		importer.result.Operations = append(importer.result.Operations, operation)
		importer.result.LineMappings = append(importer.result.LineMappings, mapping)
		return []parserpolicy.Stage{stage}
	}
	unsupported := func(description, repair string) []parserpolicy.Stage {
		operation.Status, mapping.Status = "unsupported", "unsupported"
		operation.IRStageID, mapping.IRStageID = "", ""
		importer.result.Operations = append(importer.result.Operations, operation)
		importer.result.LineMappings = append(importer.result.LineMappings, mapping)
		importer.result.UnsupportedConstructs = append(importer.result.UnsupportedConstructs, UnsupportedConstruct{Type: operation.Kind, Description: description, Range: rng, Severity: "error", Repair: repair})
		return nil
	}

	switch kind {
	case "regex":
		pattern, ok := stringValue(attrs["expression"])
		if !ok || pattern == "" {
			return unsupported("regex expression is not a literal string", "replace dynamic expressions with a reviewed RE2 pattern")
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return unsupported("regex is not RE2-compatible: "+err.Error(), "rewrite lookarounds/backreferences as bounded parser stages")
		}
		groups := map[string]string{}
		for _, name := range compiled.SubexpNames() {
			if name != "" {
				groups[name] = name
			}
		}
		input, _ := stringValue(attrs["source"])
		return translated(parserpolicy.Stage{ID: stageID, Name: "Alloy regex", Type: parserpolicy.StageTypeRegex, Config: map[string]interface{}{"input_field": input, "pattern": pattern, "groups": groups}, FailurePolicy: parserpolicy.FailurePolicyRejectRule})
	case "timestamp":
		input, inputOK := stringValue(attrs["source"])
		layout, layoutOK := stringValue(attrs["format"])
		if !inputOK || !layoutOK || input == "" || layout == "" {
			return unsupported("timestamp source/format must be literal", "supply explicit source and Go time layout")
		}
		return translated(parserpolicy.Stage{ID: stageID, Name: "Alloy timestamp", Type: parserpolicy.StageTypeTimestamp, Config: map[string]interface{}{"input_field": input, "output_field": input, "layouts": []string{layout}}, FailurePolicy: parserpolicy.FailurePolicyContinue})
	case "replace":
		field, fieldOK := stringValue(attrs["source"])
		pattern, patternOK := stringValue(attrs["expression"])
		replacement, replaceOK := stringValue(attrs["replace"])
		if !fieldOK || !patternOK || !replaceOK || field == "" || pattern == "" {
			return unsupported("replace source/expression/replacement must be literal", "supply a literal bounded replacement")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return unsupported("replace expression is not RE2-compatible: "+err.Error(), "rewrite the replacement expression for RE2")
		}
		return translated(parserpolicy.Stage{ID: stageID, Name: "Alloy replace", Type: parserpolicy.StageTypeReplace, Config: map[string]interface{}{"field": field, "pattern": pattern, "replacement": replacement, "regex": true}, FailurePolicy: parserpolicy.FailurePolicyContinue})
	case "json":
		input, _ := stringValue(attrs["source"])
		stages := translated(parserpolicy.Stage{ID: stageID, Name: "Alloy JSON", Type: parserpolicy.StageTypeJSON, Config: map[string]interface{}{"input_field": input}, FailurePolicy: parserpolicy.FailurePolicyRejectRule})
		return append(stages, importer.mappingStage(attrs["expressions"], ruleID, rng)...)
	case "logfmt":
		input, _ := stringValue(attrs["source"])
		stages := translated(parserpolicy.Stage{ID: stageID, Name: "Alloy logfmt", Type: parserpolicy.StageTypeLogfmt, Config: map[string]interface{}{
			"input_field": input, "pair_separators": []string{" ", "\t"}, "assignment_separators": []string{"="},
			"quote_characters": "\"'", "escape_character": "\\", "key_pattern": "^[A-Za-z_][A-Za-z0-9_.-]*$", "duplicate_policy": "last",
		}, FailurePolicy: parserpolicy.FailurePolicyRejectRule})
		return append(stages, importer.mappingStage(attrs["mapping"], ruleID, rng)...)
	case "template":
		field, fieldOK := stringValue(attrs["source"])
		template, templateOK := stringValue(attrs["template"])
		if !fieldOK || !templateOK || field == "" {
			return unsupported("template source/value must be literal", "replace dynamic template construction with output_mapping")
		}
		if template == "" {
			return translated(parserpolicy.Stage{ID: stageID, Name: "Alloy empty template", Type: parserpolicy.StageTypeOutputMapping, Config: map[string]interface{}{"constants": map[string]interface{}{field: ""}}, FailurePolicy: parserpolicy.FailurePolicyContinue})
		}
		simple := regexp.MustCompile(`^\s*\{\{[- ]*\.([A-Za-z_][A-Za-z0-9_.-]*)[- ]*\}\}\s*$`).FindStringSubmatch(template)
		if len(simple) == 2 {
			return translated(parserpolicy.Stage{ID: stageID, Name: "Alloy field template", Type: parserpolicy.StageTypeOutputMapping, Config: map[string]interface{}{"fields": map[string]string{field: simple[1]}}, FailurePolicy: parserpolicy.FailurePolicyContinue})
		}
		return unsupported("Go template requires functions/conditionals not present in parser-policy IR", "add a reviewed declarative template stage; never execute uploaded Go templates")
	case "labels":
		return unsupported("labels affect transport metadata rather than event parsing", "map approved labels in controller routing metadata")
	case "match":
		for _, statement := range block.Body {
			if child, ok := statement.(*ast.BlockStmt); ok && strings.HasPrefix(child.GetBlockName(), "stage.") {
				importer.recordUnsupportedTree(child, ruleID, "nested match scope cannot be represented without conditional stage blocks")
			}
		}
		return unsupported("nested Loki selector scope cannot be represented exactly by the flat parser-policy IR", "extend parser-policy with scoped conditional blocks before activation")
	case "output":
		return unsupported("stage.output replaces the emitted body and is not an event-field mapping", "translate the reviewed output template into an explicit allowlist/output mapping")
	default:
		return unsupported("unsupported Alloy stage", "implement a reviewed declarative stage equivalent")
	}
}

func (importer *alloyImporter) mappingStage(value interface{}, ruleID string, rng SourceRange) []parserpolicy.Stage {
	object, ok := value.(map[string]interface{})
	if !ok || len(object) == 0 {
		return nil
	}
	fields := map[string]string{}
	for source, targetValue := range object {
		target, ok := stringValue(targetValue)
		if !ok || target == "" {
			target = source
		}
		fields[source] = target
	}
	stageID := importer.nextStageID()
	importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: "field_mapping", Range: rng, IRRuleID: ruleID, IRStageID: stageID, Status: "translated"})
	importer.result.LineMappings = append(importer.result.LineMappings, LineMapping{SourceFile: importer.fileName, Construct: "field_mapping", Range: rng, IRRuleID: ruleID, IRStageID: stageID, Status: "translated"})
	return []parserpolicy.Stage{{ID: stageID, Name: "Alloy field mapping", Type: parserpolicy.StageTypeRename, Config: map[string]interface{}{"fields": fields}, FailurePolicy: parserpolicy.FailurePolicyContinue}}
}

func (importer *alloyImporter) recordUnsupportedTree(block *ast.BlockStmt, ruleID, description string) {
	rng := importer.nodeRange(block)
	kind := block.GetBlockName()
	importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: kind, Range: rng, IRRuleID: ruleID, Status: "unsupported"})
	importer.result.LineMappings = append(importer.result.LineMappings, LineMapping{SourceFile: importer.fileName, Construct: kind, IRRuleID: ruleID, Range: rng, Status: "unsupported"})
	importer.result.UnsupportedConstructs = append(importer.result.UnsupportedConstructs, UnsupportedConstruct{Type: kind, Description: description, Range: rng, Severity: "error", Repair: "extend parser-policy with nested conditional stage blocks"})
	for _, statement := range block.Body {
		if child, ok := statement.(*ast.BlockStmt); ok && strings.HasPrefix(child.GetBlockName(), "stage.") {
			importer.recordUnsupportedTree(child, ruleID, description)
		}
	}
}

func (importer *alloyImporter) unsupported(kind, description string, node ast.Node, repair string) {
	rng := importer.nodeRange(node)
	importer.result.Operations = append(importer.result.Operations, OperationRecord{Kind: kind, Range: rng, Status: "unsupported"})
	importer.result.LineMappings = append(importer.result.LineMappings, LineMapping{SourceFile: importer.fileName, Construct: kind, Range: rng, Status: "unsupported"})
	importer.result.UnsupportedConstructs = append(importer.result.UnsupportedConstructs, UnsupportedConstruct{Type: kind, Description: description, Range: rng, Severity: "warning", Repair: repair})
}

func (importer *alloyImporter) attributes(body ast.Body) map[string]interface{} {
	values := map[string]interface{}{}
	for _, statement := range body {
		attribute, ok := statement.(*ast.AttributeStmt)
		if !ok {
			continue
		}
		value, _ := importer.expressionValue(attribute.Value)
		values[attribute.Name.Name] = value
	}
	return values
}

func (importer *alloyImporter) expressionValue(expression ast.Expr) (interface{}, bool) {
	switch value := expression.(type) {
	case *ast.LiteralExpr:
		switch value.Kind {
		case token.STRING:
			decoded, err := strconv.Unquote(value.Value)
			return decoded, err == nil
		case token.NUMBER:
			number, err := strconv.ParseInt(value.Value, 10, 64)
			return number, err == nil
		case token.FLOAT:
			number, err := strconv.ParseFloat(value.Value, 64)
			return number, err == nil
		case token.BOOL:
			return value.Value == "true", true
		case token.NULL:
			return nil, true
		}
	case *ast.ArrayExpr:
		items := make([]interface{}, 0, len(value.Elements))
		for _, element := range value.Elements {
			item, ok := importer.expressionValue(element)
			if !ok {
				item = importer.expressionText(element)
			}
			items = append(items, item)
		}
		return items, true
	case *ast.ObjectExpr:
		object := map[string]interface{}{}
		for _, field := range value.Fields {
			item, ok := importer.expressionValue(field.Value)
			if !ok {
				item = importer.expressionText(field.Value)
			}
			object[field.Name.Name] = item
		}
		return object, true
	case *ast.BinaryExpr:
		if value.Kind == token.ADD {
			left, leftOK := importer.expressionValue(value.Left)
			right, rightOK := importer.expressionValue(value.Right)
			leftText, leftString := left.(string)
			rightText, rightString := right.(string)
			if leftOK && rightOK && leftString && rightString {
				return leftText + rightText, true
			}
		}
	case *ast.ParenExpr:
		return importer.expressionValue(value.Inner)
	}
	return importer.expressionText(expression), false
}

func (importer *alloyImporter) expressionText(expression ast.Expr) string {
	start, end := ast.StartPos(expression).Offset(), ast.EndPos(expression).Offset()+1
	if start < 0 || end <= start || end > len(importer.source) {
		return ""
	}
	return string(importer.source[start:end])
}

func (importer *alloyImporter) nodeRange(node ast.Node) SourceRange {
	start, end := ast.StartPos(node).Position(), ast.EndPos(node).Position()
	return SourceRange{
		Start: SourcePosition{Line: start.Line, Column: start.Column, Offset: start.Offset},
		End:   SourcePosition{Line: end.Line, Column: end.Column, Offset: end.Offset + 1},
	}
}

func (importer *alloyImporter) nextStageID() string {
	importer.stageSeq++
	return fmt.Sprintf("stage_%d", importer.stageSeq)
}

func stringValue(value interface{}) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

func serviceFromName(value string) string {
	lower := strings.ToLower(value)
	for _, service := range []string{"smtp", "imap", "pop", "mta", "webtop", "webmail", "pabd", "pss", "capd", "mss"} {
		if strings.Contains(lower, service) {
			return service
		}
	}
	return "*"
}

func identifier(value string) string {
	value = regexp.MustCompile(`[^A-Za-z0-9_-]+`).ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		value = "migrated"
	}
	if len(value) > 64 {
		digest := sha256.Sum256([]byte(value))
		suffix := hex.EncodeToString(digest[:6])
		value = value[:51] + "_" + suffix
	}
	return value
}

func baseName(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	parts := strings.Split(value, "/")
	return parts[len(parts)-1]
}

func extension(value string) string {
	base := baseName(value)
	if index := strings.LastIndexByte(base, '.'); index >= 0 {
		return base[index:]
	}
	return ""
}

func sortedStringKeys(values map[string]interface{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
