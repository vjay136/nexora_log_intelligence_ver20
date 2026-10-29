package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/multiline"
	"nexora.local/nli-v20/internal/output"
)

func TestSourceWorkersKeepEachInputOutputOrder(t *testing.T) {
	root := t.TempDir()
	inputA, inputB := filepath.Join(root, "a.log"), filepath.Join(root, "b.log")
	outputA, outputB := filepath.Join(root, "a.jsonl"), filepath.Join(root, "b.jsonl")
	writeInput := func(path, prefix string) {
		t.Helper()
		var lines []string
		for i := 0; i < 12; i++ {
			lines = append(lines, `{"service":"test","id":"`+prefix+`-`+string(rune('a'+i))+`"}`)
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeInput(inputA, "a")
	writeInput(inputB, "b")
	cfg := Config{
		Version: 2, Inputs: []InputConfig{{Path: inputA, Service: "source-a"}, {Path: inputB, Service: "source-b"}},
		StateDir: filepath.Join(root, "state"), PollMS: 50, SourceWorkers: 2, DisableV5Parser: true,
		Outputs: []output.Config{
			{Type: "file", Path: outputA, Routes: []string{"normal"}, Families: []string{"source-a"}},
			{Type: "file", Path: outputB, Routes: []string{"normal"}, Families: []string{"source-b"}},
		},
		TelemetryOutputs: []output.Config{{Type: "discard", Routes: []string{"telemetry"}}},
	}
	if err := Run(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ path, prefix string }{{outputA, "a"}, {outputB, "b"}} {
		data, err := os.ReadFile(item.path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) != 12 {
			t.Fatalf("%s output has %d records, want 12: %s", item.prefix, len(lines), data)
		}
		for i, line := range lines {
			var payload map[string]any
			if err := json.Unmarshal([]byte(line), &payload); err != nil {
				t.Fatal(err)
			}
			want := item.prefix + "-" + string(rune('a'+i))
			if payload["id"] != want {
				t.Fatalf("%s output[%d] = %v, want %s", item.prefix, i, payload["id"], want)
			}
		}
	}
}

func TestHundredSourcesAppendRestartAndCheckpointExactlyOnce(t *testing.T) {
	root := t.TempDir()
	const sourceCount = 101
	const firstBatch = 2
	const appendedBatch = 1
	inputs := make([]InputConfig, 0, sourceCount)
	for index := 0; index < sourceCount; index++ {
		path := filepath.Join(root, fmt.Sprintf("input-%03d.log", index))
		var initial strings.Builder
		for line := 0; line < firstBatch; line++ {
			fmt.Fprintf(&initial, `{"source":%q,"id":%d}`+"\n", filepath.Base(path), line)
		}
		if err := os.WriteFile(path, []byte(initial.String()), 0600); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, InputConfig{Path: path, Service: "source"})
	}
	outputPath := filepath.Join(root, "events.jsonl")
	stateDir := filepath.Join(root, "state")
	cfg := Config{
		Version: 2, Inputs: inputs, StateDir: stateDir, PollMS: 50,
		SourceWorkers: 8, SourceBatchLines: 1, DisableV5Parser: true,
		Outputs:          []output.Config{{Type: "file", Path: outputPath, Routes: []string{"normal"}}},
		TelemetryOutputs: []output.Config{{Type: "discard", Routes: []string{"telemetry"}}},
	}
	if err := Run(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	for index := range inputs {
		file, err := os.OpenFile(inputs[index].Path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := fmt.Fprintf(file, `{"source":%q,"id":%d}`+"\n", filepath.Base(inputs[index].Path), firstBatch)
		closeErr := file.Close()
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if err := Run(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	// Reopening without new bytes must not replay acknowledged output.
	if err := Run(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	wantRecords := sourceCount * (firstBatch + appendedBatch)
	if len(lines) != wantRecords {
		t.Fatalf("output has %d rows, want %d", len(lines), wantRecords)
	}
	seen := make(map[string]map[int]int, sourceCount)
	lastID := make(map[string]int, sourceCount)
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		source, _ := event["source"].(string)
		id, ok := event["id"].(float64)
		if !ok || source == "" {
			t.Fatalf("missing source or id in event %v", event)
		}
		if seen[source] == nil {
			seen[source] = map[int]int{}
		}
		if previous, exists := lastID[source]; exists && int(id) <= previous {
			t.Fatalf("source %s output order regressed from %d to %d", source, previous, int(id))
		}
		lastID[source] = int(id)
		seen[source][int(id)]++
	}
	for _, input := range inputs {
		name := filepath.Base(input.Path)
		for id := 0; id < firstBatch+appendedBatch; id++ {
			if got := seen[name][id]; got != 1 {
				t.Fatalf("%s id %d occurred %d times, want exactly once", name, id, got)
			}
		}
	}
	checkpointData, err := os.ReadFile(filepath.Join(stateDir, "offsets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints map[string]cursor
	if err := json.Unmarshal(checkpointData, &checkpoints); err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != sourceCount {
		t.Fatalf("checkpoint has %d sources, want %d", len(checkpoints), sourceCount)
	}
	for index, input := range inputs {
		info, err := os.Stat(input.Path)
		if err != nil {
			t.Fatal(err)
		}
		state := checkpoints[input.Path]
		if state.Offset != info.Size() || state.Line != firstBatch+appendedBatch {
			t.Fatalf("source %d checkpoint = offset %d line %d, want %d and %d", index, state.Offset, state.Line, info.Size(), firstBatch+appendedBatch)
		}
	}
}

func TestCompletedMultilinePrefixIsCheckpointedWhileNextRecordIsPending(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "auto.log")
	first := `{"id":"first","message":"completed"}` + "\n"
	second := `{"id":"second","message":"pending until timeout"}` + "\n"
	if err := os.WriteFile(inputPath, []byte(first+second), 0600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")
	outputPath := filepath.Join(root, "auto.jsonl")
	cfg := Config{
		Version: 2, Inputs: []InputConfig{{
			Path: inputPath, Service: "auto",
			Multiline: multiline.Config{Enabled: true, FlushTimeoutMS: 4000, Rules: []string{"generic_continuation"}},
		}},
		StateDir: stateDir, PollMS: 10, SourceWorkers: 1, DisableV5Parser: true,
		Outputs:          []output.Config{{Type: "file", Path: outputPath, Routes: []string{"normal"}}},
		TelemetryOutputs: []output.Config{{Type: "discard", Routes: []string{"telemetry"}}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- Run(ctx, cfg, false) }()
	t.Cleanup(cancel)

	checkpointPath := filepath.Join(stateDir, "offsets.json")
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, err := os.ReadFile(checkpointPath)
		if err == nil {
			var checkpoints map[string]cursor
			if json.Unmarshal(data, &checkpoints) == nil && checkpoints[inputPath].Offset == int64(len(first)) {
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			if runErr := <-result; runErr != nil {
				t.Fatalf("agent stopped before checkpoint: %v", runErr)
			}
			t.Fatalf("completed prefix was not checkpointed while the trailing event was pending")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	readRecords := func() int {
		t.Helper()
		data, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatal(err)
		}
		return len(strings.Split(strings.TrimSpace(string(data)), "\n"))
	}
	if got := readRecords(); got != 2 {
		t.Fatalf("normalized output has %d records after pending flush, want 2", got)
	}
	if err := Run(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	if got := readRecords(); got != 2 {
		t.Fatalf("restarting replayed an acknowledged event: output has %d rows, want 2", got)
	}
}

func BenchmarkAgentHundredSourcesAppend(b *testing.B) {
	root := b.TempDir()
	const sourceCount = 101
	inputs := make([]InputConfig, 0, sourceCount)
	for index := 0; index < sourceCount; index++ {
		path := filepath.Join(root, fmt.Sprintf("input-%03d.log", index))
		if err := os.WriteFile(path, nil, 0600); err != nil {
			b.Fatal(err)
		}
		inputs = append(inputs, InputConfig{Path: path, Service: "source"})
	}
	cfg := Config{
		Version: 2, Inputs: inputs, StateDir: filepath.Join(root, "state"), PollMS: 50,
		SourceWorkers: 8, SourceBatchLines: 100, DisableV5Parser: true,
		Outputs:          []output.Config{{Type: "file", Path: filepath.Join(root, "events.jsonl"), Routes: []string{"normal"}}},
		TelemetryOutputs: []output.Config{{Type: "discard", Routes: []string{"telemetry"}}},
	}
	if err := Run(context.Background(), cfg, true); err != nil {
		b.Fatal(err)
	}
	sampleLine := fmt.Sprintf(`{"source":%q,"id":%d}`+"\n", filepath.Base(inputs[0].Path), 0)
	b.SetBytes(int64(sourceCount * 100 * len(sampleLine)))
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		for index := range inputs {
			file, err := os.OpenFile(inputs[index].Path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				b.Fatal(err)
			}
			for line := 0; line < 100; line++ {
				if _, err := fmt.Fprintf(file, `{"source":%q,"id":%d}`+"\n", filepath.Base(inputs[index].Path), iteration*100+line); err != nil {
					file.Close()
					b.Fatal(err)
				}
			}
			if err := file.Close(); err != nil {
				b.Fatal(err)
			}
		}
		if err := Run(context.Background(), cfg, true); err != nil {
			b.Fatal(err)
		}
	}
}

func TestMultilineSourceFlushesAsOneEventAndRestartDoesNotReplay(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "stack.log")
	stack := "java.lang.IllegalStateException: failed\n\tat Example.call(Example.java:7)"
	input := stack + "\n" + `{"service":"test","id":"after"}` + "\n"
	if err := os.WriteFile(inputPath, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(root, "events.jsonl")
	stateDir := filepath.Join(root, "state")
	cfg := Config{
		Version: 2, Inputs: []InputConfig{{Path: inputPath, Service: "test", Multiline: multiline.Config{Enabled: true, Rules: []string{"java_exception"}}}},
		StateDir: stateDir, PollMS: 50, SourceWorkers: 1, DisableV5Parser: true,
		Outputs:          []output.Config{{Type: "file", Path: outputPath, Routes: []string{"normal", "warning"}}},
		TelemetryOutputs: []output.Config{{Type: "discard", Routes: []string{"telemetry"}}},
	}
	for run := 0; run < 2; run++ {
		if err := Run(context.Background(), cfg, true); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("output has %d events after restart, want exactly two: %s", len(lines), data)
	}
	stackEvents, ordinaryEvents := 0, 0
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid output JSONL record %q: %v", line, err)
		}
		// The generic key/value parser preserves a Java exception as the
		// exception name plus the assembled continuation lines. Verify that
		// the multiline reader emitted the complete logical event once.
		if value, ok := event["java.lang.IllegalStateException"].(string); ok && value == "failed\n\tat Example.call(Example.java:7)" {
			stackEvents++
		}
		if event["id"] == "after" {
			ordinaryEvents++
		}
	}
	if stackEvents != 1 || ordinaryEvents != 1 {
		t.Fatalf("assembled stack and following event counts = %d and %d, want one each: %s", stackEvents, ordinaryEvents, data)
	}
	checkpointData, err := os.ReadFile(filepath.Join(stateDir, "offsets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints map[string]cursor
	if err := json.Unmarshal(checkpointData, &checkpoints); err != nil {
		t.Fatal(err)
	}
	if got := checkpoints[inputPath]; got.Offset != int64(len(input)) || got.Line != 3 {
		t.Fatalf("checkpoint = %+v, want full input offset and three physical lines", got)
	}
}

func TestAutoLineOrientedJSONBatchCheckpointsOnceAndRestartsExactlyOnce(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "auto.log")
	var input strings.Builder
	for id := 0; id < 49; id++ {
		fmt.Fprintf(&input, `{"service":"auto","id":%d}`+"\n", id)
	}
	if err := os.WriteFile(inputPath, []byte(input.String()), 0600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(root, "auto.jsonl")
	stateDir := filepath.Join(root, "state")
	cfg := Config{
		Version: 2, Inputs: []InputConfig{{Path: inputPath, Service: "auto", Multiline: multiline.Config{
			Enabled: true, FlushTimeoutMS: 4000, Rules: []string{"java_exception", "python_traceback", "go_panic"},
		}}},
		StateDir: stateDir, PollMS: 10, SourceWorkers: 1, DisableV5Parser: true,
		Outputs:          []output.Config{{Type: "file", Path: outputPath, Routes: []string{"normal"}}},
		TelemetryOutputs: []output.Config{{Type: "discard", Routes: []string{"telemetry"}}},
	}
	for run := 0; run < 2; run++ {
		if err := Run(context.Background(), cfg, true); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 49 {
		t.Fatalf("output has %d rows after restart, want 49", len(lines))
	}
	seen := make(map[int]int, 49)
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		id, ok := event["id"].(float64)
		if !ok {
			t.Fatalf("event has no numeric id: %v", event)
		}
		seen[int(id)]++
	}
	for id := 0; id < 49; id++ {
		if seen[id] != 1 {
			t.Fatalf("input id %d emitted %d times, want exactly once", id, seen[id])
		}
	}
	checkpointData, err := os.ReadFile(filepath.Join(stateDir, "offsets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints map[string]cursor
	if err := json.Unmarshal(checkpointData, &checkpoints); err != nil {
		t.Fatal(err)
	}
	if got := checkpoints[inputPath]; got.Offset != int64(input.Len()) || got.Line != 49 {
		t.Fatalf("checkpoint = %+v, want offset %d and line 49", got, input.Len())
	}
}

func BenchmarkAgentAuto49JSONLines(b *testing.B) {
	root := b.TempDir()
	inputPath := filepath.Join(root, "auto.log")
	outputPath := filepath.Join(root, "auto.jsonl")
	var batch strings.Builder
	for id := 0; id < 49; id++ {
		fmt.Fprintf(&batch, `{"service":"auto","id":%d}`+"\n", id)
	}
	if err := os.WriteFile(inputPath, nil, 0600); err != nil {
		b.Fatal(err)
	}
	cfg := Config{
		Version: 2, Inputs: []InputConfig{{Path: inputPath, Service: "auto", Multiline: multiline.Config{
			Enabled: true, FlushTimeoutMS: 4000, Rules: []string{"java_exception", "python_traceback", "go_panic"},
		}}},
		StateDir: filepath.Join(root, "state"), PollMS: 50, SourceWorkers: 1, DisableV5Parser: true,
		Outputs:          []output.Config{{Type: "file", Path: outputPath, Routes: []string{"normal"}}},
		TelemetryOutputs: []output.Config{{Type: "discard", Routes: []string{"telemetry"}}},
	}
	b.SetBytes(int64(batch.Len()))
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		file, err := os.OpenFile(inputPath, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := file.WriteString(batch.String()); err != nil {
			file.Close()
			b.Fatal(err)
		}
		if err := file.Close(); err != nil {
			b.Fatal(err)
		}
		if err := Run(context.Background(), cfg, true); err != nil {
			b.Fatal(err)
		}
	}
}
