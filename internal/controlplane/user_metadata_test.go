package controlplane

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/store"
)

type supabaseModeStore struct {
	store.Store
	failUsers bool
}

func (s *supabaseModeStore) Mode() string { return "supabase" }
func (s *supabaseModeStore) Put(ctx context.Context, doc store.Document) (store.Document, error) {
	if s.failUsers && doc.Collection == "users" {
		return store.Document{}, errors.New("simulated Supabase outage")
	}
	return s.Store.Put(ctx, doc)
}

func TestUserMetadataMirrorsWithoutCredentials(t *testing.T) {
	f := newAPIFixture(t)
	wrapped := &supabaseModeStore{Store: f.server.metadata}
	f.server.metadata = wrapped
	response, data := f.request(http.MethodPost, "/api/v1/users", map[string]any{"username": "mirror-user", "email": "mirror@example.test", "role": "viewer", "password": "Mirror-password-12345!"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %s", response.StatusCode, data)
	}
	docs, err := wrapped.List(context.Background(), "users", store.Query{Limit: 10})
	if err != nil || len(docs) != 1 {
		t.Fatalf("metadata docs=%d err=%v", len(docs), err)
	}
	body := string(docs[0].Payload)
	for _, forbidden := range []string{"password_hash", "salt", "jwt_secret", "Mirror-password"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("credential material mirrored: %s", forbidden)
		}
	}
	for _, required := range []string{"mirror-user", "mirror@example.test", "viewer", "active"} {
		if !strings.Contains(body, required) {
			t.Fatalf("user metadata missing %q: %s", required, body)
		}
	}
}

func TestUserCreateReportsSupabaseMirrorFailure(t *testing.T) {
	f := newAPIFixture(t)
	wrapped := &supabaseModeStore{Store: f.server.metadata, failUsers: true}
	f.server.metadata = wrapped
	response, data := f.request(http.MethodPost, "/api/v1/users", map[string]any{"username": "offline-user", "email": "offline@example.test", "role": "viewer", "password": "Offline-password-12345!"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create status %d: %s", response.StatusCode, data)
	}
	result := decodeMap(t, data)
	if result["status"] != "local_created_sync_failed" || result["retryable"] != true {
		t.Fatalf("missing partial failure status: %s", data)
	}
	if _, _, err := f.server.auth.Authenticate("offline-user", "Offline-password-12345!"); err != nil {
		t.Fatalf("local create was not preserved: %v", err)
	}
}
