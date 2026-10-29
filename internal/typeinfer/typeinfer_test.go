package typeinfer_test

import (
	"reflect"
	"testing"

	"nexora.local/nli-v20/internal/typeinfer"
)

func TestInferRepresentativeValues(t *testing.T) {
	tests := map[string]string{
		"true":                     "boolean",
		"OFF":                      "boolean",
		"-42":                      "integer",
		"3.14":                     "float",
		"2001:db8::1":              "ip",
		"2026-10-23":               "timestamp",
		"2026-10-23T10:11:12.123Z": "timestamp",
		"user@example.com":         "email",
		"https://example.com/path": "url",
		"plain text":               "string",
	}
	for input, want := range tests {
		if got := typeinfer.Infer(input); got != want {
			t.Errorf("Infer(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCoercePreservesOrConvertsValue(t *testing.T) {
	tests := []struct {
		input string
		want  interface{}
	}{
		{"42", int64(42)},
		{"3.5", float64(3.5)},
		{"yes", true},
		{"off", false},
		{"10.0.0.1", "10.0.0.1"},
	}
	for _, test := range tests {
		if got := typeinfer.Coerce(test.input); !reflect.DeepEqual(got, test.want) {
			t.Errorf("Coerce(%q) = %#v (%T), want %#v (%T)", test.input, got, got, test.want, test.want)
		}
	}
}
