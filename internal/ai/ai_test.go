package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"nexora.local/nli-v20/internal/drain"
	"nexora.local/nli-v20/internal/registry"
)

func TestPendingQueueIsBoundedAndObservable(t *testing.T) {
	d := New(Config{Enabled: true, MaxPendingClusters: 2}, nil, nil, t.TempDir())
	d.Submit(1, "one")
	d.Submit(2, "two")
	d.Submit(3, "three")

	stats := d.Stats()
	if stats.PendingClusters != 2 || stats.DroppedPending != 1 {
		t.Fatalf("unexpected queue stats: %+v", stats)
	}
}

func TestSamplesAreUTF8SafeAndBounded(t *testing.T) {
	d := New(Config{Enabled: true, MaxSampleBytes: 7}, nil, nil, t.TempDir())
	d.Submit(1, "abc€defgh")

	if len(d.pending) != 1 || len(d.pending[0].Samples) != 1 {
		t.Fatalf("sample was not queued: %#v", d.pending)
	}
	got := d.pending[0].Samples[0]
	if len(got) > 7 || !utf8.ValidString(got) {
		t.Fatalf("sample is not safely bounded UTF-8: %q (%d bytes)", got, len(got))
	}
	if d.Stats().TruncatedSamples != 1 {
		t.Fatalf("expected a visible truncation count, got %+v", d.Stats())
	}

	d.Submit(1, strings.Repeat("x", 100))
	if len(d.pending[0].Samples[1]) > 7 {
		t.Fatalf("additional sample exceeded configured bound")
	}
}

func TestMaxSampleBytesUsesHardCeiling(t *testing.T) {
	d := New(Config{Enabled: true, MaxSampleBytes: hardMaxSampleBytes * 2}, nil, nil, t.TempDir())
	d.Submit(1, strings.Repeat("x", hardMaxSampleBytes+1))
	if got := len(d.pending[0].Samples[0]); got != hardMaxSampleBytes {
		t.Fatalf("sample length = %d, want hard ceiling %d", got, hardMaxSampleBytes)
	}
}

func TestValidAIDiscoveryStagesCandidateWithoutActivation(t *testing.T) {
	const parserJSON = `{"id":"mail.event","version":1,"description":"candidate","format":"json","fields":[{"name":"event","source":"json:event","type":"string","required":true}],"tests":[{"input":"{\"event\":\"login\"}","expected":{"event":"login"}}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, parserJSON)
	}))
	defer server.Close()

	t.Setenv("NLI_AI_TEST_KEY", "test-only")
	stateDir := t.TempDir()
	reg := registry.New()
	miner := drain.New(drain.Config{})
	d := New(Config{
		Enabled:        true,
		Provider:       "openai",
		Endpoint:       server.URL,
		APIKeyEnv:      "NLI_AI_TEST_KEY",
		MinClusterSize: 1,
	}, reg, miner, stateDir)
	d.Submit(7, `{"event":"login"}`)

	if err := d.RunBatch(context.Background(), map[int]int{7: 1}); err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if _, active := reg.Get("ai.mail.event"); active {
		t.Fatal("AI candidate was activated without managed approval")
	}
	if match := miner.Process(`fresh cluster`); match.ParserID != "" {
		t.Fatalf("Drain cluster was assigned unapproved parser %q", match.ParserID)
	}
	candidatePath := filepath.Join(stateDir, "parsers", "candidates", "ai.mail.event-v1.json")
	if _, err := os.Stat(candidatePath); err != nil {
		t.Fatalf("staged candidate missing: %v", err)
	}
}
