package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	metastore "nexora.local/nli-v20/internal/store"
)

type rolloutProposalFailingStore struct{ metastore.Store }

func (s rolloutProposalFailingStore) Put(ctx context.Context, document metastore.Document) (metastore.Document, error) {
	if document.Collection == "rollout-proposals" {
		return metastore.Document{}, errors.New("simulated rollout proposal outage")
	}
	return s.Store.Put(ctx, document)
}

func legacyParserPayload(t *testing.T, id string) json.RawMessage {
	t.Helper()
	definition := map[string]any{
		"pattern_type": "regex",
		"pattern":      `^(?P<level>[A-Z]+) (?P<message>.+)$`,
		"fields": []any{
			map[string]any{"name": "level", "source": "group:level", "type": "string", "required": true},
			map[string]any{"name": "message", "source": "group:message", "type": "string", "required": true},
		},
	}
	pack, err := assuranceRuntimePack(id, "1.0.0", "legacy-author", definition, []assuranceRequest{{Raw: "INFO ready", Service: "auto"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"id": id, "name": "Legacy parser", "version": pack.Version,
		"priority": 100, "rules": pack.Rules,
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func putApprovedLegacyParser(t *testing.T, fixture *apiFixture, id string) metastore.Document {
	t.Helper()
	document, err := fixture.server.metadata.Put(context.Background(), metastore.Document{
		Collection: "parser-packs", ID: id, State: "approved",
		Payload: legacyParserPayload(t, id), CreatedBy: "legacy-author",
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestLegacyParserPublicationCreatesApprovalGatedFleetBundle(t *testing.T) {
	fixture := newAPIFixture(t)
	putApprovedLegacyParser(t, fixture, "legacy-mail")

	response, body := fixture.request(http.MethodPost, "/api/v1/parser-packs/legacy-mail/activate", map[string]any{})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("publish legacy parser: %d %s", response.StatusCode, body)
	}
	result := decodeMap(t, body)
	if result["state"] != "published" || result["approval_gate"] != "pending_approval" || result["bundle_hash"] == "" {
		t.Fatalf("legacy parser was not approval-gated: %s", body)
	}

	response, body = fixture.request(http.MethodGet, "/api/v1/parser-packs", nil)
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"status":"published"`)) {
		t.Fatalf("catalog did not expose reconciled parser: %d %s", response.StatusCode, body)
	}
	bundle, err := os.ReadFile(filepath.Join(fixture.state, "parser-assets", "bundle.json"))
	if err != nil || !bytes.Contains(bundle, []byte(`"asset_id":"legacy-mail"`)) {
		t.Fatalf("legacy parser missing from materialized bundle: %v %s", err, bundle)
	}
}

func TestLegacyRuntimeAndCatalogIDsReconcileOnlyWhenExplicit(t *testing.T) {
	fixture := newAPIFixture(t)
	pack, err := assuranceRuntimePack("runtime-mail", "1.0.0", "legacy-author", map[string]any{
		"pattern_type": "regex", "pattern": `^(?P<level>[A-Z]+) (?P<message>.+)$`,
		"fields": []any{
			map[string]any{"name": "level", "source": "group:level", "type": "string", "required": true},
			map[string]any{"name": "message", "source": "group:message", "type": "string", "required": true},
		},
	}, []assuranceRequest{{Raw: "INFO ready", Service: "auto"}})
	if err != nil {
		t.Fatal(err)
	}
	encodedPack, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"id": "mail-catalog-entry", "catalog_id": "mail-catalog-entry", "runtime_id": "runtime-mail", "name": "Mail", "runtime_pack": json.RawMessage(encodedPack)})
	doc, err := fixture.server.metadata.Put(context.Background(), metastore.Document{Collection: "parser-packs", ID: "mail-catalog-entry", State: "published", Payload: payload, CreatedBy: "legacy-author"})
	if err != nil {
		t.Fatal(err)
	}
	converted, _, err := runtimeParserPackForDocument(doc)
	if err != nil || converted.ID != "runtime-mail" {
		t.Fatalf("explicit legacy identity pair was not reconciled: parser=%#v err=%v", converted, err)
	}
	bundle, err := fixture.server.buildActiveParserBundle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundRuntimeID := false
	for _, asset := range bundle.Assets {
		if asset.AssetClass == "parser-packs" && asset.AssetID == "runtime-mail" {
			foundRuntimeID = true
		}
	}
	if !foundRuntimeID {
		t.Fatalf("fleet bundle did not use runtime parser identity: %#v", bundle.Assets)
	}
	view := parserDocumentView(doc)
	if view["state"] != "published" || view["catalog_state"] != nil {
		t.Fatalf("valid legacy pack was not presented as published: %#v", view)
	}

	payload, _ = json.Marshal(map[string]any{"id": "mail-catalog-entry", "runtime_id": "runtime-mail", "name": "Mail", "runtime_pack": json.RawMessage(encodedPack)})
	doc.Payload = payload
	if _, _, err := runtimeParserPackForDocument(doc); err == nil {
		t.Fatal("runtime/catalog alias was accepted without explicit catalog_id")
	}
}

func TestMalformedLegacyParserIsIsolatedFromControllerAndFleetBundle(t *testing.T) {
	fixture := newAPIFixture(t)
	if _, err := fixture.server.metadata.Put(context.Background(), metastore.Document{
		Collection: "parser-packs", ID: "broken-old-parser", State: "published",
		Payload: json.RawMessage(`{"id":"broken-old-parser","name":"Broken","version":"0.1.0","rules":[{"command":"sh"}]}`), CreatedBy: "legacy-import",
	}); err != nil {
		t.Fatal(err)
	}

	bundle, err := fixture.server.buildActiveParserBundle(context.Background())
	if err != nil {
		t.Fatalf("malformed legacy parser poisoned active bundle: %v", err)
	}
	for _, asset := range bundle.Assets {
		if asset.AssetID == "broken-old-parser" {
			t.Fatal("malformed legacy parser entered the runtime bundle")
		}
	}
	for _, path := range []string{"/api/v1/parser-packs", "/api/v1/parser-sync", "/api/v1/overview", "/api/v1/telemetry/stream/snapshot"} {
		response, body := fixture.request(http.MethodGet, path, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("controller route %s failed after malformed legacy parser: %d %s", path, response.StatusCode, body)
		}
		if path == "/api/v1/parser-packs" && (!bytes.Contains(body, []byte(`"status":"blocked"`)) || !bytes.Contains(body, []byte(`"state":"blocked"`)) || !bytes.Contains(body, []byte(`"catalog_state":"published"`)) || !bytes.Contains(body, []byte("legacy parser rules are incompatible"))) {
			t.Fatalf("catalog did not expose isolated parser diagnostic: %s", body)
		}
	}
	response, body := fixture.request(http.MethodPost, "/api/v1/parser-packs/broken-old-parser/repair", map[string]any{})
	if response.StatusCode != http.StatusOK || decodeMap(t, body)["state"] != "rejected" {
		t.Fatalf("blocked legacy parser did not enter editable repair state: %d %s", response.StatusCode, body)
	}
}

func TestParserPublicationRollsBackWhenProposalCannotBePersisted(t *testing.T) {
	fixture := newAPIFixture(t)
	putApprovedLegacyParser(t, fixture, "rollback-parser")
	fixture.server.metadata = rolloutProposalFailingStore{Store: fixture.server.metadata}

	response, body := fixture.request(http.MethodPost, "/api/v1/parser-packs/rollback-parser/activate", map[string]any{})
	if response.StatusCode != http.StatusServiceUnavailable || !bytes.Contains(body, []byte("was reverted")) {
		t.Fatalf("failed publication was not reported as reverted: %d %s", response.StatusCode, body)
	}
	document, err := fixture.server.metadata.Get(context.Background(), "parser-packs", "rollback-parser", 0)
	if err != nil || document.State != "approved" {
		t.Fatalf("failed publication left catalog state=%q error=%v", document.State, err)
	}
	bundle, err := os.ReadFile(filepath.Join(fixture.state, "parser-assets", "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bundle, []byte(`"asset_id":"rollback-parser"`)) {
		t.Fatalf("failed publication remained in materialized bundle: %s", bundle)
	}
}
