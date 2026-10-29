package parserpolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"nexora.local/nli-v20/internal/grok"
	"nexora.local/nli-v20/internal/mixedkv"
	"nexora.local/nli-v20/static-layout"
)

// LoadParserPack reads exactly one bounded JSON object, rejects unknown fields
// at the pack and typed stage-config levels, and verifies the canonical hash.
func LoadParserPack(reader io.Reader) (*ParserPack, error) {
	if reader == nil {
		return nil, fmt.Errorf("parser pack reader is nil")
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxPackSize+1))
	if err != nil {
		return nil, fmt.Errorf("read parser pack: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("parser pack is empty")
	}
	if len(data) > MaxPackSize {
		return nil, fmt.Errorf("parser pack exceeds %d bytes", MaxPackSize)
	}
	var pack ParserPack
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&pack); err != nil {
		return nil, fmt.Errorf("decode parser pack: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parser pack must contain exactly one JSON object")
	}
	if pack.SHA256 == "" {
		return nil, fmt.Errorf("parser pack sha256 is required")
	}
	result := ValidateParserPack(&pack)
	if !result.Valid {
		return nil, fmt.Errorf("invalid parser pack: %s", strings.Join(result.Errors, "; "))
	}
	return &pack, nil
}

func LoadParserPackBytes(data []byte) (*ParserPack, error) {
	return LoadParserPack(bytes.NewReader(data))
}

// CanonicalParserPackHash hashes the JSON representation with sha256 blanked.
func CanonicalParserPackHash(pack *ParserPack) (string, error) {
	if pack == nil {
		return "", fmt.Errorf("parser pack is nil")
	}
	copyOfPack := *pack
	copyOfPack.SHA256 = ""
	data, err := json.Marshal(copyOfPack)
	if err != nil {
		return "", fmt.Errorf("marshal canonical parser pack: %w", err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// SealParserPack updates SHA256 after all semantic fields are finalized.
func SealParserPack(pack *ParserPack) error {
	hash, err := CanonicalParserPackHash(pack)
	if err != nil {
		return err
	}
	pack.SHA256 = hash
	return nil
}

func decodeStrictConfig(input interface{}, output interface{}) error {
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
		return fmt.Errorf("configuration must contain one JSON object")
	}
	return nil
}

type staticStageConfig struct {
	ParserType    string                   `json:"parser_type,omitempty"`
	Separator     string                   `json:"separator,omitempty"`
	Tokens        []staticlayout.TokenSpec `json:"tokens,omitempty"`
	FailOnMissing bool                     `json:"fail_on_missing,omitempty"`
	MaxInputBytes int                      `json:"max_input_bytes,omitempty"`
}

func decodeStaticStageConfig(value interface{}) (staticlayout.Config, error) {
	var config staticStageConfig
	if err := decodeStrictConfig(value, &config); err != nil {
		return staticlayout.Config{}, err
	}
	if config.ParserType != "" {
		if config.ParserType != "smtp" || len(config.Tokens) != 0 {
			return staticlayout.Config{}, fmt.Errorf("parser_type supports only the legacy smtp preset and cannot be combined with tokens")
		}
		preset := staticlayout.CreateSMTPConfig()
		if config.MaxInputBytes > 0 {
			preset.MaxInputBytes = config.MaxInputBytes
		}
		return preset, nil
	}
	return staticlayout.Config{
		Separator: config.Separator, Tokens: config.Tokens,
		FailOnMissing: config.FailOnMissing, MaxInputBytes: config.MaxInputBytes,
	}, nil
}

type mixedKVStageConfig struct {
	InputField           string                  `json:"input_field,omitempty"`
	PairSeparators       []string                `json:"pair_separators,omitempty"`
	AssignmentSeparators []string                `json:"assignment_separators,omitempty"`
	QuoteCharacters      string                  `json:"quote_characters,omitempty"`
	EscapeCharacter      string                  `json:"escape_character,omitempty"`
	BracketPairs         map[string]string       `json:"bracket_pairs,omitempty"`
	KeyPattern           string                  `json:"key_pattern,omitempty"`
	KeyAllowPatterns     []string                `json:"key_allow_patterns,omitempty"`
	KeyDenyPatterns      []string                `json:"key_deny_patterns,omitempty"`
	StopKeys             []string                `json:"stop_keys,omitempty"`
	KeyAliases           map[string]string       `json:"key_aliases,omitempty"`
	ValueTypes           map[string]string       `json:"value_types,omitempty"`
	DuplicatePolicy      mixedkv.DuplicatePolicy `json:"duplicate_policy,omitempty"`
	RejectEmptyValues    bool                    `json:"reject_empty_values,omitempty"`
	AllowLeadingText     bool                    `json:"allow_leading_text,omitempty"`
	MaxInputBytes        int                     `json:"max_input_bytes,omitempty"`
	MaxFields            int                     `json:"max_fields,omitempty"`
	MaxKeyBytes          int                     `json:"max_key_bytes,omitempty"`
	MaxValueBytes        int                     `json:"max_value_bytes,omitempty"`
}

func decodeMixedKVStageConfig(value interface{}) (mixedKVStageConfig, mixedkv.Config, error) {
	var stage mixedKVStageConfig
	if err := decodeStrictConfig(value, &stage); err != nil {
		return stage, mixedkv.Config{}, err
	}
	config := mixedkv.Config{
		PairSeparators: stage.PairSeparators, AssignmentSeparators: stage.AssignmentSeparators,
		QuoteCharacters: stage.QuoteCharacters, EscapeCharacter: stage.EscapeCharacter,
		BracketPairs: stage.BracketPairs, KeyPattern: stage.KeyPattern,
		KeyAllowPatterns: stage.KeyAllowPatterns, KeyDenyPatterns: stage.KeyDenyPatterns,
		StopKeys: stage.StopKeys, KeyAliases: stage.KeyAliases, ValueTypes: stage.ValueTypes,
		DuplicatePolicy: stage.DuplicatePolicy, RejectEmptyValues: stage.RejectEmptyValues,
		AllowLeadingText: stage.AllowLeadingText,
		MaxInputBytes:    stage.MaxInputBytes, MaxFields: stage.MaxFields,
		MaxKeyBytes: stage.MaxKeyBytes, MaxValueBytes: stage.MaxValueBytes,
	}
	return stage, config, nil
}

type regexStageConfig struct {
	InputField string            `json:"input_field,omitempty"`
	Pattern    string            `json:"pattern"`
	Groups     map[string]string `json:"groups,omitempty"`
}

type grokStageConfig struct {
	InputField string            `json:"input_field,omitempty"`
	Pattern    string            `json:"pattern"`
	Patterns   map[string]string `json:"patterns,omitempty"`
}

type conversionStageConfig struct {
	Conversions map[string]string `json:"conversions"`
}

type projectionStageConfig struct {
	Mode          ProjectionMode `json:"mode,omitempty"`
	AllowedFields []string       `json:"allowed_fields,omitempty"`
	DeniedFields  []string       `json:"denied_fields,omitempty"`
}

type jsonStageConfig struct {
	InputField string `json:"input_field,omitempty"`
}

type timestampStageConfig struct {
	InputField  string   `json:"input_field"`
	OutputField string   `json:"output_field,omitempty"`
	Layouts     []string `json:"layouts"`
}

type replaceStageConfig struct {
	Field       string `json:"field"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Regex       bool   `json:"regex,omitempty"`
}

type renameStageConfig struct {
	Fields map[string]string `json:"fields"`
}

type conditionalStageConfig struct {
	Field    string `json:"field,omitempty"`
	Operator string `json:"operator"`
	Value    string `json:"value,omitempty"`
	Pattern  string `json:"pattern,omitempty"`
}

type outputMappingStageConfig struct {
	Fields    map[string]string      `json:"fields,omitempty"`
	Constants map[string]interface{} `json:"constants,omitempty"`
}

type dropStageConfig struct {
	Reason string `json:"reason,omitempty"`
}

type semanticExtractStageConfig struct {
	Entities          []string          `json:"entities,omitempty"`
	DecodeMIMEHeaders bool              `json:"decode_mime_headers,omitempty"`
	InputField        string            `json:"input_field,omitempty"`
	OutputFields      map[string]string `json:"output_fields,omitempty"`
	MaxInputBytes     int               `json:"max_input_bytes,omitempty"`
	MaxHeaderBytes    int               `json:"max_header_bytes,omitempty"`
}

func validateStageConfig(stage Stage) error {
	switch stage.Type {
	case StageTypeStaticLayout, StageTypeDelimited:
		config, err := decodeStaticStageConfig(stage.Config)
		if err != nil {
			return err
		}
		_, err = staticlayout.NewParser(config)
		return err
	case StageTypeMixedKV, StageTypeKV, StageTypeLogfmt:
		_, config, err := decodeMixedKVStageConfig(stage.Config)
		if err != nil {
			return err
		}
		_, err = mixedkv.Compile(config)
		return err
	case StageTypeRegex:
		var config regexStageConfig
		if err := decodeStrictConfig(stage.Config, &config); err != nil {
			return err
		}
		if config.Pattern == "" || len(config.Pattern) > MaxRegexLength {
			return fmt.Errorf("regex pattern is required and must not exceed %d bytes", MaxRegexLength)
		}
		_, err := regexp.Compile(config.Pattern)
		return err
	case StageTypeGrok:
		var config grokStageConfig
		if err := decodeStrictConfig(stage.Config, &config); err != nil {
			return err
		}
		if config.Pattern == "" || len(config.Pattern) > MaxRegexLength {
			return fmt.Errorf("grok pattern is required and must not exceed %d bytes", MaxRegexLength)
		}
		_, err := grok.New(config.Patterns).Compile(config.Pattern)
		return err
	case StageTypeTypeConversion:
		var config conversionStageConfig
		if err := decodeStrictConfig(stage.Config, &config); err != nil {
			return err
		}
		if len(config.Conversions) == 0 {
			return fmt.Errorf("conversions are required")
		}
		for field, target := range config.Conversions {
			if !isValidIdentifier(field) || !validConversionType(target) {
				return fmt.Errorf("invalid conversion for field %q", field)
			}
		}
		return nil
	case StageTypeFieldProjection:
		var config projectionStageConfig
		return decodeStrictConfig(stage.Config, &config)
	case StageTypeJSON:
		var config jsonStageConfig
		return decodeStrictConfig(stage.Config, &config)
	case StageTypeTimestamp:
		var config timestampStageConfig
		if err := decodeStrictConfig(stage.Config, &config); err != nil {
			return err
		}
		if config.InputField == "" || len(config.Layouts) == 0 {
			return fmt.Errorf("timestamp requires input_field and layouts")
		}
		return nil
	case StageTypeReplace:
		var config replaceStageConfig
		if err := decodeStrictConfig(stage.Config, &config); err != nil {
			return err
		}
		if config.Field == "" || config.Pattern == "" {
			return fmt.Errorf("replace requires field and pattern")
		}
		if config.Regex {
			_, err := regexp.Compile(config.Pattern)
			return err
		}
		return nil
	case StageTypeRename:
		var config renameStageConfig
		if err := decodeStrictConfig(stage.Config, &config); err != nil {
			return err
		}
		if len(config.Fields) == 0 {
			return fmt.Errorf("rename requires fields")
		}
		return nil
	case StageTypeConditionalMatch:
		var config conditionalStageConfig
		if err := decodeStrictConfig(stage.Config, &config); err != nil {
			return err
		}
		switch config.Operator {
		case "exists", "equals", "contains", "regex":
		default:
			return fmt.Errorf("unsupported conditional operator %q", config.Operator)
		}
		if config.Operator == "regex" {
			_, err := regexp.Compile(config.Pattern)
			return err
		}
		return nil
	case StageTypeOutputMapping:
		var config outputMappingStageConfig
		return decodeStrictConfig(stage.Config, &config)
	case StageTypeDrop:
		var config dropStageConfig
		return decodeStrictConfig(stage.Config, &config)
	case StageTypeSemanticExtract:
		var config semanticExtractStageConfig
		if err := decodeStrictConfig(stage.Config, &config); err != nil {
			return err
		}
		allowed := map[string]bool{"ipv4": true, "ipv6": true, "domain": true, "url": true, "email": true}
		seen := map[string]bool{}
		for _, entity := range config.Entities {
			if !allowed[entity] || seen[entity] {
				return fmt.Errorf("semantic extraction entity %q is unsupported or duplicated", entity)
			}
			seen[entity] = true
		}
		if len(config.Entities) == 0 && !config.DecodeMIMEHeaders {
			return fmt.Errorf("semantic extraction requires entities or MIME header decoding")
		}
		if config.MaxInputBytes < 0 || config.MaxInputBytes > 1<<20 || config.MaxHeaderBytes < 0 || config.MaxHeaderBytes > 64<<10 || config.MaxHeaderBytes > config.MaxInputBytes && config.MaxInputBytes > 0 {
			return fmt.Errorf("semantic extraction limits exceed configured bounds")
		}
		if config.InputField != "" && !isValidIdentifier(config.InputField) {
			return fmt.Errorf("semantic extraction input_field is invalid")
		}
		for entity, output := range config.OutputFields {
			if entity != "ipv4" && entity != "ipv6" && entity != "domain" && entity != "url" && entity != "email" && entity != "email_subject_decoded" {
				return fmt.Errorf("semantic extraction output mapping %q is unsupported", entity)
			}
			if !isValidIdentifier(output) {
				return fmt.Errorf("semantic extraction output field %q is invalid", output)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported stage type %q", stage.Type)
	}
}

func validConversionType(value string) bool {
	switch strings.ToLower(value) {
	case "string", "int", "integer", "float", "bool", "boolean", "time", "ip", "url", "email":
		return true
	default:
		return false
	}
}
