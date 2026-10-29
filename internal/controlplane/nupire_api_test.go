package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/nupire"
)

func heldOutIDs() []string {
	ids := make([]string, 10)
	for i := range ids {
		ids[i] = "held-" + string(rune('a'+i))
	}
	return ids
}

func createNUPIRECandidate(t *testing.T, f *apiFixture) {
	t.Helper()
	response, body := f.request(http.MethodPost, "/api/v1/nupire/discoveries", map[string]any{"id": "candidate-a", "source": "assurance", "generator": "operator", "training_ids": []string{"train-1"}, "held_out_ids": heldOutIDs()})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create discovery %d: %s", response.StatusCode, body)
	}
	response, body = f.request(http.MethodPost, "/api/v1/nupire/candidates/candidate-a/artifact", map[string]any{"artifact": map[string]any{"kind": "key_value", "version": 1}})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("submit artifact %d: %s", response.StatusCode, body)
	}
}

func TestNUPIREControllerCreateArtifactEvaluateAndRead(t *testing.T) {
	f := newAPIFixture(t)
	createNUPIRECandidate(t, f)
	response, body := f.request(http.MethodPost, "/api/v1/nupire/candidates/candidate-a/evaluation", map[string]any{"metrics": nupire.Metrics{Samples: 10, ExactMatches: 10, FieldPrecision: 1, FieldRecall: 1, ErrorRate: 0}, "held_out_ids": heldOutIDs()})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("evaluate %d: %s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state":"evaluated"`) || !strings.Contains(string(body), `"samples":10`) {
		t.Fatalf("evaluation evidence missing: %s", body)
	}
	response, body = f.request(http.MethodGet, "/api/v1/nupire/snapshot", nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"count":1`) {
		t.Fatalf("snapshot %d: %s", response.StatusCode, body)
	}
	response, body = f.request(http.MethodGet, "/api/v1/nupire/candidates/candidate-a", nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"held_out_ids"`) || !strings.Contains(string(body), `"digest_valid":true`) {
		t.Fatalf("detail %d: %s", response.StatusCode, body)
	}
}

func TestNUPIREControllerRejectsEvidenceAndUnsafeArtifacts(t *testing.T) {
	f := newAPIFixture(t)
	createNUPIRECandidate(t, f)
	response, body := f.request(http.MethodPost, "/api/v1/nupire/candidates/candidate-a/evaluation", map[string]any{"metrics": nupire.Metrics{Samples: 10, ExactMatches: 10, FieldPrecision: 1, FieldRecall: 1, ErrorRate: 0}, "held_out_ids": []string{"wrong-evidence"}})
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid evidence status %d: %s", response.StatusCode, body)
	}
	response, body = f.request(http.MethodPost, "/api/v1/nupire/candidates/candidate-a/artifact", map[string]any{"artifact": map[string]any{"script": "unsafe"}})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsafe artifact status %d: %s", response.StatusCode, body)
	}
}

func TestNUPIREControllerEnforcesMethodAndAuthentication(t *testing.T) {
	f := newAPIFixture(t)
	response, body := f.request(http.MethodGet, "/api/v1/nupire/discoveries", nil)
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("method status %d: %s", response.StatusCode, body)
	}
	req, err := http.NewRequest(http.MethodPost, f.http.URL+"/api/v1/nupire/discoveries", strings.NewReader(`{"id":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	unauthenticated := &http.Client{}
	response, err = unauthenticated.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create status %d", response.StatusCode)
	}
}

func TestNUPIRERejectsInvalidLifecycleTransition(t *testing.T) {
	f := newAPIFixture(t)
	createNUPIRECandidate(t, f)
	response, body := f.request(http.MethodPost, "/api/v1/nupire/candidates/candidate-a/actions/promote", map[string]any{})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("transition status %d: %s", response.StatusCode, body)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
}
