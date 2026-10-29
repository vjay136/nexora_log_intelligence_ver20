package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalRevisionAndPurge(t *testing.T) {
	storage, err := Open(Config{Provider: "local", LocalDir: filepath.Join(t.TempDir(), "metadata")})
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	first, err := storage.Put(context.Background(), Document{Collection: "parser-packs", ID: "mail", State: "draft", Payload: json.RawMessage(`{"version":"1.0.0"}`), CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := storage.Put(context.Background(), Document{Collection: "parser-packs", ID: "mail", State: "in_review", Payload: json.RawMessage(`{"version":"1.0.1"}`), CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || second.Revision != 2 || first.Hash == second.Hash {
		t.Fatalf("invalid revisions: %+v %+v", first, second)
	}
	latest, err := storage.Get(context.Background(), "parser-packs", "mail", 0)
	if err != nil || latest.Revision != 2 {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	if err := storage.Delete(context.Background(), "parser-packs", "mail", true, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Get(context.Background(), "parser-packs", "mail", 0); err == nil {
		t.Fatal("purged document remains readable")
	}
}

func TestSupabaseAdapterUsesProtectedRPCs(t *testing.T) {
	const anonKey = "public-anon-key-for-test"
	const controllerSecret = "controller-secret-123456789012345678901234"
	now := time.Now().UTC().Truncate(time.Second)
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("apikey") != anonKey || r.Header.Get("Authorization") != "Bearer "+anonKey || r.Header.Get("X-NLI-Controller-Secret") != controllerSecret {
			t.Errorf("invalid Supabase request method=%s headers=%v", r.Method, r.Header)
		}
		var args map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&args); err != nil {
			t.Error(err)
			return
		}
		calls = append(calls, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		doc := Document{Collection: "parser-packs", ID: "mail", Revision: 1, State: "draft", Payload: json.RawMessage(`{"version":"1.0.0"}`), Hash: "abc", CreatedBy: "alice", CreatedAt: now, UpdatedAt: now}
		switch r.URL.Path {
		case "/rest/v1/rpc/nli_health":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "schema_version": 1})
		case "/rest/v1/rpc/nli_put_document":
			if args["p_collection"] != "parser-packs" || args["p_id"] != "mail" || args["p_sha256"] == "" {
				t.Errorf("put args=%v", args)
			}
			_ = json.NewEncoder(w).Encode(doc)
		case "/rest/v1/rpc/nli_get_document":
			_ = json.NewEncoder(w).Encode(doc)
		case "/rest/v1/rpc/nli_list_documents":
			_ = json.NewEncoder(w).Encode([]Document{doc})
		case "/rest/v1/rpc/nli_delete_document":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "purged"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	storage, err := Open(Config{Provider: "supabase", SupabaseURL: server.URL, SupabaseAnonKey: anonKey, SupabaseControllerSecret: []byte(controllerSecret), AllowInsecure: true, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	ctx := context.Background()
	if err := storage.Health(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := storage.Put(ctx, Document{Collection: "parser-packs", ID: "mail", State: "draft", Payload: json.RawMessage(`{"version":"1.0.0"}`), CreatedBy: "alice"})
	if err != nil || created.ID != "mail" {
		t.Fatalf("put=%+v err=%v", created, err)
	}
	if _, err = storage.Get(ctx, "parser-packs", "mail", 0); err != nil {
		t.Fatal(err)
	}
	listed, err := storage.List(ctx, "parser-packs", Query{Limit: 10})
	if err != nil || len(listed) != 1 {
		t.Fatalf("list=%+v err=%v", listed, err)
	}
	if err = storage.Delete(ctx, "parser-packs", "mail", true, "admin"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 5 {
		t.Fatalf("calls=%v", calls)
	}
}

func TestSupabaseRetriesOnlyIdempotentOperations(t *testing.T) {
	var healthCalls, putCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/v1/rpc/nli_health":
			healthCalls++
			if healthCalls < 3 {
				w.WriteHeader(530)
				_, _ = io.WriteString(w, `{"message":"temporary edge recovery"}`)
				return
			}
			_, _ = io.WriteString(w, `{"status":"ok","schema_version":1}`)
		case "/rest/v1/rpc/nli_put_document":
			putCalls++
			w.WriteHeader(530)
			_, _ = io.WriteString(w, `{"message":"response unavailable"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	storage, err := Open(Config{
		Provider: "supabase", SupabaseURL: server.URL, SupabaseAnonKey: "sb_publishable_test",
		SupabaseControllerSecret: []byte(strings.Repeat("s", 32)), AllowInsecure: true, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	if err := storage.Health(context.Background()); err != nil {
		t.Fatalf("idempotent health did not recover: %v", err)
	}
	if healthCalls != 3 {
		t.Fatalf("health attempts=%d, want 3", healthCalls)
	}
	_, err = storage.Put(context.Background(), Document{Collection: "parser-packs", ID: "mail", State: "draft", Payload: json.RawMessage(`{}`), CreatedBy: "alice"})
	if err == nil || putCalls != 1 {
		t.Fatalf("write retry contract error=%v calls=%d, want failure after one attempt", err, putCalls)
	}
}

func TestSupabaseAdapterRejectsUnsafeConfiguration(t *testing.T) {
	secret := []byte("controller-secret-123456789012345678901234")
	if _, err := Open(Config{Provider: "supabase", SupabaseURL: "http://project.supabase.co", SupabaseAnonKey: "anon", SupabaseControllerSecret: secret}); err == nil {
		t.Fatal("HTTP Supabase URL accepted")
	}
	if _, err := Open(Config{Provider: "supabase", SupabaseURL: "https://project.supabase.co/path", SupabaseAnonKey: "anon", SupabaseControllerSecret: secret}); err == nil {
		t.Fatal("Supabase URL path accepted")
	}
	if _, err := Open(Config{Provider: "supabase", SupabaseURL: "https://project.supabase.co", SupabaseAnonKey: "anon", SupabaseControllerSecret: []byte("short")}); err == nil {
		t.Fatal("short controller secret accepted")
	}
}

func TestSupabasePublishableKeyAndMissingDocument(t *testing.T) {
	const key = "sb_publishable_test_key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("apikey") != key || r.Header.Get("Authorization") != "" {
			t.Error("publishable key must be sent only in apikey")
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":"P0002","message":"document not found"}`)
	}))
	defer server.Close()
	storage, err := Open(Config{Provider: "supabase", SupabaseURL: server.URL, SupabaseAnonKey: key, SupabaseControllerSecret: []byte(strings.Repeat("s", 32)), AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	if _, err := storage.Get(context.Background(), "parsers", "missing", 0); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing document must preserve store contract: %v", err)
	}
}

func TestSupabaseDoesNotFollowSecretBearingRedirect(t *testing.T) {
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = true
		_, _ = io.WriteString(w, `{"status":"ok","schema_version":1}`)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	storage, err := Open(Config{Provider: "supabase", SupabaseURL: server.URL, SupabaseAnonKey: "sb_publishable_test", SupabaseControllerSecret: []byte(strings.Repeat("s", 32)), AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	if err := storage.Health(context.Background()); err == nil || leaked {
		t.Fatalf("redirect leaked=%v error=%v", leaked, err)
	}
}

func TestSupabaseRejectsIncompatibleSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok","schema_version":999}`)
	}))
	defer server.Close()
	storage, err := Open(Config{Provider: "supabase", SupabaseURL: server.URL, SupabaseAnonKey: "sb_publishable_test", SupabaseControllerSecret: []byte(strings.Repeat("s", 32)), AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	if err := storage.Health(context.Background()); err == nil {
		t.Fatal("incompatible schema reported healthy")
	}
}

func TestLocalListClampsNegativeOffset(t *testing.T) {
	storage, err := Open(Config{Provider: "local", LocalDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	if _, err := storage.Put(context.Background(), Document{Collection: "parsers", ID: "test", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	docs, err := storage.List(context.Background(), "parsers", Query{Offset: -1})
	if err != nil || len(docs) != 1 {
		t.Fatalf("negative-offset list=%v error=%v", docs, err)
	}
}
