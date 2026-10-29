// Package benchmarks provides comprehensive performance testing for NLI v5.
// This implements Task 28: Performance benchmarks covering parsing throughput,
// API response times, memory usage, and concurrent load handling.
package benchmarks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/auth"
	"nexora.local/nli-v20/internal/controlplane"
	"nexora.local/nli-v20/internal/engine"
)

// BenchmarkSuite provides comprehensive performance testing
type BenchmarkSuite struct {
	server    *controlplane.Server
	authStore *auth.Store
}

// NewBenchmarkSuite creates a new performance benchmark suite
func NewBenchmarkSuite() *BenchmarkSuite {
	authStore, _ := auth.NewStoreWithBootstrap("/tmp/bench_users.json", "bench-admin-password-12345")

	cfg := controlplane.Config{
		ListenAddress:          "127.0.0.1:0",
		StateDir:               "/tmp/bench_state",
		EnrollmentToken:        "bench-token-123456789012345",
		BootstrapAdminPassword: "bench-admin-password-12345",
		RateLimit:              1_000_000,
		RateWindowSeconds:      3600,
	}

	server, _ := controlplane.New(cfg)

	return &BenchmarkSuite{
		server:    server,
		authStore: authStore,
	}
}

// BenchmarkParserThroughput tests raw parsing performance
func BenchmarkParserThroughput(b *testing.B) {
	testLogs := []string{
		`{"level":"info","msg":"user logged in","user_id":12345,"timestamp":"2026-10-05T10:00:00Z"}`,
		"Oct  5 10:00:00 localhost sshd[1234]: Accepted password for user from 192.168.1.100 port 22 ssh2",
		`192.168.1.100 - - [05/Oct/2026:10:00:00 +0000] "GET /api/v1/users HTTP/1.1" 200 1234 "-" "curl/7.68.0"`,
		"2026-10-05T10:00:00.123Z ERROR [main] com.example.App - Database connection failed: timeout after 30s",
	}

	source := engine.Source{
		Hostname: "benchmark-host",
		Service:  "benchmark",
		File:     "benchmark.log",
	}

	deps := engine.Default()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		logLine := testLogs[i%len(testLogs)]
		result := engine.Parse(logLine, source, deps)
		if result.NLI.Version == "" {
			b.Fatal("Parse returned an uninitialized envelope")
		}
	}
}

// BenchmarkParserConcurrency tests concurrent parsing performance
func BenchmarkParserConcurrency(b *testing.B) {
	testLog := `{"level":"info","msg":"concurrent test","request_id":"req-${ID}","timestamp":"2026-10-05T10:00:00Z"}`
	source := engine.Source{
		Hostname: "benchmark-host",
		Service:  "concurrent",
		File:     "concurrent.log",
	}

	deps := engine.Default()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		id := 0
		for pb.Next() {
			logLine := strings.Replace(testLog, "${ID}", fmt.Sprintf("%d", id), 1)
			result := engine.Parse(logLine, source, deps)
			if result.NLI.Version == "" {
				b.Fatal("Parse returned an uninitialized envelope")
			}
			id++
		}
	})
}

// BenchmarkAuthenticationAPI tests auth API performance
func BenchmarkAuthenticationAPI(b *testing.B) {
	suite := NewBenchmarkSuite()
	server := httptest.NewServer(suite.server.Mux())
	defer server.Close()

	loginReq := map[string]string{
		"username": "admin",
		"password": "bench-admin-password-12345",
	}
	loginData, _ := json.Marshal(loginReq)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest("POST", server.URL+"/api/v1/auth/login",
			bytes.NewReader(loginData))
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			b.Fatalf("Expected status 200, got %d", resp.StatusCode)
		}
	}
}

// BenchmarkParsePreviewAPI tests parse preview API performance.
func BenchmarkParsePreviewAPI(b *testing.B) {
	suite := NewBenchmarkSuite()
	server := httptest.NewServer(suite.server.Mux())
	defer server.Close()

	client, csrf := getAuthClient(b, server.URL)
	previewData, _ := json.Marshal(map[string]string{
		"raw":     `{"level":"info","msg":"benchmark test","timestamp":"2026-10-05T10:00:00Z"}`,
		"service": "benchmark",
	})

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/assurance/run", bytes.NewReader(previewData))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", csrf)
		response, err := client.Do(request)
		if err != nil {
			b.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			b.Fatalf("Expected status 200, got %d", response.StatusCode)
		}
	}
}

// BenchmarkConcurrentAPILoad tests API under concurrent load.
func BenchmarkConcurrentAPILoad(b *testing.B) {
	suite := NewBenchmarkSuite()
	server := httptest.NewServer(suite.server.Mux())
	defer server.Close()

	client, _ := getAuthClient(b, server.URL)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/overview", nil)
			response, err := client.Do(request)
			if err != nil {
				b.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				b.Fatalf("Expected status 200, got %d", response.StatusCode)
			}
		}
	})
}

// BenchmarkMemoryUsage tests memory allocation patterns
func BenchmarkMemoryUsage(b *testing.B) {
	testLogs := generateTestLogs(1000)
	source := engine.Source{
		Hostname: "memory-test",
		Service:  "memory",
		File:     "memory.log",
	}
	deps := engine.Default()

	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		logLine := testLogs[i%len(testLogs)]
		result := engine.Parse(logLine, source, deps)
		if result.NLI.Version == "" {
			b.Fatal("Parse returned an uninitialized envelope")
		}
	}

	runtime.GC()
	runtime.ReadMemStats(&m2)

	b.ReportMetric(float64(m2.TotalAlloc-m1.TotalAlloc)/float64(b.N), "bytes/op")
	b.ReportMetric(float64(m2.Mallocs-m1.Mallocs)/float64(b.N), "allocs/op")
}

// BenchmarkLargeLogBatch tests performance with large log batches
func BenchmarkLargeLogBatch(b *testing.B) {
	batchSizes := []int{100, 1000, 10000}

	for _, size := range batchSizes {
		b.Run(fmt.Sprintf("batch-%d", size), func(b *testing.B) {
			logs := generateTestLogs(size)
			source := engine.Source{
				Hostname: "batch-test",
				Service:  "batch",
				File:     "batch.log",
			}
			deps := engine.Default()

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				for _, logLine := range logs {
					result := engine.Parse(logLine, source, deps)
					if result.NLI.Version == "" {
						b.Fatal("Parse returned an uninitialized envelope")
					}
				}
			}

			b.ReportMetric(float64(size*b.N)/b.Elapsed().Seconds(), "logs/sec")
		})
	}
}

// BenchmarkJWTValidation tests JWT token validation performance
func BenchmarkJWTValidation(b *testing.B) {
	authStore, _ := auth.NewStoreWithBootstrap("/tmp/jwt_bench.json", "jwt-test-password-12345")

	// Create test user and get token
	authStore.CreateUser("admin", "testuser", "test@example.com", "admin", "test-password-12345")
	token, _, err := authStore.Authenticate("testuser", "test-password-12345")
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		username, role, err := authStore.ValidateToken(token)
		if err != nil {
			b.Fatal(err)
		}
		if username != "testuser" || role != "admin" {
			b.Fatal("Invalid token validation result")
		}
	}
}

// BenchmarkDrainClustering tests Drain template mining performance
func BenchmarkDrainClustering(b *testing.B) {
	// Generate varied log patterns for clustering
	patterns := []string{
		"User %s logged in from IP %s",
		"Database query took %s ms: %s",
		"HTTP %s %s returned status %s in %s ms",
		"Failed to connect to service %s: %s",
		"Cache hit for key %s (size: %s bytes)",
	}

	logs := make([]string, 1000)
	for i := 0; i < 1000; i++ {
		pattern := patterns[i%len(patterns)]
		logs[i] = fmt.Sprintf(pattern,
			fmt.Sprintf("value%d", i%10),
			fmt.Sprintf("data%d", i%20),
			fmt.Sprintf("result%d", i%5),
			fmt.Sprintf("extra%d", i%7))
	}

	source := engine.Source{
		Hostname: "drain-test",
		Service:  "clustering",
		File:     "clustering.log",
	}
	deps := engine.Default()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		logLine := logs[i%len(logs)]
		result := engine.Parse(logLine, source, deps)
		if result.NLI.Version == "" {
			b.Fatal("Parse returned an uninitialized envelope")
		}
	}
}

// BenchmarkTelemetrySeparation tests v5 telemetry vs event performance
func BenchmarkTelemetrySeparation(b *testing.B) {
	testLog := `{"level":"info","msg":"telemetry test","user_id":12345,"action":"login"}`
	source := engine.Source{
		Hostname: "telemetry-test",
		Service:  "auth",
		File:     "auth.log",
	}
	deps := engine.Default()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		result := engine.Parse(testLog, source, deps)
		if result.NLI.Version == "" {
			b.Fatal("Parse returned an uninitialized envelope")
		}

		// Simulate telemetry extraction (v5 feature)
		if result.Data != nil {
			// Extract metadata for telemetry
			_ = map[string]interface{}{
				"service":   result.Source.Service,
				"parser":    result.NLI.Parser,
				"timestamp": result.Timestamp,
				"route":     result.Meta.Route,
			}
		}
	}
}

// Performance test utilities

func generateTestLogs(count int) []string {
	logs := make([]string, count)
	logTypes := []string{
		`{"level":"info","msg":"user action","user_id":%d,"timestamp":"2026-10-05T10:00:%02d.000Z"}`,
		`{"level":"error","msg":"database error","error":"connection timeout","query_id":%d}`,
		`{"level":"warn","msg":"rate limit exceeded","ip":"192.168.1.%d","endpoint":"/api/users"}`,
		"Oct  5 10:00:%02d localhost app[%d]: Processing request %d",
		`192.168.1.%d - - [05/Oct/2026:10:00:%02d +0000] "GET /health HTTP/1.1" 200 %d`,
	}

	for i := 0; i < count; i++ {
		template := logTypes[i%len(logTypes)]
		logs[i] = fmt.Sprintf(template, i, i%60, i%255, i, i%60, i*10+100)
	}

	return logs
}

func getAuthClient(b *testing.B, baseURL string) (*http.Client, string) {
	b.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		b.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	loginData, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": "bench-admin-password-12345",
	})
	response, err := client.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginData))
	if err != nil {
		b.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		b.Fatalf("login status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	response, err = client.Get(baseURL + "/api/v1/auth/csrf")
	if err != nil {
		b.Fatal(err)
	}
	defer response.Body.Close()
	var csrf map[string]string
	if err := json.NewDecoder(response.Body).Decode(&csrf); err != nil {
		b.Fatal(err)
	}
	if csrf["csrf_token"] == "" {
		b.Fatal("controller returned no CSRF token")
	}
	return client, csrf["csrf_token"]
}

// Comprehensive benchmark runner
func RunAllBenchmarks(b *testing.B) {
	benchmarks := []struct {
		name string
		fn   func(*testing.B)
	}{
		{"ParserThroughput", BenchmarkParserThroughput},
		{"ParserConcurrency", BenchmarkParserConcurrency},
		{"AuthenticationAPI", BenchmarkAuthenticationAPI},
		{"ParsePreviewAPI", BenchmarkParsePreviewAPI},
		{"ConcurrentAPILoad", BenchmarkConcurrentAPILoad},
		{"MemoryUsage", BenchmarkMemoryUsage},
		{"JWTValidation", BenchmarkJWTValidation},
		{"DrainClustering", BenchmarkDrainClustering},
		{"TelemetrySeparation", BenchmarkTelemetrySeparation},
	}

	for _, bench := range benchmarks {
		b.Run(bench.name, bench.fn)
	}

	b.Run("LargeLogBatch", BenchmarkLargeLogBatch)
}

// LoadTestScenario simulates realistic production load
func LoadTestScenario(b *testing.B) {
	suite := NewBenchmarkSuite()
	server := httptest.NewServer(suite.server.Mux())
	defer server.Close()

	// Simulate multiple clients
	clients := 10
	requestsPerClient := b.N / clients

	var wg sync.WaitGroup
	start := time.Now()

	b.ResetTimer()

	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			client, _ := getAuthClient(b, server.URL)

			for j := 0; j < requestsPerClient; j++ {
				// Mix of different API calls
				endpoints := []string{
					"/api/v1/overview",
					"/api/v1/fleet",
					"/api/v1/users",
					"/api/v1/metadata?limit=10",
				}

				endpoint := endpoints[j%len(endpoints)]
				req, _ := http.NewRequest("GET", server.URL+endpoint, nil)

				resp, err := client.Do(req)
				if err != nil {
					b.Errorf("Client %d request failed: %v", clientID, err)
					return
				}
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					b.Errorf("Client %d status=%d, want %d", clientID, resp.StatusCode, http.StatusOK)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	totalRequests := clients * requestsPerClient
	b.ReportMetric(float64(totalRequests)/elapsed.Seconds(), "requests/sec")
}
