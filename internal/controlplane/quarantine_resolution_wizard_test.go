package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/fleet"
	metastore "nexora.local/nli-v20/internal/store"
	parserpolicy "nexora.local/nli-v20/parser-policy"
)

func TestQuarantineResolutionWizardParserRolloutRequiresExactIndependentApproval(t *testing.T) {
	f := newAPIFixture(t)
	definition := map[string]any{"pattern_type": "regex", "pattern": `status=(?P<status>\w+)`, "fields": []any{map[string]any{"name": "status", "source": "group:status", "type": "string"}}}
	pack, err := assuranceRuntimePack("qrw-rollout", "1.0.0", "author", definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	packJSON, _ := json.Marshal(pack)
	parserPayload, _ := json.Marshal(map[string]any{"id": "qrw-rollout", "name": "QRW rollout", "review_state": "published", "runtime_pack": json.RawMessage(packJSON)})
	if _, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: "parser-packs", ID: "qrw-rollout", State: "published", Payload: parserPayload, CreatedBy: "author"}); err != nil {
		t.Fatal(err)
	}
	f.server.fleet.Register(&fleet.Node{ID: "qrw-node", Hostname: "qrw-node"})
	f.login("admin", "Admin-password-12345!")
	f.refreshCSRF()
	response, data := f.request(http.MethodPost, "/api/v1/parser-sync/rollout", map[string]any{"target_mode": "origin", "origin_node_id": "qrw-node"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create origin rollout proposal %d: %s", response.StatusCode, data)
	}
	proposal := decodeMap(t, data)["proposal"].(map[string]any)
	proposalID := proposal["id"].(string)
	proposalPayload := proposal["payload"].(map[string]any)
	bundleHash := proposalPayload["bundle_hash"]
	if proposal["state"] != "pending_approval" {
		t.Fatalf("parser rollout bypassed its approval gate: %#v", proposal)
	}
	response, data = f.request(http.MethodPost, "/api/v1/rollouts/"+proposalID+"/approve", map[string]any{})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("rollout author approved their own parser rollout: %d %s", response.StatusCode, data)
	}
	if _, err := f.server.auth.CreateUser("admin", "independent-rollout-reviewer", "rollout-reviewer@example.test", "admin", "Rollout-reviewer-password-12345!"); err != nil {
		t.Fatal(err)
	}
	f.login("independent-rollout-reviewer", "Rollout-reviewer-password-12345!")
	f.refreshCSRF()
	response, data = f.request(http.MethodPost, "/api/v1/rollouts/"+proposalID+"/approve", map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("independent parser rollout approval %d: %s", response.StatusCode, data)
	}
	var syncState map[string]any
	doc, err := f.server.metadata.Get(context.Background(), "fleet-sync", "qrw-node", 0)
	if err != nil || json.Unmarshal(doc.Payload, &syncState) != nil || syncState["desired_asset_bundle_hash"] != bundleHash {
		t.Fatalf("approved exact bundle was not marked desired for origin client: state=%#v err=%v", syncState, err)
	}
	command, ok := f.server.rollouts.NextForNode("qrw-node", 0)
	if !ok || command.Type != rolloutTypeParserSync {
		t.Fatalf("approved parser bundle did not reach client command queue: command=%+v ok=%t", command, ok)
	}
	var commandPayload map[string]any
	if json.Unmarshal(command.Payload, &commandPayload) != nil || commandPayload["bundle_hash"] != bundleHash {
		t.Fatalf("approved command changed exact parser bundle: %s", command.Payload)
	}
}

func TestQuarantineResolutionWizardParserRolloutTargetModesAndStaleProposal(t *testing.T) {
	f := newAPIFixture(t)
	pack, err := assuranceRuntimePack("qrw-targets", "1.0.0", "author", map[string]any{
		"pattern_type": "regex", "pattern": `status=(?P<status>\w+)`,
		"fields": []any{map[string]any{"name": "status", "source": "group:status", "type": "string"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	packJSON, _ := json.Marshal(pack)
	parserPayload, _ := json.Marshal(map[string]any{"id": "qrw-targets", "name": "QRW targets", "review_state": "published", "runtime_pack": json.RawMessage(packJSON)})
	if _, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: "parser-packs", ID: "qrw-targets", State: "published", Payload: parserPayload, CreatedBy: "author"}); err != nil {
		t.Fatal(err)
	}
	f.server.fleet.Register(&fleet.Node{ID: "qrw-target-a", Hostname: "qrw-target-a"})
	f.server.fleet.Register(&fleet.Node{ID: "qrw-target-b", Hostname: "qrw-target-b"})
	f.login("admin", "Admin-password-12345!")
	f.refreshCSRF()
	response, data := f.request(http.MethodPost, "/api/v1/parser-sync/rollout", map[string]any{"target_mode": "selected", "node_ids": []string{"qrw-target-a"}})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("selected rollout proposal %d: %s", response.StatusCode, data)
	}
	selected := decodeMap(t, data)["proposal"].(map[string]any)
	selectedTargets := selected["payload"].(map[string]any)["target_nodes"].([]any)
	if len(selectedTargets) != 1 || selectedTargets[0] != "qrw-target-a" {
		t.Fatalf("selected target set changed: %#v", selectedTargets)
	}
	response, data = f.request(http.MethodPost, "/api/v1/parser-sync/rollout", map[string]any{"target_mode": "all_eligible"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("all-eligible rollout proposal %d: %s", response.StatusCode, data)
	}
	all := decodeMap(t, data)["proposal"].(map[string]any)
	allTargets := all["payload"].(map[string]any)["target_nodes"].([]any)
	if len(allTargets) != 2 {
		t.Fatalf("all-eligible target set omitted an online client: %#v", allTargets)
	}
	response, data = f.request(http.MethodPost, "/api/v1/parser-sync/rollout", map[string]any{"target_mode": "selected", "node_ids": []string{"missing-node"}})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("unknown selected node was accepted: %d %s", response.StatusCode, data)
	}
	var changed map[string]any
	if json.Unmarshal(parserPayload, &changed) != nil {
		t.Fatal("decode seeded parser payload")
	}
	changed["name"] = "QRW targets changed after proposal"
	changedPayload, _ := json.Marshal(changed)
	if _, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: "parser-packs", ID: "qrw-targets", State: "published", Payload: changedPayload, CreatedBy: "author"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.auth.CreateUser("admin", "stale-proposal-reviewer", "stale-reviewer@example.test", "admin", "Stale-reviewer-password-12345!"); err != nil {
		t.Fatal(err)
	}
	f.login("stale-proposal-reviewer", "Stale-reviewer-password-12345!")
	f.refreshCSRF()
	response, data = f.request(http.MethodPost, "/api/v1/rollouts/"+selected["id"].(string)+"/approve", map[string]any{})
	if response.StatusCode != http.StatusConflict || !strings.Contains(string(data), "stale") {
		t.Fatalf("stale parser bundle proposal was approved: %d %s", response.StatusCode, data)
	}
}

func TestQuarantineResolutionWizardFixtureCompilesToExistingRuntime(t *testing.T) {
	const raw = "20250812 130035122+0100 aaa-gmailrgo009 mail 3609 0 140293768701696 Note;systemlogs_for_data"
	definition := map[string]any{
		"pattern_type": "regex",
		"pattern":      `^(?P<date>\S+)\s+(?P<time_timezone>\S+)\s+(?P<hostname>\S+)\s+(?P<service>\S+)\s+(?P<pid>\S+)\s+(?P<status>\S+)\s+(?P<minor_pid>\S+)\s+(?P<event_type>[^;\s]+);(?P<event_name>\S+)$`,
		"fields": []any{
			map[string]any{"name": "date", "source": "group:date", "type": "string"},
			map[string]any{"name": "time_timezone", "source": "group:time_timezone", "type": "string"},
			map[string]any{"name": "hostname", "source": "group:hostname", "type": "string"},
			map[string]any{"name": "service", "source": "group:service", "type": "string"},
			map[string]any{"name": "pid", "source": "group:pid", "type": "int"},
			map[string]any{"name": "status", "source": "group:status", "type": "int"},
			map[string]any{"name": "minor_pid", "source": "group:minor_pid", "type": "int"},
			map[string]any{"name": "event_type", "source": "group:event_type", "type": "string"},
			map[string]any{"name": "event_name", "source": "group:event_name", "type": "string"},
		},
	}
	pack, err := assuranceRuntimePack("qrw-mail", "0.1.0", "operator", definition, []assuranceRequest{{Raw: raw, Service: "mail"}})
	if err != nil {
		t.Fatalf("compile wizard candidate through existing runtime: %v", err)
	}
	executor, err := parserpolicy.NewParserPackExecutor(pack)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(&parserpolicy.ExecutionRequest{Input: raw, Service: "mail", NodeID: "node-1", Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Quarantined {
		t.Fatalf("verified wizard fixture was quarantined: %+v", result)
	}
	wants := map[string]any{"date": "20250812", "time_timezone": "130035122+0100", "hostname": "aaa-gmailrgo009", "service": "mail", "pid": 3609, "status": 0, "minor_pid": 140293768701696, "event_type": "Note", "event_name": "systemlogs_for_data"}
	for key, want := range wants {
		got, ok := result.EventPayload.Get(key)
		if !ok || got != want {
			t.Fatalf("field %s = %#v present=%t, want %#v", key, got, ok, want)
		}
	}
}

func TestQuarantineResolutionWizardRejectsResourceExceedingCandidate(t *testing.T) {
	definition := map[string]any{
		"pattern_type": "regex",
		"pattern":      strings.Repeat("a", parserpolicy.MaxRegexLength+1),
		"fields":       []any{map[string]any{"name": "message", "source": "group:message", "type": "string"}},
	}
	if _, err := assuranceRuntimePack("unsafe-qrw", "0.1.0", "operator", definition, []assuranceRequest{{Raw: "a", Service: "mail"}}); err == nil {
		t.Fatal("resource-exceeding wizard candidate compiled into the runtime")
	}
}

func TestQuarantineResolutionWizardPreservesRuntimePresenceAndValidation(t *testing.T) {
	definition := map[string]any{
		"pattern_type": "regex",
		"pattern":      `^(?P<level>\w+)(?:\s+(?P<count>\d+))?$`,
		"fields": []any{
			map[string]any{"name": "level", "source": "group:level", "type": "string", "required": true, "validation": "one_of=INFO|WARN"},
			map[string]any{"name": "count", "source": "group:count", "type": "int", "required": false, "validation": "min=0,max=10"},
		},
	}
	pack, err := assuranceRuntimePack("qrw-schema", "0.1.0", "operator", definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	level := pack.Rules[0].FieldSchema["level"]
	count := pack.Rules[0].FieldSchema["count"]
	if !level.Required || count.Required {
		t.Fatalf("presence policy changed during runtime conversion: level=%+v count=%+v", level, count)
	}
	if len(level.Enum) != 2 || level.Enum[0] != "INFO" || level.Enum[1] != "WARN" {
		t.Fatalf("enum validation changed during runtime conversion: %+v", level.Enum)
	}
	if count.Range == nil || count.Range.Min == nil || count.Range.Max == nil || *count.Range.Min != 0 || *count.Range.Max != 10 {
		t.Fatalf("range validation changed during runtime conversion: %+v", count.Range)
	}

	executor, err := parserpolicy.NewParserPackExecutor(pack)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"INFO", "WARN 10"} {
		result, runErr := executor.Execute(&parserpolicy.ExecutionRequest{Input: input, Service: "mail", NodeID: "node-1", Context: context.Background()})
		if runErr != nil || result.Quarantined {
			t.Fatalf("valid wizard input %q rejected: result=%+v err=%v", input, result, runErr)
		}
	}
	for _, input := range []string{"DEBUG 5", "INFO 11"} {
		result, runErr := executor.Execute(&parserpolicy.ExecutionRequest{Input: input, Service: "mail", NodeID: "node-1", Context: context.Background()})
		if runErr != nil {
			t.Fatalf("execute invalid wizard input %q: %v", input, runErr)
		}
		if !result.Quarantined {
			t.Fatalf("invalid wizard input %q bypassed runtime schema", input)
		}
	}
}

func TestQuarantineResolutionWizardRejectsPreviewOnlyValidation(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		fieldType  string
		validation string
	}{
		{name: "unsupported", fieldType: "string", validation: "min_length=2"},
		{name: "non-finite", fieldType: "float", validation: "min=NaN"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			definition := map[string]any{
				"pattern_type": "regex",
				"pattern":      `^(?P<message>.*)$`,
				"fields": []any{
					map[string]any{"name": "message", "source": "group:message", "type": testCase.fieldType, "validation": testCase.validation},
				},
			}
			if _, err := assuranceRuntimePack("qrw-preview-only", "0.1.0", "operator", definition, nil); err == nil {
				t.Fatalf("unsafe validation %q compiled into the runtime", testCase.validation)
			}
		})
	}
}
