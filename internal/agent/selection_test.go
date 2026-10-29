package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/output"
)

func TestExtractLineMetadata(t *testing.T) {
	cases := []struct{ line, event, method, service, hostname string }{
		{"20250320 130000012+0000 server001 imapserv 1536593 3183330 139631346116352 Note;ImapDisconnected(56/17) user=x", "ImapDisconnected", "", "imap", "server001"},
		{"20250320 130000012+0000 server009 popserv 1 2 3 Note;AcctBadPswd(50/6) user=x", "AcctBadPswd", "", "pop", "server009"},
		{"2026-10-10 10:10:10:006+0100 method=\"auth.loginByToken\" hostname=\"mail01\"", "auth.loginByToken", "auth.loginByToken", "webtop", "mail01"},
		{"20260310 090000006+0000 server001 mta 249489 251927 140409946728192 Note;MtaRequestingDomainTableUpdate(79/102) fromhost=[11.22.33.44]", "MtaRequestingDomainTableUpdate", "", "mta", "server001"},
		{"20260401 134433123+0100 server001 pabd 1 0 3 Note;CARDDAV id=1", "CARDDAV", "", "pabd", "server001"},
		{"20250811 080053105+0100 aaa-gmailrgi009 smtp 3692 0 140026854221568 Note;SieveRuleMsg(2005/1) id=1", "SieveRuleMsg", "", "smtp", "aaa-gmailrgi009"},
	}
	for _, tc := range cases {
		got := extractLineMetadata(tc.line)
		if got.event != tc.event || got.method != tc.method || got.service != tc.service || got.hostname != tc.hostname {
			t.Errorf("metadata = %#v, want event=%q method=%q service=%q hostname=%q", got, tc.event, tc.method, tc.service, tc.hostname)
		}
	}
}

func TestFilteredSelectionIgnoresNonMatchingEventBeforeParsing(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "imap.log")
	eventPath := filepath.Join(root, "events.jsonl")
	telemetryPath := filepath.Join(root, "telemetry.jsonl")
	target := "20250320 130000046+0000 server001 imapserv 1536593 3171842 139632053430016 Note;ImapDisconnected(56/17) user=demouser@sample.com:1234567890123456789:mss=aaa-gmail02:port=55014:fromhost=11.22.33.44:fromport=33486:sid=3b0ac4a0-058b-11f0-b1f0-7c973e173a6d:respcode=NO:resptime=156:client=com.google.android.gm:node=aaa-gmail2mss015:nodePort=55014:cosId=free_signin:haProxyDstPort=993"
	nonTarget := strings.Replace(target, "ImapDisconnected", "ImapConnected", 1)
	if err := os.WriteFile(inputPath, []byte(nonTarget+"\n"+target+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Version:  2,
		Inputs:   []InputConfig{{Path: inputPath, Service: "imap", Selection: SelectionConfig{Mode: "filtered", Events: []string{"ImapDisconnected"}, MatchService: "imap"}}},
		StateDir: filepath.Join(root, "state"), PollMS: 50,
		Outputs:          []output.Config{{Type: "file", Path: eventPath, Routes: []string{"normal"}}},
		TelemetryOutputs: []output.Config{{Type: "file", Path: telemetryPath, Routes: []string{"telemetry"}}},
	}
	if err := Run(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(eventPath)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 1 {
		t.Fatalf("filtered output has %d records, want exactly 1: %s", lines, data)
	}
}

func TestSelectionModesAndConjunction(t *testing.T) {
	line := "20250320 130000012+0000 server001 imapserv 1 2 3 Note;ImapDisconnected(56/17)"
	s := SelectionConfig{Mode: "filtered", Events: []string{"ImapDisconnected"}, Services: []string{"imapserv"}, Hostnames: []string{"SERVER001"}}
	if ok, _ := selectionMatches(s, line); !ok {
		t.Fatal("all exact selectors should match")
	}
	s.Hostnames = []string{"server002"}
	if ok, _ := selectionMatches(s, line); ok {
		t.Fatal("selectors should combine with AND")
	}
	s = SelectionConfig{Mode: "filtered", Events: []string{"ImapDisconnected"}}
	if ok, _ := selectionMatches(s, line); !ok {
		t.Fatal("matching event should pass")
	}
	s.Events = []string{"Other"}
	if ok, _ := selectionMatches(s, line); ok {
		t.Fatal("nonmatching filtered line must be skipped")
	}
	s = SelectionConfig{Mode: "hybrid", Events: []string{"ImapDisconnected"}, MatchService: "imap"}
	if ok, service := selectionMatches(s, line); !ok || service != "imap" {
		t.Fatalf("hybrid match = %v, %q", ok, service)
	}
	if ok, service := selectionMatches(s, "ordinary line"); !ok || service != "auto" {
		t.Fatalf("hybrid nonmatch should use normal auto parser: %v, %q", ok, service)
	}
}

func TestWebtopMethodAllowlistIsExactAndDistinct(t *testing.T) {
	line := `2025-07-22 10:00:00:001+0100 remoteport="26514" application="RichUI" username="ancient.art@sample.com" method="mail.message.list" hostname="aaa-gmailweb001" api="webtop" remoteaddr="11.22.33.44"`
	if got := extractLineMetadata(line); got.method != "mail.message.list" || got.event != "mail.message.list" {
		t.Fatalf("method metadata = %#v", got)
	}
	filtered := SelectionConfig{Mode: "filtered", Methods: []string{"mail.message.list"}, MatchService: "webtop"}
	if keep, service := selectionMatches(filtered, line); !keep || service != "webtop" {
		t.Fatalf("target webtop method = %v, %q", keep, service)
	}
	filtered.Methods = []string{"auth.loginByToken"}
	if keep, _ := selectionMatches(filtered, line); keep {
		t.Fatal("different method must be ignored by filtered mode")
	}
	hybrid := SelectionConfig{Mode: "hybrid", Methods: []string{"auth.loginByToken"}, MatchService: "webtop"}
	if keep, service := selectionMatches(hybrid, line); !keep || service != "auto" {
		t.Fatalf("unmatched hybrid method should fall back to auto parser: %v, %q", keep, service)
	}
}

func TestFilteredWebtopConfigMayUseMethodsAlone(t *testing.T) {
	c := Config{
		Version: 2,
		Inputs: []InputConfig{{
			Path: filepath.Join(t.TempDir(), "webtop.log"), Service: "webtop",
			Selection: SelectionConfig{Mode: "filtered", Methods: []string{"mail.message.list"}},
		}},
		StateDir: filepath.Join(t.TempDir(), "state"), PollMS: 500,
	}
	if err := validate(c); err != nil {
		t.Fatalf("methods-only filtered config rejected: %v", err)
	}
}
