// Package store provides revisioned controller metadata storage. Normal event
// payloads are deliberately outside this API; explicit Assurance fixtures must
// be encrypted by the caller before they are stored.
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxDocumentBytes = 4 << 20
const maxResponseBytes = 64 << 20

var safeComponent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Document is an immutable revision. Payload is domain metadata or encrypted
// ciphertext; it must never contain ordinary raw logs or parsed customer JSON.
type Document struct {
	Collection string          `json:"collection"`
	ID         string          `json:"id"`
	Revision   int             `json:"revision"`
	State      string          `json:"state,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Hash       string          `json:"sha256"`
	CreatedBy  string          `json:"created_by"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	Deleted    bool            `json:"deleted,omitempty"`
}

// Query bounds every list operation.
type Query struct {
	State  string
	Limit  int
	Offset int
}

// Store is the controller's durable metadata boundary.
type Store interface {
	Mode() string
	Health(context.Context) error
	Put(context.Context, Document) (Document, error)
	Get(context.Context, string, string, int) (Document, error)
	List(context.Context, string, Query) ([]Document, error)
	Delete(context.Context, string, string, bool, string) error
	Close() error
}

// Config selects the local durable store or a Supabase project. Supabase uses
// the public anon key only to reach PostgREST; every RPC additionally requires
// a controller-only secret enforced by the installed database functions.
type Config struct {
	Provider                 string
	LocalDir                 string
	SupabaseURL              string
	SupabaseAnonKey          string // Legacy name; accepts an anon JWT or publishable key.
	SupabaseControllerSecret []byte
	AllowInsecure            bool
	Timeout                  time.Duration
}

func Open(cfg Config) (Store, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "local":
		if cfg.LocalDir == "" {
			return nil, errors.New("local store directory is required")
		}
		return newLocal(cfg.LocalDir)
	case "supabase":
		return newSupabase(cfg)
	default:
		return nil, fmt.Errorf("unsupported store provider %q", cfg.Provider)
	}
}

func validateDocument(doc Document) error {
	if !safeComponent.MatchString(doc.Collection) || !safeComponent.MatchString(doc.ID) {
		return errors.New("invalid collection or document id")
	}
	if len(doc.Payload) > maxDocumentBytes || (len(doc.Payload) > 0 && !json.Valid(doc.Payload)) {
		return errors.New("payload must be valid JSON no larger than 4 MiB")
	}
	if len(doc.State) > 64 || len(doc.CreatedBy) > 128 {
		return errors.New("document metadata exceeds bounds")
	}
	return nil
}

func finalize(doc Document, previous *Document) Document {
	now := time.Now().UTC()
	if previous != nil {
		doc.Revision = previous.Revision + 1
		doc.CreatedAt = previous.CreatedAt
		if doc.CreatedBy == "" {
			doc.CreatedBy = previous.CreatedBy
		}
	} else {
		doc.Revision = 1
		doc.CreatedAt = now
	}
	doc.UpdatedAt = now
	digest := sha256.Sum256(doc.Payload)
	doc.Hash = hex.EncodeToString(digest[:])
	return doc
}

type localStore struct {
	root string
	mu   sync.RWMutex
}

func newLocal(root string) (*localStore, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0750); err != nil {
		return nil, err
	}
	return &localStore{root: absolute}, nil
}

func (s *localStore) Mode() string { return "local" }
func (s *localStore) Close() error { return nil }

func (s *localStore) Health(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	probe := filepath.Join(s.root, ".health-"+fmt.Sprint(time.Now().UnixNano()))
	if err := os.WriteFile(probe, []byte("ok"), 0600); err != nil {
		return err
	}
	return os.Remove(probe)
}

func (s *localStore) collectionDir(collection string) (string, error) {
	if !safeComponent.MatchString(collection) {
		return "", errors.New("invalid collection")
	}
	return filepath.Join(s.root, collection), nil
}

func (s *localStore) Put(ctx context.Context, doc Document) (Document, error) {
	if err := validateDocument(doc); err != nil {
		return Document{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, err := s.latestLocked(doc.Collection, doc.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Document{}, err
	}
	if errors.Is(err, os.ErrNotExist) {
		previous = nil
	}
	doc = finalize(doc, previous)
	dir, _ := s.collectionDir(doc.Collection)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return Document{}, err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s--%09d.json", doc.ID, doc.Revision))
	encoded, err := json.Marshal(doc)
	if err != nil {
		return Document{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0600); err != nil {
		return Document{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return Document{}, err
	}
	return doc, nil
}

func (s *localStore) latestLocked(collection, id string) (*Document, error) {
	dir, err := s.collectionDir(collection)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(dir, id+"--*.json"))
	if err != nil || len(matches) == 0 {
		return nil, os.ErrNotExist
	}
	sort.Strings(matches)
	data, err := os.ReadFile(matches[len(matches)-1])
	if err != nil {
		return nil, err
	}
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (s *localStore) Get(ctx context.Context, collection, id string, revision int) (Document, error) {
	if !safeComponent.MatchString(collection) || !safeComponent.MatchString(id) {
		return Document{}, errors.New("invalid collection or document id")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var doc *Document
	var err error
	if revision <= 0 {
		doc, err = s.latestLocked(collection, id)
	} else {
		dir, _ := s.collectionDir(collection)
		data, readErr := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%s--%09d.json", id, revision)))
		if readErr != nil {
			return Document{}, readErr
		}
		doc = &Document{}
		err = json.Unmarshal(data, doc)
	}
	if err != nil {
		return Document{}, err
	}
	if doc.Deleted {
		return Document{}, os.ErrNotExist
	}
	return *doc, nil
}

func (s *localStore) List(ctx context.Context, collection string, query Query) ([]Document, error) {
	if query.Limit <= 0 || query.Limit > 1000 {
		query.Limit = 100
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	dir, err := s.collectionDir(collection)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Document{}, nil
	}
	if err != nil {
		return nil, err
	}
	latest := map[string]Document{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil || len(data) > maxDocumentBytes+(64<<10) {
			continue
		}
		var doc Document
		if json.Unmarshal(data, &doc) == nil && doc.Revision > latest[doc.ID].Revision {
			latest[doc.ID] = doc
		}
	}
	out := make([]Document, 0, len(latest))
	for _, doc := range latest {
		if doc.Deleted || (query.State != "" && doc.State != query.State) {
			continue
		}
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if query.Offset >= len(out) {
		return []Document{}, nil
	}
	out = out[query.Offset:]
	if len(out) > query.Limit {
		out = out[:query.Limit]
	}
	return out, nil
}

func (s *localStore) Delete(ctx context.Context, collection, id string, purge bool, actor string) error {
	if !safeComponent.MatchString(collection) || !safeComponent.MatchString(id) {
		return errors.New("invalid collection or document id")
	}
	if purge {
		s.mu.Lock()
		defer s.mu.Unlock()
		dir, _ := s.collectionDir(collection)
		matches, _ := filepath.Glob(filepath.Join(dir, id+"--*.json"))
		for _, path := range matches {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	_, err := s.Put(ctx, Document{Collection: collection, ID: id, State: "deleted", Deleted: true, CreatedBy: actor, Payload: json.RawMessage(`{}`)})
	return err
}

type supabaseStore struct {
	base             *url.URL
	anonKey          string
	controllerSecret []byte
	client           *http.Client
}

func newSupabase(cfg Config) (*supabaseStore, error) {
	projectURL := strings.TrimSpace(cfg.SupabaseURL)
	if projectURL == "" {
		return nil, errors.New("Supabase project URL is required")
	}
	if strings.TrimSpace(cfg.SupabaseAnonKey) == "" {
		return nil, errors.New("Supabase anon/public key is required")
	}
	if len(cfg.SupabaseControllerSecret) < 32 {
		return nil, errors.New("Supabase controller secret must contain at least 32 bytes")
	}
	base, err := url.Parse(projectURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("invalid Supabase project URL")
	}
	if base.Scheme != "https" && !(base.Scheme == "http" && cfg.AllowInsecure) {
		return nil, errors.New("Supabase project URL must use HTTPS")
	}
	if base.Path != "" && base.Path != "/" {
		return nil, errors.New("Supabase project URL must not contain a path")
	}
	base.Path = ""
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &supabaseStore{
		base:             base,
		anonKey:          strings.TrimSpace(cfg.SupabaseAnonKey),
		controllerSecret: append([]byte(nil), cfg.SupabaseControllerSecret...),
		// Redirects must never forward the controller secret to another origin.
		client: &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}, nil
}

func (s *supabaseStore) Mode() string { return "supabase" }
func (s *supabaseStore) Close() error {
	s.client.CloseIdleConnections()
	return nil
}

func (s *supabaseStore) call(ctx context.Context, function string, args any, out any) error {
	body, err := json.Marshal(args)
	if err != nil {
		return err
	}
	endpoint := s.base.ResolveReference(&url.URL{Path: "/rest/v1/rpc/" + function})
	// Health and reads are idempotent. Retry a small, bounded number of times
	// when the Supabase edge returns a transient 429/5xx response (including
	// Cloudflare 530) or drops the connection. Revision-creating writes are
	// never retried because a lost response must not create a second revision.
	maxAttempts := 1
	if function == "nli_health" || function == "nli_get_document" || function == "nli_list_documents" {
		maxAttempts = 3
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
		if requestErr != nil {
			return requestErr
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("apikey", s.anonKey)
		// Modern sb_publishable_ keys are not JWTs. Supabase accepts them only
		// through apikey; retain Bearer for legacy anon JWT deployments.
		if !strings.HasPrefix(s.anonKey, "sb_") {
			req.Header.Set("Authorization", "Bearer "+s.anonKey)
		}
		req.Header.Set("X-NLI-Controller-Secret", string(s.controllerSecret))
		resp, requestErr := s.client.Do(req)
		if requestErr != nil {
			lastErr = requestErr
			if attempt+1 < maxAttempts && ctx.Err() == nil {
				if err := waitForStoreRetry(ctx, attempt); err != nil {
					return err
				}
				continue
			}
			return requestErr
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > maxResponseBytes {
			return fmt.Errorf("Supabase %s response exceeds 64 MiB", function)
		}
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			if out != nil && len(strings.TrimSpace(string(data))) > 0 {
				return json.Unmarshal(data, out)
			}
			return nil
		}
		var envelope struct {
			Message string `json:"message"`
			Details string `json:"details"`
			Hint    string `json:"hint"`
			Code    string `json:"code"`
		}
		_ = json.Unmarshal(data, &envelope)
		if envelope.Code == "P0002" {
			return fmt.Errorf("Supabase %s: %w", function, os.ErrNotExist)
		}
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = strings.TrimSpace(string(data))
		}
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		message = strings.ReplaceAll(message, string(s.controllerSecret), "[redacted]")
		if len(message) > 1024 {
			message = message[:1024]
		}
		lastErr = fmt.Errorf("Supabase %s failed with HTTP %d: %s", function, resp.StatusCode, message)
		transient := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if transient && attempt+1 < maxAttempts && ctx.Err() == nil {
			if err := waitForStoreRetry(ctx, attempt); err != nil {
				return err
			}
			continue
		}
		return lastErr
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("Supabase %s request failed", function)
}

func waitForStoreRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(100+(attempt*150)) * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *supabaseStore) Health(ctx context.Context) error {
	var response struct {
		Status        string `json:"status"`
		SchemaVersion int    `json:"schema_version"`
	}
	if err := s.call(ctx, "nli_health", map[string]any{}, &response); err != nil {
		return err
	}
	if response.Status != "ok" {
		return fmt.Errorf("Supabase nli_health returned status %q", response.Status)
	}
	if response.SchemaVersion != 1 {
		return fmt.Errorf("Supabase schema version %d is unsupported; install supabase/nli_schema.sql", response.SchemaVersion)
	}
	return nil
}

func (s *supabaseStore) Put(ctx context.Context, doc Document) (Document, error) {
	if err := validateDocument(doc); err != nil {
		return Document{}, err
	}
	digest := sha256.Sum256(doc.Payload)
	var result Document
	err := s.call(ctx, "nli_put_document", map[string]any{
		"p_collection": doc.Collection,
		"p_id":         doc.ID,
		"p_state":      doc.State,
		"p_payload":    json.RawMessage(doc.Payload),
		"p_sha256":     hex.EncodeToString(digest[:]),
		"p_created_by": doc.CreatedBy,
		"p_deleted":    doc.Deleted,
	}, &result)
	return result, err
}

func (s *supabaseStore) Get(ctx context.Context, collection, id string, revision int) (Document, error) {
	if !safeComponent.MatchString(collection) || !safeComponent.MatchString(id) {
		return Document{}, errors.New("invalid collection or document id")
	}
	var result Document
	err := s.call(ctx, "nli_get_document", map[string]any{"p_collection": collection, "p_id": id, "p_revision": revision}, &result)
	return result, err
}

func (s *supabaseStore) List(ctx context.Context, collection string, query Query) ([]Document, error) {
	if !safeComponent.MatchString(collection) {
		return nil, errors.New("invalid collection")
	}
	if query.Limit <= 0 || query.Limit > 1000 {
		query.Limit = 100
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	var result []Document
	err := s.call(ctx, "nli_list_documents", map[string]any{
		"p_collection": collection,
		"p_state":      query.State,
		"p_limit":      query.Limit,
		"p_offset":     query.Offset,
	}, &result)
	return result, err
}

func (s *supabaseStore) Delete(ctx context.Context, collection, id string, purge bool, actor string) error {
	if !safeComponent.MatchString(collection) || !safeComponent.MatchString(id) {
		return errors.New("invalid collection or document id")
	}
	return s.call(ctx, "nli_delete_document", map[string]any{
		"p_collection": collection,
		"p_id":         id,
		"p_purge":      purge,
		"p_actor":      actor,
	}, nil)
}
