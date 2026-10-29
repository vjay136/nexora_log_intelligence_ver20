package controlplane

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"nexora.local/nli-v20/parser-policy"
)

// assuranceRuntimePack converts the bounded Assurance regex definition into
// the strict IR consumed by the client runtime. The conversion is deliberately
// narrow: unsupported declarative forms stay test-only instead of being
// silently interpreted differently in production.
func assuranceRuntimePack(id, version, author string, definition map[string]any, cases []assuranceRequest) (*parserpolicy.ParserPack, error) {
	if definition["pattern_type"] != "regex" {
		return nil, fmt.Errorf("only validated regex candidates can be published to runtime")
	}
	pattern, _ := definition["pattern"].(string)
	compiled, err := regexp.Compile(pattern)
	if err != nil || pattern == "" || len(pattern) > parserpolicy.MaxRegexLength {
		return nil, fmt.Errorf("runtime regex is invalid or exceeds its size limit")
	}
	fields, ok := definition["fields"].([]any)
	if !ok || len(fields) == 0 {
		return nil, fmt.Errorf("runtime parser needs at least one mapped field")
	}
	groups := map[string]string{}
	schema := map[string]parserpolicy.FieldDef{}
	for _, item := range fields {
		field, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("runtime field mapping is invalid")
		}
		name, _ := field["name"].(string)
		source, _ := field["source"].(string)
		group := strings.TrimPrefix(source, "group:")
		if name == "" || group == source || !containsString(compiled.SubexpNames(), group) {
			return nil, fmt.Errorf("field %q must map to a named regex capture group", name)
		}
		groups[name] = group
		fieldType := parserpolicy.FieldTypeString
		if value, ok := field["type"].(string); ok && value != "" {
			fieldType = parserpolicy.FieldType(value)
		}
		required, _ := field["required"].(bool)
		definition := parserpolicy.FieldDef{Type: fieldType, Required: required}
		validation, _ := field["validation"].(string)
		for _, rawRule := range strings.Split(validation, ",") {
			rule := strings.TrimSpace(rawRule)
			if rule == "" {
				continue
			}
			parts := strings.SplitN(rule, "=", 2)
			if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
				return nil, fmt.Errorf("field %q validation rule %q is invalid", name, rule)
			}
			kind, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			switch kind {
			case "min", "max":
				if fieldType != parserpolicy.FieldTypeInt && fieldType != parserpolicy.FieldTypeFloat {
					return nil, fmt.Errorf("field %q range validation requires int or float type", name)
				}
				parsed, parseErr := strconv.ParseFloat(value, 64)
				if parseErr != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
					return nil, fmt.Errorf("field %q validation rule %q has an invalid number", name, rule)
				}
				if definition.Range == nil {
					definition.Range = &parserpolicy.RangeConstraint{}
				}
				if kind == "min" {
					definition.Range.Min = &parsed
				} else {
					definition.Range.Max = &parsed
				}
			case "one_of":
				values := strings.Split(value, "|")
				if len(values) > 100 {
					return nil, fmt.Errorf("field %q enum exceeds the 100-value runtime limit", name)
				}
				definition.Enum = definition.Enum[:0]
				seen := map[string]struct{}{}
				for _, enumValue := range values {
					enumValue = strings.TrimSpace(enumValue)
					if enumValue == "" {
						return nil, fmt.Errorf("field %q enum values cannot be empty", name)
					}
					if _, exists := seen[enumValue]; exists {
						continue
					}
					seen[enumValue] = struct{}{}
					definition.Enum = append(definition.Enum, enumValue)
				}
			default:
				return nil, fmt.Errorf("field %q validation %q is unsupported by the runtime", name, kind)
			}
		}
		schema[name] = definition
	}
	service := ""
	if len(cases) != 0 {
		service = strings.TrimSpace(cases[0].Service)
	}
	if service == "" || service == "auto" {
		service = "*"
	}
	pack := &parserpolicy.ParserPack{
		ID: id, Version: version, Revision: 1, SourceType: "manual", Author: author,
		ReviewState: parserpolicy.ReviewStateApproved, CreatedAt: time.Now().UTC(),
		ResourceLimits: parserpolicy.DefaultResourceLimits(),
		Rules: []parserpolicy.ParserRule{{
			ID: id + "-rule", Name: id, Priority: 1, ServiceSelectors: []string{service},
			Stages:        []parserpolicy.Stage{{ID: id + "-regex", Name: "Assurance regex", Type: parserpolicy.StageTypeRegex, Config: map[string]any{"pattern": pattern, "groups": groups}, FailurePolicy: parserpolicy.FailurePolicyRejectRule}},
			FailurePolicy: parserpolicy.FailurePolicyQuarantine, FieldSchema: schema,
			ProjectionMode: parserpolicy.ProjectionModeAllowlist, AllowedFields: sortedKeys(schema), CountPolicy: parserpolicy.CountPolicyExactSource,
		}},
	}
	if err := parserpolicy.SealParserPack(pack); err != nil {
		return nil, err
	}
	if result := parserpolicy.ValidateParserPack(pack); !result.Valid {
		return nil, fmt.Errorf("runtime parser validation failed: %s", strings.Join(result.Errors, "; "))
	}
	encoded, err := json.Marshal(pack)
	if err != nil {
		return nil, err
	}
	if _, err := parserpolicy.LoadParserPackBytes(encoded); err != nil {
		return nil, err
	}
	return pack, nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func sortedKeys(fields map[string]parserpolicy.FieldDef) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	// Keep rule serialization and bundle hashes deterministic.
	sort.Strings(keys)
	return keys
}
