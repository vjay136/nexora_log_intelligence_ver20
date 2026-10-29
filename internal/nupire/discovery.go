package nupire

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/detector"
)

type Structure string

const (
	StructureJSON        Structure = "json"
	StructureKV          Structure = "key_value"
	StructureDelimited   Structure = "delimited"
	StructureSyslog      Structure = "syslog"
	StructureAccess      Structure = "access"
	StructureTimestamped Structure = "timestamped"
	StructureUnknown     Structure = "unknown"
)

type Hypothesis struct {
	Structure  Structure `json:"structure"`
	Evidence   []string  `json:"evidence"`
	Confidence float64   `json:"confidence"`
	Ambiguous  bool      `json:"ambiguous"`
	Delimiter  string    `json:"delimiter,omitempty"`
}

// Classify returns a deterministic structural hypothesis with observable evidence.
func Classify(line string) Hypothesis {
	s := strings.TrimSpace(line)
	h := Hypothesis{Structure: StructureUnknown, Evidence: []string{"no recognized structural evidence"}}
	if len(line) > MaxRecordBytes {
		return Hypothesis{Structure: StructureUnknown, Evidence: []string{"record exceeds discovery size limit"}}
	}
	if s == "" {
		return h
	}
	if json.Valid([]byte(s)) && strings.HasPrefix(s, "{") {
		return Hypothesis{Structure: StructureJSON, Evidence: []string{"valid JSON object"}, Confidence: 1}
	}

	f := detector.Detect(s)
	switch f {
	case detector.FormatSyslogRFC5424, detector.FormatSyslogRFC3164:
		return Hypothesis{Structure: StructureSyslog, Evidence: []string{"NLI detector matched " + f}, Confidence: .97}
	case detector.FormatCLF, detector.FormatNginx:
		return Hypothesis{Structure: StructureAccess, Evidence: []string{"NLI detector matched " + f}, Confidence: .97}
	case detector.FormatKVPairs, detector.FormatLogfmt:
		return Hypothesis{Structure: StructureKV, Evidence: []string{"NLI detector matched " + f}, Confidence: .9}
	}
	if looksTimestamped(s) {
		return Hypothesis{Structure: StructureTimestamped, Evidence: []string{"leading token resembles RFC3339 or syslog timestamp"}, Confidence: .75}
	}
	if delim, score, ok := InferDelimiter([]string{s}); ok {
		return Hypothesis{Structure: StructureDelimited, Evidence: []string{fmt.Sprintf("delimiter %q yields repeated fields", delim)}, Confidence: score, Delimiter: string(delim)}
	}
	if strings.Count(s, "=") > 0 || strings.Count(s, ":") > 0 {
		return Hypothesis{Structure: StructureKV, Evidence: []string{"key/value separator present but structure is weak"}, Confidence: .45, Ambiguous: true}
	}
	return h
}

func looksTimestamped(s string) bool {
	if len(s) >= 20 {
		if _, err := time.Parse(time.RFC3339Nano, s[:20]); err == nil {
			return true
		}
		if len(s) >= 19 {
			if _, err := time.Parse("2006-01-02 15:04:05", s[:19]); err == nil {
				return true
			}
		}
	}
	return len(s) >= 16 && strings.Contains(s[:16], ":") && strings.Contains(s[:16], " ") && strings.ContainsAny(s[:3], "0123456789")
}

// SplitFields separates one record while honoring quoted and backslash-escaped delimiters.
func SplitFields(input string, delimiter rune) ([]string, error) {
	return splitFieldsLimit(input, delimiter, MaxTokens)
}

func splitFieldsLimit(input string, delimiter rune, limit int) ([]string, error) {
	if delimiter == 0 {
		return nil, fmt.Errorf("delimiter_empty")
	}
	if len(input) > MaxRecordBytes || limit < 1 {
		return nil, fmt.Errorf("record_limit")
	}
	fields := make([]string, 0, 8)
	var b strings.Builder
	quote := rune(0)
	escaped := false
	for _, r := range input {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == delimiter {
			if len(fields)+1 >= limit {
				return nil, fmt.Errorf("token_limit")
			}
			fields = append(fields, strings.TrimSpace(b.String()))
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("quote_unclosed")
	}
	fields = append(fields, strings.TrimSpace(b.String()))
	return fields, nil
}

// InferDelimiter chooses the delimiter with the most consistent multi-field rows.
func InferDelimiter(lines []string) (rune, float64, bool) {
	if len(lines) == 0 || len(lines) > MaxTokens {
		return 0, 0, false
	}
	candidates := []rune{',', '\t', '|', ';'}
	best, bestScore, bestCount := rune(0), 0.0, 1
	for _, d := range candidates {
		counts := map[int]int{}
		valid := 0
		for _, line := range lines {
			fields, err := SplitFields(line, d)
			if err == nil && len(fields) > 1 {
				counts[len(fields)]++
				valid++
			}
		}
		if valid == 0 {
			continue
		}
		n, count := 1, 0
		for k, v := range counts {
			if v > count || (v == count && k < n) {
				n, count = k, v
			}
		}
		score := float64(count) / float64(len(lines))
		if count > 0 && (score > bestScore || (score == bestScore && n > bestCount)) {
			best, bestScore, bestCount = d, score, n
		}
	}
	return best, bestScore, best != 0
}
