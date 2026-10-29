package multiline_test

import (
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/multiline"
)

var now = time.Now()

func TestJavaStackTrace(t *testing.T) {
	a := multiline.New(multiline.Config{Enabled: true})
	lines := []string{
		"java.lang.NullPointerException: something went wrong",
		"\tat com.example.Foo.bar(Foo.java:42)",
		"\tat com.example.Main.main(Main.java:10)",
		"Next normal log line",
	}
	var results []*multiline.Result
	for _, l := range lines {
		if r := a.Feed(l, now); r != nil {
			results = append(results, r)
		}
	}
	if flush := a.Flush(); flush != nil {
		results = append(results, flush)
	}
	// Should have assembled java exception + emitted "Next normal log line"
	if len(results) < 2 {
		t.Fatalf("expected >=2 results, got %d", len(results))
	}
	if !containsStr(results[0].Line, "NullPointerException") {
		t.Errorf("first result should be java exception, got: %q", results[0].Line)
	}
}

func TestSingleLinePassthrough(t *testing.T) {
	a := multiline.New(multiline.Config{Enabled: true})
	r := a.Feed("normal log line with no continuation", now)
	if r == nil {
		t.Error("single non-multiline should pass through")
	}
	if r.Line != "normal log line with no continuation" {
		t.Errorf("unexpected line: %q", r.Line)
	}
}

func TestDisabledPassthrough(t *testing.T) {
	a := multiline.New(multiline.Config{Enabled: false})
	r := a.Feed("any line at all", now)
	if r == nil || r.Line != "any line at all" {
		t.Error("disabled assembler should pass through all lines unchanged")
	}
}

func TestFlushOnTimeout(t *testing.T) {
	a := multiline.New(multiline.Config{Enabled: true, FlushTimeout: 1 * time.Millisecond})
	a.Feed("java.lang.Exception: test", now)
	// Simulate timeout by advancing time
	future := now.Add(10 * time.Millisecond)
	r := a.Feed("new start line", future)
	if r == nil {
		t.Error("expected timeout flush before new line")
	}
}

func TestBufferOverflow(t *testing.T) {
	a := multiline.New(multiline.Config{Enabled: true, MaxBytes: 50})
	a.Feed("java.lang.Exception: start", now)
	// Feed lines that exceed the buffer
	for i := 0; i < 20; i++ {
		a.Feed("\tat com.example.VeryLongClassName.methodName(File.java:100)", now)
	}
	if r := a.Flush(); r != nil && !r.Truncated {
		t.Error("expected truncated=true for overflow")
	}
}

func TestInitialLineRespectsByteCap(t *testing.T) {
	a := multiline.New(multiline.Config{Enabled: true, MaxBytes: 8})
	a.Feed("java.lang.Exception: this first line is oversized", now)
	r := a.Flush()
	if r == nil {
		t.Fatal("expected buffered first line")
	}
	if len(r.Line) != 8 || !r.Truncated {
		t.Fatalf("expected an 8-byte truncated event, got %d bytes, truncated=%v", len(r.Line), r.Truncated)
	}
}

func TestConfiguredByteCapIsClamped(t *testing.T) {
	a := multiline.New(multiline.Config{Enabled: true, MaxBytes: int(^uint(0) >> 1)})
	// The hard ceiling is below this input; it also covers the first-line path.
	a.Feed("java.lang.Exception: "+strings.Repeat("x", 9*1024*1024), now)
	r := a.Flush()
	if r == nil || len(r.Line) > 8*1024*1024 || !r.Truncated {
		t.Fatalf("expected event bounded to 8 MiB and marked truncated, got result=%v", r != nil)
	}
}

func TestContinuationLineCountSplitsEvents(t *testing.T) {
	a := multiline.New(multiline.Config{Enabled: true, MaxBytes: 128, MaxLines: 2})
	a.Feed("java.lang.Exception: start", now)
	a.Feed("\tat first", now)
	r := a.Feed("\tat second", now)
	if r == nil || !r.Truncated || strings.Count(r.Line, "\n") != 1 {
		t.Fatalf("expected bounded, truncated two-line event; got %#v", r)
	}
	if pending := a.Flush(); pending == nil || pending.Line != "\tat second" {
		t.Fatalf("expected overflow line to begin next event, got %#v", pending)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
