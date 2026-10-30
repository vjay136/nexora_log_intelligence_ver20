package controlplane

import (
	"testing"
)

func TestValidateParserCandidateReportsBlankAndPassingCasesDeterministically(t *testing.T) {
	definition := map[string]any{
		"id": "mail-parser", "version": float64(1), "pattern_type": "regex",
		"pattern": `^(?P<level>[A-Z]+) (?P<message>.+)$`,
		"fields": []any{
			map[string]any{"name": "level", "source": "group:level", "type": "string", "required": true},
			map[string]any{"name": "message", "source": "group:message", "type": "string", "required": true},
		},
	}
	result := validateParserCandidate(definition, []assuranceRequest{
		{Raw: "INFO ready", Desired: map[string]any{"level": "INFO", "message": "ready"}},
		{Raw: "   ", Desired: map[string]any{"level": "INFO", "message": "ready"}},
	})
	if result["status"] != "failed" || result["passed"] != 1 || result["failed"] != 1 || result["total"] != 2 || result["reason"] == "" {
		t.Fatalf("unexpected validation accounting: %#v", result)
	}
	results, ok := result["results"].([]map[string]any)
	if !ok || len(results) != 2 || results[1]["passed"] != false {
		t.Fatalf("blank case was not reported as a deterministic failure: %#v", result["results"])
	}
}
