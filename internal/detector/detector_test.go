package detector_test

import (
	"testing"

	"nexora.local/nli-v20/internal/detector"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		input  string
		expect string
	}{
		{`{"key":"value"}`, detector.FormatJSON},
		{`<190>1 2026-10-02T10:00:00Z host app proc msg - message`, detector.FormatSyslogRFC5424},
		{`<34>Oct  2 10:00:00 myhost sshd[123]: Accepted password`, detector.FormatSyslogRFC3164},
		{`Oct  2 10:00:00 myhost sshd[123]: Accepted password`, detector.FormatSyslogRFC3164},
		{`CEF:0|Vendor|Product|1.0|100|Login|5|src=10.0.0.1`, detector.FormatCEF},
		{`192.168.1.1 - - [02/Oct/2026:10:00:00 +0000] "GET /index HTTP/1.1" 200 1234`, detector.FormatCLF},
		{`level=info msg="started" service=api port=8080`, detector.FormatLogfmt},
		{`user=alice action=login ip=10.0.0.1`, detector.FormatLogfmt},
		{`just a plain log line with no structure`, detector.FormatPlain},
	}
	for _, c := range cases {
		got := detector.Detect(c.input)
		if got != c.expect {
			t.Errorf("Detect(%q) = %q, want %q", c.input, got, c.expect)
		}
	}
}
