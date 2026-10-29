package controlplane

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	parserpolicy "nexora.local/nli-v20/parser-policy"
)

const controlplaneTestAlloy = `
loki.process "smtp" {
  stage.regex {
    expression = "^(?P<level>[A-Z]+) (?P<message>.*)$"
  }
  stage.metrics {
    mode = "counter"
  }
}
`

const controlplaneTestVector = `
[sources.smtp]
type = "file"
include = ["/var/log/smtp.log"]

[transforms.parse_smtp]
type = "remap"
inputs = ["smtp"]
source = '''
pattern = r'^(?P<level>[A-Z]+) (?P<message>.*)$'
parsed, err = parse_regex(.message, pattern)
.port = to_int(.port) ?? 0
.message = replace(.message, r'secret', "[redacted]")
del(.debug)
'''
`

func migrationResponsePayload(t *testing.T, data []byte) map[string]any {
	t.Helper()
	envelope := decodeMap(t, data)
	payload, ok := envelope["payload"].(map[string]any)
	if !ok {
		t.Fatalf("migration response lacks payload: %s", data)
	}
	return payload
}

func migrationSlice(t *testing.T, value any, name string) []any {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("%s is not an array: %#v", name, value)
	}
	return items
}

func TestMigrationAPIRealPipelineDraftAndNoRetention(t *testing.T) {
	f := newAPIFixture(t)
	sourceMarker := "MIGRATION_UPLOAD_SOURCE_MARKER_9137"
	rawMarker := "MIGRATION_REGRESSION_RAW_MARKER_2468"
	source := strings.Replace(controlplaneTestAlloy, "stage.regex {", "// "+sourceMarker+"\n  stage.regex {", 1)
	request := map[string]any{
		"filename":     "smtp.alloy",
		"source_type":  "alloy",
		"content":      source,
		"create_draft": true,
		"regression_candidate": map[string]any{
			"name": "smtp-candidate", "raw": "INFO " + rawMarker,
			"expected": map[string]any{"level": "INFO", "message": rawMarker},
			"service":  "smtp", "expected_parser": "*", "expected_route": "normal",
		},
	}
	response, data := f.request("POST", "/api/v1/migration/import", request)
	if response.StatusCode != 201 {
		t.Fatalf("migration status=%d: %s", response.StatusCode, data)
	}
	payload := migrationResponsePayload(t, data)
	if payload["source_retained"] != false || payload["raw_payload_retained"] != false {
		t.Fatalf("retention boundary missing: %#v", payload)
	}
	if payload["manual_approval_required"] != true || payload["auto_activated"] != false {
		t.Fatalf("approval gate missing: %#v", payload)
	}
	if len(migrationSlice(t, payload["supported"], "supported")) == 0 {
		t.Fatalf("real translated operations were not reported: %#v", payload)
	}
	unsupported := migrationSlice(t, payload["unsupported"], "unsupported")
	if len(unsupported) == 0 || unsupported[0].(map[string]any)["type"] != "stage.metrics" {
		t.Fatalf("unsupported construct was not explicit: %#v", unsupported)
	}
	results := migrationSlice(t, payload["results"], "results")
	result := results[0].(map[string]any)
	if result["parsed"] != true || result["success"] != true {
		t.Fatalf("real Alloy translation failed: %#v", result)
	}
	if len(migrationSlice(t, result["operations"], "operations")) == 0 || len(migrationSlice(t, result["line_mappings"], "line_mappings")) == 0 {
		t.Fatalf("translation provenance missing: %#v", result)
	}
	candidate := payload["candidate_assessment"].(map[string]any)
	if candidate["retained"] != false || candidate["parity_status"] != "passed" {
		t.Fatalf("candidate assessment=%#v", candidate)
	}

	draftID, ok := payload["draft_id"].(string)
	if !ok || draftID == "" {
		t.Fatalf("draft id missing: %#v", payload)
	}
	response, data = f.request("GET", "/api/v1/parser-packs/"+draftID, nil)
	if response.StatusCode != 200 {
		t.Fatalf("draft lookup status=%d: %s", response.StatusCode, data)
	}
	draft := decodeMap(t, data)
	if draft["state"] != "draft" {
		t.Fatalf("import auto-activated draft: %#v", draft)
	}
	draftPayload := draft["payload"].(map[string]any)
	if draftPayload["review_state"] != "draft" {
		t.Fatalf("parser pack review state=%#v", draftPayload["review_state"])
	}
	if _, exists := draftPayload["activated_at"]; exists {
		t.Fatalf("draft has activation timestamp: %#v", draftPayload["activated_at"])
	}
	if regressions, exists := draftPayload["regression_cases"]; exists && regressions != nil && len(migrationSlice(t, regressions, "regression_cases")) != 0 {
		t.Fatalf("raw regression was retained: %#v", regressions)
	}

	for _, forbidden := range [][]byte{[]byte(source), []byte(sourceMarker), []byte(rawMarker)} {
		err := filepath.Walk(f.state, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return walkErr
			}
			stored, readErr := os.ReadFile(path)
			if readErr == nil && bytes.Contains(stored, forbidden) {
				t.Errorf("forbidden migration source or candidate retained in %s", path)
			}
			return readErr
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationAPIVectorAndDeterministicReportHashes(t *testing.T) {
	f := newAPIFixture(t)
	request := map[string]any{
		"filename": "smtp.toml", "source_type": "vector", "content": controlplaneTestVector, "create_draft": false,
	}
	response, firstData := f.request("POST", "/api/v1/migration/import", request)
	if response.StatusCode != 201 {
		t.Fatalf("first migration status=%d: %s", response.StatusCode, firstData)
	}
	response, secondData := f.request("POST", "/api/v1/migration/import", request)
	if response.StatusCode != 201 {
		t.Fatalf("second migration status=%d: %s", response.StatusCode, secondData)
	}
	firstEnvelope := decodeMap(t, firstData)
	secondEnvelope := decodeMap(t, secondData)
	first := firstEnvelope["payload"].(map[string]any)
	second := secondEnvelope["payload"].(map[string]any)
	if first["report_hash"] != second["report_hash"] || firstEnvelope["sha256"] != secondEnvelope["sha256"] {
		t.Fatalf("identical imports were not deterministic: first=%#v second=%#v", firstEnvelope, secondEnvelope)
	}
	if firstEnvelope["id"] != secondEnvelope["id"] || secondEnvelope["revision"].(float64) != 2 {
		t.Fatalf("deterministic report was not revisioned in place: first=%#v second=%#v", firstEnvelope, secondEnvelope)
	}
	results := migrationSlice(t, first["results"], "results")
	if results[0].(map[string]any)["success"] != true {
		t.Fatalf("Vector translation failed: %#v", results[0])
	}
	if _, exists := first["draft_id"]; exists {
		t.Fatalf("create_draft=false persisted a draft id: %#v", first["draft_id"])
	}
	response, data := f.request("GET", "/api/v1/parser-packs", nil)
	if response.StatusCode != 200 || decodeMap(t, data)["count"].(float64) != 0 {
		t.Fatalf("create_draft=false created parser pack: %d %s", response.StatusCode, data)
	}
}

func TestMigrationAPIStrictNLIParserPackValidation(t *testing.T) {
	f := newAPIFixture(t)
	valid := controlplaneNLIParserPack(t)
	response, data := f.request("POST", "/api/v1/migration/import", map[string]any{
		"filename": "mail-pack.json", "source_type": "nli", "content": string(valid), "create_draft": true,
	})
	if response.StatusCode != 201 {
		t.Fatalf("valid NLI migration status=%d: %s", response.StatusCode, data)
	}
	payload := migrationResponsePayload(t, data)
	if payload["source_type"] != "nli" || len(migrationSlice(t, payload["generated_packs"], "generated_packs")) != 1 {
		t.Fatalf("valid NLI pack not imported: %#v", payload)
	}
	draftID := payload["draft_id"].(string)
	response, data = f.request("GET", "/api/v1/parser-packs/"+draftID, nil)
	if response.StatusCode != 200 {
		t.Fatalf("NLI draft lookup status=%d: %s", response.StatusCode, data)
	}
	draftPayload := decodeMap(t, data)["payload"].(map[string]any)
	if draftPayload["source_type"] != "imported" || draftPayload["review_state"] != "draft" {
		t.Fatalf("NLI pack was not normalized to an imported draft: %#v", draftPayload)
	}

	var invalid map[string]any
	if err := json.Unmarshal(valid, &invalid); err != nil {
		t.Fatal(err)
	}
	invalid["unknown_executable_field"] = "must be rejected by strict loading"
	invalidBytes, _ := json.Marshal(invalid)
	response, data = f.request("POST", "/api/v1/migration/import", map[string]any{
		"filename": "invalid-pack.json", "source_type": "nli", "content": string(invalidBytes), "create_draft": true,
	})
	if response.StatusCode != 201 {
		t.Fatalf("invalid NLI compatibility report status=%d: %s", response.StatusCode, data)
	}
	invalidReport := migrationResponsePayload(t, data)
	if invalidReport["parity_status"] != "failed" || len(migrationSlice(t, invalidReport["generated_packs"], "generated_packs")) != 0 {
		t.Fatalf("invalid NLI pack produced a draft: %#v", invalidReport)
	}
}

func TestMigrationAPIZIPImportAndArchiveGuards(t *testing.T) {
	f := newAPIFixture(t)
	validArchive := controlplaneMigrationZIP(t,
		migrationZIPEntry{name: "pipelines/smtp.alloy", content: []byte(controlplaneTestAlloy)},
		migrationZIPEntry{name: "packs/mail.json", content: controlplaneNLIParserPack(t)},
	)
	response, data := f.request("POST", "/api/v1/migration/import", map[string]any{
		"filename": "migration.zip", "source_type": "zip", "content_base64": base64.StdEncoding.EncodeToString(validArchive), "create_draft": false,
	})
	if response.StatusCode != 201 {
		t.Fatalf("valid ZIP migration status=%d: %s", response.StatusCode, data)
	}
	payload := migrationResponsePayload(t, data)
	compatibility := payload["compatibility"].(map[string]any)
	if compatibility["translated_files"].(float64) != 2 || len(migrationSlice(t, payload["archive_entries"], "archive_entries")) != 2 {
		t.Fatalf("ZIP sources were not translated and hash-scanned: %#v", payload)
	}

	traversal := controlplaneMigrationZIP(t, migrationZIPEntry{name: "../escape.alloy", content: []byte(controlplaneTestAlloy)})
	symlink := controlplaneMigrationZIP(t, migrationZIPEntry{name: "link.alloy", content: []byte("target"), mode: os.ModeSymlink | 0o777})
	encrypted := markControlplaneZIPEncrypted(t, controlplaneMigrationZIP(t, migrationZIPEntry{name: "encrypted.alloy", content: []byte(controlplaneTestAlloy)}))
	bomb := controlplaneMigrationZIP(t, migrationZIPEntry{name: "bomb.alloy", content: bytes.Repeat([]byte("A"), int(migrationCompressionThreshold)+1)})
	for name, archive := range map[string][]byte{
		"traversal": traversal,
		"symlink":   symlink,
		"encrypted": encrypted,
		"zip-bomb":  bomb,
	} {
		t.Run(name, func(t *testing.T) {
			response, data := f.request("POST", "/api/v1/migration/import", map[string]any{
				"filename": name + ".zip", "source_type": "zip", "content_base64": base64.StdEncoding.EncodeToString(archive),
			})
			if response.StatusCode != 400 {
				t.Fatalf("guard status=%d: %s", response.StatusCode, data)
			}
		})
	}
}

func TestMigrationAPIUploadLimit(t *testing.T) {
	f := newAPIFixture(t)
	response, data := f.request("POST", "/api/v1/migration/import", map[string]any{
		"filename": "oversized.alloy", "source_type": "alloy", "content": strings.Repeat("x", int(migrationMaxSourceBytes)+1),
	})
	if response.StatusCode != 413 {
		t.Fatalf("oversized source status=%d: %s", response.StatusCode, data)
	}
}

func controlplaneNLIParserPack(t *testing.T) []byte {
	t.Helper()
	pack := &parserpolicy.ParserPack{
		ID: "uploaded_mail", Version: "5.0.0", Revision: 3, SourceType: "manual", Author: "fixture",
		ReviewState: parserpolicy.ReviewStateDraft, CreatedAt: time.Unix(0, 0).UTC(),
		Rules: []parserpolicy.ParserRule{{
			ID: "mail", Name: "Mail", Priority: 10, ServiceSelectors: []string{"smtp"},
			Stages: []parserpolicy.Stage{{
				ID: "capture", Name: "Capture", Type: parserpolicy.StageTypeRegex,
				Config:        map[string]any{"pattern": `^(?P<level>[A-Z]+)$`, "groups": map[string]string{"level": "level"}},
				FailurePolicy: parserpolicy.FailurePolicyRejectRule,
			}},
			FailurePolicy: parserpolicy.FailurePolicyRejectRule, ProjectionMode: parserpolicy.ProjectionModeAll,
			CountPolicy: parserpolicy.CountPolicyAllowDerived,
		}},
		ResourceLimits: parserpolicy.DefaultResourceLimits(),
	}
	if err := parserpolicy.SealParserPack(pack); err != nil {
		t.Fatal(err)
	}
	if validation := parserpolicy.ValidateParserPack(pack); !validation.Valid {
		t.Fatalf("fixture pack invalid: %v", validation.Errors)
	}
	encoded, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

type migrationZIPEntry struct {
	name    string
	content []byte
	mode    os.FileMode
}

func controlplaneMigrationZIP(t *testing.T, entries ...migrationZIPEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func markControlplaneZIPEncrypted(t *testing.T, input []byte) []byte {
	t.Helper()
	output := append([]byte(nil), input...)
	localFound, centralFound := false, false
	for offset := 0; offset+10 <= len(output); offset++ {
		switch binary.LittleEndian.Uint32(output[offset : offset+4]) {
		case 0x04034b50:
			flags := binary.LittleEndian.Uint16(output[offset+6 : offset+8])
			binary.LittleEndian.PutUint16(output[offset+6:offset+8], flags|0x1)
			localFound = true
		case 0x02014b50:
			flags := binary.LittleEndian.Uint16(output[offset+8 : offset+10])
			binary.LittleEndian.PutUint16(output[offset+8:offset+10], flags|0x1)
			centralFound = true
		}
	}
	if !localFound || !centralFound {
		t.Fatal("ZIP headers not found")
	}
	return output
}
