package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/adapter"
	"nexora.local/nli-v20/internal/agent"
	"nexora.local/nli-v20/internal/engine"
	"nexora.local/nli-v20/internal/geoipdb"
	"nexora.local/nli-v20/internal/multiline"
	"nexora.local/nli-v20/internal/quarantine"
	"nexora.local/nli-v20/internal/routing"
	metastore "nexora.local/nli-v20/internal/store"
	"nexora.local/nli-v20/telemetry"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "nli:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(out, `NLI Log Intelligence CLI v`+engine.Version+`
Usage:
  nli parse   [--service NAME] [--parser-pack-dir DIR] [--multiline=true] [--full-envelope] <file|->    parse logs, emit EventPayload (--full-envelope for v4 diagnostics)
  nli detect  [--service NAME] [--multiline=true] <file|->    detect format per event
  nli analyze [--service NAME] [--multiline=true] <file|->    summary statistics
  nli replay  [--quarantine FILE]          replay quarantined logs
               [--config FILE]             route successful replays to configured outputs
  nli geoip-update [--root DIR] [--client-dir DIR]
               fetch a commit-pinned GeoIP candidate; optionally publish or
               activate it only when the corresponding environment gates are enabled
  nli geoip-activate --generation COMMIT [--root DIR] [--client-dir DIR]
               validate and activate an approved local generation
  nli geoip-rollback [--root DIR] [--client-dir DIR]
               revalidate and restore the previous managed generation
  nli status  [--endpoint URL]            check agent health
  nli version`)
		return nil
	}

	command := args[0]

	if command == "version" || command == "--version" {
		fmt.Fprintln(out, "nli", engine.Version)
		return nil
	}
	if command == "geoip-update" {
		return geoIPUpdateCmd(args[1:], out)
	}
	if command == "geoip-activate" || command == "geoip-rollback" {
		return geoIPActivationCmd(command, args[1:], out)
	}

	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	service := flags.String("service", "auto", "source service name (auto detects supported record headers)")
	parserPackDir := flags.String("parser-pack-dir", os.Getenv("NLI_PARSER_PACK_DIR"), "approved/activated controller ParserPack directory")
	multilineEnabled := flags.Bool("multiline", true, "assemble Java, Python, Go, and indented multiline events")
	fullEnvelope := flags.Bool("full-envelope", false, "WARNING: diagnostic mode includes raw log text and metadata (disabled by default)")
	endpoint := flags.String("endpoint", "http://127.0.0.1:12380/health", "health URL")
	quarantine := flags.String("quarantine", "/var/log/nli/quarantine.jsonl", "quarantine JSONL file")
	configPath := flags.String("config", "", "agent config for durable queue reprocessing and configured outputs")
	recordIDs := flags.String("record-ids", "", "comma-separated quarantine IDs to retry; defaults to all unresolved records")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	switch command {
	case "status":
		client := &http.Client{Timeout: 5 * time.Second}
		response, err := client.Get(*endpoint)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("health returned %d", response.StatusCode)
		}
		_, err = io.Copy(out, io.LimitReader(response.Body, 65536))
		return err

	case "replay":
		if *configPath != "" {
			ids := make([]string, 0)
			for _, id := range strings.Split(*recordIDs, ",") {
				if id = strings.TrimSpace(id); id != "" {
					ids = append(ids, id)
				}
			}
			return replayQueue(*quarantine, *configPath, *parserPackDir, ids, out)
		}
		return replayCmd(*quarantine, out)

	case "parse", "detect", "analyze":
		if flags.NArg() != 1 {
			return fmt.Errorf("usage: nli %s [--service NAME] <file|->", command)
		}
		return parseCmd(command, flags.Arg(0), *service, *parserPackDir, *multilineEnabled, *fullEnvelope, out)

	default:
		return fmt.Errorf("unsupported command %q; use nli help", command)
	}
}

func enabledEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func geoIPUpdateCmd(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("geoip-update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "/var/lib/nli-geoip", "immutable GeoIP candidate root")
	clientDir := flags.String("client-dir", "/etc/geoip-db", "exact MMDB path directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !enabledEnv("NLI_GEOIP_LICENSE_ACCEPTED") {
		return fmt.Errorf("set NLI_GEOIP_LICENSE_ACCEPTED=true only after reviewing the GeoLite2 EULA and CC BY-SA terms")
	}
	operator := strings.TrimSpace(os.Getenv("NLI_GEOIP_OPERATOR"))
	if operator == "" {
		return fmt.Errorf("NLI_GEOIP_OPERATOR must identify the administrator responsible for the update")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	manifest, err := geoipdb.FetchCandidate(ctx, geoipdb.Config{Root: *root})
	if err != nil {
		return err
	}
	result := map[string]any{"generation": manifest.Generation, "candidate": true, "published": false, "activated": false}
	if enabledEnv("NLI_GEOIP_PUBLISH_SUPABASE") {
		projectURL := strings.TrimSpace(os.Getenv("SUPABASE_URL"))
		publishable := strings.TrimSpace(os.Getenv("SUPABASE_PUBLISHABLE_KEY"))
		if publishable == "" {
			publishable = strings.TrimSpace(os.Getenv("SUPABASE_ANON_KEY"))
		}
		controllerSecret := []byte(os.Getenv("NLI_SUPABASE_CONTROLLER_SECRET"))
		blobSecret := strings.TrimSpace(os.Getenv("SUPABASE_SECRET_KEY"))
		metadata, openErr := metastore.Open(metastore.Config{Provider: "supabase", SupabaseURL: projectURL, SupabaseAnonKey: publishable, SupabaseControllerSecret: controllerSecret, Timeout: 30 * time.Second})
		if openErr != nil {
			return fmt.Errorf("open Supabase GeoIP metadata store: %w", openErr)
		}
		defer metadata.Close()
		blobs, blobErr := metastore.NewSupabaseBlobStore(projectURL, blobSecret, nil)
		if blobErr != nil {
			return fmt.Errorf("open Supabase GeoIP private storage: %w", blobErr)
		}
		document, publishErr := geoipdb.PublishCandidate(ctx, *root, manifest.Generation, operator, true, blobs, metadata)
		if publishErr != nil {
			return publishErr
		}
		result["published"] = true
		result["supabase_revision"] = document.Revision
		result["state"] = document.State
	}
	if enabledEnv("NLI_GEOIP_AUTO_ACTIVATE") {
		activation, activateErr := geoipdb.Activate(*root, *clientDir, manifest.Generation, operator)
		if activateErr != nil {
			return activateErr
		}
		result["activated"] = true
		result["previous_generation"] = activation.PreviousGeneration
	}
	return json.NewEncoder(out).Encode(result)
}

func geoIPActivationCmd(command string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "/var/lib/nli-geoip", "immutable GeoIP candidate root")
	clientDir := flags.String("client-dir", "/etc/geoip-db", "exact MMDB path directory")
	generation := flags.String("generation", "", "approved source commit SHA")
	operator := flags.String("operator", strings.TrimSpace(os.Getenv("NLI_GEOIP_OPERATOR")), "approving administrator identity")
	if err := flags.Parse(args); err != nil {
		return err
	}
	var (
		activation geoipdb.Activation
		err        error
	)
	if command == "geoip-rollback" {
		activation, err = geoipdb.Rollback(*root, *clientDir, *operator)
	} else {
		if strings.TrimSpace(*generation) == "" {
			return fmt.Errorf("--generation is required")
		}
		activation, err = geoipdb.Activate(*root, *clientDir, strings.TrimSpace(*generation), *operator)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(activation)
}

func parseCmd(command, path, service, parserPackDir string, multilineEnabled bool, fullEnvelope bool, out io.Writer) error {
	input := os.Stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		input = file
	}

	deps := engine.Default()
	host, _ := os.Hostname()

	// Embedded product profiles are always present. An optional managed directory
	// may replace them only with strictly validated approved/activated packs.
	v5Adapter, err := adapter.NewParserPackAdapterFromDirectory(parserPackDir)
	if err != nil {
		return fmt.Errorf("v5 parser initialization: %w", err)
	}

	encoder := json.NewEncoder(out)
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), engine.MaxLine)
	assembler := multiline.New(multiline.Config{Enabled: multilineEnabled})

	var total, normal, warning, quarantined, unknown int64
	var totalConfidence float64
	var sourceLine int64

	emit := func(raw string, firstLine int64, truncated bool) error {
		if strings.TrimSpace(raw) == "" {
			return nil
		}

		source := engine.Source{Hostname: host, Service: service, File: path, LineNumber: firstLine}

		// Try v5 parsing first if available and not in diagnostic mode.
		if v5Adapter != nil && !fullEnvelope && !truncated {
			v5Result := v5Adapter.TryParseV5Production(raw, source)
			if v5Result.Success {
				total++
				totalConfidence += v5Result.ParseTelemetry.Confidence
				normal++
				switch command {
				case "parse":
					return encoder.Encode(v5Result.EventPayload)
				case "detect":
					return encoder.Encode(map[string]interface{}{
						"line_number": firstLine, "format": "v5_parserpack",
						"parser":     v5Result.ParseTelemetry.ParserID,
						"confidence": v5Result.ParseTelemetry.Confidence, "route": "normal",
					})
				}
				return nil
			}
			if v5Result.Quarantined {
				total++
				quarantined++
				if command == "detect" {
					return encoder.Encode(map[string]interface{}{
						"line_number": firstLine, "format": "v5_parserpack",
						"parser":     v5Result.ParseTelemetry.ParserID,
						"confidence": 0, "route": "quarantine",
						"errors": v5Result.ParseTelemetry.ErrorCodes,
					})
				}
				return nil
			}
		}

		// Fallback to v4 parsing
		envelope := engine.ParseWithTruncated(raw, source, deps, truncated)
		total++
		totalConfidence += envelope.NLI.Confidence
		switch envelope.Meta.Route {
		case "normal":
			normal++
		case "warning":
			warning++
		case "quarantine":
			quarantined++
		default:
			unknown++
		}

		switch command {
		case "parse":
			if fullEnvelope {
				// Diagnostic mode - emit full envelope with warning
				fmt.Fprintf(os.Stderr, "WARNING: Full envelope mode includes raw log text and metadata\n")
				return encoder.Encode(envelope)
			} else {
				// Default mode emits customer fields only, including on legacy fallback.
				eventPayload := telemetry.EventPayload{Fields: sanitizePayload(envelope.Data, envelope.Meta.Route)}
				return encoder.Encode(eventPayload)
			}
		case "detect":
			return encoder.Encode(map[string]interface{}{
				"line_number": firstLine, "format": envelope.Meta.Format,
				"parser": envelope.NLI.Parser, "confidence": envelope.NLI.Confidence,
				"route": envelope.Meta.Route, "assurance": envelope.Meta.Assurance,
				"errors": envelope.Meta.Errors,
			})
		}
		return nil
	}

	bufferStart := int64(1)
	for scanner.Scan() {
		sourceLine++
		raw := scanner.Text()
		if strings.TrimSpace(raw) == "" {
			continue
		}
		wasPending := assembler.Pending()
		if !wasPending {
			bufferStart = sourceLine
		}
		result := assembler.Feed(raw, time.Now())
		if result != nil {
			if err := emit(result.Line, bufferStart, result.Truncated); err != nil {
				return err
			}
			if assembler.Pending() {
				bufferStart = sourceLine
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading logs: %w", err)
	}
	if result := assembler.Flush(); result != nil {
		if err := emit(result.Line, bufferStart, result.Truncated); err != nil {
			return err
		}
	}

	if command == "analyze" {
		average := 0.0
		if total > 0 {
			average = totalConfidence / float64(total)
		}
		return encoder.Encode(map[string]interface{}{
			"events": total, "normal": normal, "warning": warning,
			"quarantine": quarantined, "unknown": unknown,
			"average_confidence": average, "engine_version": engine.Version,
		})
	}
	return nil
}

func replayCmd(path string, out io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	deps := engine.Default()
	host, _ := os.Hostname()
	enc := json.NewEncoder(out)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), engine.MaxLine)

	replayed, improved := 0, 0
	for scanner.Scan() {
		var envelope map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			continue
		}
		raw, _ := envelope["raw"].(string)
		if raw == "" {
			if data, ok := envelope["data"].(map[string]interface{}); ok {
				if unknown, ok := data["_unknown"].(map[string]interface{}); ok {
					raw, _ = unknown["raw"].(string)
				}
			}
		}
		if raw == "" {
			continue
		}

		e := engine.Parse(raw, engine.Source{Hostname: host, Service: "replay", File: path}, deps)
		replayed++
		if e.NLI.Parser != "unknown" {
			improved++
			enc.Encode(e)
		}
	}
	fmt.Fprintf(out, `{"replayed":%d,"improved":%d}`+"\n", replayed, improved)
	return scanner.Err()
}

type quarantineHistoryEntry struct {
	ID              string    `json:"id"`
	NodeID          string    `json:"node_id"`
	Service         string    `json:"service"`
	EventHash       string    `json:"event_hash"`
	AttemptedAt     time.Time `json:"attempted_at"`
	AttemptCount    int       `json:"attempt_count"`
	Status          string    `json:"status"`
	ErrorCodes      []string  `json:"error_codes,omitempty"`
	ParserRevision  string    `json:"parser_revision,omitempty"`
	RawRetained     bool      `json:"raw_retained"`
	TelemetryStatus string    `json:"telemetry_status,omitempty"`
}

type replaySummary struct {
	Replayed           int      `json:"replayed"`
	Resolved           int      `json:"resolved"`
	Waiting            int      `json:"waiting"`
	ResolvedIDs        []string `json:"resolved_ids"`
	WaitingIDs         []string `json:"waiting_ids"`
	TelemetryFailedIDs []string `json:"telemetry_failed_ids,omitempty"`
	MissingIDs         []string `json:"missing_ids,omitempty"`
}

// replayQueue re-runs unresolved raw records against the current approved
// ParserPack generation, delivers successful events through configured sinks,
// and atomically leaves only unresolved records in the active queue.
func replayQueue(path, configPath, parserPackDir string, selectedIDs []string, out io.Writer) error {
	cfg, err := agent.Load(configPath)
	if err != nil {
		return fmt.Errorf("load agent config: %w", err)
	}
	if parserPackDir == "" {
		parserPackDir = cfg.ParserPackDir
		if parserPackDir == "" {
			parserPackDir = filepath.Join(cfg.StateDir, "parsers")
		}
	}
	parser, err := adapter.NewParserPackAdapterFromDirectory(parserPackDir)
	if err != nil {
		return fmt.Errorf("load approved parser generation: %w", err)
	}
	router, err := routing.NewV5Router(cfg.Outputs, cfg.TelemetryOutputs)
	if err != nil {
		return fmt.Errorf("initialize configured outputs: %w", err)
	}
	defer router.Close()
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	var unresolved []quarantine.Record
	var history []quarantineHistoryEntry
	var replayed, resolved int
	requested := make(map[string]bool, len(selectedIDs))
	for _, id := range selectedIDs {
		requested[id] = true
	}
	seen := make(map[string]bool, len(selectedIDs))
	queueSeen := make(map[string]bool)
	provisional := make([]quarantine.Record, 0)
	provisionalHistory := make([]int, 0)
	provisionalMetadata := make([]*telemetry.ParseTelemetry, 0)
	pendingTelemetry := make([]*routing.V5ParseResult, 0)
	attemptedIDs := make([]string, 0)
	summary := replaySummary{ResolvedIDs: []string{}, WaitingIDs: []string{}, TelemetryFailedIDs: []string{}, MissingIDs: []string{}}
	host, _ := os.Hostname()
	for scanner.Scan() {
		var record quarantine.Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return fmt.Errorf("invalid quarantine JSONL record: %w", err)
		}
		if record.ID == "" {
			return fmt.Errorf("quarantine JSONL record has no id; preserving queue for operator review")
		}
		if queueSeen[record.ID] {
			// Stable IDs make duplicate appends from a crash/restart harmless.
			// The atomic rewrite below compacts them to one queue record.
			continue
		}
		queueSeen[record.ID] = true
		if record.Raw == "" {
			return fmt.Errorf("quarantine record %s has no exact raw line; preserving queue for operator review", record.ID)
		}
		if record.Status == "resolved" {
			continue
		}
		if len(requested) > 0 && !requested[record.ID] {
			unresolved = append(unresolved, record)
			continue
		}
		seen[record.ID] = true
		replayed++
		attemptedIDs = append(attemptedIDs, record.ID)
		attemptedAt := time.Now().UTC()
		record.AttemptCount++
		record.LastAttempt = attemptedAt
		source := engine.Source{Hostname: host, Service: record.Service, File: record.SourcePath, LineNumber: int64(record.SourceLine)}
		parsed := parser.TryParseV5Production(record.Raw, source)
		if parsed == nil || parsed.ParseTelemetry == nil {
			parsed = &adapter.V5ParseResult{Quarantined: true}
			parsed.ParseTelemetry = telemetry.NewParseTelemetry(record.Raw, record.NodeID)
			parsed.ParseTelemetry.PolicyHash = "unavailable"
			parsed.ParseTelemetry.Route = "quarantine"
			parsed.ParseTelemetry.Result = "quarantined"
			parsed.ParseTelemetry.ErrorCodes = []string{"no_eligible_parser"}
		}
		metadata := parsed.ParseTelemetry
		record.ParserRevision = metadata.PolicyHash
		metadata.QuarantineID = record.ID
		metadata.QuarantineAttempt = record.AttemptCount
		metadata.QuarantineStatus = "waiting"
		telemetryStatus := "sent"
		if parsed.Success && !parsed.Quarantined && parsed.EventPayload != nil {
			metadata.QuarantineStatus = "resolved"
			result := &routing.V5ParseResult{EventPayload: parsed.EventPayload, ParseTelemetry: metadata, Success: true}
			if err := router.RouteV5Customer(result); err == nil {
				record.Status = "resolved"
				record.ParserRevision = metadata.PolicyHash
				provisional = append(provisional, record)
				provisionalHistory = append(provisionalHistory, len(history))
				provisionalMetadata = append(provisionalMetadata, metadata)
			} else {
				metadata.QuarantineStatus = "waiting"
				metadata.ErrorCodes = append(metadata.ErrorCodes, "replay_output_failed")
				record.Status = "waiting"
				record.ErrorCodes = append(record.ErrorCodes, "replay_output_failed")
				unresolved = append(unresolved, record)
			}
			pendingTelemetry = append(pendingTelemetry, result)
		} else {
			metadata.QuarantineStatus = "waiting"
			if len(metadata.ErrorCodes) == 0 {
				metadata.ErrorCodes = []string{"no_eligible_parser"}
			}
			record.Status = "waiting"
			record.ErrorCodes = append([]string(nil), metadata.ErrorCodes...)
			record.ParserRevision = metadata.PolicyHash
			unresolved = append(unresolved, record)
			pendingTelemetry = append(pendingTelemetry, &routing.V5ParseResult{ParseTelemetry: metadata, Quarantined: true})
		}
		history = append(history, quarantineHistoryEntry{
			ID: record.ID, NodeID: record.NodeID, Service: record.Service, EventHash: record.EventHash,
			AttemptedAt: attemptedAt, AttemptCount: record.AttemptCount, Status: record.Status,
			ErrorCodes: append([]string(nil), record.ErrorCodes...), ParserRevision: record.ParserRevision, RawRetained: false, TelemetryStatus: telemetryStatus,
		})
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := router.FlushCustomer(); err != nil {
		for index, record := range provisional {
			record.Status = "waiting"
			record.ErrorCodes = append(record.ErrorCodes, "replay_output_flush_failed")
			unresolved = append(unresolved, record)
			provisionalMetadata[index].QuarantineStatus = "waiting"
			provisionalMetadata[index].ErrorCodes = append(provisionalMetadata[index].ErrorCodes, "replay_output_flush_failed")
			history[provisionalHistory[index]].Status = "waiting"
			history[provisionalHistory[index]].ErrorCodes = append(history[provisionalHistory[index]].ErrorCodes, "replay_output_flush_failed")
		}
	} else {
		for _, record := range provisional {
			resolved++
			summary.ResolvedIDs = append(summary.ResolvedIDs, record.ID)
		}
	}
	for _, result := range pendingTelemetry {
		if err := router.RouteV5Telemetry(result); err != nil {
			summary.TelemetryFailedIDs = append(summary.TelemetryFailedIDs, result.ParseTelemetry.QuarantineID)
		}
	}
	if err := router.FlushTelemetry(); err != nil {
		for _, id := range attemptedIDs {
			if !containsString(summary.TelemetryFailedIDs, id) {
				summary.TelemetryFailedIDs = append(summary.TelemetryFailedIDs, id)
			}
		}
		for index := range history {
			if containsString(summary.TelemetryFailedIDs, history[index].ID) {
				history[index].TelemetryStatus = "failed"
			}
		}
	}
	for index := range history {
		if containsString(summary.TelemetryFailedIDs, history[index].ID) {
			history[index].TelemetryStatus = "failed"
		}
	}
	for _, record := range unresolved {
		summary.WaitingIDs = append(summary.WaitingIDs, record.ID)
	}
	for _, id := range selectedIDs {
		if !seen[id] {
			summary.MissingIDs = append(summary.MissingIDs, id)
		}
	}
	summary.Replayed, summary.Resolved, summary.Waiting = replayed, resolved, len(summary.WaitingIDs)
	// Close every configured file destination before atomically replacing the
	// active quarantine queue. Linux permits renaming an open file, while
	// Windows correctly rejects it; closing here keeps replay portable and also
	// guarantees all destination buffers are durable before queue compaction.
	if err := router.Close(); err != nil {
		return fmt.Errorf("close replay outputs: %w", err)
	}
	if err := appendQuarantineHistory(path+".history.jsonl", history); err != nil {
		return err
	}
	if err := rewriteUnresolved(path, unresolved); err != nil {
		return err
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendQuarantineHistory(path string, entries []quarantineHistoryEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			return err
		}
	}
	return file.Sync()
}

func rewriteUnresolved(path string, records []quarantine.Record) error {
	temporary := path + ".replay.tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			file.Close()
			return err
		}
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func sanitizePayload(data map[string]interface{}, route string) map[string]interface{} {
	if route == "unknown" || route == "quarantine" {
		return map[string]interface{}{}
	}
	result := make(map[string]interface{}, len(data))
	for key, value := range data {
		if key == "raw" || key == "_raw" || key == "_unknown" {
			continue
		}
		result[key] = sanitizePayloadValue(value)
	}
	return result
}

func sanitizePayloadValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			if key == "raw" || key == "_raw" || key == "_unknown" {
				continue
			}
			result[key] = sanitizePayloadValue(child)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(typed))
		for index := range typed {
			result[index] = sanitizePayloadValue(typed[index])
		}
		return result
	default:
		return typed
	}
}
