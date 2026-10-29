package timestamp_test

import (
	"testing"
	"time"

	ts "nexora.local/nli-v20/internal/timestamp"
)

var ingest = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)

func TestISO8601(t *testing.T) {
	r := ts.Extract("2026-10-02T10:00:00Z event happened", ingest)
	if r.Inferred {
		t.Error("should not be inferred")
	}
	if r.Time.Year() != 2026 {
		t.Errorf("year = %d, want 2026", r.Time.Year())
	}
}

func TestApacheCLF(t *testing.T) {
	r := ts.Extract("[02/Oct/2026:10:00:00 +0000] GET /", ingest)
	if r.Inferred {
		t.Error("CLF timestamp not extracted")
	}
}

func TestSyslogRFC3164(t *testing.T) {
	r := ts.Extract("Oct  2 10:00:00 host program: message", ingest)
	if r.Inferred {
		t.Error("syslog timestamp not extracted")
	}
}

func TestEpochMillis(t *testing.T) {
	r := ts.Extract("1727862000000 event", ingest)
	if r.Inferred {
		t.Error("epoch ms not extracted")
	}
}

func TestFallbackToIngest(t *testing.T) {
	r := ts.Extract("no timestamp in this line at all", ingest)
	if !r.Inferred {
		t.Error("should fall back to ingest time")
	}
	if !r.Time.Equal(ingest) {
		t.Errorf("ingest time mismatch: %v vs %v", r.Time, ingest)
	}
}
