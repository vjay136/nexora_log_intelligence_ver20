package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/fleet"
	metastore "nexora.local/nli-v20/internal/store"
)

func TestParserSyncProposalApprovalPreservesBundleHashAndRequiresIndependentApprover(t *testing.T) {
	f := newAPIFixture(t)
	f.server.fleet.Register(&fleet.Node{ID: "parser-node", Hostname: "parser-node"})
	// Keep a decimal in the stored payload to catch float64 normalization while
	// a proposal is decoded and approved.
	assetPayload := []byte(`{"name":"decimal parser","config":{"weight":1.0}}`)
	if _, err := f.server.metadata.Put(context.Background(), metastore.Document{
		Collection: "regex-patterns", ID: "decimal-parser", State: "active", Payload: assetPayload, CreatedBy: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	bundle, err := f.server.buildActiveParserBundle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := f.server.proposeParserBundleRollout(context.Background(), "admin", "Decimal parser rollout", "test approval", "selected", bundle, []string{"parser-node"})
	if err != nil {
		t.Fatal(err)
	}

	response, body := f.request(http.MethodPost, "/api/v1/rollouts/"+proposal.ID+"/approve", map[string]any{})
	if response.StatusCode != http.StatusConflict || !strings.Contains(string(body), "creator cannot approve own rollout") {
		t.Fatalf("proposal creator approval should be rejected, got %d: %s", response.StatusCode, body)
	}

	response, body = f.request(http.MethodPost, "/api/v1/users", map[string]any{
		"username": "independent-approver", "email": "independent@example.test", "role": "admin", "password": "Independent-password-12345!",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create independent approver %d: %s", response.StatusCode, body)
	}
	f.login("independent-approver", "Independent-password-12345!")
	f.refreshCSRF()
	response, body = f.request(http.MethodPost, "/api/v1/rollouts/"+proposal.ID+"/approve", map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("independent parser sync approval %d: %s", response.StatusCode, body)
	}

	approved := decodeMap(t, body)
	payload, ok := approved["payload"].(map[string]any)
	if !ok || payload["approved_by"] != "independent-approver" || approved["state"] != "active" {
		t.Fatalf("approval attribution/state missing: %s", body)
	}
	liveID, ok := payload["live_rollout_id"].(string)
	if !ok || liveID == "" {
		t.Fatalf("approval did not create live rollout: %s", body)
	}
	live, ok := f.server.rollouts.Get(liveID)
	if !ok {
		t.Fatalf("live rollout %q not found", liveID)
	}
	var rolloutPayload struct {
		Bundle     parserAssetBundle `json:"asset_bundle"`
		BundleHash string            `json:"bundle_hash"`
	}
	if err := json.Unmarshal(live.Payload, &rolloutPayload); err != nil {
		t.Fatal(err)
	}
	if rolloutPayload.BundleHash != bundle.BundleHash || rolloutPayload.Bundle.BundleHash != bundle.BundleHash {
		t.Fatalf("live rollout bundle hash changed: payload=%s bundle=%s expected=%s", rolloutPayload.BundleHash, rolloutPayload.Bundle.BundleHash, bundle.BundleHash)
	}
	hash, _, err := bundleDigest(rolloutPayload.Bundle)
	if err != nil || hash != bundle.BundleHash {
		t.Fatalf("live rollout bundle is not verifiable: calculated=%s expected=%s err=%v", hash, bundle.BundleHash, err)
	}
	var decimalPayload json.RawMessage
	for _, asset := range rolloutPayload.Bundle.Assets {
		if asset.AssetClass == "regex-patterns" && asset.AssetID == "decimal-parser" {
			decimalPayload = asset.Payload
			break
		}
	}
	if !strings.Contains(string(decimalPayload), `"weight":1.0`) {
		t.Fatalf("live rollout did not preserve exact parser asset payload: %s", decimalPayload)
	}
}
