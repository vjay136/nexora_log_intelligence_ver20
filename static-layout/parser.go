// Package staticlayout implements bounded declarative positional parsing.
package staticlayout

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"nexora.local/nli-v20/internal/grok"
)

const (
	defaultMaxInputBytes = 1024 * 1024
	hardMaxInputBytes    = 16 * 1024 * 1024
)

// Config defines a positional layout. An empty Separator tokenizes Unicode
// whitespace while preserving byte offsets; a non-empty separator is literal.
type Config struct {
	Separator     string      `json:"separator,omitempty"`
	Tokens        []TokenSpec `json:"tokens"`
	FailOnMissing bool        `json:"fail_on_missing,omitempty"`
	MaxInputBytes int         `json:"max_input_bytes,omitempty"`
}

// TokenSpec defines one positional extraction. Optional is the inverse of
// Required and exists for policy JSON readability; both may not be true.
type TokenSpec struct {
	Index     int         `json:"index"`
	Name      string      `json:"name"`
	Type      TokenType   `json:"type"`
	Config    interface{} `json:"config,omitempty"`
	Required  bool        `json:"required,omitempty"`
	Optional  bool        `json:"optional,omitempty"`
	Transform []Transform `json:"transform,omitempty"`
}

type TokenType string

const (
	TokenTypeString     TokenType = "string"
	TokenTypeWhitespace TokenType = "whitespace"
	TokenTypeInt        TokenType = "int"
	TokenTypeFloat      TokenType = "float"
	TokenTypeSplit      TokenType = "split"
	TokenTypeRegex      TokenType = "regex"
	TokenTypeGrok       TokenType = "grok"
	TokenTypeFixedWidth TokenType = "fixed_width"
	TokenTypeLiteral    TokenType = "literal"
	TokenTypeRestOfLine TokenType = "rest_of_line"
)

type Transform struct {
	Type   TransformType     `json:"type"`
	Config map[string]string `json:"config,omitempty"`
}

type TransformType string

const (
	TransformTrim        TransformType = "trim"
	TransformStripPrefix TransformType = "strip_prefix"
	TransformStripSuffix TransformType = "strip_suffix"
	TransformReplace     TransformType = "replace"
	TransformSplit       TransformType = "split"
)

type SplitConfig struct {
	Delimiter string `json:"delimiter"`
	Index     *int   `json:"index,omitempty"`
	LeftName  string `json:"left_name,omitempty"`
	RightName string `json:"right_name,omitempty"`
}

type RegexConfig struct {
	Pattern string            `json:"pattern"`
	Groups  map[string]string `json:"groups,omitempty"`
}

type GrokConfig struct {
	Pattern  string            `json:"pattern"`
	Patterns map[string]string `json:"patterns,omitempty"`
}

type FixedWidthConfig struct {
	Start int `json:"start"`
	Width int `json:"width"`
}

type LiteralConfig struct {
	Value string `json:"value"`
	Emit  bool   `json:"emit,omitempty"`
}

type ParseResult struct {
	Fields      map[string]interface{} `json:"fields"`
	TokensFound int                    `json:"tokens_found"`
	TokensUsed  int                    `json:"tokens_used"`
	Evidence    []TokenEvidence        `json:"evidence"`
	Errors      []string               `json:"errors,omitempty"`
}

// TokenEvidence stores byte offsets, not the complete input.
type TokenEvidence struct {
	TokenIndex  int         `json:"token_index"`
	FieldName   string      `json:"field_name"`
	RawValue    string      `json:"raw_value"`
	ParsedValue interface{} `json:"parsed_value"`
	Start       int         `json:"start"`
	End         int         `json:"end"`
}

type inputToken struct {
	value      string
	start, end int
}

type Parser struct {
	config Config
}

func NewParser(config Config) (*Parser, error) {
	if len(config.Tokens) == 0 {
		return nil, fmt.Errorf("at least one token specification is required")
	}
	if config.MaxInputBytes == 0 {
		config.MaxInputBytes = defaultMaxInputBytes
	}
	if config.MaxInputBytes < 1 || config.MaxInputBytes > hardMaxInputBytes {
		return nil, fmt.Errorf("max_input_bytes must be 1..%d", hardMaxInputBytes)
	}
	if len(config.Separator) > 16 {
		return nil, fmt.Errorf("separator exceeds 16 bytes")
	}
	for i, token := range config.Tokens {
		if token.Index < 0 {
			return nil, fmt.Errorf("token[%d]: index must be non-negative", i)
		}
		if token.Name == "" && token.Type != TokenTypeLiteral {
			return nil, fmt.Errorf("token[%d]: name is required", i)
		}
		if token.Required && token.Optional {
			return nil, fmt.Errorf("token[%d]: required and optional are mutually exclusive", i)
		}
		switch token.Type {
		case TokenTypeString, TokenTypeWhitespace, TokenTypeInt, TokenTypeFloat, TokenTypeRestOfLine:
		case TokenTypeSplit:
			var cfg SplitConfig
			if err := decodeConfig(token.Config, &cfg); err != nil || cfg.Delimiter == "" {
				return nil, fmt.Errorf("token[%d]: split requires a delimiter", i)
			}
		case TokenTypeRegex:
			var cfg RegexConfig
			if err := decodeConfig(token.Config, &cfg); err != nil || cfg.Pattern == "" {
				return nil, fmt.Errorf("token[%d]: regex requires a pattern", i)
			}
			if _, err := regexp.Compile(cfg.Pattern); err != nil {
				return nil, fmt.Errorf("token[%d]: invalid regex: %w", i, err)
			}
		case TokenTypeGrok:
			var cfg GrokConfig
			if err := decodeConfig(token.Config, &cfg); err != nil || cfg.Pattern == "" {
				return nil, fmt.Errorf("token[%d]: grok requires a pattern", i)
			}
			if _, err := grok.New(cfg.Patterns).Compile(cfg.Pattern); err != nil {
				return nil, fmt.Errorf("token[%d]: invalid grok: %w", i, err)
			}
		case TokenTypeFixedWidth:
			var cfg FixedWidthConfig
			if err := decodeConfig(token.Config, &cfg); err != nil || cfg.Start < 0 || cfg.Width < 1 {
				return nil, fmt.Errorf("token[%d]: fixed_width requires start >= 0 and width > 0", i)
			}
		case TokenTypeLiteral:
			var cfg LiteralConfig
			if err := decodeConfig(token.Config, &cfg); err != nil || cfg.Value == "" {
				return nil, fmt.Errorf("token[%d]: literal requires a non-empty value", i)
			}
		default:
			return nil, fmt.Errorf("token[%d]: unsupported type %q", i, token.Type)
		}
		for j, transform := range token.Transform {
			switch transform.Type {
			case TransformTrim, TransformStripPrefix, TransformStripSuffix, TransformReplace, TransformSplit:
			default:
				return nil, fmt.Errorf("token[%d].transform[%d]: unsupported type %q", i, j, transform.Type)
			}
		}
	}
	return &Parser{config: config}, nil
}

func (p *Parser) Parse(input string) (*ParseResult, error) {
	if len(input) > p.config.MaxInputBytes {
		return nil, fmt.Errorf("input exceeds max_input_bytes")
	}
	result := &ParseResult{Fields: map[string]interface{}{}, Evidence: []TokenEvidence{}, Errors: []string{}}
	tokens := tokenize(input, p.config.Separator)
	result.TokensFound = len(tokens)
	for _, spec := range p.config.Tokens {
		if err := p.processToken(input, tokens, spec, result); err != nil {
			if spec.Required && !spec.Optional && p.config.FailOnMissing {
				return nil, fmt.Errorf("required token %s at index %d: %w", spec.Name, spec.Index, err)
			}
			result.Errors = append(result.Errors, fmt.Sprintf("token %s at index %d: %v", spec.Name, spec.Index, err))
		}
	}
	return result, nil
}

func (p *Parser) processToken(input string, tokens []inputToken, spec TokenSpec, result *ParseResult) error {
	var raw string
	var start, end int
	used := 1
	switch spec.Type {
	case TokenTypeFixedWidth:
		var cfg FixedWidthConfig
		if err := decodeConfig(spec.Config, &cfg); err != nil {
			return err
		}
		start, end = cfg.Start, cfg.Start+cfg.Width
		if start < 0 || end > len(input) {
			if spec.Optional || !spec.Required {
				return nil
			}
			return fmt.Errorf("fixed-width slice is outside input")
		}
		raw = input[start:end]
	case TokenTypeRestOfLine:
		if spec.Index >= len(tokens) {
			if spec.Optional || !spec.Required {
				return nil
			}
			return fmt.Errorf("rest-of-line start not found")
		}
		start, end = tokens[spec.Index].start, len(input)
		raw = input[start:end]
		used = len(tokens) - spec.Index
	default:
		if spec.Index >= len(tokens) {
			if spec.Optional || !spec.Required {
				return nil
			}
			return fmt.Errorf("token not found")
		}
		raw, start, end = tokens[spec.Index].value, tokens[spec.Index].start, tokens[spec.Index].end
	}

	value, emit, err := p.parseTokenValue(raw, spec)
	if err != nil {
		return err
	}
	if !emit {
		result.TokensUsed += used
		return nil
	}
	if len(spec.Transform) > 0 {
		if _, structured := value.(map[string]interface{}); structured {
			return fmt.Errorf("transforms cannot be applied to multi-field captures")
		}
		value, err = p.applyTransforms(value, spec.Transform)
		if err != nil {
			return err
		}
	}
	if fields, ok := value.(map[string]interface{}); ok {
		for name, captured := range fields {
			if err := addField(result, spec.Index, name, raw, captured, start, end); err != nil {
				return err
			}
		}
	} else {
		if err := addField(result, spec.Index, spec.Name, raw, value, start, end); err != nil {
			return err
		}
	}
	result.TokensUsed += used
	return nil
}

func addField(result *ParseResult, tokenIndex int, name, raw string, value interface{}, start, end int) error {
	if name == "" {
		return fmt.Errorf("capture has no output field")
	}
	if _, exists := result.Fields[name]; exists {
		return fmt.Errorf("duplicate output field %q", name)
	}
	result.Fields[name] = value
	result.Evidence = append(result.Evidence, TokenEvidence{TokenIndex: tokenIndex, FieldName: name, RawValue: raw, ParsedValue: value, Start: start, End: end})
	return nil
}

func (p *Parser) parseTokenValue(raw string, spec TokenSpec) (interface{}, bool, error) {
	switch spec.Type {
	case TokenTypeString, TokenTypeWhitespace, TokenTypeFixedWidth, TokenTypeRestOfLine:
		return raw, true, nil
	case TokenTypeInt:
		value, err := strconv.Atoi(raw)
		return value, true, err
	case TokenTypeFloat:
		value, err := strconv.ParseFloat(raw, 64)
		return value, true, err
	case TokenTypeSplit:
		value, err := parseSplitToken(raw, spec.Config)
		return value, true, err
	case TokenTypeRegex:
		value, err := parseRegexToken(raw, spec.Config)
		return value, true, err
	case TokenTypeGrok:
		value, err := parseGrokToken(raw, spec.Config)
		return value, true, err
	case TokenTypeLiteral:
		var cfg LiteralConfig
		if err := decodeConfig(spec.Config, &cfg); err != nil {
			return nil, false, err
		}
		if raw != cfg.Value {
			return nil, false, fmt.Errorf("literal mismatch")
		}
		return raw, cfg.Emit, nil
	default:
		return nil, false, fmt.Errorf("unsupported token type %q", spec.Type)
	}
}

func parseSplitToken(raw string, config interface{}) (interface{}, error) {
	var cfg SplitConfig
	if err := decodeConfig(config, &cfg); err != nil {
		return nil, err
	}
	parts := strings.Split(raw, cfg.Delimiter)
	if cfg.LeftName != "" || cfg.RightName != "" {
		if len(parts) < 2 || cfg.LeftName == "" || cfg.RightName == "" {
			return nil, fmt.Errorf("left_name/right_name require at least two parts")
		}
		return map[string]interface{}{cfg.LeftName: parts[0], cfg.RightName: strings.Join(parts[1:], cfg.Delimiter)}, nil
	}
	if cfg.Index != nil {
		if *cfg.Index < 0 || *cfg.Index >= len(parts) {
			return nil, fmt.Errorf("split index out of range")
		}
		return parts[*cfg.Index], nil
	}
	values := make([]interface{}, len(parts))
	for i := range parts {
		values[i] = parts[i]
	}
	return values, nil
}

func parseRegexToken(raw string, config interface{}) (interface{}, error) {
	var cfg RegexConfig
	if err := decodeConfig(config, &cfg); err != nil {
		return nil, err
	}
	re, err := regexp.Compile(cfg.Pattern)
	if err != nil {
		return nil, err
	}
	match := re.FindStringSubmatch(raw)
	if match == nil {
		return nil, fmt.Errorf("regex did not match")
	}
	fields := map[string]interface{}{}
	for i, name := range re.SubexpNames() {
		if i == 0 || i >= len(match) || name == "" {
			continue
		}
		output := name
		if alias := cfg.Groups[name]; alias != "" {
			output = alias
		}
		fields[output] = match[i]
	}
	if len(fields) > 0 {
		return fields, nil
	}
	if len(match) == 1 {
		return match[0], nil
	}
	values := make([]interface{}, len(match)-1)
	for i := 1; i < len(match); i++ {
		values[i-1] = match[i]
	}
	return values, nil
}

func parseGrokToken(raw string, config interface{}) (interface{}, error) {
	var cfg GrokConfig
	if err := decodeConfig(config, &cfg); err != nil {
		return nil, err
	}
	compiled, err := grok.New(cfg.Patterns).Compile(cfg.Pattern)
	if err != nil {
		return nil, err
	}
	matched := compiled.Match(raw)
	if matched == nil {
		return nil, fmt.Errorf("grok did not match")
	}
	fields := make(map[string]interface{}, len(matched))
	types := map[string]string{}
	for _, field := range compiled.Fields {
		types[field.Name] = field.Type
	}
	for name, value := range matched {
		switch types[name] {
		case "int":
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("grok integer capture %s is invalid", name)
			}
			fields[name] = n
		case "float":
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return nil, fmt.Errorf("grok float capture %s is invalid", name)
			}
			fields[name] = n
		default:
			fields[name] = value
		}
	}
	return fields, nil
}

func (p *Parser) applyTransforms(value interface{}, transforms []Transform) (interface{}, error) {
	current := value
	for _, transform := range transforms {
		var err error
		current, err = applyTransform(current, transform)
		if err != nil {
			return nil, fmt.Errorf("transform %s: %w", transform.Type, err)
		}
	}
	return current, nil
}

func applyTransform(value interface{}, transform Transform) (interface{}, error) {
	text := fmt.Sprint(value)
	switch transform.Type {
	case TransformTrim:
		return strings.TrimSpace(text), nil
	case TransformStripPrefix:
		prefix, ok := transform.Config["prefix"]
		if !ok {
			return nil, fmt.Errorf("strip_prefix requires prefix")
		}
		return strings.TrimPrefix(text, prefix), nil
	case TransformStripSuffix:
		suffix, ok := transform.Config["suffix"]
		if !ok {
			return nil, fmt.Errorf("strip_suffix requires suffix")
		}
		return strings.TrimSuffix(text, suffix), nil
	case TransformReplace:
		old, oldOK := transform.Config["old"]
		newValue, newOK := transform.Config["new"]
		if !oldOK || !newOK {
			return nil, fmt.Errorf("replace requires old and new")
		}
		return strings.ReplaceAll(text, old, newValue), nil
	case TransformSplit:
		separator, ok := transform.Config["delimiter"]
		if !ok || separator == "" {
			return nil, fmt.Errorf("split requires delimiter")
		}
		parts := strings.Split(text, separator)
		values := make([]interface{}, len(parts))
		for i := range parts {
			values[i] = parts[i]
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unsupported transform %q", transform.Type)
	}
}

func tokenize(input, separator string) []inputToken {
	if separator != "" {
		var tokens []inputToken
		start := 0
		for {
			index := strings.Index(input[start:], separator)
			if index < 0 {
				tokens = append(tokens, inputToken{value: input[start:], start: start, end: len(input)})
				return tokens
			}
			end := start + index
			tokens = append(tokens, inputToken{value: input[start:end], start: start, end: end})
			start = end + len(separator)
		}
	}
	var tokens []inputToken
	for position := 0; position < len(input); {
		for position < len(input) {
			r, size := utf8.DecodeRuneInString(input[position:])
			if !unicode.IsSpace(r) {
				break
			}
			position += size
		}
		if position >= len(input) {
			break
		}
		start := position
		for position < len(input) {
			r, size := utf8.DecodeRuneInString(input[position:])
			if unicode.IsSpace(r) {
				break
			}
			position += size
		}
		tokens = append(tokens, inputToken{value: input[start:position], start: start, end: position})
	}
	return tokens
}

// decodeConfig accepts programmatic structs or map-backed JSON values without
// introducing a dependency on a particular JSON decoder.
func decodeConfig(input interface{}, output interface{}) error {
	if input == nil {
		return fmt.Errorf("configuration is required")
	}
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("configuration must contain one JSON value")
	}
	return nil
}
