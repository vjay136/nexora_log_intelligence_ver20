package enrichment

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"nexora.local/nli-v20/internal/adapter"
	"nexora.local/nli-v20/internal/engine"
)

func TestApplyDerivesEmailAndIPv4Buckets(t *testing.T) {
	fields := map[string]interface{}{
		"user":     "Ancient.Art.T21@sample.com",
		"fromhost": "11.22.33.44:4555",
	}
	Apply(fields, nil)
	if got, want := fields["email_bucket"], firstHash("ancient.art", 2); got != want {
		t.Fatalf("email_bucket = %v, want %v", got, want)
	}
	if got, want := fields["network_bucket"], firstHash("11.22.33.0", 1); got != want {
		t.Fatalf("network_bucket = %v, want %v", got, want)
	}
	if fields["is_private_ip"] != false {
		t.Fatalf("public IPv4 private flag = %v", fields["is_private_ip"])
	}
	if fields["fromhost"] != "11.22.33.44:4555" {
		t.Fatal("enrichment must preserve original parsed fields")
	}
}

func TestApplySupportsPrivateIPv6AndOmitsGeoWithoutDatabases(t *testing.T) {
	fields := map[string]interface{}{"username": "alice.t1@example.com", "remoteaddr": "[fd00::1234]:8443"}
	Apply(fields, nil)
	if fields["is_private_ip"] != true {
		t.Fatalf("private IPv6 flag = %v", fields["is_private_ip"])
	}
	if _, ok := fields["network_bucket"]; !ok {
		t.Fatal("IPv6 prefix bucket missing")
	}
	if _, ok := fields["geoip_country_code"]; ok {
		t.Fatal("GeoIP country must be omitted without a database")
	}
	if got, want := fields["email_bucket"], firstHash("alice", 2); got != want {
		t.Fatalf("email_bucket = %v, want %v", got, want)
	}
}

func TestOpenMissingDBIsGracefulAndMalformedDBFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.mmdb")
	geo, err := Open(Config{CountryDB: missing})
	if err != nil || geo != nil {
		t.Fatalf("missing configured database should be skipped: geo=%v err=%v", geo, err)
	}
	if err := os.WriteFile(missing, []byte("not an mmdb"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{CountryDB: missing}); err == nil {
		t.Fatal("malformed configured database must be reported")
	}
}

func TestConfigRequiresAbsoluteMMDBPaths(t *testing.T) {
	if err := ValidateConfig(Config{CountryDB: "relative.mmdb"}); err == nil {
		t.Fatal("relative MMDB path accepted")
	}
}

func TestProvidedPOPAndWebtopSamplesReceiveBucketsAfterParserPack(t *testing.T) {
	parser, err := adapter.NewParserPackAdapter()
	if err != nil {
		t.Fatal(err)
	}
	samples := []struct{ service, line string }{
		{"pop", "20250905 000001040+0100 server009 popserv 1994089 3289010 139972830078720 Note;AcctBadPswd(50/6) user=demouser@sample.com:cmd=pass <pswd>:fromhost=11.22.33.44:fromport=64472:sid=e2ab5bd0-89e2-11f0-b46d-4feb1d957533:resptime=307:haProxyDstPort=995"},
		{"webtop", `2025-07-22 10:00:00:001+0100 remoteport="26514" application="RichUI" username="ancient.art@sample.com" method="mail.message.list" email="aaa-gmailweb001-admin@mx.internal" duration="322" hostname="aaa-gmailweb001" servername="email.gmail.com" api="webtop" remoteaddr="11.22.33.44" useragent="Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/11.22.33.44 Safari/537.36" sessionid="c02OXHcbjGEv8jWYUHiidV-upww" userid="ancient.art@sample.com" usercosid="all_done"`},
	}
	for _, sample := range samples {
		result := parser.TryParseV5Production(sample.line, engine.Source{Service: sample.service, Hostname: "sample-node"})
		if !result.Success || result.EventPayload == nil {
			t.Fatalf("%s sample did not parse: %+v", sample.service, result)
		}
		Apply(result.EventPayload.Fields, nil)
		if _, ok := result.EventPayload.Fields["email_bucket"]; !ok {
			t.Errorf("%s sample did not receive email_bucket: %#v", sample.service, result.EventPayload.Fields)
		}
		if _, ok := result.EventPayload.Fields["network_bucket"]; !ok {
			t.Errorf("%s sample did not receive network_bucket: %#v", sample.service, result.EventPayload.Fields)
		}
	}
}

func firstHash(value string, chars int) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])[:chars]
}
