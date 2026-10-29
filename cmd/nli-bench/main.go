package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/engine"
)

type BenchReport struct {
	Timestamp            string           `json:"timestamp"`
	EngineVersion        string           `json:"engine_version"`
	InputFile            string           `json:"input_file"`
	TotalLines           int64            `json:"total_lines"`
	TemplatDiscoveryRate float64          `json:"template_discovery_rate"`
	SuccessfulRouteRate  float64          `json:"successful_route_rate"`
	JSONValidity         float64          `json:"json_validity"`
	UnknownTemplatePct   float64          `json:"unknown_template_pct"`
	AvgLatencyUs         float64          `json:"avg_latency_us"`
	P99LatencyUs         float64          `json:"p99_latency_us"`
	MemoryMB             float64          `json:"memory_mb"`
	ParserCoverage       int              `json:"parser_coverage"`
	RouteBreakdown       map[string]int64 `json:"route_breakdown"`
	MetricScope          string           `json:"metric_scope"`
}

func main() {
	runCmd := flag.NewFlagSet("run", flag.ExitOnError)
	input := runCmd.String("input", "", "input log file")
	service := runCmd.String("service", "bench", "service name")
	output := runCmd.String("output", "bench-report.json", "output report file")

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: nli-bench run --input FILE [--service NAME] [--output FILE]")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "run":
		runCmd.Parse(os.Args[2:])
		if *input == "" {
			fmt.Fprintln(os.Stderr, "--input is required")
			os.Exit(2)
		}
		report, err := runBench(*input, *service)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bench:", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(*output, data, 0644); err != nil {
			fmt.Fprintln(os.Stderr, "write report:", err)
			os.Exit(1)
		}
		fmt.Printf("Benchmark complete. Report: %s\n", *output)
		printSummary(report)

	case "compare":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "usage: nli-bench compare OLD.json NEW.json")
			os.Exit(2)
		}
		compareReports(os.Args[2], os.Args[3])

	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}
}

func runBench(inputFile, service string) (*BenchReport, error) {
	f, err := os.Open(inputFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	deps := engine.Default()
	host, _ := os.Hostname()

	var total, jsonValid, unknown int64
	routes := map[string]int64{}
	parsers := map[string]bool{}
	latencies := []int64{}
	var memBefore runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), engine.MaxLine)
	line := int64(0)

	for scanner.Scan() {
		raw := scanner.Text()
		if strings.TrimSpace(raw) == "" {
			continue
		}
		line++
		e := engine.Parse(raw, engine.Source{Hostname: host, Service: service, File: inputFile, LineNumber: line}, deps)
		total++
		routes[e.Meta.Route]++
		parsers[e.NLI.Parser] = true
		latencies = append(latencies, e.Meta.Latency)
		if encoded, marshalErr := json.Marshal(e); marshalErr == nil && json.Valid(encoded) {
			jsonValid++
		}
		if e.Meta.Route == "unknown" {
			unknown++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	avgLat := 0.0
	p99 := int64(0)
	if len(latencies) > 0 {
		sum := int64(0)
		for _, l := range latencies {
			sum += l
		}
		avgLat = float64(sum) / float64(len(latencies))
		sorted := append([]int64{}, latencies...)
		sortInt64(sorted)
		p99 = sorted[int(float64(len(sorted))*0.99)]
	}

	discoveryRate := 0.0
	unknownPct := 0.0
	jsonValidPct := 0.0
	successfulRouteRate := 0.0
	if total > 0 {
		discoveryRate = float64(total-unknown) / float64(total) * 100
		unknownPct = float64(unknown) / float64(total) * 100
		jsonValidPct = float64(jsonValid) / float64(total) * 100
		successfulRouteRate = float64(routes["parsed"]) / float64(total) * 100
	}

	memDelta := int64(memAfter.HeapAlloc) - int64(memBefore.HeapAlloc)
	if memDelta < 0 {
		memDelta = 0
	}
	memMB := float64(memDelta) / 1024 / 1024

	return &BenchReport{
		Timestamp:            time.Now().UTC().Format(time.RFC3339),
		EngineVersion:        engine.Version,
		InputFile:            inputFile,
		TotalLines:           total,
		TemplatDiscoveryRate: discoveryRate,
		SuccessfulRouteRate:  successfulRouteRate,
		JSONValidity:         jsonValidPct,
		UnknownTemplatePct:   unknownPct,
		AvgLatencyUs:         avgLat,
		P99LatencyUs:         float64(p99),
		MemoryMB:             memMB,
		ParserCoverage:       len(parsers),
		RouteBreakdown:       routes,
		MetricScope:          "unlabeled operational corpus; successful_route_rate is not field-level accuracy",
	}, nil
}

func printSummary(r *BenchReport) {
	fmt.Printf("  Total lines:        %d\n", r.TotalLines)
	fmt.Printf("  Template discovery: %.1f%%\n", r.TemplatDiscoveryRate)
	fmt.Printf("  Successful routes:  %.1f%%\n", r.SuccessfulRouteRate)
	fmt.Printf("  JSON validity:      %.1f%%\n", r.JSONValidity)
	fmt.Printf("  Unknown template:   %.1f%%\n", r.UnknownTemplatePct)
	fmt.Printf("  Avg latency:        %.1f µs\n", r.AvgLatencyUs)
	fmt.Printf("  P99 latency:        %.0f µs\n", r.P99LatencyUs)
	fmt.Printf("  Parser coverage:    %d parsers used\n", r.ParserCoverage)
	fmt.Printf("  Routes: %v\n", r.RouteBreakdown)
}

func compareReports(oldPath, newPath string) {
	old := loadReport(oldPath)
	new_ := loadReport(newPath)

	fmt.Printf("%-30s  %-12s  %-12s  %-12s\n", "Metric", "Old", "New", "Delta")
	fmt.Println(strings.Repeat("-", 70))
	printDiff("Template discovery %", old.TemplatDiscoveryRate, new_.TemplatDiscoveryRate, true)
	printDiff("Successful route %", old.SuccessfulRouteRate, new_.SuccessfulRouteRate, true)
	printDiff("JSON validity %", old.JSONValidity, new_.JSONValidity, true)
	printDiff("Unknown template %", old.UnknownTemplatePct, new_.UnknownTemplatePct, false)
	printDiff("Avg latency µs", old.AvgLatencyUs, new_.AvgLatencyUs, false)
	printDiff("P99 latency µs", old.P99LatencyUs, new_.P99LatencyUs, false)
	printDiff("Memory MB", old.MemoryMB, new_.MemoryMB, false)
}

func loadReport(path string) *BenchReport {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load report:", err)
		os.Exit(1)
	}
	var r BenchReport
	json.Unmarshal(data, &r)
	return &r
}

func printDiff(label string, old, new_ float64, higherBetter bool) {
	delta := new_ - old
	sign := "+"
	if delta < 0 {
		sign = ""
	}
	indicator := ""
	if higherBetter && delta > 0 {
		indicator = "✓"
	} else if !higherBetter && delta < 0 {
		indicator = "✓"
	} else if delta != 0 {
		indicator = "✗"
	}
	fmt.Printf("%-30s  %-12.2f  %-12.2f  %s%.2f %s\n", label, old, new_, sign, delta, indicator)
}

func sortInt64(a []int64) {
	// Simple insertion sort for small-medium slices
	for i := 1; i < len(a); i++ {
		key := a[i]
		j := i - 1
		for j >= 0 && a[j] > key {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = key
	}
}
