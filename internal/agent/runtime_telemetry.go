package agent

import (
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"nexora.local/nli-v20/telemetry"
)

type journalLimiter struct {
	mu       sync.Mutex
	last     map[string]time.Time
	previous map[string]journalProgress
	failed   map[string]bool
	interval time.Duration
}

type journalCounts struct{ read, parsed, quarantined, errors, bytes uint64 }

type journalProgress struct {
	counts journalCounts
	phase  string
	logged time.Time
}

type sampleSet struct {
	mu    sync.RWMutex
	files map[string]telemetry.StreamFileTelemetry
}

func newSampleSet() *sampleSet { return &sampleSet{files: map[string]telemetry.StreamFileTelemetry{}} }
func (s *sampleSet) put(sample telemetry.StreamFileTelemetry) {
	if sample.Source == "" {
		return
	}
	s.mu.Lock()
	if len(s.files) >= 256 {
		if _, exists := s.files[sample.Source]; !exists {
			s.mu.Unlock()
			return
		}
	}
	s.files[sample.Source] = sample
	s.mu.Unlock()
}
func (s *sampleSet) snapshot() []telemetry.StreamFileTelemetry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]telemetry.StreamFileTelemetry, 0, len(s.files))
	for _, sample := range s.files {
		out = append(out, sample)
	}
	return out
}

func newJournal(interval time.Duration) *journalLimiter {
	return &journalLimiter{last: map[string]time.Time{}, previous: map[string]journalProgress{}, failed: map[string]bool{}, interval: interval}
}
func (j *journalLimiter) event(kind, source, detail string) {
	key := kind + "\x00" + source
	now := time.Now()
	j.mu.Lock()
	previous := j.last[key]
	if !previous.IsZero() && now.Sub(previous) < j.interval {
		j.mu.Unlock()
		return
	}
	j.last[key] = now
	j.mu.Unlock()
	attrs := []any{"source", source, "event", kind}
	if detail != "" {
		attrs = append(attrs, "detail", detail)
	}
	slog.Info("runtime", attrs...)
}

// progress emits a bounded, cumulative source summary. Counter changes are
// accumulated until the next interval, while a phase transition is emitted
// immediately so operators can distinguish active reading from EOF wait.
func (j *journalLimiter) progress(source, phase string, current journalCounts) {
	now := time.Now()
	j.mu.Lock()
	previous := j.previous[source]
	changed := current != previous.counts
	phaseChanged := phase != previous.phase
	idleHeartbeat := 6 * j.interval
	if idleHeartbeat < time.Minute {
		idleHeartbeat = time.Minute
	}
	if !changed && !phaseChanged && !previous.logged.IsZero() && now.Sub(previous.logged) < idleHeartbeat {
		j.mu.Unlock()
		return
	}
	if changed && !phaseChanged && !previous.logged.IsZero() && now.Sub(previous.logged) < j.interval {
		j.mu.Unlock()
		return
	}
	j.previous[source] = journalProgress{counts: current, phase: phase, logged: now}
	j.mu.Unlock()
	readDelta := counterDelta(current.read, previous.counts.read)
	parsedDelta := counterDelta(current.parsed, previous.counts.parsed)
	quarantinedDelta := counterDelta(current.quarantined, previous.counts.quarantined)
	errorDelta := counterDelta(current.errors, previous.counts.errors)
	bytesDelta := counterDelta(current.bytes, previous.counts.bytes)
	attrs := []any{
		"source", source, "event", "progress", "phase", phase,
		"active", phase == "reading", "complete_to_eof", phase == "eof_wait",
		"read_delta", readDelta, "processed_delta", parsedDelta + quarantinedDelta,
		"parsed_delta", parsedDelta, "quarantined_delta", quarantinedDelta,
		"errors_delta", errorDelta, "bytes_delta", bytesDelta,
		"read_total", current.read, "processed_total", current.parsed + current.quarantined,
		"parsed_total", current.parsed, "quarantined_total", current.quarantined,
		"errors_total", current.errors, "bytes_total", current.bytes,
	}
	slog.Info("runtime", attrs...)
}

func counterDelta(current, previous uint64) uint64 {
	if current < previous {
		return current
	}
	return current - previous
}

func (j *journalLimiter) outputFailure(source, detail string) {
	j.mu.Lock()
	j.failed[source] = true
	j.mu.Unlock()
	j.event("output_failure", source, detail)
}

func (j *journalLimiter) outputRecovered(source string) {
	j.mu.Lock()
	failed := j.failed[source]
	delete(j.failed, source)
	j.mu.Unlock()
	if failed {
		j.event("output_recovery", source, "output writes resumed")
	}
}

type streamState struct {
	nodeID                                                                                                                    string
	sourceWorkers                                                                                                             int
	thresholds                                                                                                                telemetry.StreamThresholds
	capacity                                                                                                                  int
	readLines, normalized, quarantined, validationAttempts, validationPassed, validationFailed, bytes, errors                 atomic.Uint64
	readDuration, parseDuration, outputDuration, totalDuration                                                                atomic.Int64
	lastMu                                                                                                                    sync.Mutex
	last                                                                                                                      time.Time
	prevLines, prevNormalized, prevQuarantined, prevValidationAttempts, prevValidationPassed, prevValidationFailed, prevBytes uint64
	prevOutputDuration                                                                                                        int64
}

func (s *streamState) addParse(d time.Duration)  { s.parseDuration.Add(int64(d)) }
func (s *streamState) addOutput(d time.Duration) { s.outputDuration.Add(int64(d)) }
func (s *streamState) addTotal(d time.Duration)  { s.totalDuration.Add(int64(d)) }
func (s *streamState) publish(source, phase string, depth, capacity int, now time.Time) telemetry.StreamFileTelemetry {
	s.lastMu.Lock()
	defer s.lastMu.Unlock()
	if s.last.IsZero() {
		s.prevLines = s.readLines.Load()
		s.prevNormalized = s.normalized.Load()
		s.prevQuarantined = s.quarantined.Load()
		s.prevValidationAttempts = s.validationAttempts.Load()
		s.prevValidationPassed = s.validationPassed.Load()
		s.prevValidationFailed = s.validationFailed.Load()
		s.prevBytes = s.bytes.Load()
		s.prevOutputDuration = s.outputDuration.Load()
		s.last = now
		return telemetry.StreamFileTelemetry{NodeID: s.nodeID, Source: source, Phase: phase, SourceWorkers: s.sourceWorkers, ReadLines: s.prevLines, Normalized: s.prevNormalized, Quarantined: s.prevQuarantined, ValidationAttempts: s.prevValidationAttempts, ValidationPassed: s.prevValidationPassed, ValidationFailed: s.prevValidationFailed, Errors: s.errors.Load(), Bytes: s.prevBytes, QueueDepth: depth, QueueCapacity: capacity, Backpressure: queueBackpressure(depth, capacity, s.thresholds.QueueDepth), Thresholds: s.thresholds, UpdatedAt: now.UTC()}
	}
	elapsed := now.Sub(s.last).Seconds()
	if elapsed <= 0 {
		return telemetry.StreamFileTelemetry{}
	}
	rl := s.readLines.Load()
	no := s.normalized.Load()
	q := s.quarantined.Load()
	va := s.validationAttempts.Load()
	vp := s.validationPassed.Load()
	vf := s.validationFailed.Load()
	b := s.bytes.Load()
	outputDuration := s.outputDuration.Load()
	outputDelta := outputDuration - s.prevOutputDuration
	outputWaitBackpressure := float64(outputDelta) >= elapsed*float64(time.Second)*0.5
	sample := telemetry.StreamFileTelemetry{NodeID: s.nodeID, Source: source, Phase: phase, SourceWorkers: s.sourceWorkers, ReadLinesPerSecond: float64(rl-s.prevLines) / elapsed, NormalizedPerSecond: float64(no-s.prevNormalized) / elapsed, QuarantinedPerSecond: float64(q-s.prevQuarantined) / elapsed, ValidationAttemptsPerSecond: float64(va-s.prevValidationAttempts) / elapsed, ValidationPassedPerSecond: float64(vp-s.prevValidationPassed) / elapsed, ValidationFailedPerSecond: float64(vf-s.prevValidationFailed) / elapsed, BytesPerSecond: float64(b-s.prevBytes) / elapsed, QueueDepth: depth, QueueCapacity: capacity, Backpressure: queueBackpressure(depth, capacity, s.thresholds.QueueDepth) || outputWaitBackpressure, ReadDuration: time.Duration(s.readDuration.Load()), ParseDuration: time.Duration(s.parseDuration.Load()), OutputDuration: time.Duration(outputDuration), TotalDuration: time.Duration(s.totalDuration.Load()), ReadLines: rl, Normalized: no, Quarantined: q, ValidationAttempts: va, ValidationPassed: vp, ValidationFailed: vf, Errors: s.errors.Load(), Bytes: b, UpdatedAt: now.UTC(), Thresholds: s.thresholds}
	s.prevLines, s.prevNormalized, s.prevQuarantined, s.prevValidationAttempts, s.prevValidationPassed, s.prevValidationFailed, s.prevBytes, s.prevOutputDuration, s.last = rl, no, q, va, vp, vf, b, outputDuration, now
	return sample
}

func queueBackpressure(depth, capacity, configuredThreshold int) bool {
	if capacity <= 0 {
		return false
	}
	threshold := configuredThreshold
	if threshold <= 0 || threshold > capacity {
		threshold = (capacity * 80) / 100
		if threshold < 1 {
			threshold = 1
		}
	}
	return depth >= threshold
}

func init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
}
