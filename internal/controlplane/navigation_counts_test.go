package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	metastore "nexora.local/nli-v20/internal/store"
)

type navigationFailingStore struct {
	metastore.Store
	failCollection string
}

func (s navigationFailingStore) List(ctx context.Context, collection string, query metastore.Query) ([]metastore.Document, error) {
	if collection == s.failCollection {
		return nil, errors.New("temporary store failure")
	}
	return s.Store.List(ctx, collection, query)
}

func putNavigationCountDocument(t *testing.T, store metastore.Store, collection, id, state string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"id": id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), metastore.Document{Collection: collection, ID: id, State: state, Payload: payload, CreatedBy: "test"}); err != nil {
		t.Fatalf("put %s/%s: %v", collection, id, err)
	}
}

func navigationPageCount(t *testing.T, body []byte, page string) float64 {
	t.Helper()
	root := decodeMap(t, body)
	counts, ok := root["counts"].(map[string]any)
	if !ok {
		t.Fatalf("navigation counts missing: %s", body)
	}
	entry, ok := counts[page].(map[string]any)
	if !ok {
		t.Fatalf("navigation count %q missing: %s", page, body)
	}
	return entry["total"].(float64)
}

func TestNavigationCountsContainOnlyCurrentOperatorActions(t *testing.T) {
	f := newAPIFixture(t)
	putNavigationCountDocument(t, f.server.metadata, "parser-packs", "parser-action", "draft")
	putNavigationCountDocument(t, f.server.metadata, "parser-packs", "parser-complete", "active")
	putNavigationCountDocument(t, f.server.metadata, "input-paths", "input-action", "in_review")
	putNavigationCountDocument(t, f.server.metadata, "rollout-proposals", "rollout-action", "pending_approval")
	putNavigationCountDocument(t, f.server.metadata, "rollout-proposals", "rollout-complete", "approved")
	putNavigationCountDocument(t, f.server.metadata, "assurance-runs", "run-action", "queued")
	putNavigationCountDocument(t, f.server.metadata, "assurance-runs", "run-complete", "passed")
	putNavigationCountDocument(t, f.server.quarantine.store, quarantineRecordsCollection, "quarantine-action", "waiting")
	putNavigationCountDocument(t, f.server.quarantine.store, quarantineRecordsCollection, "quarantine-complete", "resolved")

	response, body := f.request(http.MethodGet, "/api/v1/navigation/counts", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("navigation counts: %d %s", response.StatusCode, body)
	}
	for page, want := range map[string]float64{
		"parsers": 1, "parser-workspace": 1, "input-paths": 1, "rollouts": 1,
		"assurance": 1, "quarantine-wizard": 1, "unknowns": 1, "upes": 1,
	} {
		if got := navigationPageCount(t, body, page); got != want {
			t.Errorf("%s count = %v, want %v; body=%s", page, got, want, body)
		}
	}

	putNavigationCountDocument(t, f.server.metadata, "parser-packs", "parser-action", "active")
	putNavigationCountDocument(t, f.server.metadata, "input-paths", "input-action", "active")
	putNavigationCountDocument(t, f.server.metadata, "rollout-proposals", "rollout-action", "approved")
	putNavigationCountDocument(t, f.server.metadata, "assurance-runs", "run-action", "passed")
	putNavigationCountDocument(t, f.server.quarantine.store, quarantineRecordsCollection, "quarantine-action", "resolved")

	response, body = f.request(http.MethodGet, "/api/v1/navigation/counts", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("navigation counts after resolution: %d %s", response.StatusCode, body)
	}
	for _, page := range []string{"parsers", "parser-workspace", "input-paths", "rollouts", "assurance", "quarantine-wizard", "unknowns", "upes"} {
		if got := navigationPageCount(t, body, page); got != 0 {
			t.Errorf("%s count remained %v after resolution; body=%s", page, got, body)
		}
	}
}

func TestNavigationCountsRejectNonGET(t *testing.T) {
	f := newAPIFixture(t)
	response, _ := f.request(http.MethodPost, "/api/v1/navigation/counts", map[string]any{})
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST navigation counts status = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestNavigationCountsRemainAvailableWhenOneCollectionFails(t *testing.T) {
	f := newAPIFixture(t)
	putNavigationCountDocument(t, f.server.metadata, "rollout-proposals", "rollout-action", "pending_approval")
	f.server.metadata = navigationFailingStore{Store: f.server.metadata, failCollection: "parser-packs"}

	response, body := f.request(http.MethodGet, "/api/v1/navigation/counts", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("navigation counts should degrade per collection: %d %s", response.StatusCode, body)
	}
	root := decodeMap(t, body)
	counts := root["counts"].(map[string]any)
	parserCount := counts["parsers"].(map[string]any)
	if available, _ := parserCount["available"].(bool); available {
		t.Fatalf("failed parser count reported available: %s", body)
	}
	if got := navigationPageCount(t, body, "rollouts"); got != 1 {
		t.Fatalf("independent rollout count = %v, want 1; body=%s", got, body)
	}
}
