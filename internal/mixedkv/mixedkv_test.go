package mixedkv_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/mixedkv"
)

func TestSMTPMixedDelimiterTail(t *testing.T) {
	input := `id=6A291BF5048F23DE:policy=outbound:checkpoint=AUTH:rule=INOUT_action:msg=PolicyMsgInfo:eventType=PolicyMsgInfo:action=disconnect:reason=badpassword:fromhost=11.22.33.44:geo=IL:helo=Site6:auth_ok=false:auth_user=managers999@012.meg.in:auth_fail=6:auth_tries=7:score=0`
	config := mixedkv.DefaultConfig()
	config.PairSeparators = []string{":"}
	config.ValueTypes = map[string]string{
		"auth_ok": "boolean", "auth_fail": "integer", "auth_tries": "integer", "score": "integer",
	}
	result, err := mixedkv.Parse(input, config)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Fields) != 16 || len(result.Entries) != 16 {
		t.Fatalf("fields=%d entries=%d, want 16: %#v", len(result.Fields), len(result.Entries), result.Fields)
	}
	checks := map[string]interface{}{
		"id": "6A291BF5048F23DE", "policy": "outbound", "checkpoint": "AUTH",
		"rule": "INOUT_action", "eventType": "PolicyMsgInfo", "fromhost": "11.22.33.44",
		"auth_ok": false, "auth_fail": int64(6), "auth_tries": int64(7), "score": int64(0),
	}
	for key, expected := range checks {
		if actual := result.Fields[key]; !reflect.DeepEqual(actual, expected) {
			t.Errorf("%s=%#v, want %#v", key, actual, expected)
		}
	}
	assertEvidenceSpans(t, input, result)
}

func TestQuotesBracketsEmptyAndAllPairSeparators(t *testing.T) {
	input := `a=1;b:"two; three",c='x,y'|d=(left:right;still) empty=`
	config := mixedkv.DefaultConfig()
	config.PairSeparators = []string{";", ",", "|", " "}
	result, err := mixedkv.Parse(input, config)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]interface{}{
		"a": "1", "b": "two; three", "c": "x,y", "d": "(left:right;still)", "empty": "",
	}
	if !reflect.DeepEqual(result.Fields, expected) {
		t.Fatalf("fields=%#v, want %#v", result.Fields, expected)
	}
	assertEvidenceSpans(t, input, result)
}

func TestEscapesNestedJSONAndDelimiterCollisions(t *testing.T) {
	input := `msg=hello\:world:object={"url":"https://example.test/a:b","items":[1,2]}:next=ok`
	config := mixedkv.DefaultConfig()
	config.PairSeparators = []string{":"}
	config.ValueTypes = map[string]string{"object": "json"}
	result, err := mixedkv.Parse(input, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Fields["msg"] != "hello:world" || result.Fields["next"] != "ok" {
		t.Fatalf("escaped or trailing values parsed incorrectly: %#v", result.Fields)
	}
	object, ok := result.Fields["object"].(map[string]interface{})
	if !ok || object["url"] != "https://example.test/a:b" {
		t.Fatalf("nested JSON parsed incorrectly: %#v", result.Fields["object"])
	}
}

func TestURLIPv6AndTimeColonsAreNotPairBoundaries(t *testing.T) {
	input := `url=https://example.test:8443/a:b:ip=2001:db8::1:time=12:34:56:status=ok`
	config := mixedkv.DefaultConfig()
	config.PairSeparators = []string{":"}
	config.KeyAllowPatterns = []string{`^(url|ip|time|status)$`}
	config.ValueTypes = map[string]string{"ip": "ip"}
	result, err := mixedkv.Parse(input, config)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]interface{}{
		"url": "https://example.test:8443/a:b", "ip": "2001:db8::1", "time": "12:34:56", "status": "ok",
	}
	if !reflect.DeepEqual(result.Fields, expected) {
		t.Fatalf("fields=%#v, want %#v", result.Fields, expected)
	}
}

func TestMultiByteSeparators(t *testing.T) {
	config := mixedkv.DefaultConfig()
	config.PairSeparators = []string{"||", ","}
	config.AssignmentSeparators = []string{"=>", "="}
	result, err := mixedkv.Parse(`a=>one||b=two,c=>three`, config)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]interface{}{"a": "one", "b": "two", "c": "three"}
	if !reflect.DeepEqual(result.Fields, expected) {
		t.Fatalf("fields=%#v, want %#v", result.Fields, expected)
	}
}

func TestAliasesTypesDenyAndStopKeys(t *testing.T) {
	config := mixedkv.DefaultConfig()
	config.PairSeparators = []string{":"}
	config.KeyAllowPatterns = []string{`^(old|drop|stop|later)$`}
	config.KeyDenyPatterns = []string{`^drop$`}
	config.KeyAliases = map[string]string{"old": "new"}
	config.ValueTypes = map[string]string{"new": "integer"}
	config.StopKeys = []string{"stop"}
	result, err := mixedkv.Parse(`old=42:drop=x:stop=yes:later=no`, config)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Fields, map[string]interface{}{"new": int64(42)}) {
		t.Fatalf("unexpected fields: %#v", result.Fields)
	}
	if result.StoppedBy != "stop" || result.RemainderSpan == nil {
		t.Fatalf("stop accounting missing: %#v", result)
	}
	if remainder := `old=42:drop=x:stop=yes:later=no`[result.RemainderSpan.Start:]; remainder != "stop=yes:later=no" {
		t.Fatalf("remainder=%q", remainder)
	}
	if len(result.Entries) != 2 || result.Entries[1].Emitted || result.Entries[1].SkipReason != "denied" {
		t.Fatalf("deny accounting incorrect: %#v", result.Entries)
	}
}

func TestDuplicatePolicies(t *testing.T) {
	tests := []struct {
		name     string
		policy   mixedkv.DuplicatePolicy
		expected interface{}
		wantErr  bool
	}{
		{name: "error", policy: mixedkv.DuplicateError, wantErr: true},
		{name: "first", policy: mixedkv.DuplicateFirst, expected: "1"},
		{name: "last", policy: mixedkv.DuplicateLast, expected: "2"},
		{name: "array", policy: mixedkv.DuplicateArray, expected: []interface{}{"1", "2"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := mixedkv.DefaultConfig()
			config.PairSeparators = []string{":"}
			config.DuplicatePolicy = test.policy
			result, err := mixedkv.Parse(`key=1:key=2`, config)
			if test.wantErr {
				assertErrorCode(t, err, mixedkv.CodeDuplicateKey)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Fields["key"], test.expected) {
				t.Fatalf("key=%#v, want %#v", result.Fields["key"], test.expected)
			}
		})
	}
}

func TestAllowLeadingTextRequiresAllowlistedTopLevelBoundary(t *testing.T) {
	config := mixedkv.DefaultConfig()
	config.PairSeparators = []string{":"}
	config.AssignmentSeparators = []string{"="}
	config.KeyAllowPatterns = []string{`^(user|status)$`}
	config.AllowLeadingText = true
	input := `diagnostic:{protocolType=[imap], email=[nested@example.test]}:healthinsurance=message:user=demo:status=ok`
	result, err := mixedkv.Parse(input, config)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Fields, map[string]interface{}{"user": "demo", "status": "ok"}) {
		t.Fatalf("leading-text fields=%#v", result.Fields)
	}
	if result.LeadingSpan == nil || input[result.LeadingSpan.Start:result.LeadingSpan.End] != `diagnostic:{protocolType=[imap], email=[nested@example.test]}:healthinsurance=message:` {
		t.Fatalf("leading span lost source evidence: %#v", result.LeadingSpan)
	}
}

func TestPayloadFragmentsRemainValues(t *testing.T) {
	config := mixedkv.DefaultConfig()
	config.PairSeparators = []string{":"}
	config.AssignmentSeparators = []string{"="}
	config.KeyAllowPatterns = []string{`^(reply|InternalId|Hostname|status|originator|size|value)$`}
	input := `reply=250 [InternalId=42, Hostname=mx.example.test]:status=sent:originator=bounces+tag=customer.example@relay.test:size=7:value==?utf-8?B?YWJjZA==?=`
	result, err := mixedkv.Parse(input, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Fields["reply"] != `250 [InternalId=42, Hostname=mx.example.test]` || result.Fields["originator"] != `bounces+tag=customer.example@relay.test` || result.Fields["value"] != `=?utf-8?B?YWJjZA==?=` {
		t.Fatalf("payload fragments were split: %#v", result.Fields)
	}
	if _, exists := result.Fields["InternalId"]; exists {
		t.Fatal("bracketed InternalId was promoted to a field")
	}
	if _, exists := result.Fields["Hostname"]; exists {
		t.Fatal("bracketed Hostname was promoted to a field")
	}
}

func TestAliasesRetainSourceKeyEvidence(t *testing.T) {
	config := mixedkv.DefaultConfig()
	config.PairSeparators = []string{":"}
	config.AssignmentSeparators = []string{"="}
	config.KeyAllowPatterns = []string{`^(source|envelope)$`}
	config.KeyAliases = map[string]string{"source": "input.source", "envelope": "input.envelope"}
	result, err := mixedkv.Parse(`source=header:envelope=sender@example.test`, config)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]interface{}{"input.source": "header", "input.envelope": "sender@example.test"}
	if !reflect.DeepEqual(result.Fields, expected) {
		t.Fatalf("aliased fields=%#v, want %#v", result.Fields, expected)
	}
	if len(result.Entries) != 2 || result.Entries[0].SourceKey != "source" || result.Entries[0].Key != "input.source" || result.Entries[1].SourceKey != "envelope" || result.Entries[1].Key != "input.envelope" {
		t.Fatalf("alias source mapping lost: %#v", result.Entries)
	}
}

func TestStringsRemainStringsUnlessExplicitlyTyped(t *testing.T) {
	config := mixedkv.DefaultConfig()
	config.ValueTypes = map[string]string{"enabled": "boolean"}
	result, err := mixedkv.Parse(`id=000123 name="José 東京" enabled=true`, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Fields["id"] != "000123" {
		t.Fatalf("leading-zero identifier was coerced: %#v", result.Fields["id"])
	}
	if result.Fields["name"] != "José 東京" || result.Fields["enabled"] != true {
		t.Fatalf("unicode or explicit type parsing failed: %#v", result.Fields)
	}
}

func TestMalformedInputAndLimits(t *testing.T) {
	t.Run("unterminated quote", func(t *testing.T) {
		_, err := mixedkv.Parse(`key="open`, mixedkv.DefaultConfig())
		assertErrorCode(t, err, mixedkv.CodeMalformedQuote)
	})
	t.Run("mismatched bracket", func(t *testing.T) {
		_, err := mixedkv.Parse(`key=[value}`, mixedkv.DefaultConfig())
		assertErrorCode(t, err, mixedkv.CodeMalformedBracket)
	})
	t.Run("dangling escape", func(t *testing.T) {
		_, err := mixedkv.Parse("key=value\\", mixedkv.DefaultConfig())
		assertErrorCode(t, err, mixedkv.CodeDanglingEscape)
	})
	t.Run("no pair", func(t *testing.T) {
		_, err := mixedkv.Parse(`not a kv record`, mixedkv.DefaultConfig())
		assertErrorCode(t, err, mixedkv.CodeNoKeyValue)
	})
	t.Run("input limit", func(t *testing.T) {
		config := mixedkv.DefaultConfig()
		config.MaxInputBytes = 4
		_, err := mixedkv.Parse(`key=value`, config)
		assertErrorCode(t, err, mixedkv.CodeInputTooLarge)
	})
	t.Run("field limit", func(t *testing.T) {
		config := mixedkv.DefaultConfig()
		config.MaxFields = 1
		_, err := mixedkv.Parse(`a=1 b=2`, config)
		assertErrorCode(t, err, mixedkv.CodeTooManyFields)
	})
	t.Run("value limit", func(t *testing.T) {
		config := mixedkv.DefaultConfig()
		config.MaxValueBytes = 3
		_, err := mixedkv.Parse(`key=value`, config)
		assertErrorCode(t, err, mixedkv.CodeValueTooLarge)
	})
	t.Run("empty rejected", func(t *testing.T) {
		config := mixedkv.DefaultConfig()
		config.RejectEmptyValues = true
		_, err := mixedkv.Parse(`key=`, config)
		assertErrorCode(t, err, mixedkv.CodeEmptyValue)
	})
	t.Run("type conversion", func(t *testing.T) {
		config := mixedkv.DefaultConfig()
		config.ValueTypes = map[string]string{"count": "integer"}
		_, err := mixedkv.Parse(`count=NaN`, config)
		assertErrorCode(t, err, mixedkv.CodeTypeConversion)
	})
}

func TestConfigValidation(t *testing.T) {
	tests := []mixedkv.Config{
		{PairSeparators: []string{":"}, AssignmentSeparators: []string{"="}, DuplicatePolicy: "merge"},
		{PairSeparators: []string{"", ":"}},
		{KeyPattern: `(`},
		{ValueTypes: map[string]string{"key": "executable"}},
		{EscapeCharacter: "xx"},
		{MaxFields: 4097},
	}
	for index, config := range tests {
		if _, err := mixedkv.Compile(config); err == nil {
			t.Errorf("case %d: expected validation error", index)
		} else {
			assertErrorCode(t, err, mixedkv.CodeInvalidConfig)
		}
	}
}

func FuzzParseNeverPanics(f *testing.F) {
	for _, seed := range []string{
		`a=1:b=2`, `message="quoted:value" status=ok`, `ip=2001:db8::1:status=ok`,
		`object={"nested":[1,2,3]}|next=yes`, `key='unterminated`, "名前=値 status=ok",
	} {
		f.Add(seed)
	}
	parser := mixedkv.MustCompile(mixedkv.DefaultConfig())
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 || strings.IndexByte(input, 0) >= 0 {
			t.Skip()
		}
		_, _ = parser.Parse(input)
	})
}

func assertEvidenceSpans(t *testing.T, input string, result mixedkv.Result) {
	t.Helper()
	if len(result.Entries) != len(result.ConsumedSpans) {
		t.Fatalf("entry/span mismatch: %d/%d", len(result.Entries), len(result.ConsumedSpans))
	}
	for index, entry := range result.Entries {
		if input[entry.KeySpan.Start:entry.KeySpan.End] != entry.SourceKey {
			t.Errorf("entry %d key span does not point to source evidence", index)
		}
		if entry.ValueSpan.Start < entry.KeySpan.End || entry.ValueSpan.End > len(input) || entry.ValueSpan.Start > entry.ValueSpan.End {
			t.Errorf("entry %d has an invalid value evidence span: %+v", index, entry.ValueSpan)
		}
		if entry.PairSpan != result.ConsumedSpans[index] {
			t.Errorf("entry %d pair span not accounted", index)
		}
	}
}

func assertErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error", code)
	}
	var parseErr *mixedkv.Error
	if !errors.As(err, &parseErr) || parseErr.Code != code {
		t.Fatalf("error=%v, want code %s", err, code)
	}
}

func BenchmarkSMTPMixedDelimiter(b *testing.B) {
	input := `id=6A291BF5048F23DE:policy=outbound:checkpoint=AUTH:rule=INOUT_action:msg=PolicyMsgInfo:eventType=PolicyMsgInfo:action=disconnect:reason=badpassword:fromhost=11.22.33.44:geo=IL:helo=Site6:auth_ok=false:auth_user=managers999@012.meg.in:auth_fail=6:auth_tries=7:score=0`
	config := mixedkv.DefaultConfig()
	config.PairSeparators = []string{":"}
	config.ValueTypes = map[string]string{
		"auth_ok": "boolean", "auth_fail": "integer", "auth_tries": "integer", "score": "integer",
	}
	parser := mixedkv.MustCompile(config)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		result, err := parser.Parse(input)
		if err != nil || len(result.Fields) != 16 {
			b.Fatalf("parse failed: fields=%d err=%v", len(result.Fields), err)
		}
	}
}
