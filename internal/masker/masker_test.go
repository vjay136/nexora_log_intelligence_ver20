package masker_test

import (
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/masker"
)

func TestMaskIPv4(t *testing.T) {
	m := masker.New(nil)
	out := m.MaskForTemplate("Connection from 192.168.1.1 port 22")
	if strings.Contains(out, "192.168.1.1") {
		t.Errorf("IPv4 not masked: %q", out)
	}
	if !strings.Contains(out, masker.TokenIP) {
		t.Errorf("IP token not present: %q", out)
	}
}

func TestMaskEmail(t *testing.T) {
	m := masker.New(nil)
	out := m.MaskForTemplate("From: user@example.com to admin@corp.org")
	if strings.Contains(out, "user@example.com") {
		t.Errorf("email not masked: %q", out)
	}
}

func TestMaskUUID(t *testing.T) {
	m := masker.New(nil)
	out := m.MaskForTemplate("request_id=550e8400-e29b-41d4-a716-446655440000")
	if strings.Contains(out, "550e8400") {
		t.Errorf("UUID not masked: %q", out)
	}
}

func TestMaskURL(t *testing.T) {
	m := masker.New(nil)
	out := m.MaskForTemplate("fetching https://example.com/api/data?token=abc")
	if strings.Contains(out, "https://example.com") {
		t.Errorf("URL not masked: %q", out)
	}
}

func TestMaskSecret(t *testing.T) {
	out := masker.MaskSecrets("password=mysecret123 user=alice")
	if strings.Contains(out, "mysecret123") {
		t.Errorf("secret not redacted: %q", out)
	}
}

func TestMaskDisableType(t *testing.T) {
	m := masker.New(&masker.Config{DisableTypes: []masker.MaskType{masker.MaskIPv4}})
	out := m.MaskForTemplate("Connection from 10.0.0.1")
	if !strings.Contains(out, "10.0.0.1") {
		t.Errorf("IPv4 should NOT be masked when disabled: %q", out)
	}
}
