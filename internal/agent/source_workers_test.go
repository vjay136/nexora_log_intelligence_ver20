package agent

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/multiline"
)

func TestFinishPendingSourcesEmitsBeforeCheckpointAndPreservesFailedOffset(t *testing.T) {
	assembler := multiline.New(multiline.Config{Enabled: true, Rules: []string{"java_exception"}})
	assembler.Feed("java.lang.IllegalStateException: pending", time.Now())
	source := &tail{input: InputConfig{Path: "input.log"}, ml: assembler}
	var calls []string
	flushErr := errors.New("output unavailable")
	err := finishPendingSources([]*tail{source}, func(_ *tail, line string, truncated bool) error {
		calls = append(calls, "emit:"+line)
		if truncated {
			t.Fatal("unexpected truncation")
		}
		return flushErr
	}, func(*tail) error {
		calls = append(calls, "commit")
		return nil
	})
	if !errors.Is(err, flushErr) {
		t.Fatalf("finish error = %v, want output failure", err)
	}
	if !reflect.DeepEqual(calls, []string{"emit:java.lang.IllegalStateException: pending"}) {
		t.Fatalf("calls = %v, want only emission after failure", calls)
	}
}

func TestFinishPendingSourcesEmitsBeforeCheckpoint(t *testing.T) {
	assembler := multiline.New(multiline.Config{Enabled: true, Rules: []string{"java_exception"}})
	assembler.Feed("java.lang.IllegalStateException: pending", time.Now())
	source := &tail{input: InputConfig{Path: "input.log"}, ml: assembler}
	var calls []string
	err := finishPendingSources([]*tail{source}, func(_ *tail, line string, _ bool) error {
		calls = append(calls, "emit:"+line)
		return nil
	}, func(*tail) error {
		calls = append(calls, "commit")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"emit:java.lang.IllegalStateException: pending", "commit"}) {
		t.Fatalf("calls = %v, want emission then checkpoint", calls)
	}
}

func TestSourceWorkerLimitDefaultsToBoundedAutoAndHonorsOne(t *testing.T) {
	config := Config{Inputs: make([]InputConfig, 3)}
	want := runtime.NumCPU()
	if want > 32 {
		want = 32
	}
	if want > len(config.Inputs) {
		want = len(config.Inputs)
	}
	if got := sourceWorkerLimit(config); got != want {
		t.Fatalf("auto workers = %d, want %d", got, want)
	}
	config.SourceWorkers = 1
	if got := sourceWorkerLimit(config); got != 1 {
		t.Fatalf("explicit workers = %d, want 1", got)
	}
}

func TestSourceBatchLineLimitDefaultsAndHonorsConfiguration(t *testing.T) {
	if got := sourceBatchLineLimit(Config{}); got != 1000 {
		t.Fatalf("default source batch lines = %d, want 1000", got)
	}
	if got := sourceBatchLineLimit(Config{SourceBatchLines: 256}); got != 256 {
		t.Fatalf("configured source batch lines = %d, want 256", got)
	}
}

func TestSourceWorkersRunTwoSourcesConcurrentlyAndPreserveSourceOrder(t *testing.T) {
	tails := []*tail{{input: InputConfig{Path: "source-a"}}, {input: InputConfig{Path: "source-b"}}}
	started := make(chan string, 2)
	continueWork := make(chan struct{})
	done := make(chan error, 1)
	var mu sync.Mutex
	order := map[string][]int{}
	go func() {
		done <- runSourceWorkers(context.Background(), tails, 2, func(source *tail) error {
			started <- source.input.Path
			<-continueWork
			for i := 0; i < 3; i++ {
				mu.Lock()
				order[source.input.Path] = append(order[source.input.Path], i)
				mu.Unlock()
			}
			return nil
		})
	}()
	seen := map[string]bool{}
	timeout := time.After(2 * time.Second)
	for range tails {
		select {
		case source := <-started:
			seen[source] = true
		case <-timeout:
			t.Fatal("source workers did not make progress concurrently")
		}
	}
	close(continueWork)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("started sources = %#v", seen)
	}
	for _, source := range tails {
		got := order[source.input.Path]
		if len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
			t.Fatalf("source %s order = %v", source.input.Path, got)
		}
	}
}
