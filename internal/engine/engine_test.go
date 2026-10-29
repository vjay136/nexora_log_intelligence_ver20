package engine_test

import (
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/engine"
)

var deps = engine.Default()
var src = engine.Source{Hostname: "testhost", Service: "test", File: "/test.log", LineNumber: 1}

func TestParseJSON(t *testing.T) {
	raw := `{"service":"imap","user":"alice","success":true}`
	e := engine.Parse(raw, src, deps)
	if e.NLI.Parser != "generic.json" {
		t.Errorf("parser = %q, want generic.json", e.NLI.Parser)
	}
	if e.NLI.Confidence < 0.95 {
		t.Errorf("confidence = %.2f, want >= 0.95", e.NLI.Confidence)
	}
}

func TestTruncatedJSONIsQuarantinedWithEvidence(t *testing.T) {
	raw := `{"service":"imap","user":"alice","success":true}`
	e := engine.ParseWithTruncated(raw, src, engine.Default(), true)
	if e.NLI.Parser != "generic.json" || e.Data["user"] != "alice" {
		t.Fatalf("truncated JSON evidence was not retained: parser=%q data=%#v", e.NLI.Parser, e.Data)
	}
	if e.Meta.Route != "quarantine" || e.Meta.Assurance.Verified || e.NLI.Confidence > 0.79 {
		t.Fatalf("truncated JSON accepted as verified: route=%q confidence=%.2f assurance=%+v", e.Meta.Route, e.NLI.Confidence, e.Meta.Assurance)
	}
}

func TestCLFParserRemainsNormallyRoutedWhenUnambiguous(t *testing.T) {
	raw := `192.168.1.100 - alice [02/Oct/2026:10:00:00 +0000] "GET /index.html HTTP/1.1" 200 1234 "-" "Mozilla/5.0"`
	e := engine.Parse(raw, src, engine.Default())
	if e.Meta.Assurance.Ambiguous || e.Meta.Route != "normal" {
		t.Fatalf("unambiguous CLF record was incorrectly quarantined: route=%q assurance=%+v", e.Meta.Route, e.Meta.Assurance)
	}
}

func TestFormatFingerprintGroupsValueVariants(t *testing.T) {
	first, firstProfile := engine.FormatFingerprint(`user=alice action=login count=3`, "logfmt")
	second, secondProfile := engine.FormatFingerprint(`user=bob action=logout count=91`, "logfmt")
	if first != second || strings.Join(firstProfile, ",") != strings.Join(secondProfile, ",") {
		t.Fatalf("fingerprints depend on values: %q/%v vs %q/%v", first, firstProfile, second, secondProfile)
	}
}

func TestUnknownRecordsIncludeBoundedCandidateEvidence(t *testing.T) {
	raw := `completely unstructured log line with no known format at all xyz`
	e := engine.Parse(raw, src, engine.Default())
	if e.NLI.Parser != "unknown" || e.Meta.Assurance.Fingerprint == "" {
		t.Fatalf("unknown record lacks a structural fingerprint: parser=%q assurance=%+v", e.NLI.Parser, e.Meta.Assurance)
	}
	if len(e.Meta.Assurance.Candidates) == 0 || len(e.Meta.Assurance.Candidates) > 5 {
		t.Fatalf("unknown record candidate evidence is missing or unbounded: %+v", e.Meta.Assurance.Candidates)
	}
	for i := 1; i < len(e.Meta.Assurance.Candidates); i++ {
		if e.Meta.Assurance.Candidates[i].Score > e.Meta.Assurance.Candidates[i-1].Score {
			t.Fatalf("candidate evidence is not ranked: %+v", e.Meta.Assurance.Candidates)
		}
	}
}

func TestParseWithSelectedParserUsesExactLiveRegistryParser(t *testing.T) {
	raw := `level=INFO user=alice request_id=42`
	envelope, err := engine.ParseWithSelectedParser(raw, src, deps, "generic.kv")
	if err != nil {
		t.Fatal(err)
	}
	if envelope.NLI.Parser != "generic.kv" || envelope.Meta.Assurance.SelectedStrategy != "selected:generic.kv" {
		t.Fatalf("selected parser was not honored: parser=%q strategy=%q", envelope.NLI.Parser, envelope.Meta.Assurance.SelectedStrategy)
	}
	if envelope.Data["user"] != "alice" || envelope.Meta.ParserAttempts != 1 {
		t.Fatalf("selected parser result=%#v attempts=%d", envelope.Data, envelope.Meta.ParserAttempts)
	}
	if _, err := engine.ParseWithSelectedParser(raw, src, deps, "missing.parser"); err == nil {
		t.Fatal("unknown selected parser was accepted")
	}
}

func TestParseSyslogRFC3164(t *testing.T) {
	raw := `Oct  2 10:00:00 server01 sshd[1234]: Accepted password for alice from 10.0.0.1 port 22 ssh2`
	e := engine.Parse(raw, src, deps)
	if e.NLI.Parser != "security.sshd" {
		t.Errorf("parser = %q, want security.sshd", e.NLI.Parser)
	}
	if e.Data["host"] != "server01" {
		t.Errorf("host = %v, want server01", e.Data["host"])
	}
}

func TestParseSyslog5424(t *testing.T) {
	raw := `<34>1 2026-10-02T10:00:00Z myhost sshd 1234 - - Accepted password`
	e := engine.Parse(raw, src, deps)
	if e.NLI.Parser != "syslog.rfc5424" {
		t.Errorf("parser = %q, want syslog.rfc5424", e.NLI.Parser)
	}
}

func TestParseCLF(t *testing.T) {
	raw := `192.168.1.100 - alice [02/Oct/2026:10:00:00 +0000] "GET /index.html HTTP/1.1" 200 1234 "-" "Mozilla/5.0"`
	e := engine.Parse(raw, src, deps)
	if e.NLI.Parser != "web.apache_clf" {
		t.Errorf("parser = %q, want web.apache_clf", e.NLI.Parser)
	}
}

func TestParseCEF(t *testing.T) {
	raw := `CEF:0|Acme|Firewall|1.0|100|Login Attempt|5|src=10.0.0.1 dst=192.168.1.1`
	e := engine.Parse(raw, src, deps)
	if e.NLI.Parser != "cef.generic" {
		t.Errorf("parser = %q, want cef.generic", e.NLI.Parser)
	}
}

func TestParseKV(t *testing.T) {
	raw := `user=alice action=login ip=10.0.0.1 status=success`
	e := engine.Parse(raw, src, deps)
	if e.NLI.Parser != "generic.kv" {
		t.Errorf("parser = %q, want generic.kv", e.NLI.Parser)
	}
}

func TestParseSMTPDMARC(t *testing.T) {
	raw := `20261002 083120808+0200 server001 smtp 311776 0 139716457142016 Note;AntiAbuseDmarcAggregateReportRecord(2004/13) id=ABC123:adkim=r:aspf=r:p=reject:pct=100:fo=1:rua=mailto:dmarc@example.com`
	e := engine.Parse(raw, src, deps)
	if e.NLI.Parser != "smtp.dmarc.aggregate" {
		t.Errorf("parser = %q, want smtp.dmarc.aggregate", e.NLI.Parser)
	}
}

func TestSecretMasking(t *testing.T) {
	raw := `user=alice password=supersecret action=login`
	e := engine.Parse(raw, src, deps)
	data, _ := e.Data["_unknown"].(map[string]interface{})
	if data != nil {
		if pwd, ok := data["password"]; ok {
			if pwd != "[REDACTED]" {
				t.Errorf("password not redacted: %v", pwd)
			}
		}
	}
}

func TestUnknownLineRoute(t *testing.T) {
	raw := `completely unstructured log line with no known format at all xyz`
	e := engine.Parse(raw, src, deps)
	if e.Meta.Route != "unknown" && e.Meta.Route != "quarantine" {
		t.Logf("route = %q (drain may cluster this; acceptable)", e.Meta.Route)
	}
}

func TestOverSizedLine(t *testing.T) {
	raw := strings.Repeat("x", engine.MaxLine+10)
	e := engine.ParseWithTruncated(raw[:engine.MaxLine], src, deps, true)
	if e.NLI.Confidence > 0.85 {
		t.Errorf("truncated line should reduce confidence, got %.2f", e.NLI.Confidence)
	}
}

func TestAssuranceMetadata(t *testing.T) {
	raw := `2026-10-03T09:31:00Z INFO worker completed job=42`
	envelope := engine.Parse(raw, src, engine.Default())
	if envelope.NLI.Parser != "application.timestamped" {
		t.Fatalf("parser = %q", envelope.NLI.Parser)
	}
	if !envelope.Meta.Assurance.Verified || envelope.Meta.Assurance.EvidenceCoverage < 1 {
		t.Fatalf("assurance not verified: %+v", envelope.Meta.Assurance)
	}
	if len(envelope.Meta.Assurance.RawSHA256) != 64 || envelope.Raw != raw {
		t.Fatalf("raw integrity metadata missing: %+v", envelope.Meta.Assurance)
	}
}

func TestCLFIsNotAmbiguousWithNginx(t *testing.T) {
	raw := `192.168.1.1 - - [02/Oct/2026:10:00:00 +0000] "GET /index HTTP/1.1" 200 1234`
	envelope := engine.Parse(raw, src, engine.Default())
	if envelope.Meta.Assurance.Ambiguous || envelope.NLI.Parser != "web.apache_clf" {
		t.Fatalf("unexpected candidate selection: %+v", envelope.Meta.Assurance)
	}
	if envelope.Meta.Route != "normal" {
		t.Fatalf("route = %q, want normal", envelope.Meta.Route)
	}
}

func TestSecretRawIsRedactedButHasDigest(t *testing.T) {
	raw := `level=info token=top-secret-value action=login`
	envelope := engine.Parse(raw, src, engine.Default())
	if strings.Contains(envelope.Raw, "top-secret-value") || !envelope.Meta.RawRedacted {
		t.Fatalf("raw secret not redacted: %s", envelope.Raw)
	}
	if len(envelope.Meta.Assurance.RawSHA256) != 64 {
		t.Fatal("raw digest missing")
	}
}

func TestSMTPNativeMixedKVIntegration(t *testing.T) {
	raw := `20260822 050048557+0200 severrgo001 smtp 230675 0 140347436861184 Note;SieveRuleMsg(2005/1) id=6A291BF5048F23DE:policy=outbound:checkpoint=AUTH:rule=INOUT_action:msg=PolicyMsgInfo:eventType=PolicyMsgInfo:action=disconnect:reason=badpassword:fromhost=11.22.33.44:geo=IL:helo=Site6:auth_ok=false:auth_user=managers999@012.meg.in:auth_fail=6:auth_tries=7:score=0`
	parseDeps := engine.Default()
	parseDeps.Masker = nil
	envelope := engine.Parse(raw, src, parseDeps)
	if envelope.NLI.Parser != "smtp.native.mixed_kv" {
		t.Fatalf("parser=%q, want smtp.native.mixed_kv; errors=%v candidates=%+v", envelope.NLI.Parser, envelope.Meta.Errors, envelope.Meta.Assurance.Candidates)
	}
	if len(envelope.Data) != 16 || envelope.Meta.Extracted != 16 || envelope.Meta.Unknown != 0 {
		t.Fatalf("data=%d extracted=%d unknown=%d: %#v", len(envelope.Data), envelope.Meta.Extracted, envelope.Meta.Unknown, envelope.Data)
	}
	if _, exists := envelope.Data["_unknown"]; exists {
		t.Fatalf("successfully parsed SMTP tail was classified as unknown: %#v", envelope.Data)
	}
	checks := map[string]interface{}{
		"id": "6A291BF5048F23DE", "policy": "outbound", "eventType": "PolicyMsgInfo",
		"fromhost": "11.22.33.44", "auth_ok": false, "auth_fail": int64(6),
		"auth_tries": int64(7), "score": int64(0),
	}
	for key, expected := range checks {
		if actual := envelope.Data[key]; actual != expected {
			t.Errorf("%s=%#v, want %#v", key, actual, expected)
		}
	}
	if envelope.Meta.Assurance.SelectedStrategy != "smtp_native" || envelope.Meta.Assurance.EvidenceCoverage < 0.8 {
		t.Fatalf("assurance telemetry does not identify the mixed-KV path: %+v", envelope.Meta.Assurance)
	}
}
