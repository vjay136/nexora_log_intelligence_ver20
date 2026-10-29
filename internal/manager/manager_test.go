package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRegistryOwnsSnapshotsAndMarksStaleClientsOffline(t *testing.T) {
	r := NewRegistry()
	original := &ClientInfo{Host: "client-a", Version: "20.0.0"}
	r.Register(original)
	original.Version = "mutated-after-register"

	got := r.Get("client-a")
	if got == nil || got.Version != "20.0.0" || !got.Online {
		t.Fatalf("stored client = %#v", got)
	}
	got.Version = "mutated-after-get"
	if again := r.Get("client-a"); again.Version != "20.0.0" {
		t.Fatalf("Get exposed registry state: %#v", again)
	}

	r.mu.Lock()
	r.clients["client-a"].LastSeen = time.Now().Add(-91 * time.Second)
	r.mu.Unlock()
	if clients := r.All(); len(clients) != 1 || clients[0].Online {
		t.Fatalf("stale client snapshot = %#v", clients)
	}
}

func TestManagerRegistrationAndClientAPI(t *testing.T) {
	s := New(Config{})
	register := httptest.NewRequest(http.MethodPost, "/api/v1/register", strings.NewReader(`{"host":"client-a","pid":42,"version":"20.0.0"}`))
	registerResponse := httptest.NewRecorder()
	s.mux.ServeHTTP(registerResponse, register)
	if registerResponse.Code != http.StatusOK {
		t.Fatalf("register status = %d, body = %s", registerResponse.Code, registerResponse.Body.String())
	}

	clientsResponse := httptest.NewRecorder()
	s.mux.ServeHTTP(clientsResponse, httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil))
	var clients []ClientInfo
	if err := json.NewDecoder(clientsResponse.Body).Decode(&clients); err != nil {
		t.Fatal(err)
	}
	if len(clients) != 1 || clients[0].Host != "client-a" || !clients[0].Online {
		t.Fatalf("clients response = %#v", clients)
	}

	missingResponse := httptest.NewRecorder()
	s.mux.ServeHTTP(missingResponse, httptest.NewRequest(http.MethodGet, "/api/v1/clients/missing", nil))
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing client status = %d, want %d", missingResponse.Code, http.StatusNotFound)
	}
}

func TestRegisterRejectsWrongMethodAndInvalidJSON(t *testing.T) {
	s := New(Config{})
	for name, test := range map[string]struct {
		request *http.Request
		want    int
	}{
		"method": {httptest.NewRequest(http.MethodGet, "/api/v1/register", nil), http.StatusMethodNotAllowed},
		"json":   {httptest.NewRequest(http.MethodPost, "/api/v1/register", strings.NewReader("{")), http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		s.mux.ServeHTTP(response, test.request)
		if response.Code != test.want {
			t.Errorf("%s status = %d, want %d", name, response.Code, test.want)
		}
	}
}
