package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"nexora.local/nli-v20/internal/fleet"
	metastore "nexora.local/nli-v20/internal/store"
)

func TestPublishedAssurancePackIsOnlyRuntimeBundleHandoff(t *testing.T) {
	f := newAPIFixture(t)
	definition := map[string]any{"pattern_type": "regex", "pattern": `status=(?P<status>\w+)`, "fields": []any{map[string]any{"name": "status", "source": "group:status", "type": "string"}}}
	cases := []assuranceRequest{{Raw: "status=OK", Service: "mail", Desired: map[string]any{"status": "OK"}}}
	pack, err := assuranceRuntimePack("recovery-parser", "1.2.0", "author", definition, cases)
	if err != nil {
		t.Fatal(err)
	}
	packJSON, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"draft", "rejected", "approved", "published"} {
		id := "recovery-" + state
		if state == "published" {
			id = "recovery-parser"
		}
		payload := map[string]any{"id": id, "name": "Recovery " + state, "review_state": state}
		if state == "published" {
			payload["runtime_pack"] = json.RawMessage(packJSON)
		}
		encoded, _ := json.Marshal(payload)
		if _, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: "parser-packs", ID: id, State: state, Payload: encoded, CreatedBy: "author"}); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := f.server.buildActiveParserBundle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var included *parserBundleAsset
	for i := range bundle.Assets {
		if bundle.Assets[i].AssetClass != "parser-packs" {
			continue
		}
		if included != nil {
			t.Fatalf("more than one Assurance parser entered desired bundle: %#v", bundle.Assets)
		}
		included = &bundle.Assets[i]
	}
	if included == nil || included.AssetID != "recovery-parser" {
		t.Fatalf("non-published Assurance parsers entered desired bundle: %#v", bundle.Assets)
	}
	wantPayloadHash := sha256.Sum256(included.Payload)
	if included.SHA256 != hex.EncodeToString(wantPayloadHash[:]) || included.Revision != 1 {
		t.Fatalf("published parser bundle asset lost immutable payload revision/hash: %+v", included)
	}

	f.server.fleet.Register(&fleet.Node{ID: "recovery-node", Hostname: "recovery-node"})
	rollout, err := f.server.startParserBundleRollout(context.Background(), "operator", "Recovery parser rollout", bundle, []string{"recovery-node"})
	if err != nil || rollout == nil {
		t.Fatalf("start selected-client rollout: rollout=%+v err=%v", rollout, err)
	}
	var rolloutPayload map[string]any
	if json.Unmarshal(rollout.Payload, &rolloutPayload) != nil || rolloutPayload["bundle_hash"] != bundle.BundleHash {
		t.Fatalf("selected-client rollout targets a different bundle hash: %s", rollout.Payload)
	}
	var storedBundle parserAssetBundle
	rolloutBundleJSON, _ := json.Marshal(rolloutPayload["asset_bundle"])
	if err := json.Unmarshal(rolloutBundleJSON, &storedBundle); err != nil || storedBundle.BundleHash != bundle.BundleHash {
		t.Fatalf("rollout bundle does not match inventory hash: %+v err=%v", storedBundle, err)
	}

	if err := f.server.storeQuarantineRecord(context.Background(), quarantineRecordInput{ID: "recovery-q", NodeID: "recovery-node", Status: "waiting"}, false); err != nil {
		t.Fatal(err)
	}
	f.login("admin", "Admin-password-12345!")
	f.refreshCSRF()
	replayBody := map[string]any{"node_id": "recovery-node", "record_ids": []string{"recovery-q"}}
	response, body := f.request(http.MethodPost, "/api/v1/quarantine/reprocess", replayBody)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("replay must wait for target applied acknowledgement, got %d: %s", response.StatusCode, body)
	}
	stateBytes, _ := json.Marshal(map[string]any{"node_id": "recovery-node", "desired_asset_bundle_hash": "stale-desired-hash", "active_asset_bundle_hash": bundle.BundleHash, "asset_sync_status": "applied"})
	if _, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: "fleet-sync", ID: "recovery-node", State: "reported", Payload: stateBytes, CreatedBy: "node:recovery-node"}); err != nil {
		t.Fatal(err)
	}
	response, body = f.request(http.MethodPost, "/api/v1/quarantine/reprocess", replayBody)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("matching active hash with stale desired hash must remain gated, got %d: %s", response.StatusCode, body)
	}
	stateBytes, _ = json.Marshal(map[string]any{"node_id": "recovery-node", "desired_asset_bundle_hash": bundle.BundleHash, "active_asset_bundle_hash": bundle.BundleHash, "asset_sync_status": "drifted"})
	if _, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: "fleet-sync", ID: "recovery-node", State: "reported", Payload: stateBytes, CreatedBy: "node:recovery-node"}); err != nil {
		t.Fatal(err)
	}
	response, body = f.request(http.MethodPost, "/api/v1/quarantine/reprocess", replayBody)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("matching hash without applied acknowledgement must remain gated, got %d: %s", response.StatusCode, body)
	}
	stateBytes, _ = json.Marshal(map[string]any{"node_id": "recovery-node", "desired_asset_bundle_hash": bundle.BundleHash, "active_asset_bundle_hash": bundle.BundleHash, "asset_sync_status": "applied"})
	if _, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: "fleet-sync", ID: "recovery-node", State: "reported", Payload: stateBytes, CreatedBy: "node:recovery-node"}); err != nil {
		t.Fatal(err)
	}
	response, body = f.request(http.MethodPost, "/api/v1/quarantine/reprocess", replayBody)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("replay should open only after matching applied acknowledgement, got %d: %s", response.StatusCode, body)
	}
}
