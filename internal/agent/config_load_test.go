package agent

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/engine"
)

func writeConfigFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRejectsTrailingJSONDocument(t *testing.T) {
	path := writeConfigFixture(t, `{"version":2,"inputs":[],"state_dir":"C:/state","poll_ms":100}{}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "exactly one JSON document") {
		t.Fatalf("expected trailing-document error, got %v", err)
	}
}

func TestLoadRejectsOversizedConfig(t *testing.T) {
	path := writeConfigFixture(t, strings.Repeat(" ", (1<<20)+1))
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("expected size error, got %v", err)
	}
}

func TestValidateAllowsMoreThanOneHundredBoundedInputs(t *testing.T) {
	root := t.TempDir()
	config := Config{StateDir: filepath.Join(root, "state"), PollMS: 100, Inputs: make([]InputConfig, 101)}
	for index := range config.Inputs {
		config.Inputs[index] = InputConfig{Path: filepath.Join(root, "input-"+string(rune('A'+index%26))+"-"+strings.Repeat("x", index/26)+".log"), Service: "test"}
	}
	if err := validate(config); err != nil {
		t.Fatalf("101 bounded inputs should be accepted: %v", err)
	}
	config.Inputs = make([]InputConfig, 257)
	if err := validate(config); err == nil || !strings.Contains(err.Error(), "1..256 inputs") {
		t.Fatalf("expected input cap error, got %v", err)
	}
	config.Inputs = []InputConfig{{Path: filepath.Join(root, "input.log"), Service: "test"}}
	config.SourceBatchLines = 10001
	if err := validate(config); err == nil || !strings.Contains(err.Error(), "source_batch_lines") {
		t.Fatalf("expected source batch cap error, got %v", err)
	}
}

func TestInputIntegrityErrors(t *testing.T) {
	if got := inputIntegrityErrors("normal\ttext\ncontinuation"); len(got) != 0 {
		t.Fatalf("valid text was rejected: %v", got)
	}
	if got := inputIntegrityErrors(string([]byte{'a', 0xff, 'b'})); len(got) == 0 || !strings.Contains(strings.Join(got, " "), "invalid UTF-8") {
		t.Fatalf("invalid UTF-8 was not detected: %v", got)
	}
	if got := inputIntegrityErrors("abc\x00def"); len(got) == 0 || !strings.Contains(strings.Join(got, " "), "control") {
		t.Fatalf("NUL byte was not detected: %v", got)
	}
}

func TestReadRecordReturnsBoundedOversizedSegments(t *testing.T) {
	input := strings.Repeat("x", engine.MaxLine-1) + "\r" + strings.Repeat("x", 17) + "\n"
	reader := bufio.NewReaderSize(strings.NewReader(input), 64*1024)

	first, consumed, complete, oversized, err := readRecord(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != engine.MaxLine || consumed != engine.MaxLine || complete || !oversized {
		t.Fatalf("unexpected first segment: bytes=%d consumed=%d complete=%v oversized=%v", len(first), consumed, complete, oversized)
	}
	if first[len(first)-1] != '\r' {
		t.Fatal("bounded segment lost a trailing control byte at the segment boundary")
	}

	second, consumed, complete, oversized, err := readRecord(reader)
	if err != nil {
		t.Fatal(err)
	}
	if second != strings.Repeat("x", 17) || consumed != 18 || !complete || oversized {
		t.Fatalf("unexpected final segment: %q consumed=%d complete=%v oversized=%v", second, consumed, complete, oversized)
	}
}
