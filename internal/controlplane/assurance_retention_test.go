package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	metastore "nexora.local/nli-v20/internal/store"
)

func TestAssuranceRetentionCannotBeBypassedByCallerID(t *testing.T) {
	f := newAPIFixture(t)
	f.server.assurance.max = 1
	save := func(id string) int {
		response, _ := f.request(http.MethodPost, "/api/v1/assurance/cases", map[string]any{
			"id": id, "name": "Case", "retain": true,
			"case": map[string]any{"raw": `{"message":"sample"}`, "service": "auto"},
		})
		return response.StatusCode
	}
	if status := save("first"); status != http.StatusCreated {
		t.Fatalf("first save: %d", status)
	}
	if status := save("second"); status != http.StatusConflict {
		t.Fatalf("caller id bypassed maximum: %d", status)
	}
	if status := save("first"); status != http.StatusCreated {
		t.Fatalf("existing revision should still be allowed: %d", status)
	}
}

func TestAssuranceExpiredFixtureCannotBeReadOrDownloaded(t *testing.T) {
	for _, suffix := range []string{"", "/download"} {
		t.Run(suffix, func(t *testing.T) {
			f := newAPIFixture(t)
			f.server.assurance.manual = false
			payload, _ := json.Marshal(sealedAssuranceCase{Name: "Expired", ExpiresAt: time.Now().Add(-time.Hour)})
			_, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: "assurance-cases", ID: "expired", Payload: payload})
			if err != nil {
				t.Fatal(err)
			}
			response, _ := f.request(http.MethodGet, "/api/v1/assurance/cases/expired"+suffix, nil)
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("expired fixture status=%d", response.StatusCode)
			}
			docs, err := f.server.metadata.List(context.Background(), "assurance-cases", metastore.Query{})
			if err != nil || len(docs) != 0 {
				t.Fatalf("expired ciphertext not purged: count=%d error=%v", len(docs), err)
			}
		})
	}
}

func TestAssuranceInvalidNonceFailsWithoutPanicking(t *testing.T) {
	f := newAPIFixture(t)
	payload, _ := json.Marshal(sealedAssuranceCase{Name: "Damaged", Nonce: "AA==", Ciphertext: "AA=="})
	_, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: "assurance-cases", ID: "damaged", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	response, _ := f.request(http.MethodGet, "/api/v1/assurance/cases/damaged", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("invalid nonce status=%d", response.StatusCode)
	}
}

func TestAssuranceExpiredFixtureReclaimsCapacityDuringSave(t *testing.T) {
	f := newAPIFixture(t)
	f.server.assurance.manual = false
	f.server.assurance.max = 1
	payload, _ := json.Marshal(sealedAssuranceCase{Name: "Expired", ExpiresAt: time.Now().Add(-time.Hour)})
	_, err := f.server.metadata.Put(context.Background(), metastore.Document{Collection: "assurance-cases", ID: "expired", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	response, data := f.request(http.MethodPost, "/api/v1/assurance/cases", map[string]any{
		"name": "Fresh", "retain": true, "case": map[string]any{"raw": `{"message":"sample"}`, "service": "auto"},
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("expired fixture blocked new save: %d %s", response.StatusCode, data)
	}
}
