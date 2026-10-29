// Package registry manages the NLI declarative parser DSL registry.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"nexora.local/nli-v20/internal/grok"
	"nexora.local/nli-v20/internal/typeinfer"
)

// ParserDef is a declarative parser definition — never executable code.
type ParserDef struct {
	ID          string           `json:"id"`
	Version     int              `json:"version"`
	Description string           `json:"description"`
	Format      string           `json:"format"` // hint: json, syslog_rfc3164, etc.
	Pattern     string           `json:"pattern,omitempty"`
	PatternType string           `json:"pattern_type,omitempty"` // grok or regex
	TemplateIDs []string         `json:"template_ids,omitempty"`
	Fields      []FieldDef       `json:"fields"`
	Tests       []RegressionTest `json:"tests,omitempty"`
}

// FieldDef describes one extracted field.
type FieldDef struct {
	Name     string `json:"name"`
	Source   string `json:"source"` // "token:N", "group:N", "grok:PATTERN", "kv:key", "json:key"
	Type     string `json:"type"`   // string/int/float/bool/ip/timestamp/email/url
	Required bool   `json:"required"`
}

// RegressionTest is an input/expected pair for parser validation.
type RegressionTest struct {
	Input    string                 `json:"input"`
	Expected map[string]interface{} `json:"expected"`
}

// Registry holds all registered parsers.
type Registry struct {
	mu      sync.RWMutex
	parsers map[string]*ParserDef
}

// New creates an empty registry.
func New() *Registry {
	return &Registry{parsers: make(map[string]*ParserDef)}
}

// Register adds or replaces a parser definition.
func (r *Registry) Register(p *ParserDef) {
	if p == nil {
		return
	}
	stored := cloneParserDef(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parsers[stored.ID] = stored
}

// Get returns a parser by ID.
func (r *Registry) Get(id string) (*ParserDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.parsers[id]
	if !ok {
		return nil, false
	}
	return cloneParserDef(p), true
}

// All returns a copy of all parsers.
func (r *Registry) All() []*ParserDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ParserDef, 0, len(r.parsers))
	for _, p := range r.parsers {
		out = append(out, cloneParserDef(p))
	}
	return out
}

func cloneParserDef(p *ParserDef) *ParserDef {
	if p == nil {
		return nil
	}
	clone := *p
	clone.TemplateIDs = append([]string(nil), p.TemplateIDs...)
	clone.Fields = append([]FieldDef(nil), p.Fields...)
	clone.Tests = make([]RegressionTest, len(p.Tests))
	for i, test := range p.Tests {
		clone.Tests[i] = RegressionTest{Input: test.Input}
		if test.Expected != nil {
			clone.Tests[i].Expected = make(map[string]interface{}, len(test.Expected))
			for key, value := range test.Expected {
				clone.Tests[i].Expected[key] = cloneJSONValue(value)
			}
		}
	}
	return &clone
}

func cloneJSONValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		clone := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			clone[key] = cloneJSONValue(item)
		}
		return clone
	case []interface{}:
		clone := make([]interface{}, len(typed))
		for i, item := range typed {
			clone[i] = cloneJSONValue(item)
		}
		return clone
	default:
		return value
	}
}

// LoadDir loads all *.json parser files from a directory.
func (r *Registry) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := r.LoadFile(path); err != nil {
			return fmt.Errorf("registry: load %s: %w", path, err)
		}
	}
	return nil
}

// LoadFile loads and validates a single parser JSON file.
func (r *Registry) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var p ParserDef
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if err := Validate(&p); err != nil {
		return fmt.Errorf("parser file %s: %w", path, err)
	}
	r.Register(&p)
	return nil
}

// SaveParser validates and atomically writes a parser definition as JSON.
func (r *Registry) SaveParser(dir string, p *ParserDef) error {
	if err := Validate(p); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	filename := fmt.Sprintf("%s-v%d.json", p.ID, p.Version)
	path := filepath.Join(dir, filename)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var parserIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,127}$`)
var fieldNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

// Validate enforces the non-executable parser DSL contract.
func Validate(p *ParserDef) error {
	if p == nil || !parserIDRE.MatchString(p.ID) {
		return fmt.Errorf("invalid parser id")
	}
	if p.Version < 1 {
		return fmt.Errorf("parser version must be positive")
	}
	if len(p.Fields) == 0 || len(p.Fields) > 256 {
		return fmt.Errorf("parser must define 1..256 fields")
	}
	if p.Pattern != "" && p.PatternType != "grok" && p.PatternType != "regex" {
		return fmt.Errorf("pattern_type must be grok or regex")
	}
	seen := make(map[string]bool, len(p.Fields))
	for _, field := range p.Fields {
		if !fieldNameRE.MatchString(field.Name) || seen[field.Name] {
			return fmt.Errorf("invalid or duplicate field name %q", field.Name)
		}
		seen[field.Name] = true
		if !validSource(field.Source) {
			return fmt.Errorf("unsupported source %q", field.Source)
		}
	}
	if p.PatternType == "regex" && p.Pattern != "" {
		if _, err := regexp.Compile(p.Pattern); err != nil {
			return fmt.Errorf("invalid regex pattern: %w", err)
		}
	}
	if p.PatternType == "grok" && p.Pattern != "" {
		if _, err := grok.New(nil).Compile(p.Pattern); err != nil {
			return fmt.Errorf("invalid grok pattern: %w", err)
		}
	}
	return nil
}

func validSource(source string) bool {
	for _, prefix := range []string{"token:", "group:", "grok:", "kv:", "json:"} {
		if strings.HasPrefix(source, prefix) && len(source) > len(prefix) {
			return true
		}
	}
	return false
}

// Apply executes a validated parser definition against one raw record.
func Apply(p *ParserDef, input string) (map[string]interface{}, []string) {
	if err := Validate(p); err != nil {
		return nil, []string{err.Error()}
	}
	captures := make(map[string]string)
	if p.Pattern != "" {
		var err error
		if p.PatternType == "grok" {
			var compiled *grok.Compiled
			compiled, err = grok.New(nil).Compile(p.Pattern)
			if err == nil {
				captures = compiled.Match(input)
				if captures == nil {
					err = fmt.Errorf("pattern did not match")
				}
			}
		} else {
			var compiled *regexp.Regexp
			compiled, err = regexp.Compile(p.Pattern)
			if err == nil {
				match := compiled.FindStringSubmatch(input)
				if match == nil {
					err = fmt.Errorf("pattern did not match")
				} else {
					for i, name := range compiled.SubexpNames() {
						if i > 0 && name != "" && i < len(match) {
							captures[name] = match[i]
						}
					}
				}
			}
		}
		if err != nil {
			return nil, []string{err.Error()}
		}
	}

	jsonValues := make(map[string]interface{})
	_ = json.Unmarshal([]byte(input), &jsonValues)
	kvValues := extractKV(input)
	tokens := strings.Fields(input)
	grokEngine := grok.New(nil)
	out := make(map[string]interface{})
	var failures []string

	for _, field := range p.Fields {
		var value interface{}
		var ok bool
		source := strings.SplitN(field.Source, ":", 2)
		switch source[0] {
		case "json":
			if source[1] == "*" {
				for key, item := range jsonValues {
					out[key] = item
				}
				continue
			}
			value, ok = jsonValues[source[1]]
		case "kv":
			if source[1] == "*" {
				for key, item := range kvValues {
					out[key] = item
				}
				continue
			}
			value, ok = kvValues[source[1]]
		case "token":
			index, err := strconv.Atoi(source[1])
			if err == nil && index >= 0 && index < len(tokens) {
				value, ok = tokens[index], true
			}
		case "group":
			value, ok = captures[field.Name]
		case "grok":
			if captured, exists := captures[field.Name]; exists {
				value, ok = captured, true
			} else if compiled, err := grokEngine.Compile("%{" + source[1] + ":value}"); err == nil {
				if values := compiled.Match(input); values != nil {
					value, ok = values["value"], true
				}
			}
		}
		if !ok || value == "" {
			if field.Required {
				failures = append(failures, "required field missing: "+field.Name)
			}
			continue
		}
		out[field.Name] = coerce(value, field.Type)
	}
	return out, failures
}

func coerce(value interface{}, kind string) interface{} {
	text, stringValue := value.(string)
	if !stringValue || kind == "string" || kind == "" {
		return value
	}
	switch kind {
	case "int", "integer":
		if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
			return parsed
		}
	case "float":
		if parsed, err := strconv.ParseFloat(text, 64); err == nil {
			return parsed
		}
	case "bool", "boolean":
		if parsed, err := strconv.ParseBool(text); err == nil {
			return parsed
		}
	default:
		return typeinfer.Coerce(text)
	}
	return text
}

var kvRE = regexp.MustCompile(`(?:^|\s)([A-Za-z_][A-Za-z0-9_.-]*)=("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\S+)`)

func extractKV(input string) map[string]interface{} {
	out := make(map[string]interface{})
	for _, match := range kvRE.FindAllStringSubmatch(input, -1) {
		value := match[2]
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		out[match[1]] = value
	}
	return out
}

// RunRegressionTests executes every declared input/expected assertion.
func RunRegressionTests(p *ParserDef) []string {
	if err := Validate(p); err != nil {
		return []string{err.Error()}
	}
	var failures []string
	for i, test := range p.Tests {
		if test.Input == "" || len(test.Expected) == 0 {
			failures = append(failures, fmt.Sprintf("test %d: input and expected fields are required", i))
			continue
		}
		actual, applyFailures := Apply(p, test.Input)
		for _, failure := range applyFailures {
			failures = append(failures, fmt.Sprintf("test %d: %s", i, failure))
		}
		for key, expected := range test.Expected {
			if got, ok := actual[key]; !ok || !reflect.DeepEqual(normalizeNumber(got), normalizeNumber(expected)) {
				failures = append(failures, fmt.Sprintf("test %d: field %s = %v, want %v", i, key, got, expected))
			}
		}
	}
	return failures
}

func normalizeNumber(value interface{}) interface{} {
	switch number := value.(type) {
	case int:
		return float64(number)
	case int64:
		return float64(number)
	case json.Number:
		parsed, _ := number.Float64()
		return parsed
	default:
		return value
	}
}
