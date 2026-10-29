package bench_test

import (
	"os"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/bench"
	"nexora.local/nli-v20/internal/engine"
)

func TestRun_BasicAccuracy(t *testing.T) {
	// Write a small test log with known formats
	f, err := os.CreateTemp("", "bench-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	lines := []string{
		`{"level":"info","msg":"started","pid":1234}`,
		`Oct  2 10:00:01 myhost sshd[1234]: Accepted publickey for user from 192.168.1.1 port 22 ssh2`,
		`192.168.1.100 - frank [10/Oct/2000:13:55:36 -0700] "GET /apache_pb.gif HTTP/1.0" 200 2326`,
		`time="2024-01-15T10:30:00Z" level=info msg="server started" port=8080`,
	}
	f.WriteString(strings.Join(lines, "\n"))
	f.Close()

	deps := engine.Default()
	report, err := bench.Run("test", f.Name(), "test", deps)
	if err != nil {
		t.Fatalf("bench.Run: %v", err)
	}

	if report.TotalLines != 4 {
		t.Errorf("expected 4 lines, got %d", report.TotalLines)
	}
	if report.JSONValidity < 0.99 {
		t.Errorf("expected JSON validity near 1.0, got %.2f", report.JSONValidity)
	}
}

func TestCompareReport(t *testing.T) {
	a := &bench.Report{Label: "v1", SuccessfulRouteRate: 0.50, JSONValidity: 0.50, AvgLatencyUS: 200}
	b := &bench.Report{Label: "v20", SuccessfulRouteRate: 0.95, JSONValidity: 0.99, AvgLatencyUS: 50}
	md := bench.CompareReport(a, b)
	if !strings.Contains(md, "v1") || !strings.Contains(md, "v20") {
		t.Error("compare report should mention both labels")
	}
	if !strings.Contains(md, "Successful Route Rate") {
		t.Error("compare report should mention Successful Route Rate")
	}
}
