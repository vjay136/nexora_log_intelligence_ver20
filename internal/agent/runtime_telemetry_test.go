package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestStreamTelemetryFirstPublishStartsRateBaselineAtCurrentCounters(t *testing.T) {
	state := &streamState{nodeID: "node", sourceWorkers: 2}
	state.readLines.Store(40)
	state.bytes.Store(4000)
	first := state.publish("/source.log", "reading", 0, 256, time.Unix(100, 0))
	if first.ReadLines != 40 || first.Bytes != 4000 {
		t.Fatalf("first sample lost cumulative counts: %+v", first)
	}
	state.readLines.Add(10)
	state.validationAttempts.Add(10)
	state.validationPassed.Add(9)
	state.validationFailed.Add(1)
	state.bytes.Add(1000)
	second := state.publish("/source.log", "reading", 8, 256, time.Unix(101, 0))
	if second.ReadLinesPerSecond != 10 || second.BytesPerSecond != 1000 {
		t.Fatalf("rates include pre-baseline counters: %+v", second)
	}
	if second.ValidationAttemptsPerSecond != 10 || second.ValidationPassedPerSecond != 9 || second.ValidationFailedPerSecond != 1 || second.ValidationAttempts != 10 || second.ValidationPassed != 9 || second.ValidationFailed != 1 {
		t.Fatalf("validation counters/rates were not published exactly: %+v", second)
	}
}

func TestStreamTelemetryReportsSourceErrorsAndOutputBackpressure(t *testing.T) {
	state := &streamState{nodeID: "node", sourceWorkers: 4}
	state.publish("/source.log", "reading", 0, 10, time.Unix(100, 0))
	state.errors.Store(2)
	state.readLines.Add(10)
	sample := state.publish("/source.log", "reading", 8, 10, time.Unix(101, 0))
	if sample.Errors != 2 {
		t.Fatalf("source errors = %d, want 2", sample.Errors)
	}
	if !sample.Backpressure || sample.QueueDepth != 8 || sample.QueueCapacity != 10 {
		t.Fatalf("bounded output saturation should be visible: %+v", sample)
	}
}

func TestJournalProgressIsAggregatedAndOutputRecoveryIsTransitionOnly(t *testing.T) {
	var output bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previousLogger)
	journal := newJournal(time.Hour)
	journal.progress("/source.log", "eof_wait", journalCounts{read: 100, parsed: 90, quarantined: 10, bytes: 1000})
	journal.progress("/source.log", "eof_wait", journalCounts{read: 101, parsed: 91, quarantined: 10, bytes: 1010})
	journal.progress("/source.log", "reading", journalCounts{read: 101, parsed: 91, quarantined: 10, bytes: 1010})
	journal.progress("/source.log", "reading", journalCounts{read: 101, parsed: 91, quarantined: 10, bytes: 1010})
	journal.progress("/source.log", "eof_wait", journalCounts{read: 101, parsed: 91, quarantined: 10, bytes: 1010})
	journal.outputFailure("/source.log", "disk unavailable")
	journal.outputRecovered("/source.log")
	journal.outputRecovered("/source.log")
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 5 { // initial summary + two phase changes + failure + recovery
		t.Fatalf("journal emitted %d events, want bounded summaries and one failure/recovery transition: %s", len(lines), output.String())
	}
	var initial, active, complete map[string]any
	if json.Unmarshal([]byte(lines[0]), &initial) != nil || json.Unmarshal([]byte(lines[1]), &active) != nil || json.Unmarshal([]byte(lines[2]), &complete) != nil {
		t.Fatalf("journal progress is not valid structured JSON: %s", output.String())
	}
	if initial["phase"] != "eof_wait" || initial["processed_total"] != float64(100) || initial["read_total"] != float64(100) || initial["bytes_total"] != float64(1000) {
		t.Fatalf("initial cumulative progress is incomplete: %#v", initial)
	}
	if active["phase"] != "reading" || active["active"] != true || active["read_delta"] != float64(1) || active["processed_delta"] != float64(1) {
		t.Fatalf("active transition did not retain accumulated deltas: %#v", active)
	}
	if complete["phase"] != "eof_wait" || complete["complete_to_eof"] != true || complete["processed_total"] != float64(101) {
		t.Fatalf("EOF completion summary is incomplete: %#v", complete)
	}
}

func TestCounterDeltaHandlesCounterReset(t *testing.T) {
	if got := counterDelta(3, 10); got != 3 {
		t.Fatalf("counter reset delta = %d, want 3", got)
	}
}
