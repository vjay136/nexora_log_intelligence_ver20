package confidence_test

import (
	"math"
	"testing"

	"nexora.local/nli-v20/internal/confidence"
)

func TestScoreAndRouteBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		score float64
		route string
	}{
		{"normal", 0.95, confidence.RouteNormal},
		{"warning", 0.80, confidence.RouteWarning},
		{"quarantine", 0.50, confidence.RouteQuarantine},
		{"unknown", 0.49, confidence.RouteUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := confidence.Route(test.score); got != test.route {
				t.Fatalf("Route(%v) = %q, want %q", test.score, got, test.route)
			}
		})
	}

	if got := confidence.Score(false, 0, 0, 0, false); got != 0 {
		t.Fatalf("unmatched parser score = %v, want 0", got)
	}
	got := confidence.Score(true, 1, 1, 3, true)
	if math.Abs(got-0.675) > 1e-9 {
		t.Fatalf("penalized score = %v, want 0.675", got)
	}
	if got := confidence.Score(true, 40, 0, 1, true); got != 0 {
		t.Fatalf("score must clamp at zero, got %v", got)
	}
}
