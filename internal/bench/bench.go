// Package bench implements the NLI benchmark harness.
// It measures parse accuracy, latency, and coverage across a sample log file.
package bench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/engine"
)

// Report holds benchmark results for one run.
type Report struct {
	Label                 string  `json:"label"`
	Timestamp             string  `json:"timestamp"`
	TotalLines            int     `json:"total_lines"`
	ParsedLines           int     `json:"parsed_lines"`
	UnknownLines          int     `json:"unknown_lines"`
	QuarantinedLines      int     `json:"quarantined_lines"`
	JSONValidLines        int     `json:"json_valid_lines"`
	TemplateMisses        int     `json:"template_misses"`
	TemplateDiscoveryRate float64 `json:"template_discovery_rate"`
	SuccessfulRouteRate   float64 `json:"successful_route_rate"`
	JSONValidity          float64 `json:"json_validity"`
	UnknownTemplatePct    float64 `json:"unknown_template_pct"`
	AvgLatencyUS          float64 `json:"avg_latency_us"`
	P99LatencyUS          int64   `json:"p99_latency_us"`
	MemoryMB              float64 `json:"memory_mb"`
	ParserCoverage        int     `json:"parser_coverage"` // distinct parsers used
	AICalls               int     `json:"ai_calls"`
	MetricScope           string  `json:"metric_scope"`
}

// Run executes a benchmark against the given log file using the provided engine deps.
func Run(label, inputPath, service string, deps *engine.Deps) (*Report, error) {
	f, err := os.Open(inputPath)
	if err != nil {
		return nil, fmt.Errorf("open input: %w", err)
	}
	defer f.Close()

	r := &Report{
		Label:       label,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		MetricScope: "unlabeled operational corpus; successful_route_rate is not field-level accuracy",
	}

	parsersUsed := make(map[string]struct{})
	var latencies []int64
	hostname, _ := os.Hostname()

	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, engine.MaxLine), engine.MaxLine)
	lineNum := int64(0)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		r.TotalLines++
		lineNum++

		src := engine.Source{
			Hostname:   hostname,
			Service:    service,
			File:       inputPath,
			LineNumber: lineNum,
		}

		env := engine.Parse(line, src, deps)
		latencies = append(latencies, env.Meta.Latency)

		// Validate JSON round-trip
		if b, err2 := json.Marshal(env); err2 == nil {
			var check map[string]interface{}
			if json.Unmarshal(b, &check) == nil {
				r.JSONValidLines++
			}
		}

		parsersUsed[env.NLI.Parser] = struct{}{}

		switch env.Meta.Route {
		case "unknown":
			r.UnknownLines++
			r.TemplateMisses++
		case "quarantine":
			r.QuarantinedLines++
		default:
			r.ParsedLines++
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan error: %w", err)
	}

	runtime.ReadMemStats(&memAfter)
	r.MemoryMB = float64(memAfter.Alloc-memBefore.Alloc) / (1024 * 1024)
	r.ParserCoverage = len(parsersUsed)

	if r.TotalLines > 0 {
		r.SuccessfulRouteRate = float64(r.ParsedLines) / float64(r.TotalLines)
		r.JSONValidity = float64(r.JSONValidLines) / float64(r.TotalLines)
		r.UnknownTemplatePct = float64(r.UnknownLines) / float64(r.TotalLines)
		r.TemplateDiscoveryRate = 1.0 - r.UnknownTemplatePct
	}

	if len(latencies) > 0 {
		var sum int64
		for _, l := range latencies {
			sum += l
		}
		r.AvgLatencyUS = float64(sum) / float64(len(latencies))
		sorted := append([]int64{}, latencies...)
		sortInt64(sorted)
		p99idx := int(float64(len(sorted)) * 0.99)
		if p99idx >= len(sorted) {
			p99idx = len(sorted) - 1
		}
		r.P99LatencyUS = sorted[p99idx]
	}

	return r, nil
}

func sortInt64(s []int64) {
	// insertion sort — benchmarks are small enough
	for i := 1; i < len(s); i++ {
		key := s[i]
		j := i - 1
		for j >= 0 && s[j] > key {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = key
	}
}

// CompareReport renders a markdown diff table comparing two reports.
func CompareReport(baseline, improved *Report) string {
	var sb strings.Builder
	sb.WriteString("# NLI Benchmark Comparison\n\n")
	sb.WriteString(fmt.Sprintf("| Metric | %s | %s | Delta |\n", baseline.Label, improved.Label))
	sb.WriteString("|--------|-------|---------|-------|\n")

	pct := func(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }
	us := func(v float64) string { return fmt.Sprintf("%.1fµs", v) }
	i64 := func(v int64) string { return fmt.Sprintf("%dµs", v) }
	mb := func(v float64) string { return fmt.Sprintf("%.1fMB", v) }

	row := func(name, a, b string, delta float64) {
		sign := ""
		if delta > 0 {
			sign = "+"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s%.1f%% |\n", name, a, b, sign, delta))
	}

	deltaF := func(a, b float64) float64 {
		if a == 0 {
			return 0
		}
		return (b - a) / a * 100
	}

	row("Template Discovery Rate", pct(baseline.TemplateDiscoveryRate), pct(improved.TemplateDiscoveryRate), deltaF(baseline.TemplateDiscoveryRate, improved.TemplateDiscoveryRate))
	row("Successful Route Rate", pct(baseline.SuccessfulRouteRate), pct(improved.SuccessfulRouteRate), deltaF(baseline.SuccessfulRouteRate, improved.SuccessfulRouteRate))
	row("JSON Validity", pct(baseline.JSONValidity), pct(improved.JSONValidity), deltaF(baseline.JSONValidity, improved.JSONValidity))
	row("Unknown Template %", pct(baseline.UnknownTemplatePct), pct(improved.UnknownTemplatePct), deltaF(baseline.UnknownTemplatePct, improved.UnknownTemplatePct))
	row("Avg Latency", us(baseline.AvgLatencyUS), us(improved.AvgLatencyUS), deltaF(baseline.AvgLatencyUS, improved.AvgLatencyUS))
	row("P99 Latency", i64(baseline.P99LatencyUS), i64(improved.P99LatencyUS), deltaF(float64(baseline.P99LatencyUS), float64(improved.P99LatencyUS)))
	row("Memory", mb(baseline.MemoryMB), mb(improved.MemoryMB), deltaF(baseline.MemoryMB, improved.MemoryMB))
	row("Parser Coverage", fmt.Sprint(baseline.ParserCoverage), fmt.Sprint(improved.ParserCoverage), deltaF(float64(baseline.ParserCoverage), float64(improved.ParserCoverage)))

	sb.WriteString(fmt.Sprintf("\n*Generated %s*\n", time.Now().UTC().Format(time.RFC3339)))
	return sb.String()
}
