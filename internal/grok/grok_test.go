package grok_test

import (
	"testing"

	"nexora.local/nli-v20/internal/grok"
)

func TestCompileAndMatch(t *testing.T) {
	g := grok.New(nil)
	c, err := g.Compile(`%{IPV4:client_ip} - - \[%{HTTPDATE:timestamp}\] "%{WORD:method} %{NOTSPACE:uri}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	m := c.Match(`192.168.1.1 - - [02/Oct/2026:10:00:00 +0000] "GET /index.html`)
	if m == nil {
		t.Fatal("expected match")
	}
	if m["client_ip"] != "192.168.1.1" {
		t.Errorf("client_ip = %q, want 192.168.1.1", m["client_ip"])
	}
	if m["method"] != "GET" {
		t.Errorf("method = %q, want GET", m["method"])
	}
}

func TestRecursiveSubstitution(t *testing.T) {
	g := grok.New(nil)
	// SYSLOGBASE references SYSLOGTIMESTAMP, HOSTNAME, WORD, INT
	c, err := g.Compile(`%{SYSLOGBASE}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	if c.RE == nil {
		t.Error("expected compiled regexp")
	}
}

func TestCycleDetection(t *testing.T) {
	g := grok.New(map[string]string{
		"A": `%{B}`,
		"B": `%{A}`,
	})
	_, err := g.Compile(`%{A}`)
	if err == nil {
		t.Error("expected cycle detection error")
	}
}

func TestUnknownPattern(t *testing.T) {
	g := grok.New(nil)
	_, err := g.Compile(`%{NONEXISTENT:field}`)
	if err == nil {
		t.Error("expected error for unknown pattern")
	}
}
