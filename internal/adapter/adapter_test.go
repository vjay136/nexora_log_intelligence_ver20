package adapter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/engine"
	"nexora.local/nli-v20/internal/familyprofile"
	"nexora.local/nli-v20/parser-policy"
	"nexora.local/nli-v20/telemetry"
)

// TestTask4Integration verifies a reviewed SMTP-OUT corpus record through the
// engine's real ParserPack integration with no source service override.
func TestTask4Integration(t *testing.T) {
	input := "20250812 130034198+0100 aaa-gmailrgo009 smtp 3609 0 140290550523648 Note;SmtpServerConnect(2001/3) id=6864BD5E047384B0:host=11.22.33.44:connection_count=1:local_port=2525"

	deps := engine.Default()
	adapter, err := NewParserPackAdapter()
	if err != nil {
		t.Fatal(err)
	}
	deps.SetV5Parser(adapter)
	result := engine.Parse(input, engine.Source{
		Hostname: "testnode", Service: "auto", File: "/var/log/unrelated.log", LineNumber: 1,
	}, deps)

	if result.NLI.Parser != "nli_reviewed_smtp_out/reviewed_smtp_out" {
		t.Fatalf("reviewed SMTP-OUT profile was not selected: %s", result.NLI.Parser)
	}
	for _, field := range []string{
		"date", "time", "hostname", "service", "major_pid", "pid_status", "minor_pid",
		"event_type", "event", "code", "id", "host", "connection_count", "local_port",
	} {
		if _, exists := result.Data[field]; !exists {
			t.Errorf("expected field %s not found", field)
		}
	}
	if result.Data["date"] != "20250812" || result.Data["host"] != "11.22.33.44" || result.Data["service"] != "smtp" {
		t.Fatalf("unexpected reviewed payload: %#v", result.Data)
	}
	if _, hasRaw := result.Data["raw"]; hasRaw {
		t.Error("EventPayload must not contain raw input")
	}
	if !strings.HasPrefix(result.Meta.Assurance.SelectedStrategy, "parserpack:") {
		t.Errorf("expected ParserPack strategy, got %s", result.Meta.Assurance.SelectedStrategy)
	}
}

// TestV5FallbackToV4 verifies fallback when v5 parsing fails
func TestV5FallbackToV4(t *testing.T) {
	// Use input that v5 won't handle but v4 might
	input := "2024-10-04 10:30:45 INFO This is a generic log message"

	deps := engine.Default()
	adapter, err := NewParserPackAdapter()
	if err != nil {
		t.Fatal(err)
	}
	deps.SetV5Parser(adapter)

	source := engine.Source{
		Hostname: "testnode",
		Service:  "generic",
		File:     "/var/log/generic.log",
	}

	result := engine.Parse(input, source, deps)

	// Should fallback to v4 parsing - exact behavior depends on v4 parsers
	// At minimum, should not crash and should return a valid envelope
	if result.NLI.Version != engine.Version {
		t.Errorf("Expected version %s, got %s", engine.Version, result.NLI.Version)
	}
}

// TestEventPayloadTelemetrySeparation verifies no raw data in EventPayload
func TestEventPayloadTelemetrySeparation(t *testing.T) {
	adapter, err := NewParserPackAdapter()
	if err != nil {
		t.Fatal(err)
	}

	source := engine.Source{
		Hostname: "testnode",
		Service:  "smtp",
		File:     "/var/log/smtp.log",
	}

	input := parserpolicy.SMTPGoldenInput
	result := adapter.TryParseV5(input, source)

	if !result.Success {
		t.Fatal("Expected successful v5 parsing")
	}
	if result.ParserID != "smtp_native_v5/smtp_sieve_rule" {
		t.Fatalf("exact SMTP golden selected parser %q", result.ParserID)
	}
	if !reflect.DeepEqual(result.EventPayload, parserpolicy.SMTPGoldenPayload()) {
		t.Fatalf("exact SMTP golden mismatch\nactual:   %#v\nexpected: %#v", result.EventPayload, parserpolicy.SMTPGoldenPayload())
	}

	// EventPayload must not contain raw log text
	if _, hasRaw := result.EventPayload["raw"]; hasRaw {
		t.Errorf("EventPayload should not contain raw field")
	}

	// ParseTelemetry must not contain full parsed events
	if result.ParseTelemetry == nil {
		t.Fatal("ParseTelemetry should not be nil")
	}

	// ParseTelemetry should have metadata but no event payload fields
	if result.ParseTelemetry["event_id"] == "" {
		t.Error("ParseTelemetry should contain event_id")
	}
	if result.ParseTelemetry["parser_id"] == "" {
		t.Error("ParseTelemetry should contain parser_id")
	}
}

func TestQuarantineIsNotSuccessfulEmptyPayload(t *testing.T) {
	adapter, err := NewParserPackAdapter()
	if err != nil {
		t.Fatal(err)
	}
	input := "20260401 072011706+0100 aabbbccc001 capd 250622 0 140212679857920 Note;CalDavResponse id=:username:cosId:auth:request:depth:user-agent:error_id:http_code:status:resp_time:resp_size"
	result := adapter.TryParseV5Production(input, engine.Source{Service: "auto", Hostname: "node"})
	if result.Success || !result.Quarantined || result.EventPayload != nil || result.ParseTelemetry == nil {
		t.Fatalf("quarantine contract violated: %#v", result)
	}
	if result.ParseTelemetry.Route != "quarantine" || result.ParseTelemetry.Result != "quarantined" || len(result.ParseTelemetry.ErrorCodes) == 0 {
		t.Fatalf("quarantine telemetry incomplete: %+v", result.ParseTelemetry)
	}
}

func TestParallelPackAttemptsKeepDeterministicWinnerAndAttemptOrder(t *testing.T) {
	adapter, err := NewParserPackAdapter()
	if err != nil {
		t.Fatal(err)
	}
	input := parserpolicy.SMTPGoldenInput
	source := engine.Source{Service: "smtp", Hostname: "stable-node"}
	first := adapter.TryParseV5Production(input, source)
	if !first.Success {
		t.Fatal("golden SMTP record did not parse")
	}
	firstAttempts, _ := json.Marshal(stableParserAttempts(first.ParseTelemetry.OrderedAttempts))
	firstFields, _ := json.Marshal(first.EventPayload.Fields)
	for i := 0; i < 20; i++ {
		got := adapter.TryParseV5Production(input, source)
		if !got.Success || got.ParseTelemetry.ParserID != first.ParseTelemetry.ParserID {
			t.Fatalf("iteration %d selected a different parser", i)
		}
		attempts, _ := json.Marshal(stableParserAttempts(got.ParseTelemetry.OrderedAttempts))
		fields, _ := json.Marshal(got.EventPayload.Fields)
		if string(attempts) != string(firstAttempts) || string(fields) != string(firstFields) {
			t.Fatalf("iteration %d changed attempt order or exact output\nfirst attempts: %s\nnext attempts:  %s\nfirst fields: %s\nnext fields:  %s", i, firstAttempts, attempts, firstFields, fields)
		}
	}
}

func stableParserAttempts(attempts []telemetry.ParserAttempt) []struct {
	ParserID      string
	Priority      int
	Outcome       telemetry.AttemptOutcome
	FailureReason string
	FieldsEmitted int
} {
	stable := make([]struct {
		ParserID      string
		Priority      int
		Outcome       telemetry.AttemptOutcome
		FailureReason string
		FieldsEmitted int
	}, len(attempts))
	for index, attempt := range attempts {
		stable[index] = struct {
			ParserID      string
			Priority      int
			Outcome       telemetry.AttemptOutcome
			FailureReason string
			FieldsEmitted int
		}{
			ParserID:      attempt.ParserID,
			Priority:      attempt.Priority,
			Outcome:       attempt.Outcome,
			FailureReason: attempt.FailureReason,
			FieldsEmitted: attempt.FieldsEmitted,
		}
	}
	return stable
}

func BenchmarkProductionAdapterPackAttempts(b *testing.B) {
	adapter, err := NewParserPackAdapter()
	if err != nil {
		b.Fatal(err)
	}
	source := engine.Source{Service: "smtp", Hostname: "bench-node"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := adapter.TryParseV5Production(parserpolicy.SMTPGoldenInput, source)
		if !result.Success {
			b.Fatal("golden SMTP record did not parse")
		}
	}
}

func TestAllReviewedCorpusRoutesThroughProductionAdapter(t *testing.T) {
	adapter, err := NewParserPackAdapter()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		name      string
		file      string
		parserID  string
		records   int
		malformed map[int]bool
	}{
		{"IMAP", "imap.log", "nli_reviewed_imap/reviewed_imap", 51, nil},
		{"POP", "pop.log", "nli_reviewed_pop/reviewed_pop", 8, nil},
		{"WEBTOP/WEBMAIL", "webtop-webmail.log", "nli_reviewed_webtop_webmail/reviewed_webtop_webmail", 19, nil},
		{"MTA", "mta.log", "nli_reviewed_mta/reviewed_mta", 18, nil},
		{"PSS/PABD", "pss-pabd.log", "nli_reviewed_pss_pabd/reviewed_pss_pabd", 13, nil},
		{"MSS/CAPD", "mss-capd.log", "nli_reviewed_mss_capd/reviewed_mss_capd", 13, map[int]bool{10: true, 11: true, 12: true, 13: true}},
		{"SMTP-IN", "smtp-in.log", "nli_reviewed_smtp_in/reviewed_smtp_in", 424, nil},
		{"SMTP-OUT", "smtp-out.log", "nli_reviewed_smtp_out/reviewed_smtp_out", 132, nil},
	}

	total, valid, quarantined := 0, 0, 0
	sourceAliases, envelopeAliases := 0, 0
	for _, fixture := range fixtures {
		t.Run(strings.ReplaceAll(fixture.name, "/", "-"), func(t *testing.T) {
			path := filepath.Join("..", "..", "testdata", "migration", "sample-logs", "families", fixture.file)
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 64*1024), 1<<20)
			line := 0
			for scanner.Scan() {
				line++
				raw := scanner.Text()
				result := adapter.TryParseV5Production(raw, engine.Source{
					Service: "auto", Hostname: "runtime-node", File: "/var/log/not-a-family-name.log",
				})
				total++
				if fixture.malformed[line] {
					quarantined++
					if result.Success || !result.Quarantined || result.EventPayload != nil || result.ParseTelemetry == nil {
						t.Errorf("line %d malformed CAPD record was not quarantined", line)
					}
					continue
				}
				valid++
				if !result.Success || result.Quarantined || result.EventPayload == nil || result.ParseTelemetry == nil {
					t.Errorf("line %d did not route successfully: %+v", line, result.ParseTelemetry)
					continue
				}
				if result.ParseTelemetry.ParserID != fixture.parserID {
					t.Errorf("line %d parser = %s, want %s", line, result.ParseTelemetry.ParserID, fixture.parserID)
				}
				if result.ParseTelemetry.AttemptCount < 1 || result.ParseTelemetry.AttemptCount > 32 || len(result.ParseTelemetry.OrderedAttempts) != result.ParseTelemetry.AttemptCount {
					t.Errorf("line %d unbounded/incomplete attempts: %+v", line, result.ParseTelemetry)
				}
				if len(result.ParseTelemetry.PolicyHash) != 64 {
					t.Errorf("line %d lacks immutable policy hash", line)
				}
				encodedTelemetry, err := json.Marshal(result.ParseTelemetry)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(encodedTelemetry), raw) {
					t.Errorf("line %d raw input leaked into telemetry", line)
				}
				fields := result.EventPayload.Fields
				for _, forbidden := range []string{"raw", "nli", "meta", "source", "envelope"} {
					if _, exists := fields[forbidden]; exists {
						t.Errorf("line %d emitted forbidden field %s", line, forbidden)
					}
				}
				if _, exists := fields["input.source"]; exists {
					sourceAliases++
				}
				if _, exists := fields["input.envelope"]; exists {
					envelopeAliases++
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if line != fixture.records {
				t.Errorf("records = %d, want %d", line, fixture.records)
			}
		})
	}
	if total != 678 || valid != 674 || quarantined != 4 || sourceAliases != 7 || envelopeAliases != 5 {
		t.Fatalf("corpus routing totals: total=%d valid=%d quarantine=%d source_aliases=%d envelope_aliases=%d", total, valid, quarantined, sourceAliases, envelopeAliases)
	}
}

func TestMultipleApprovedPackSelectionAndTelemetryAreDeterministic(t *testing.T) {
	adapter, err := NewParserPackAdapter()
	if err != nil {
		t.Fatal(err)
	}
	input := "20250812 130034198+0100 mail001 smtp 3609 0 140290550523648 Note;SmtpServerConnect(2001/3) id=6864BD5E047384B0:host=11.22.33.44:connection_count=1:local_port=2525"
	for iteration := 0; iteration < 3; iteration++ {
		result := adapter.TryParseV5Production(input, engine.Source{Service: "smtp", Hostname: "runtime-node"})
		if !result.Success || result.Quarantined || result.ParseTelemetry == nil {
			t.Fatalf("iteration %d failed: %+v", iteration, result)
		}
		metadata := result.ParseTelemetry
		if metadata.ParserID != "nli_reviewed_smtp_in/reviewed_smtp_in" || metadata.AttemptCount != 3 || len(metadata.OrderedAttempts) != 3 {
			t.Fatalf("iteration %d selection/attempts = %+v", iteration, metadata)
		}
		if metadata.OrderedAttempts[0].ParserID != "smtp_native_v5" ||
			metadata.OrderedAttempts[1].ParserID != "nli_reviewed_smtp_in/reviewed_smtp_in" ||
			metadata.OrderedAttempts[2].ParserID != "nli_reviewed_smtp_out/reviewed_smtp_out" {
			t.Fatalf("iteration %d attempt order = %+v", iteration, metadata.OrderedAttempts)
		}
	}
}

func TestGlobalPackAttemptLimitFailsClosed(t *testing.T) {
	profiles, err := familyprofile.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	var sourcePack *parserpolicy.ParserPack
	for _, profile := range profiles {
		if profile.Pack.ID == "nli_reviewed_smtp_in" {
			sourcePack = profile.Pack
			break
		}
	}
	if sourcePack == nil {
		t.Fatal("SMTP-IN builtin not found")
	}
	directory := t.TempDir()
	for index := 0; index < 9; index++ {
		data, err := json.Marshal(sourcePack)
		if err != nil {
			t.Fatal(err)
		}
		pack, err := parserpolicy.LoadParserPackBytes(data)
		if err != nil {
			t.Fatal(err)
		}
		pack.ID = fmt.Sprintf("managed_smtp_%02d", index)
		pack.Author = "controller-approved-review"
		pack.ReviewState = parserpolicy.ReviewStateApproved
		pack.ActivatedAt = nil
		pack.Metadata["runtime_order"] = fmt.Sprint(200 + index)
		pack.Rules[0].ServiceSelectors = []string{"smtp"}
		pack.RegressionCases[0].Service = "smtp"
		pack.RegressionCases[0].ExpectedParser = pack.ID + "/" + pack.Rules[0].ID
		if err := parserpolicy.SealParserPack(pack); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(pack)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, fmt.Sprintf("managed-%02d.parserpack.json", index))
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	adapter, err := NewParserPackAdapterFromDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	input := "20250812 130034198+0100 mail001 smtp 3609 0 140290550523648 Note;SmtpServerConnect(2001/3) id=6864BD5E047384B0:host=11.22.33.44:connection_count=1:local_port=2525"
	result := adapter.TryParseV5Production(input, engine.Source{Service: "smtp", Hostname: "runtime-node"})
	if result.Success || !result.Quarantined || result.EventPayload != nil || result.ParseTelemetry == nil {
		t.Fatalf("attempt limit did not fail closed: %#v", result)
	}
	if result.ParseTelemetry.AttemptCount > maxProfileExecutions*familyprofile.MaxAttemptsPerPack ||
		!containsString(result.ParseTelemetry.ErrorCodes, "pack_attempt_limit") {
		t.Fatalf("attempt bound telemetry is incomplete: %+v", result.ParseTelemetry)
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
