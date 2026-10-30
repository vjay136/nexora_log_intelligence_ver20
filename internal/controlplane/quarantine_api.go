package controlplane

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/auditlog"
	"nexora.local/nli-v20/internal/middleware"
	metastore "nexora.local/nli-v20/internal/store"
)

const (
	quarantineRecordsCollection = "quarantine-records"
	quarantineRawCollection     = "quarantine-raw"
	quarantineHistoryCollection = "quarantine-history"
	maxQuarantineRawBytes       = 1 << 20
	maxQuarantineBatchRecords   = 1000
)

type quarantineVault struct {
	mu      sync.Mutex
	enabled bool
	max     int
	ttl     time.Duration
	aead    cipher.AEAD
	store   metastore.Store
	audit   *auditlog.ChainLog
}

type quarantineRecord struct {
	ID             string    `json:"id"`
	NodeID         string    `json:"node_id"`
	Service        string    `json:"service"`
	Hash           string    `json:"hash"`
	FirstSeen      time.Time `json:"first_seen"`
	LastAttempt    time.Time `json:"last_attempt"`
	RetryAfter     time.Time `json:"retry_after,omitempty"`
	AttemptCount   int       `json:"attempt_count"`
	Status         string    `json:"status"`
	ErrorCodes     []string  `json:"error_codes,omitempty"`
	ParserRevision string    `json:"parser_revision,omitempty"`
	SourcePath     string    `json:"source_path,omitempty"`
	SourceLine     uint64    `json:"source_line,omitempty"`
	RawAvailable   bool      `json:"raw_available"`
	RawExpiresAt   time.Time `json:"raw_expires_at,omitempty"`
	ExcludedAt     time.Time `json:"excluded_at,omitempty"`
	ExcludedBy     string    `json:"excluded_by,omitempty"`
	ExcludeReason  string    `json:"exclude_reason,omitempty"`
}

type sealedQuarantineRaw struct {
	Nonce      string    `json:"nonce"`
	Ciphertext string    `json:"ciphertext"`
	SHA256     string    `json:"sha256"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type quarantineHistoryEvent struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"`
	RecordID  string    `json:"record_id,omitempty"`
	NodeID    string    `json:"node_id,omitempty"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	RolloutID string    `json:"rollout_id,omitempty"`
}

func newQuarantineVault(cfg Config, _ metastore.Store, audit *auditlog.ChainLog) (*quarantineVault, error) {
	store, err := metastore.Open(metastore.Config{Provider: "local", LocalDir: filepath.Join(cfg.StateDir, "quarantine-metadata")})
	if err != nil {
		return nil, fmt.Errorf("open local quarantine store: %w", err)
	}
	v := &quarantineVault{enabled: cfg.QuarantineRawRetentionEnabled, max: cfg.QuarantineRawRetentionMaxRecords, ttl: time.Duration(cfg.QuarantineRawRetentionHours) * time.Hour, store: store, audit: audit}
	if !v.enabled {
		return v, nil
	}
	key, err := loadOrCreateControllerKey(filepath.Join(cfg.StateDir, "quarantine-metadata"), "quarantine.key", nil, os.Getenv(cfg.QuarantineRawRetentionKeyEnv))
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("quarantine raw retention key: %w", err)
	}
	block, err := aes.NewCipher(key[:32])
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	v.aead, err = cipher.NewGCM(block)
	if err != nil {
		_ = store.Close()
	}
	return v, err
}

func (v *quarantineVault) seal(id, raw string, expires time.Time) (sealedQuarantineRaw, error) {
	if v.aead == nil {
		return sealedQuarantineRaw{}, errors.New("quarantine raw encryption is unavailable")
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return sealedQuarantineRaw{}, err
	}
	ciphertext := v.aead.Seal(nil, nonce, []byte(raw), []byte("nli-quarantine:"+id))
	digest := sha256.Sum256([]byte(raw))
	return sealedQuarantineRaw{Nonce: base64.StdEncoding.EncodeToString(nonce), Ciphertext: base64.StdEncoding.EncodeToString(ciphertext), SHA256: hex.EncodeToString(digest[:]), ExpiresAt: expires}, nil
}

func (v *quarantineVault) open(id string, sealed sealedQuarantineRaw) ([]byte, error) {
	if v.aead == nil {
		return nil, errors.New("quarantine raw encryption is unavailable")
	}
	nonce, err := base64.StdEncoding.DecodeString(sealed.Nonce)
	if err != nil || len(nonce) != v.aead.NonceSize() {
		return nil, errors.New("invalid retained raw nonce")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(sealed.Ciphertext)
	if err != nil {
		return nil, errors.New("invalid retained raw ciphertext")
	}
	plain, err := v.aead.Open(nil, nonce, ciphertext, []byte("nli-quarantine:"+id))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(plain)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), sealed.SHA256) {
		return nil, errors.New("retained raw checksum mismatch")
	}
	return plain, nil
}

func (s *Server) handleNodeQuarantineRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Records     []json.RawMessage `json:"records"`
		RawIncluded bool              `json:"raw_included"`
	}
	if decodeJSON(r, &request, 8<<20) != nil || len(request.Records) == 0 || len(request.Records) > maxQuarantineBatchRecords {
		jsonError(w, "records must contain 1..1000 bounded quarantine entries", http.StatusBadRequest)
		return
	}
	nodeID := strings.TrimSpace(r.URL.Query().Get("node_id"))
	// Node credentials are bound to their node ID; derive identity from the
	// submitted records only after checking every one against the token.
	var acceptedNode string
	accepted, rawStored := 0, 0
	for _, encoded := range request.Records {
		var record quarantineRecordInput
		if json.Unmarshal(encoded, &record) != nil {
			jsonError(w, "invalid quarantine record", http.StatusBadRequest)
			return
		}
		if record.NodeID == "" {
			record.NodeID = nodeID
		}
		if record.NodeID == "" || !s.authorizeNode(r, record.NodeID) || (acceptedNode != "" && acceptedNode != record.NodeID) {
			jsonError(w, "unauthorized node or mixed node batch", http.StatusUnauthorized)
			return
		}
		acceptedNode = record.NodeID
		if err := s.storeQuarantineRecord(r.Context(), record, request.RawIncluded); err != nil {
			jsonError(w, "quarantine record persistence failed", http.StatusInternalServerError)
			return
		}
		accepted++
		if s.quarantine.enabled && request.RawIncluded && record.Raw != "" {
			rawStored++
		}
	}
	jsonCreated(w, map[string]any{"status": "accepted", "accepted": accepted, "raw_stored": rawStored, "raw_available": s.quarantine.enabled && request.RawIncluded})
}

type quarantineRecordInput struct {
	ID             string    `json:"id"`
	NodeID         string    `json:"node_id"`
	Service        string    `json:"service"`
	EventHash      string    `json:"event_hash"`
	Raw            string    `json:"raw"`
	FirstSeen      time.Time `json:"first_seen"`
	LastAttempt    time.Time `json:"last_attempt"`
	AttemptCount   int       `json:"attempt_count"`
	Status         string    `json:"status"`
	ErrorCodes     []string  `json:"error_codes"`
	ParserRevision string    `json:"parser_revision"`
	SourcePath     string    `json:"source_path"`
	SourceLine     uint64    `json:"source_line"`
}

func (s *Server) storeQuarantineRecord(ctx context.Context, input quarantineRecordInput, rawIncluded bool) error {
	id := safeID(input.ID)
	if id == "" || len(input.ID) > 128 || len(input.Service) > 128 || len(input.ErrorCodes) > 32 || input.AttemptCount < 0 || len(input.Raw) > maxQuarantineRawBytes || len(input.SourcePath) > 1024 || len(input.ParserRevision) > 128 {
		return errors.New("quarantine record exceeds bounds")
	}
	for _, code := range input.ErrorCodes {
		if len(code) > 128 {
			return errors.New("quarantine error code exceeds bounds")
		}
	}
	hash := strings.ToLower(strings.TrimSpace(input.EventHash))
	if hash == "" && input.Raw != "" {
		digest := sha256.Sum256([]byte(input.Raw))
		hash = hex.EncodeToString(digest[:])
	}
	if hash != "" && (len(hash) != 64 || !isHex(hash)) {
		return errors.New("quarantine hash must be SHA-256")
	}
	if input.Raw != "" {
		digest := sha256.Sum256([]byte(input.Raw))
		if hash != "" && !strings.EqualFold(hash, hex.EncodeToString(digest[:])) {
			return errors.New("raw record does not match its event hash")
		}
	}
	now := time.Now().UTC()
	if input.FirstSeen.IsZero() {
		input.FirstSeen = now
	}
	if input.LastAttempt.IsZero() {
		input.LastAttempt = now
	}
	if input.AttemptCount < 1 {
		input.AttemptCount = 1
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = "waiting"
	}
	if len(status) > 32 {
		return errors.New("quarantine status exceeds bounds")
	}
	v := s.quarantine
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := s.expireQuarantineRaw(ctx, now); err != nil {
		return err
	}
	existingDoc, getErr := v.store.Get(ctx, quarantineRecordsCollection, id, 0)
	var existing quarantineRecord
	if getErr == nil {
		_ = json.Unmarshal(existingDoc.Payload, &existing)
		if existing.Hash != "" && hash != "" && existing.Hash != hash {
			return errors.New("record ID is already bound to a different hash")
		}
		if existing.Status == "resolved" || existing.Status == "reprocessing" {
			status = existing.Status
		}
		if input.AttemptCount < existing.AttemptCount {
			input.AttemptCount = existing.AttemptCount
		}
		if input.FirstSeen.After(existing.FirstSeen) {
			input.FirstSeen = existing.FirstSeen
		}
		if input.LastAttempt.Before(existing.LastAttempt) {
			input.LastAttempt = existing.LastAttempt
		}
	} else if !errors.Is(getErr, os.ErrNotExist) {
		return getErr
	}
	meta := quarantineRecord{ID: id, NodeID: input.NodeID, Service: input.Service, Hash: hash, FirstSeen: input.FirstSeen.UTC(), LastAttempt: input.LastAttempt.UTC(), AttemptCount: input.AttemptCount, Status: status, ErrorCodes: append([]string(nil), input.ErrorCodes...), ParserRevision: input.ParserRevision, SourcePath: input.SourcePath, SourceLine: input.SourceLine, RawAvailable: existing.RawAvailable, RawExpiresAt: existing.RawExpiresAt}
	storeRaw := v.enabled && rawIncluded && input.Raw != "" && !meta.RawAvailable
	if storeRaw {
		if len(input.Raw) > maxQuarantineRawBytes {
			return errors.New("raw quarantine line exceeds 1 MiB limit")
		}
		expires := now.Add(v.ttl)
		sealed, err := v.seal(id, input.Raw, expires)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(sealed)
		if _, err := v.store.Put(ctx, metastore.Document{Collection: quarantineRawCollection, ID: id, State: "retained", Payload: payload, CreatedBy: "node:" + input.NodeID}); err != nil {
			return err
		}
		meta.RawAvailable, meta.RawExpiresAt = true, expires
	}
	if len(existing.ID) == 0 {
		if err := s.pruneQuarantine(ctx); err != nil {
			return err
		}
	}
	payload, _ := json.Marshal(meta)
	if _, err := v.store.Put(ctx, metastore.Document{Collection: quarantineRecordsCollection, ID: id, State: meta.Status, Payload: payload, CreatedBy: "node:" + input.NodeID}); err != nil {
		if storeRaw && len(existing.ID) == 0 {
			_ = v.store.Delete(ctx, quarantineRawCollection, id, true, "controller")
		}
		return err
	}
	if len(existing.ID) == 0 {
		return s.appendQuarantineHistory(ctx, quarantineHistoryEvent{Timestamp: now, Action: "record_received", RecordID: id, NodeID: input.NodeID, Status: meta.Status, Message: "Client quarantine metadata received"}, "node:"+input.NodeID)
	}
	return nil
}

func (s *Server) pruneQuarantine(ctx context.Context) error {
	docs, err := s.quarantine.store.List(ctx, quarantineRecordsCollection, metastore.Query{Limit: 1000})
	if err != nil {
		return err
	}
	if len(docs) < s.quarantine.max {
		return nil
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].CreatedAt.Before(docs[j].CreatedAt) })
	remove := len(docs) - s.quarantine.max + 1
	for _, doc := range docs[:remove] {
		_ = s.quarantine.store.Delete(ctx, quarantineRawCollection, doc.ID, true, "controller")
		if err := s.quarantine.store.Delete(ctx, quarantineRecordsCollection, doc.ID, true, "controller"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) expireQuarantineRaw(ctx context.Context, now time.Time) error {
	docs, err := s.quarantine.store.List(ctx, quarantineRawCollection, metastore.Query{Limit: 1000})
	if err != nil {
		return err
	}
	for _, doc := range docs {
		var sealed sealedQuarantineRaw
		if json.Unmarshal(doc.Payload, &sealed) != nil || (!sealed.ExpiresAt.IsZero() && !sealed.ExpiresAt.After(now)) {
			if err := s.quarantine.store.Delete(ctx, quarantineRawCollection, doc.ID, true, "controller"); err != nil {
				return err
			}
			if record, recordDoc, getErr := s.getQuarantineRecord(ctx, doc.ID); getErr == nil {
				record.RawAvailable = false
				record.RawExpiresAt = time.Time{}
				_ = s.putQuarantineRecord(ctx, record, recordDoc.CreatedBy)
			}
		}
	}
	return nil
}

func (s *Server) getQuarantineRecord(ctx context.Context, id string) (quarantineRecord, metastore.Document, error) {
	doc, err := s.quarantine.store.Get(ctx, quarantineRecordsCollection, id, 0)
	if err != nil {
		return quarantineRecord{}, metastore.Document{}, err
	}
	var record quarantineRecord
	if err = json.Unmarshal(doc.Payload, &record); err != nil {
		return record, doc, err
	}
	return record, doc, nil
}

func (s *Server) putQuarantineRecord(ctx context.Context, record quarantineRecord, createdBy string) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = s.quarantine.store.Put(ctx, metastore.Document{Collection: quarantineRecordsCollection, ID: record.ID, State: record.Status, Payload: payload, CreatedBy: createdBy})
	return err
}

func (s *Server) listQuarantine(ctx context.Context, limit, offset int, status, nodeID string) ([]quarantineRecord, error) {
	docs, err := s.quarantine.store.List(ctx, quarantineRecordsCollection, metastore.Query{Limit: 1000})
	if err != nil {
		return nil, err
	}
	records := make([]quarantineRecord, 0, len(docs))
	for _, doc := range docs {
		var item quarantineRecord
		if json.Unmarshal(doc.Payload, &item) != nil {
			continue
		}
		if status != "" && item.Status != status || nodeID != "" && item.NodeID != nodeID {
			continue
		}
		records = append(records, item)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].FirstSeen.After(records[j].FirstSeen) })
	if offset >= len(records) {
		return []quarantineRecord{}, nil
	}
	records = records[offset:]
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func (s *Server) handleQuarantineSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	if err := s.expireQuarantineRaw(ctx, time.Now().UTC()); err != nil {
		jsonError(w, "quarantine retention status unavailable", http.StatusServiceUnavailable)
		return
	}
	records, err := s.listQuarantine(ctx, 1000, 0, "", "")
	if err != nil {
		jsonError(w, "quarantine summary unavailable", http.StatusServiceUnavailable)
		return
	}
	counts := map[string]int{"waiting": 0, "actionable": 0, "reprocessing": 0, "resolved": 0, "raw_available": 0, "total": len(records)}
	for _, item := range records {
		switch item.Status {
		case "waiting", "replay_failed":
			counts["waiting"]++
		case "reprocessing":
			counts["reprocessing"]++
		case "resolved":
			counts["resolved"]++
		}
		if item.RawAvailable {
			counts["raw_available"]++
		}
	}
	counts["actionable"] = counts["waiting"]
	counts["raw_retention_enabled"] = 0
	if s.quarantine.enabled {
		counts["raw_retention_enabled"] = 1
	}
	jsonOK(w, counts)
}

func (s *Server) handleQuarantineRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	limit, err := queryBound(r, "limit", 100, 1, 1000)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	offset, err := queryBound(r, "offset", 0, 0, 1000000)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.expireQuarantineRaw(r.Context(), time.Now().UTC()); err != nil {
		jsonError(w, "quarantine retention status unavailable", http.StatusServiceUnavailable)
		return
	}
	records, err := s.listQuarantine(r.Context(), limit, offset, r.URL.Query().Get("status"), r.URL.Query().Get("node_id"))
	if err != nil {
		jsonError(w, "quarantine records unavailable", http.StatusServiceUnavailable)
		return
	}
	jsonOK(w, map[string]any{"records": records, "count": len(records), "limit": limit, "offset": offset, "raw_retention_enabled": s.quarantine.enabled})
}

func queryBound(r *http.Request, key string, fallback, min, max int) (int, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < min || number > max {
		return 0, fmt.Errorf("%s must be %d..%d", key, min, max)
	}
	return number, nil
}

func (s *Server) handleQuarantineRecord(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/quarantine/records/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "exclude" && r.Method == http.MethodPost {
		s.handleQuarantineDisposition(w, r, parts[0], true)
		return
	}
	if len(parts) == 2 && parts[1] == "include" && r.Method == http.MethodPost {
		s.handleQuarantineDisposition(w, r, parts[0], false)
		return
	}
	if len(parts) == 2 && parts[1] == "analyze" && r.Method == http.MethodPost {
		s.handleQuarantineAnalyze(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "raw" && r.Method == http.MethodDelete {
		s.handleQuarantineRawDelete(w, r, parts[0])
		return
	}
	if len(parts) != 2 || parts[1] != "raw" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	principal, err := s.security.AuthorizeRequest(r, middleware.PermissionReadLogs)
	if err != nil {
		jsonError(w, "logs:read permission required", http.StatusForbidden)
		return
	}
	id := safeID(parts[0])
	if id == "" {
		jsonError(w, "invalid record ID", http.StatusBadRequest)
		return
	}
	if err := s.expireQuarantineRaw(r.Context(), time.Now().UTC()); err != nil {
		jsonError(w, "quarantine retention status unavailable", http.StatusServiceUnavailable)
		return
	}
	_, doc, err := s.getQuarantineRecord(r.Context(), id)
	if err != nil {
		jsonError(w, "quarantine record not found", http.StatusNotFound)
		return
	}
	rawDoc, err := s.quarantine.store.Get(r.Context(), quarantineRawCollection, id, 0)
	if err != nil {
		jsonError(w, "exact raw was not retained or has expired", http.StatusNotFound)
		return
	}
	var sealed sealedQuarantineRaw
	if json.Unmarshal(rawDoc.Payload, &sealed) != nil {
		jsonError(w, "retained raw record is invalid", http.StatusInternalServerError)
		return
	}
	plain, err := s.quarantine.open(id, sealed)
	if err != nil {
		jsonError(w, "retained raw record could not be verified", http.StatusInternalServerError)
		return
	}
	s.auditAction(principal, "quarantine.raw.download", quarantineRecordsCollection+"/"+id, "success", map[string]string{"bytes": strconv.Itoa(len(plain)), "sha256": sealed.SHA256, "record_revision": strconv.Itoa(doc.Revision)})
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="quarantine-`+id+`.log"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(plain)
}

// Exclusion is a soft disposition: the controller keeps record metadata and
// retained evidence for audit/review. Raw deletion is a separate, irreversible
// action and requires the stronger logs:write permission.
func (s *Server) handleQuarantineDisposition(w http.ResponseWriter, r *http.Request, rawID string, exclude bool) {
	principal, err := s.security.AuthorizeRequest(r, middleware.PermissionWriteLogs)
	if err != nil {
		jsonError(w, "logs:write permission required", http.StatusForbidden)
		return
	}
	id := safeID(rawID)
	if id == "" {
		jsonError(w, "invalid record ID", http.StatusBadRequest)
		return
	}
	record, doc, err := s.getQuarantineRecord(r.Context(), id)
	if err != nil {
		jsonError(w, "quarantine record not found", http.StatusNotFound)
		return
	}
	var reason string
	if exclude {
		var request struct {
			Reason string `json:"reason"`
		}
		if decodeJSON(r, &request, 4<<10) != nil {
			jsonError(w, "a short exclusion reason is required", http.StatusBadRequest)
			return
		}
		reason = strings.TrimSpace(request.Reason)
		if len(reason) == 0 || len(reason) > 256 {
			jsonError(w, "exclusion reason must contain 1..256 characters", http.StatusBadRequest)
			return
		}
		if record.Status == "resolved" || record.Status == "reprocessing" {
			jsonError(w, "resolved or reprocessing records cannot be excluded", http.StatusConflict)
			return
		}
		if record.Status == "excluded" {
			jsonError(w, "record is already excluded", http.StatusConflict)
			return
		}
		record.Status, record.ExcludedAt, record.ExcludedBy, record.ExcludeReason = "excluded", time.Now().UTC(), principal.Username, reason
	} else {
		if record.Status != "excluded" {
			jsonError(w, "only excluded records can be returned to the queue", http.StatusConflict)
			return
		}
		record.Status, record.ExcludedAt, record.ExcludedBy, record.ExcludeReason = "waiting", time.Time{}, "", ""
		reason = "returned to waiting queue"
	}
	if err := s.putQuarantineRecord(r.Context(), record, doc.CreatedBy); err != nil {
		jsonError(w, "quarantine disposition could not be saved", http.StatusServiceUnavailable)
		return
	}
	action := "excluded"
	if !exclude {
		action = "included"
	}
	if err := s.appendQuarantineHistory(r.Context(), quarantineHistoryEvent{Timestamp: record.ExcludedAt, Action: "record_" + action, RecordID: id, NodeID: record.NodeID, Status: record.Status, Message: reason}, principal.Username); err != nil {
		jsonError(w, "disposition saved but history could not be written", http.StatusServiceUnavailable)
		return
	}
	s.auditAction(principal, "quarantine."+action, quarantineRecordsCollection+"/"+id, "success", map[string]string{"reason": reason})
	jsonOK(w, map[string]any{"record": record, "status": record.Status})
}

func (s *Server) handleQuarantineRawDelete(w http.ResponseWriter, r *http.Request, rawID string) {
	principal, err := s.security.AuthorizeRequest(r, middleware.PermissionWriteLogs)
	if err != nil {
		jsonError(w, "logs:write permission required", http.StatusForbidden)
		return
	}
	id := safeID(rawID)
	if id == "" {
		jsonError(w, "invalid record ID", http.StatusBadRequest)
		return
	}
	record, doc, err := s.getQuarantineRecord(r.Context(), id)
	if err != nil {
		jsonError(w, "quarantine record not found", http.StatusNotFound)
		return
	}
	if err := s.quarantine.store.Delete(r.Context(), quarantineRawCollection, id, true, principal.Username); err != nil && !errors.Is(err, os.ErrNotExist) {
		jsonError(w, "retained evidence could not be deleted", http.StatusServiceUnavailable)
		return
	}
	now := time.Now().UTC()
	record.RawAvailable, record.RawExpiresAt = false, time.Time{}
	if record.Status == "waiting" || record.Status == "replay_failed" {
		record.Status = "evidence_deleted"
		record.ExcludedAt = now
		record.ExcludedBy = principal.Username
		record.ExcludeReason = "retained raw evidence deleted; metadata retained for audit"
	}
	if err := s.putQuarantineRecord(r.Context(), record, doc.CreatedBy); err != nil {
		jsonError(w, "evidence deleted but record metadata could not be updated", http.StatusServiceUnavailable)
		return
	}
	if err := s.appendQuarantineHistory(r.Context(), quarantineHistoryEvent{Timestamp: now, Action: "raw_evidence_deleted", RecordID: id, NodeID: record.NodeID, Status: record.Status, Message: "Retained raw evidence permanently deleted; metadata remains available for audit and is no longer actionable."}, principal.Username); err != nil {
		jsonError(w, "evidence deleted but history could not be written", http.StatusServiceUnavailable)
		return
	}
	s.auditAction(principal, "quarantine.raw.delete", quarantineRecordsCollection+"/"+id, "success", map[string]string{"record_revision": strconv.Itoa(doc.Revision)})
	jsonOK(w, map[string]any{"record_id": id, "raw_available": false, "deleted": true})
}

// handleQuarantineAnalyze runs the production parser against the exact
// encrypted sample retained for a quarantine record. It creates an audit
// event but never changes parser state or resolves the record.
func (s *Server) handleQuarantineAnalyze(w http.ResponseWriter, r *http.Request, rawID string) {
	principal, err := s.security.AuthorizeRequest(r, middleware.PermissionReadLogs)
	if err != nil {
		jsonError(w, "logs:read permission required", http.StatusForbidden)
		return
	}
	if _, err = s.security.AuthorizeRequest(r, middleware.PermissionParsePreview); err != nil {
		jsonError(w, "parsers:preview permission required", http.StatusForbidden)
		return
	}
	id := safeID(rawID)
	if id == "" {
		jsonError(w, "invalid record ID", http.StatusBadRequest)
		return
	}
	if err := s.expireQuarantineRaw(r.Context(), time.Now().UTC()); err != nil {
		jsonError(w, "quarantine retention status unavailable", http.StatusServiceUnavailable)
		return
	}
	record, _, err := s.getQuarantineRecord(r.Context(), id)
	if err != nil {
		jsonError(w, "quarantine record not found", http.StatusNotFound)
		return
	}
	rawDoc, err := s.quarantine.store.Get(r.Context(), quarantineRawCollection, id, 0)
	if err != nil {
		jsonError(w, "exact raw was not retained or has expired", http.StatusNotFound)
		return
	}
	var sealed sealedQuarantineRaw
	if json.Unmarshal(rawDoc.Payload, &sealed) != nil {
		jsonError(w, "retained raw record is invalid", http.StatusInternalServerError)
		return
	}
	plain, err := s.quarantine.open(id, sealed)
	if err != nil {
		jsonError(w, "retained raw record could not be verified", http.StatusInternalServerError)
		return
	}
	var request assuranceRequest
	if r.Body != nil && r.ContentLength != 0 {
		if decodeJSON(r, &request, 64<<10) != nil {
			jsonError(w, "invalid analysis options", http.StatusBadRequest)
			return
		}
	}
	request.Raw = string(plain)
	if request.Service == "" || request.Service == "auto" {
		request.Service = record.Service
	}
	result := s.runAssurance(request)
	result["line_endings"] = assuranceLineEndingCounts(string(plain))
	if err := s.appendQuarantineHistory(r.Context(), quarantineHistoryEvent{Timestamp: time.Now().UTC(), Action: "failure_analyzed", RecordID: id, NodeID: record.NodeID, Status: record.Status, Message: "Exact retained raw analyzed with the selected production parser; no parser state changed."}, principal.Username); err != nil {
		jsonError(w, "analysis completed but history could not be written", http.StatusServiceUnavailable)
		return
	}
	s.auditAction(principal, "quarantine.analyze", quarantineRecordsCollection+"/"+id, "success", map[string]string{"sha256": sealed.SHA256, "record_revision": strconv.Itoa(record.AttemptCount)})
	jsonOK(w, map[string]any{"record": record, "result": result, "raw_sha256": sealed.SHA256, "review_state": "analysis_only", "parser_changed": false})
}

func (s *Server) handleQuarantineExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	principal, err := s.security.AuthorizeRequest(r, middleware.PermissionReadLogs)
	if err != nil {
		jsonError(w, "logs:read permission required", http.StatusForbidden)
		return
	}
	format := r.URL.Query().Get("format")
	if format != "raw" && format != "jsonl" {
		jsonError(w, "format must be raw or jsonl", http.StatusBadRequest)
		return
	}
	if err := s.expireQuarantineRaw(r.Context(), time.Now().UTC()); err != nil {
		jsonError(w, "quarantine retention status unavailable", http.StatusServiceUnavailable)
		return
	}
	records, err := s.listQuarantine(r.Context(), 1000, 0, "", "")
	if err != nil {
		jsonError(w, "quarantine export unavailable", http.StatusServiceUnavailable)
		return
	}
	var output []byte
	for _, record := range records {
		var raw []byte
		if record.RawAvailable {
			if doc, getErr := s.quarantine.store.Get(r.Context(), quarantineRawCollection, record.ID, 0); getErr == nil {
				var sealed sealedQuarantineRaw
				if json.Unmarshal(doc.Payload, &sealed) == nil {
					raw, _ = s.quarantine.open(record.ID, sealed)
				}
			}
		}
		if format == "raw" {
			if len(raw) > 0 {
				output = append(output, raw...)
				output = append(output, '\n')
			}
			if len(output) > 8<<20 {
				jsonError(w, "quarantine export exceeds 8 MiB response limit", http.StatusRequestEntityTooLarge)
				return
			}
			continue
		}
		item := map[string]any{"id": record.ID, "node_id": record.NodeID, "service": record.Service, "hash": record.Hash, "first_seen": record.FirstSeen, "last_attempt": record.LastAttempt, "attempt_count": record.AttemptCount, "status": record.Status, "error_codes": record.ErrorCodes, "parser_revision": record.ParserRevision, "source_path": record.SourcePath, "source_line": record.SourceLine, "raw_available": record.RawAvailable}
		if len(raw) > 0 {
			item["raw"] = string(raw)
		}
		encoded, _ := json.Marshal(item)
		output = append(output, encoded...)
		output = append(output, '\n')
		if len(output) > 8<<20 {
			jsonError(w, "quarantine export exceeds 8 MiB response limit", http.StatusRequestEntityTooLarge)
			return
		}
	}
	filename, contentType := "nli-quarantine-raw.log", "text/plain; charset=utf-8"
	if format == "jsonl" {
		filename, contentType = "nli-quarantine.jsonl", "application/x-ndjson"
	}
	s.auditAction(principal, "quarantine.export", quarantineRecordsCollection, "success", map[string]string{"format": format, "records": strconv.Itoa(len(records))})
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output)
}

func (s *Server) handleQuarantineHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	limit, err := queryBound(r, "limit", 100, 1, 1000)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	docs, err := s.quarantine.store.List(r.Context(), quarantineHistoryCollection, metastore.Query{Limit: 1000})
	if err != nil {
		jsonError(w, "quarantine history unavailable", http.StatusServiceUnavailable)
		return
	}
	events := make([]quarantineHistoryEvent, 0, len(docs))
	for _, doc := range docs {
		var event quarantineHistoryEvent
		if json.Unmarshal(doc.Payload, &event) == nil {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Timestamp.After(events[j].Timestamp) })
	if len(events) > limit {
		events = events[:limit]
	}
	jsonOK(w, map[string]any{"events": events, "count": len(events), "bounded": true})
}

func (s *Server) appendQuarantineHistory(ctx context.Context, event quarantineHistoryEvent, actor string) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	event.ID = fmt.Sprintf("h-%d", event.Timestamp.UnixNano())
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	docs, err := s.quarantine.store.List(ctx, quarantineHistoryCollection, metastore.Query{Limit: 1000})
	if err != nil {
		return err
	}
	if len(docs) >= 1000 {
		sort.Slice(docs, func(i, j int) bool { return docs[i].CreatedAt.Before(docs[j].CreatedAt) })
		for _, doc := range docs[:len(docs)-999] {
			if err := s.quarantine.store.Delete(ctx, quarantineHistoryCollection, doc.ID, true, "controller"); err != nil {
				return err
			}
		}
	}
	if _, err = s.quarantine.store.Put(ctx, metastore.Document{Collection: quarantineHistoryCollection, ID: event.ID, State: event.Status, Payload: payload, CreatedBy: actor}); err != nil {
		return err
	}
	return nil
}

func (s *Server) handleQuarantineReprocess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	principal := middleware.GetSecurityContext(r)
	if principal == nil {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var request struct {
		RecordIDs []string `json:"record_ids"`
		NodeID    string   `json:"node_id"`
	}
	if decodeJSON(r, &request, 64<<10) != nil || len(request.RecordIDs) == 0 || len(request.RecordIDs) > 1000 || safeID(request.NodeID) == "" {
		jsonError(w, "node_id and 1..1000 record_ids are required", http.StatusBadRequest)
		return
	}
	node, ok := s.fleet.Get(request.NodeID)
	if !ok || node == nil {
		jsonError(w, "target node is not enrolled", http.StatusNotFound)
		return
	}
	unique := map[string]bool{}
	for _, id := range request.RecordIDs {
		id = safeID(id)
		if id == "" || unique[id] {
			continue
		}
		unique[id] = true
		record, _, err := s.getQuarantineRecord(r.Context(), id)
		if err != nil {
			jsonError(w, "one or more quarantine records do not exist", http.StatusNotFound)
			return
		}
		if record.NodeID != request.NodeID {
			jsonError(w, "all records must belong to the target node", http.StatusConflict)
			return
		}
		if record.Status != "waiting" && record.Status != "replay_failed" {
			jsonError(w, "only waiting or replay-failed records can be replayed", http.StatusConflict)
			return
		}
		if record.Status == "dead_letter" || record.Status == "manual_attention" || record.AttemptCount >= 5 {
			jsonError(w, "record reached the five-attempt retry limit and needs manual attention", http.StatusConflict)
			return
		}
		if !record.RetryAfter.IsZero() && time.Now().Before(record.RetryAfter) {
			jsonError(w, "record retry backoff has not elapsed", http.StatusTooManyRequests)
			return
		}
	}
	// Replay can mutate production records, so the target must first report
	// applying the exact currently published parser bundle. A rollout proposal
	// or controller-side publish alone is not an applied acknowledgement.
	bundle, bundleErr := s.buildActiveParserBundle(r.Context())
	if bundleErr != nil {
		jsonError(w, "current parser bundle is unavailable; replay is gated", http.StatusServiceUnavailable)
		return
	}
	syncDoc, syncErr := s.metadata.Get(r.Context(), "fleet-sync", safeID(request.NodeID), 0)
	syncState := map[string]any{}
	if syncErr == nil {
		_ = json.Unmarshal(syncDoc.Payload, &syncState)
	}
	if bundle.BundleHash == "" || fmt.Sprint(syncState["desired_asset_bundle_hash"]) != bundle.BundleHash || fmt.Sprint(syncState["active_asset_bundle_hash"]) != bundle.BundleHash || fmt.Sprint(syncState["asset_sync_status"]) != "applied" {
		jsonError(w, "target client has not acknowledged the current published parser bundle; replay remains gated", http.StatusConflict)
		return
	}
	if len(unique) == 0 {
		jsonError(w, "record_ids contains no valid IDs", http.StatusBadRequest)
		return
	}
	ids := make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	payload := map[string]any{"node_id": request.NodeID, "record_ids": ids, "requested_at": time.Now().UTC(), "mode": "quarantine_file_replay", "success_semantics": "awaiting_per_record_parser_confirmation"}
	rollout, err := s.rollouts.Create(rolloutTypeReplay, "Replay quarantined client records", principal.Username, []string{request.NodeID}, payload)
	if err != nil {
		jsonError(w, "could not create replay rollout", http.StatusInternalServerError)
		return
	}
	if err = s.rollouts.Start(rollout.ID, []string{request.NodeID}); err != nil {
		jsonError(w, "could not start replay rollout", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	for _, id := range ids {
		record, doc, getErr := s.getQuarantineRecord(r.Context(), id)
		if getErr != nil {
			continue
		}
		record.Status = "reprocessing"
		record.AttemptCount++
		record.LastAttempt = now
		if err := s.putQuarantineRecord(r.Context(), record, doc.CreatedBy); err != nil {
			jsonError(w, "replay started but queue status could not be updated", http.StatusInternalServerError)
			return
		}
		if err := s.appendQuarantineHistory(r.Context(), quarantineHistoryEvent{Timestamp: now, Action: "reprocess_requested", RecordID: id, NodeID: request.NodeID, Status: "reprocessing", Message: "Targeted TypeReplay command queued; parser outcome is pending", RolloutID: rollout.ID}, principal.Username); err != nil {
			jsonError(w, "replay started but history could not be written", http.StatusInternalServerError)
			return
		}
	}
	s.auditAction(principal, "quarantine.reprocess", "rollouts/"+rollout.ID, "success", map[string]string{"node_id": request.NodeID, "records": strconv.Itoa(len(ids))})
	jsonCreated(w, map[string]any{"rollout_id": rollout.ID, "status": rolloutStatusRunning, "node_id": request.NodeID, "record_ids": ids, "message": "Targeted TypeReplay command queued; record-level parser confirmation is pending."})
}

func (s *Server) recordQuarantineReplayResult(ctx context.Context, rolloutID, nodeID, resultStatus, message string) {
	rollout, ok := s.rollouts.Get(rolloutID)
	if !ok || rollout == nil || rollout.Type != rolloutTypeReplay {
		return
	}
	var payload struct {
		RecordIDs []string `json:"record_ids"`
		NodeID    string   `json:"node_id"`
	}
	if json.Unmarshal(rollout.Payload, &payload) != nil || payload.NodeID != nodeID {
		return
	}
	// The agent reports per-record outcomes as a compact JSON object in the
	// command result message. Bound and validate it because this field crosses
	// the authenticated node boundary and is persisted in operator history.
	var outcome struct {
		ResolvedIDs        []string          `json:"resolved_ids"`
		WaitingIDs         []string          `json:"waiting_ids"`
		TelemetryFailedIDs []string          `json:"telemetry_failed_ids"`
		MissingIDs         []string          `json:"missing_ids"`
		ParserRevision     string            `json:"parser_revision,omitempty"`
		ParserRevisions    map[string]string `json:"parser_revisions,omitempty"`
		Replayed           int               `json:"replayed"`
		Resolved           int               `json:"resolved"`
		Waiting            int               `json:"waiting"`
		Counts             struct {
			Resolved int `json:"resolved"`
			Waiting  int `json:"waiting"`
		} `json:"counts"`
	}
	parsed := len(message) <= 64<<10 && json.Unmarshal([]byte(message), &outcome) == nil
	allowed := make(map[string]bool, len(payload.RecordIDs))
	for _, id := range payload.RecordIDs {
		if id = safeID(id); id != "" {
			allowed[id] = true
		}
	}
	resolved, waiting, telemetryFailed, missing := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	if parsed && len(outcome.ResolvedIDs)+len(outcome.WaitingIDs)+len(outcome.TelemetryFailedIDs)+len(outcome.MissingIDs) <= 1000 {
		for _, id := range outcome.ResolvedIDs {
			if id = safeID(id); id != "" && allowed[id] {
				resolved[id] = true
			}
		}
		for _, id := range outcome.WaitingIDs {
			if id = safeID(id); id != "" && allowed[id] && !resolved[id] {
				waiting[id] = true
			}
		}
		for _, id := range outcome.TelemetryFailedIDs {
			if id = safeID(id); id != "" && allowed[id] && !resolved[id] {
				telemetryFailed[id] = true
			}
		}
		for _, id := range outcome.MissingIDs {
			if id = safeID(id); id != "" && allowed[id] && !resolved[id] {
				missing[id] = true
			}
		}
	} else {
		resolved, waiting, telemetryFailed, missing = map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	}
	now := time.Now().UTC()
	for _, id := range payload.RecordIDs {
		record, doc, err := s.getQuarantineRecord(ctx, id)
		if err != nil {
			continue
		}
		status, detail := "waiting", "Replay completed without a valid per-record outcome; record remains queued for review."
		switch {
		case resultStatus == rolloutStatusFailed:
			status, detail = "replay_failed", "Replay command failed."
		case resolved[id]:
			status, detail = "resolved", "Agent confirmed this record parsed successfully during replay."
		case waiting[id]:
			status, detail = "waiting", "Agent replayed this record but it remains unparsed."
		case telemetryFailed[id]:
			status, detail = "waiting", "Agent replayed this record but telemetry delivery failed; parsing status needs retry."
		case missing[id]:
			status, detail = "waiting", "Agent no longer has the quarantined record; it remains unresolved for operator review."
		case parsed:
			detail = "Agent outcome omitted this record; it remains queued for review."
		}
		record.Status, record.LastAttempt = status, now
		if status != "resolved" {
			if record.AttemptCount >= 5 {
				record.Status = "dead_letter"
				detail += " Retry limit reached; manual attention is required."
			} else {
				record.RetryAfter = now.Add(quarantineRetryBackoff(record.AttemptCount))
			}
		} else {
			record.RetryAfter = time.Time{}
		}
		if parsed && outcome.ParserRevisions != nil {
			if revision := outcome.ParserRevisions[id]; len(revision) <= 128 && revision != "" {
				record.ParserRevision = revision
			}
		}
		if parsed && outcome.ParserRevision != "" && len(outcome.ParserRevision) <= 128 {
			record.ParserRevision = outcome.ParserRevision
		}
		_ = s.putQuarantineRecord(ctx, record, doc.CreatedBy)
		_ = s.appendQuarantineHistory(ctx, quarantineHistoryEvent{Timestamp: now, Action: "reprocess_result", RecordID: id, NodeID: nodeID, Status: status, Message: detail, RolloutID: rolloutID}, "node:"+nodeID)
	}
}

func quarantineRetryBackoff(attempt int) time.Duration {
	// Attempts count the initial parse, then bounded replays. Backoff grows
	// exponentially and is capped at one hour; the fifth attempt dead-letters.
	switch attempt {
	case 0, 1, 2:
		return time.Minute
	case 3:
		return 5 * time.Minute
	case 4:
		return 15 * time.Minute
	default:
		return time.Hour
	}
}
