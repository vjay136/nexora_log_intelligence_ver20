package controlplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/auditlog"
	"nexora.local/nli-v20/internal/auth"
	"nexora.local/nli-v20/internal/engine"
	"nexora.local/nli-v20/internal/fleet"
	"nexora.local/nli-v20/internal/middleware"
	"nexora.local/nli-v20/internal/protocol"
	metastore "nexora.local/nli-v20/internal/store"
	"nexora.local/nli-v20/telemetry"
)

var semverPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
var knownCatalogKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)
var clickhouseColumnPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func documentView(doc metastore.Document) map[string]any {
	var payload any = map[string]any{}
	_ = json.Unmarshal(doc.Payload, &payload)
	return map[string]any{"id": doc.ID, "revision": doc.Revision, "state": doc.State, "sha256": doc.Hash, "created_by": doc.CreatedBy, "created_at": doc.CreatedAt, "updated_at": doc.UpdatedAt, "payload": payload}
}
func listDocuments(r *http.Request, store metastore.Store, collection string) ([]map[string]any, error) {
	state := r.URL.Query().Get("state")
	limit := 100
	if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed > 0 && parsed <= 1000 {
		limit = parsed
	}
	documents, err := store.List(r.Context(), collection, metastore.Query{State: state, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(documents))
	for _, doc := range documents {
		out = append(out, documentView(doc))
	}
	return out, nil
}
func unsafeDeclarative(value any) error {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			lower := strings.ToLower(key)
			for _, blocked := range []string{"command", "shell", "exec", "script", "binary", "private_key", "secret"} {
				if lower == blocked || strings.HasSuffix(lower, "_"+blocked) {
					return fmt.Errorf("unsafe executable or secret field %q", key)
				}
			}
			if lower == "pattern" || lower == "regex" {
				if text, ok := child.(string); ok {
					if len(text) > 4096 {
						return errors.New("regex exceeds 4096 bytes")
					}
					if _, err := regexp.Compile(text); err != nil {
						return fmt.Errorf("invalid RE2-compatible regex: %w", err)
					}
				}
			}
			if err := unsafeDeclarative(child); err != nil {
				return err
			}
		}
	case []any:
		if len(item) > 1000 {
			return errors.New("declarative array exceeds limit")
		}
		for _, child := range item {
			if err := unsafeDeclarative(child); err != nil {
				return err
			}
		}
	case string:
		if len(item) > 1<<20 {
			return errors.New("declarative string exceeds limit")
		}
	}
	return nil
}

func (s *Server) handleParserPacks(w http.ResponseWriter, r *http.Request) {
	permission := middleware.PermissionReadParsers
	if r.Method != http.MethodGet {
		permission = middleware.PermissionManageParsers
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := listDocuments(r, s.metadata, "parser-packs")
		if err != nil {
			jsonError(w, "parser catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		jsonOK(w, map[string]any{"parser_packs": items, "count": len(items)})
	case http.MethodPost:
		var input map[string]any
		if err := decodeJSON(r, &input, 2<<20); err != nil {
			jsonError(w, "invalid parser pack", http.StatusBadRequest)
			return
		}
		id := safeID(fmt.Sprint(input["id"]))
		name := strings.TrimSpace(fmt.Sprint(input["name"]))
		version := strings.TrimSpace(fmt.Sprint(input["version"]))
		priority, ok := input["priority"].(float64)
		if id == "" || name == "" || !semverPattern.MatchString(version) || !ok || priority < 1 || priority > 1e9 {
			jsonError(w, "id, name, semantic version and positive priority are required", http.StatusBadRequest)
			return
		}
		if err := unsafeDeclarative(input); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		input["review_state"] = "draft"
		input["activated"] = false
		payload, _ := json.Marshal(input)
		saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "parser-packs", ID: id, State: "draft", Payload: payload, CreatedBy: principal.Username})
		if err != nil {
			jsonError(w, "parser persistence failed", http.StatusInternalServerError)
			return
		}
		s.auditAction(principal, "parser.create", "parser-packs/"+id, "success", map[string]string{"revision": fmt.Sprint(saved.Revision)})
		jsonCreated(w, documentView(saved))
	default:
		jsonError(w, "GET or POST required", http.StatusMethodNotAllowed)
	}
}
func (s *Server) handleParserPack(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/parser-packs/"), "/")
	parts := strings.Split(path, "/")
	id := parts[0]
	if id == "" {
		jsonError(w, "parser pack id required", http.StatusBadRequest)
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if action != "" {
		s.handleParserAction(w, r, id, action)
		return
	}
	permission := middleware.PermissionReadParsers
	if r.Method != http.MethodGet {
		permission = middleware.PermissionManageParsers
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		doc, err := s.metadata.Get(r.Context(), "parser-packs", id, 0)
		if err != nil {
			jsonError(w, "parser pack not found", http.StatusNotFound)
			return
		}
		jsonOK(w, documentView(doc))
	case http.MethodPut:
		current, err := s.metadata.Get(r.Context(), "parser-packs", id, 0)
		if err != nil {
			jsonError(w, "parser pack not found", http.StatusNotFound)
			return
		}
		if current.State != "draft" && current.State != "rejected" {
			jsonError(w, "only draft or rejected parser packs may be edited", http.StatusConflict)
			return
		}
		var input map[string]any
		if err := decodeJSON(r, &input, 2<<20); err != nil || unsafeDeclarative(input) != nil {
			jsonError(w, "invalid declarative parser pack", http.StatusBadRequest)
			return
		}
		input["review_state"] = "draft"
		payload, _ := json.Marshal(input)
		saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "parser-packs", ID: id, State: "draft", Payload: payload, CreatedBy: current.CreatedBy})
		if err != nil {
			jsonError(w, "parser update failed", http.StatusInternalServerError)
			return
		}
		s.auditAction(principal, "parser.update", "parser-packs/"+id, "success", nil)
		jsonOK(w, documentView(saved))
	case http.MethodDelete:
		if err := s.metadata.Delete(r.Context(), "parser-packs", id, false, principal.Username); err != nil {
			jsonError(w, "parser deletion failed", http.StatusInternalServerError)
			return
		}
		s.auditAction(principal, "parser.delete", "parser-packs/"+id, "success", nil)
		jsonOK(w, map[string]string{"status": "deleted"})
	default:
		jsonError(w, "unsupported method", http.StatusMethodNotAllowed)
	}
}
func (s *Server) handleParserAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	permission := middleware.PermissionManageParsers
	if action == "approve" || action == "reject" {
		permission = middleware.PermissionReviewParsers
	}
	if action == "activate" {
		permission = middleware.PermissionTriggerRollout
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	doc, err := s.metadata.Get(r.Context(), "parser-packs", id, 0)
	if err != nil {
		jsonError(w, "parser pack not found", http.StatusNotFound)
		return
	}
	next := ""
	switch action {
	case "submit":
		if doc.State != "draft" && doc.State != "rejected" {
			jsonError(w, "parser pack is not editable", http.StatusConflict)
			return
		}
		next = "in_review"
	case "approve":
		if doc.State != "in_review" {
			jsonError(w, "parser pack is not awaiting review", http.StatusConflict)
			return
		}
		if doc.CreatedBy == principal.Username {
			jsonError(w, "creator cannot approve own parser pack", http.StatusConflict)
			return
		}
		next = "approved"
	case "reject":
		if doc.State != "in_review" {
			jsonError(w, "parser pack is not awaiting review", http.StatusConflict)
			return
		}
		next = "rejected"
	case "activate":
		if doc.State != "approved" {
			jsonError(w, "only approved parser packs can activate", http.StatusConflict)
			return
		}
		next = "active"
	default:
		jsonError(w, "unknown parser action", http.StatusNotFound)
		return
	}
	var payload map[string]any
	_ = json.Unmarshal(doc.Payload, &payload)
	payload["review_state"] = next
	payload["activated"] = next == "active"
	payload["reviewed_by"] = principal.Username
	updated, _ := json.Marshal(payload)
	saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "parser-packs", ID: id, State: next, Payload: updated, CreatedBy: doc.CreatedBy})
	if err != nil {
		jsonError(w, "parser transition failed", http.StatusInternalServerError)
		return
	}
	s.auditAction(principal, "parser."+action, "parser-packs/"+id, "success", map[string]string{"state": next})
	jsonOK(w, documentView(saved))
}

func (s *Server) handleConfiguration(w http.ResponseWriter, r *http.Request, collection string) {
	permission := middleware.PermissionReadParsers
	if r.Method != http.MethodGet {
		permission = middleware.PermissionManageParsers
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := listDocuments(r, s.metadata, collection)
		if err != nil {
			jsonError(w, "configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		jsonOK(w, map[string]any{"items": items, "count": len(items)})
	case http.MethodPost, http.MethodPut:
		var input struct {
			ID     string         `json:"id"`
			Name   string         `json:"name"`
			Config map[string]any `json:"config"`
		}
		if err := decodeJSON(r, &input, 2<<20); err != nil || safeID(input.ID) == "" || input.Name == "" {
			jsonError(w, "id, name and config are required", http.StatusBadRequest)
			return
		}
		if err := validateCatalogConfig(collection, input.Config); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		payload, _ := json.Marshal(map[string]any{"name": input.Name, "config": input.Config})
		saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: collection, ID: safeID(input.ID), State: "draft", Payload: payload, CreatedBy: principal.Username})
		if err != nil {
			jsonError(w, "configuration persistence failed", http.StatusInternalServerError)
			return
		}
		s.auditAction(principal, "config.update", collection+"/"+saved.ID, "success", nil)
		jsonCreated(w, documentView(saved))
	default:
		jsonError(w, "GET, POST or PUT required", http.StatusMethodNotAllowed)
	}
}

// handleConfigurationResource exposes a single revisioned parser asset. The
// collection endpoints remain the list/create surface; item routes provide
// read, edit, delete, and deterministic JSON download for the UI's asset tabs.
func (s *Server) handleConfigurationResource(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/config/"), "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		jsonError(w, "configuration collection and asset id are required", http.StatusBadRequest)
		return
	}
	collection, ok := map[string]string{
		"kv": "kv-config", "static-patterns": "static-patterns", "projections": "projections",
		"templates": "templates", "regex": "regex-patterns", "known-keys": "known-keys", "semantic-extraction": "semantic-extraction",
		"output-destinations": "output-destinations", "input-paths": "input-paths",
	}[parts[0]]
	if !ok {
		jsonError(w, "unknown configuration collection", http.StatusNotFound)
		return
	}
	id := safeID(parts[1])
	if id == "" {
		jsonError(w, "invalid configuration asset id", http.StatusBadRequest)
		return
	}
	action := ""
	if len(parts) > 2 {
		action = parts[2]
	}
	if len(parts) > 3 || (action != "" && action != "download" && action != "submit" && action != "approve" && action != "publish" && action != "rollout") {
		jsonError(w, "unknown configuration asset action", http.StatusNotFound)
		return
	}
	permission := middleware.PermissionReadParsers
	if action == "approve" {
		permission = middleware.PermissionReviewParsers
	} else if action == "publish" {
		permission = middleware.PermissionTriggerRollout
	} else if action == "rollout" {
		permission = middleware.PermissionReadFleet
		if r.Method == http.MethodPost {
			permission = middleware.PermissionTriggerRollout
		}
	} else if r.Method != http.MethodGet && r.Method != http.MethodHead {
		permission = middleware.PermissionManageParsers
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	doc, err := s.metadata.Get(r.Context(), collection, id, 0)
	if err != nil {
		jsonError(w, "configuration asset not found", http.StatusNotFound)
		return
	}
	if action == "rollout" {
		if collection != "input-paths" {
			jsonError(w, "rollout action is supported only for input path profiles", http.StatusNotFound)
			return
		}
		s.handleInputPathProfileRollout(w, r, id)
		return
	}
	if action == "download" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			jsonError(w, "GET required for download", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.json"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodGet {
			_, _ = w.Write(append(append([]byte(nil), doc.Payload...), '\n'))
		}
		return
	}
	if action != "" {
		s.handleParserAssetTransition(w, r, principal, collection, id, action, doc)
		return
	}
	switch r.Method {
	case http.MethodGet:
		jsonOK(w, documentView(doc))
	case http.MethodPut:
		if doc.State != "draft" || doc.Deleted {
			jsonError(w, "only draft assets may be edited", http.StatusConflict)
			return
		}
		var input struct {
			Name   string         `json:"name"`
			Config map[string]any `json:"config"`
		}
		if err := decodeJSON(r, &input, 2<<20); err != nil || strings.TrimSpace(input.Name) == "" {
			jsonError(w, "name and config are required", http.StatusBadRequest)
			return
		}
		if err := validateCatalogConfig(collection, input.Config); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		payload, _ := json.Marshal(map[string]any{"name": strings.TrimSpace(input.Name), "config": input.Config})
		saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: collection, ID: id, State: "draft", Payload: payload, CreatedBy: doc.CreatedBy})
		if err != nil {
			jsonError(w, "configuration persistence failed", http.StatusInternalServerError)
			return
		}
		s.auditAction(principal, "config.update", collection+"/"+id, "success", map[string]string{"revision": fmt.Sprint(saved.Revision)})
		jsonOK(w, documentView(saved))
	case http.MethodDelete:
		if doc.State != "draft" && doc.State != "rejected" {
			jsonError(w, "only draft or rejected assets may be deleted", http.StatusConflict)
			return
		}
		if err := s.metadata.Delete(r.Context(), collection, id, false, principal.Username); err != nil {
			jsonError(w, "configuration deletion failed", http.StatusInternalServerError)
			return
		}
		s.auditAction(principal, "config.delete", collection+"/"+id, "success", nil)
		jsonOK(w, map[string]string{"status": "deleted"})
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		jsonError(w, "GET, PUT or DELETE required", http.StatusMethodNotAllowed)
	}
}

func validateCatalogConfig(collection string, config map[string]any) error {
	if len(config) == 0 {
		return errors.New("config must be a non-empty object")
	}
	if err := unsafeDeclarative(config); err != nil {
		return err
	}
	switch collection {
	case "output-destinations":
		outputs, ok := config["outputs"].([]any)
		if !ok || len(outputs) == 0 || len(outputs) > 16 {
			return errors.New("output profile requires 1 to 16 destinations")
		}
		for index, item := range outputs {
			entry, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("output destination %d must be an object", index+1)
			}
			kind, _ := entry["type"].(string)
			switch kind {
			case "file":
				path, _ := entry["path"].(string)
				template, _ := entry["path_template"].(string)
				if (strings.TrimSpace(path) == "") == (strings.TrimSpace(template) == "") {
					return fmt.Errorf("output destination %d requires exactly one of path or path_template", index+1)
				}
				if path != "" && (!absoluteOutputPath(path) || len(path) > 4096) {
					return fmt.Errorf("output destination %d requires an absolute file path", index+1)
				}
				if template != "" && (!absoluteOutputPath(template) || len(template) > 4096 || strings.Count(template, "{hour_utc}") != 1) {
					return fmt.Errorf("output destination %d path_template must be absolute and contain exactly one {hour_utc} token", index+1)
				}
			case "loki", "clickhouse", "controller":
				endpoint, _ := entry["url"].(string)
				parsed, err := url.ParseRequestURI(endpoint)
				if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
					return fmt.Errorf("output destination %d requires an http(s) URL without embedded credentials or fragment", index+1)
				}
				if kind == "clickhouse" {
					for _, key := range []string{"database", "table"} {
						if value, exists := entry[key]; exists {
							name, ok := value.(string)
							if !ok || !clickhouseColumnPattern.MatchString(name) {
								return fmt.Errorf("output destination %d has invalid ClickHouse %s", index+1, key)
							}
						}
					}
					if mappings, exists := entry["field_columns"]; exists {
						fields, ok := mappings.(map[string]any)
						if !ok || len(fields) > 256 {
							return fmt.Errorf("output destination %d field_columns must be an object with at most 256 mappings", index+1)
						}
						for source, targetValue := range fields {
							target, ok := targetValue.(string)
							if source == "" || !ok || !clickhouseColumnPattern.MatchString(target) {
								return fmt.Errorf("output destination %d has an invalid ClickHouse field mapping", index+1)
							}
						}
					}
				}
				if fallback, ok := entry["fallback_path"].(string); !ok || !absoluteOutputPath(fallback) {
					return fmt.Errorf("output destination %d requires an absolute fallback_path for delivery accounting", index+1)
				}
				if kind == "controller" {
					for _, key := range []string{"token_file", "node_id_file"} {
						value, ok := entry[key].(string)
						if !ok || !absoluteOutputPath(value) {
							return fmt.Errorf("output destination %d requires an absolute %s", index+1, key)
						}
					}
				}
			default:
				return fmt.Errorf("output destination %d type must be file, loki, clickhouse, or controller", index+1)
			}
		}
	case "regex-patterns":
		pattern, ok := config["pattern"].(string)
		if !ok || strings.TrimSpace(pattern) == "" || len(pattern) > 4096 {
			return errors.New("regex asset requires a pattern of 1 to 4096 bytes")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("invalid RE2-compatible regex: %w", err)
		}
	case "input-paths":
		inputs, ok := config["inputs"].([]any)
		if !ok || len(inputs) == 0 || len(inputs) > 256 {
			return errors.New("input path profile requires 1 to 256 inputs")
		}
		families := map[string]bool{"auto": true, "imap": true, "pop": true, "webtop-webmail": true, "webtop": true, "mta": true, "smtp-in": true, "smtp-out": true, "pss": true, "pabd": true, "capd": true}
		seen := map[string]bool{}
		for index, raw := range inputs {
			input, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("input %d must be an object", index+1)
			}
			path, pathOK := input["path"].(string)
			family, familyOK := input["service"].(string)
			if !pathOK || strings.TrimSpace(path) == "" || len(path) > 4096 || strings.ContainsAny(path, "\r\n\x00") || !(strings.HasPrefix(path, "/") || (len(path) > 2 && path[1] == ':' && (path[2] == '/' || path[2] == 92))) {
				return fmt.Errorf("input %d path must be an absolute path", index+1)
			}
			if !familyOK || !families[strings.ToLower(strings.TrimSpace(family))] {
				return fmt.Errorf("input %d service must be auto, imap, pop, webtop-webmail, mta, smtp-in, smtp-out, pss, pabd, or capd", index+1)
			}
			folded := strings.ToLower(path)
			if seen[folded] {
				return fmt.Errorf("duplicate input path %q", path)
			}
			seen[folded] = true
		}
	case "known-keys":
		keys, ok := config["keys"].([]any)
		if !ok || len(keys) == 0 || len(keys) > 10000 {
			return errors.New("known-key asset requires 1 to 10000 keys")
		}
		seen := make(map[string]bool, len(keys))
		for _, item := range keys {
			key, ok := item.(string)
			if !ok || !knownCatalogKeyPattern.MatchString(key) {
				return errors.New("known keys must be identifier-like strings up to 128 bytes")
			}
			folded := strings.ToLower(key)
			if seen[folded] {
				return fmt.Errorf("duplicate known key %q", key)
			}
			seen[folded] = true
		}
	case "semantic-extraction":
		entities, ok := config["entities"].([]any)
		if !ok || len(entities) == 0 || len(entities) > 5 {
			return errors.New("semantic extraction requires 1 to 5 entity types")
		}
		allowed := map[string]bool{"ipv4": true, "ipv6": true, "domain": true, "url": true, "email": true}
		seen := map[string]bool{}
		for _, item := range entities {
			name, ok := item.(string)
			if !ok || !allowed[name] || seen[name] {
				return errors.New("entity types must be unique values from ipv4, ipv6, domain, url, email")
			}
			seen[name] = true
		}
		if decode, exists := config["decode_mime_headers"]; exists {
			if _, ok := decode.(bool); !ok {
				return errors.New("decode_mime_headers must be boolean")
			}
		}
		if maximum, exists := config["max_input_bytes"]; exists {
			value, ok := maximum.(float64)
			if !ok || value < 1 || value > 1048576 {
				return errors.New("max_input_bytes must be between 1 and 1048576")
			}
		}
		if maximum, exists := config["max_header_bytes"]; exists {
			value, ok := maximum.(float64)
			if !ok || value < 1 || value > 65536 {
				return errors.New("max_header_bytes must be between 1 and 65536")
			}
		}
		if enabled, exists := config["enabled_by_default"]; exists {
			if _, ok := enabled.(bool); !ok {
				return errors.New("enabled_by_default must be boolean")
			}
		}
	}
	return nil
}

func absoluteOutputPath(value string) bool {
	// The service runs on Linux; accepting Unix absolute paths also keeps these
	// schema checks valid when a Windows workstation builds or tests the UI.
	return filepath.IsAbs(value) || strings.HasPrefix(value, "/")
}

func (s *Server) handleMigrationReports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	items, err := listDocuments(r, s.metadata, "migration-reports")
	if err != nil {
		jsonError(w, "migration reports unavailable", http.StatusServiceUnavailable)
		return
	}
	jsonOK(w, map[string]any{"reports": items, "count": len(items)})
}
func (s *Server) handleMigrationReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/migration/reports/"), "/")
	doc, err := s.metadata.Get(r.Context(), "migration-reports", id, 0)
	if err != nil {
		jsonError(w, "migration report not found", http.StatusNotFound)
		return
	}
	jsonOK(w, documentView(doc))
}

func (s *Server) handleAIProposals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	items, err := listDocuments(r, s.metadata, "ai-proposals")
	if err != nil {
		jsonError(w, "AI proposal memory unavailable", http.StatusServiceUnavailable)
		return
	}
	jsonOK(w, map[string]any{"proposals": items, "count": len(items), "raw_samples_retained": false})
}

func (s *Server) handleAIProposal(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/assurance/ai-proposals/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		jsonError(w, "AI proposal id is required", http.StatusBadRequest)
		return
	}
	id := safeID(parts[0])
	if id == "" {
		jsonError(w, "invalid AI proposal id", http.StatusBadRequest)
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if action != "" && action != "approve" {
		jsonError(w, "unknown AI proposal action", http.StatusNotFound)
		return
	}
	permission := middleware.PermissionReadParsers
	if action == "approve" {
		permission = middleware.PermissionReviewParsers
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	doc, err := s.metadata.Get(r.Context(), "ai-proposals", id, 0)
	if err != nil {
		jsonError(w, "AI proposal not found", http.StatusNotFound)
		return
	}
	if action == "" {
		if r.Method != http.MethodGet {
			jsonError(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		jsonOK(w, documentView(doc))
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if doc.State != "draft" {
		jsonError(w, "AI proposal is not awaiting review", http.StatusConflict)
		return
	}
	if doc.CreatedBy == principal.Username {
		jsonError(w, "proposal creator cannot approve their own proposal", http.StatusConflict)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(doc.Payload, &payload); err != nil {
		jsonError(w, "stored AI proposal is invalid", http.StatusInternalServerError)
		return
	}
	payload["review_state"] = "approved"
	payload["reviewed_by"] = principal.Username
	encoded, _ := json.Marshal(payload)
	saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "ai-proposals", ID: id, State: "approved", Payload: encoded, CreatedBy: doc.CreatedBy})
	if err != nil {
		jsonError(w, "AI proposal approval failed", http.StatusInternalServerError)
		return
	}
	s.auditAction(principal, "ai-proposal.approve", "ai-proposals/"+id, "success", map[string]string{"state": "approved"})
	event, _ := json.Marshal(map[string]any{"type": "ai_proposal_changed", "id": id, "status": "approved", "reviewed_by": principal.Username, "updated_at": time.Now().UTC()})
	s.hub.Publish(event)
	jsonOK(w, map[string]any{"proposal": documentView(saved), "parser_pack_created": false, "next_step": "Create a versioned parser pack, run regression assurance, then submit it through the parser pack review and activation lifecycle."})
}

func parsePublicKeys(cfg Config) [][]byte {
	values := append([]string(nil), cfg.ArtifactPublicKeys...)
	if env := os.Getenv(cfg.ArtifactPublicKeysEnv); env != "" {
		values = append(values, strings.Split(env, ",")...)
	}
	keys := [][]byte{}
	for _, value := range values {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
		if err == nil && len(decoded) == ed25519.PublicKeySize {
			keys = append(keys, decoded)
		}
	}
	return keys
}
func safeFilename(name string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	base := filepath.Base(normalized)
	if base == "" || base == "." || base != normalized {
		return "", errors.New("unsafe filename")
	}
	return base, nil
}
func artifactLimit(kind string, max int64) int64 {
	switch kind {
	case "config":
		return 10 << 20
	case "rpm":
		if max > 0 {
			return max
		}
	}
	return 0
}
func validateArtifact(kind, name, path string, size int64) error {
	ext := strings.ToLower(filepath.Ext(name))
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 4)
	_, err = io.ReadFull(file, header)
	if err != nil {
		return errors.New("artifact is empty or truncated")
	}
	switch kind {
	case "rpm":
		if ext != ".rpm" || !bytes.Equal(header, []byte{0xed, 0xab, 0xee, 0xdb}) {
			return errors.New("RPM must have .rpm extension and ED AB EE DB magic")
		}
	case "config":
		if ext != ".json" {
			return errors.New("configuration artifacts must be strict JSON")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&value); err != nil {
			return fmt.Errorf("invalid configuration JSON: %w", err)
		}
		if err = unsafeDeclarative(value); err != nil {
			return err
		}
	default:
		return errors.New("artifact type must be rpm or config; parser assets use revisioned metadata APIs")
	}
	return nil
}
func (s *Server) handleArtifactUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	principal := middleware.GetSecurityContext(r)
	if principal == nil {
		principal, _ = s.security.AuthorizeRequest(r, middleware.PermissionUploadArtifact)
	}
	kind := strings.ToLower(r.URL.Query().Get("type"))
	limit := artifactLimit(kind, s.cfg.MaxArtifactBytes)
	if limit == 0 {
		jsonError(w, "artifact type must be rpm or config; parser assets use revisioned metadata APIs", http.StatusBadRequest)
		return
	}
	if len(s.artifactKeys) == 0 {
		jsonError(w, "artifact signing public keys are not configured", http.StatusServiceUnavailable)
		return
	}
	reader, err := r.MultipartReader()
	if err != nil {
		jsonError(w, "multipart/form-data required", http.StatusBadRequest)
		return
	}
	staging := filepath.Join(s.cfg.StateDir, "artifacts", "staging")
	if err = os.MkdirAll(staging, 0750); err != nil {
		jsonError(w, "artifact staging unavailable", http.StatusInternalServerError)
		return
	}
	temp, err := os.CreateTemp(staging, "upload-*.part")
	if err != nil {
		jsonError(w, "artifact staging unavailable", http.StatusInternalServerError)
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	var filename string
	hash := sha256.New()
	var size int64
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			temp.Close()
			jsonError(w, "invalid multipart body", http.StatusBadRequest)
			return
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		filename, err = safeFilename(part.FileName())
		if err != nil {
			temp.Close()
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		size, err = io.Copy(io.MultiWriter(temp, hash), io.LimitReader(part, limit+1))
		part.Close()
		if err != nil || size > limit {
			temp.Close()
			jsonError(w, "artifact exceeds configured size limit", http.StatusRequestEntityTooLarge)
			return
		}
		break
	}
	if err = temp.Sync(); err == nil {
		err = temp.Close()
	}
	if err != nil || filename == "" {
		jsonError(w, "artifact file is required", http.StatusBadRequest)
		return
	}
	digest := hash.Sum(nil)
	expected := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Artifact-SHA256")))
	if expected == "" || !hmac.Equal([]byte(expected), []byte(hex.EncodeToString(digest))) {
		jsonError(w, "matching X-Artifact-SHA256 is required", http.StatusBadRequest)
		return
	}
	signature, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Artifact-Signature"))
	if err != nil || len(signature) != ed25519.SignatureSize {
		jsonError(w, "valid X-Artifact-Signature is required", http.StatusBadRequest)
		return
	}
	verified := false
	for _, key := range s.artifactKeys {
		if ed25519.Verify(ed25519.PublicKey(key), digest, signature) {
			verified = true
			break
		}
	}
	if !verified {
		jsonError(w, "artifact signature verification failed", http.StatusBadRequest)
		return
	}
	if err = validateArtifact(kind, filename, tempPath, size); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := fmt.Sprintf("artifact-%x", digest[:10])
	shaText := hex.EncodeToString(digest)
	objectBackend, objectBucket, objectPath := "local", "", ""
	if s.metadata.Mode() == "supabase" {
		if s.artifactStorage == nil {
			jsonError(w, "Supabase private artifact Storage is not configured", http.StatusServiceUnavailable)
			return
		}
		objectBackend, objectBucket = "supabase-private-storage", "nli-rollout-artifacts"
		objectPath = kind + "/sha256/" + shaText + strings.ToLower(filepath.Ext(filename))
		blob, openErr := os.Open(tempPath)
		if openErr != nil {
			jsonError(w, "artifact read failed", http.StatusInternalServerError)
			return
		}
		mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		err = s.artifactStorage.PutVerified(r.Context(), objectBucket, objectPath, blob, size, mediaType, shaText)
		_ = blob.Close()
		if err != nil {
			jsonError(w, "private artifact upload failed", http.StatusServiceUnavailable)
			return
		}
	} else {
		destinationDir := filepath.Join(s.cfg.StateDir, "artifacts", "immutable")
		if err = os.MkdirAll(destinationDir, 0750); err != nil {
			jsonError(w, "artifact store unavailable", http.StatusInternalServerError)
			return
		}
		destination := filepath.Join(destinationDir, id+strings.ToLower(filepath.Ext(filename)))
		if _, err = os.Stat(destination); errors.Is(err, os.ErrNotExist) {
			if err = os.Rename(tempPath, destination); err != nil {
				jsonError(w, "artifact persistence failed", http.StatusInternalServerError)
				return
			}
		}
	}
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	metadata := map[string]any{"name": filename, "type": kind, "media_type": mediaType, "size": size, "sha256": shaText, "signature_verified": true, "immutable": true, "uploaded_by": principal.Username, "uploaded_at": time.Now().UTC(), "status": "validated", "storage_backend": objectBackend, "storage_bucket": objectBucket, "storage_path": objectPath}
	payload, _ := json.Marshal(metadata)
	saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "artifacts", ID: id, State: "validated", Payload: payload, CreatedBy: principal.Username})
	if err != nil {
		jsonError(w, "artifact metadata failed", http.StatusInternalServerError)
		return
	}
	s.auditAction(principal, "artifact.upload", "artifacts/"+id, "success", map[string]string{"sha256": hex.EncodeToString(digest), "type": kind})
	jsonCreated(w, documentView(saved))
}
func (s *Server) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	if s.authorize(w, r, middleware.PermissionReadArtifacts) == nil {
		return
	}
	items, err := listDocuments(r, s.metadata, "artifacts")
	if err != nil {
		jsonError(w, "artifact catalog unavailable", http.StatusServiceUnavailable)
		return
	}
	jsonOK(w, map[string]any{"artifacts": items, "count": len(items), "signing_keys_configured": len(s.artifactKeys)})
}
func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/artifacts/"), "/")
	parts := strings.Split(path, "/")
	id := parts[0]
	if id == "" {
		jsonError(w, "artifact id required", http.StatusBadRequest)
		return
	}
	if len(parts) > 1 && parts[1] == "download" {
		s.downloadArtifact(w, r, id)
		return
	}
	if s.authorize(w, r, middleware.PermissionReadArtifacts) == nil {
		return
	}
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	doc, err := s.metadata.Get(r.Context(), "artifacts", id, 0)
	if err != nil {
		jsonError(w, "artifact not found", http.StatusNotFound)
		return
	}
	jsonOK(w, documentView(doc))
}
func (s *Server) downloadArtifact(w http.ResponseWriter, r *http.Request, id string) {
	authorized := s.validArtifactDownload(id, r)
	if !authorized {
		if node := r.URL.Query().Get("node_id"); node != "" {
			authorized = s.authorizeNode(r, node)
		} else {
			_, err := s.security.AuthorizeRequest(r, middleware.PermissionReadArtifacts)
			authorized = err == nil
		}
	}
	if !authorized {
		jsonError(w, "authorization required", http.StatusUnauthorized)
		return
	}
	doc, err := s.metadata.Get(r.Context(), "artifacts", id, 0)
	if err != nil {
		jsonError(w, "artifact not found", http.StatusNotFound)
		return
	}
	var metadata map[string]any
	_ = json.Unmarshal(doc.Payload, &metadata)
	name := fmt.Sprint(metadata["name"])
	var file io.ReadCloser
	var size int64
	if backend, _ := metadata["storage_backend"].(string); backend == "supabase-private-storage" {
		bucket, _ := metadata["storage_bucket"].(string)
		objectPath, _ := metadata["storage_path"].(string)
		if s.artifactStorage == nil {
			jsonError(w, "private artifact storage is unavailable", http.StatusServiceUnavailable)
			return
		}
		file, size, err = s.artifactStorage.Open(r.Context(), bucket, objectPath)
	} else {
		localPath := filepath.Join(s.cfg.StateDir, "artifacts", "immutable", id+strings.ToLower(filepath.Ext(name)))
		file, err = os.Open(localPath)
		if err == nil {
			info, statErr := file.(*os.File).Stat()
			if statErr != nil {
				_ = file.Close()
				err = statErr
			} else {
				size = info.Size()
			}
		}
	}
	if err != nil {
		jsonError(w, "artifact content unavailable", http.StatusNotFound)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(name)))
	mediaType, _ := metadata["media_type"].(string)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mediaType)
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if size <= 0 {
		size = 512 << 20
	}
	_, _ = io.Copy(w, io.LimitReader(file, size+1))
}

func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	principal := middleware.GetSecurityContext(r)
	if principal == nil {
		principal, _ = s.security.AuthorizeRequest(r, middleware.PermissionTriggerRollout)
	}
	var request struct {
		ArtifactID  string   `json:"artifact_id"`
		Name        string   `json:"name"`
		TargetNodes []string `json:"target_nodes"`
	}
	if err := decodeJSON(r, &request, 64<<10); err != nil || request.ArtifactID == "" {
		jsonError(w, "artifact_id is required", http.StatusBadRequest)
		return
	}
	doc, err := s.metadata.Get(r.Context(), "artifacts", request.ArtifactID, 0)
	if err != nil || doc.State != "validated" {
		jsonError(w, "validated artifact not found", http.StatusBadRequest)
		return
	}
	var artifact map[string]any
	_ = json.Unmarshal(doc.Payload, &artifact)
	artifactType := fmt.Sprint(artifact["type"])
	rolloutType := rolloutTypePackageUpdate
	if artifactType == "config" {
		rolloutType = rolloutTypeConfigPush
	} else if artifactType != "rpm" {
		jsonError(w, "rollout requires a signed RPM or JSON configuration artifact", http.StatusBadRequest)
		return
	}
	if request.Name == "" {
		if artifactType == "config" {
			request.Name = "Deploy configuration " + fmt.Sprint(artifact["name"])
		} else {
			request.Name = "Upgrade " + fmt.Sprint(artifact["name"])
		}
	}
	proposal := map[string]any{"name": request.Name, "type": rolloutType, "target_nodes": request.TargetNodes, "artifact_id": request.ArtifactID, "payload": map[string]any{"version": "20.0.0", "artifact_type": artifactType, "download_url": s.signedArtifactURL(r, request.ArtifactID), "checksum": artifact["sha256"], "size": artifact["size"], "media_type": artifact["media_type"]}, "approval_required": true}
	s.createRolloutProposal(w, r, principal, proposal)
}

func (s *Server) signedArtifactURL(r *http.Request, id string) string {
	expires := time.Now().UTC().Add(30 * time.Minute).Unix()
	mac := hmac.New(sha256.New, s.downloadKey)
	fmt.Fprintf(mac, "%s|%d", id, expires)
	signature := hex.EncodeToString(mac.Sum(nil))
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/api/v1/artifacts/%s/download?expires=%d&signature=%s", scheme, r.Host, id, expires, signature)
}

func (s *Server) validArtifactDownload(id string, r *http.Request) bool {
	expires, err := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	if err != nil || expires < time.Now().UTC().Unix() || expires > time.Now().UTC().Add(time.Hour).Unix() {
		return false
	}
	mac := hmac.New(sha256.New, s.downloadKey)
	fmt.Fprintf(mac, "%s|%d", id, expires)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(r.URL.Query().Get("signature")))
}

func (s *Server) handleRollouts(w http.ResponseWriter, r *http.Request) {
	permission := middleware.PermissionReadFleet
	if r.Method != http.MethodGet {
		permission = middleware.PermissionTriggerRollout
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		proposals, err := listDocuments(r, s.metadata, "rollout-proposals")
		if err != nil {
			jsonError(w, "rollout catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		pending, err := s.metadata.List(r.Context(), "rollout-proposals", metastore.Query{State: "pending_approval", Limit: 1000})
		if err != nil {
			jsonError(w, "rollout approval count unavailable", http.StatusServiceUnavailable)
			return
		}
		jsonOK(w, map[string]any{"proposals": proposals, "pending_approval_count": len(pending), "live": s.rollouts.List()})
	case http.MethodPost:
		var proposal map[string]any
		if err := decodeJSON(r, &proposal, 2<<20); err != nil {
			jsonError(w, "invalid rollout proposal", http.StatusBadRequest)
			return
		}
		s.createRolloutProposal(w, r, principal, proposal)
	default:
		jsonError(w, "GET or POST required", http.StatusMethodNotAllowed)
	}
}
func (s *Server) createRolloutProposal(w http.ResponseWriter, r *http.Request, principal *middleware.SecurityContext, proposal map[string]any) {
	name := strings.TrimSpace(fmt.Sprint(proposal["name"]))
	kind := fmt.Sprint(proposal["type"])
	allowed := map[string]bool{rolloutTypeConfigPush: true, rolloutTypePackageUpdate: true, rolloutTypeParserSync: true, rolloutTypeRestart: true, rolloutTypeReplay: true}
	if name == "" || !allowed[kind] {
		jsonError(w, "valid rollout name and type are required", http.StatusBadRequest)
		return
	}
	proposal["status"] = "pending_approval"
	proposal["created_by"] = principal.Username
	proposal["created_at"] = time.Now().UTC()
	proposal["approval_required"] = true
	payload, _ := json.Marshal(proposal)
	id := fmt.Sprintf("proposal-%d", time.Now().UnixNano())
	saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "rollout-proposals", ID: id, State: "pending_approval", Payload: payload, CreatedBy: principal.Username})
	if err != nil {
		jsonError(w, "rollout proposal failed", http.StatusInternalServerError)
		return
	}
	s.auditAction(principal, "rollout.propose", "rollout-proposals/"+id, "success", nil)
	jsonCreated(w, documentView(saved))
}
func (s *Server) handleRollout(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/rollouts/"), "/")
	parts := strings.Split(path, "/")
	id := parts[0]
	if id == "" {
		jsonError(w, "rollout id required", http.StatusBadRequest)
		return
	}
	if len(parts) > 1 && parts[1] == "approve" {
		s.approveRollout(w, r, id)
		return
	}
	if len(parts) > 1 && parts[1] == "cancel" {
		s.cancelRollout(w, r, id)
		return
	}
	if s.authorize(w, r, middleware.PermissionReadFleet) == nil {
		return
	}
	if doc, err := s.metadata.Get(r.Context(), "rollout-proposals", id, 0); err == nil {
		jsonOK(w, documentView(doc))
		return
	}
	if item, ok := s.rollouts.Get(id); ok {
		jsonOK(w, item)
		return
	}
	jsonError(w, "rollout not found", http.StatusNotFound)
}
func interfaceStrings(value any) []string {
	items, _ := value.([]any)
	out := []string{}
	for _, item := range items {
		if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
			out = append(out, text)
		}
	}
	return out
}
func (s *Server) approveRollout(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	principal := s.authorize(w, r, middleware.PermissionApproveRollout)
	if principal == nil {
		return
	}
	doc, err := s.metadata.Get(r.Context(), "rollout-proposals", id, 0)
	if err != nil {
		jsonError(w, "proposal not found", http.StatusNotFound)
		return
	}
	if doc.State != "pending_approval" {
		jsonError(w, "proposal is not awaiting approval", http.StatusConflict)
		return
	}
	if doc.CreatedBy == principal.Username {
		jsonError(w, "creator cannot approve own rollout", http.StatusConflict)
		return
	}
	var proposal map[string]any
	_ = json.Unmarshal(doc.Payload, &proposal)
	targets := interfaceStrings(proposal["target_nodes"])
	var parserBundle *parserAssetBundle
	var parserRolloutPayload any
	if fmt.Sprint(proposal["type"]) == rolloutTypeParserSync {
		// Decode the parser snapshot directly from the stored JSON. Going through
		// map[string]any first converts numbers inside asset payloads to float64
		// and re-encodes the RawMessage bytes, which can change the bundle digest
		// (for example, 1.0 becomes 1) and can mutate the payload sent to clients.
		var syncProposal struct {
			BundleHash string `json:"bundle_hash"`
			Payload    struct {
				Bundle     parserAssetBundle `json:"asset_bundle"`
				BundleHash string            `json:"bundle_hash"`
			} `json:"payload"`
		}
		if json.Unmarshal(doc.Payload, &syncProposal) != nil || syncProposal.BundleHash == "" || syncProposal.Payload.BundleHash != syncProposal.BundleHash || syncProposal.Payload.Bundle.BundleHash != syncProposal.BundleHash {
			jsonError(w, "parser sync proposal has no exact bundle snapshot", http.StatusConflict)
			return
		}
		hash, _, hashErr := bundleDigest(syncProposal.Payload.Bundle)
		if hashErr != nil || hash != syncProposal.BundleHash {
			jsonError(w, "parser sync proposal bundle hash is invalid", http.StatusConflict)
			return
		}
		current, currentErr := s.buildActiveParserBundle(r.Context())
		if currentErr != nil || current.BundleHash != syncProposal.BundleHash {
			jsonError(w, "parser sync proposal is stale; create a rollout for the current published bundle", http.StatusConflict)
			return
		}
		parserBundle = &syncProposal.Payload.Bundle
		parserRolloutPayload = map[string]any{"asset_bundle": syncProposal.Payload.Bundle, "bundle_hash": syncProposal.BundleHash}
	}
	if len(targets) == 0 {
		for _, node := range s.fleet.All() {
			if node.Status != fleet.StatusOffline {
				targets = append(targets, node.ID)
			}
		}
	}
	if len(targets) > 1000 {
		jsonError(w, "rollout exceeds the 1000-client target limit", http.StatusConflict)
		return
	}
	seenTargets := map[string]bool{}
	for _, target := range targets {
		node, ok := s.fleet.Get(target)
		if !ok || node == nil || node.Status == fleet.StatusOffline || seenTargets[target] {
			jsonError(w, "rollout contains a missing, offline, or duplicate target", http.StatusConflict)
			return
		}
		seenTargets[target] = true
	}
	if len(targets) == 0 {
		jsonError(w, "rollout has no eligible target nodes", http.StatusConflict)
		return
	}
	payload := proposal["payload"]
	if parserRolloutPayload != nil {
		payload = parserRolloutPayload
	}
	live, err := s.rollouts.Create(fmt.Sprint(proposal["type"]), fmt.Sprint(proposal["name"]), doc.CreatedBy, targets, payload)
	if err == nil {
		err = s.rollouts.Start(live.ID, targets)
	}
	if err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	proposal["status"] = "active"
	proposal["approved_by"] = principal.Username
	proposal["approved_at"] = time.Now().UTC()
	proposal["live_rollout_id"] = live.ID
	updated, _ := json.Marshal(proposal)
	saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "rollout-proposals", ID: id, State: "active", Payload: updated, CreatedBy: doc.CreatedBy})
	if err != nil {
		jsonError(w, "proposal state update failed", http.StatusInternalServerError)
		return
	}
	s.markInputPathRolloutDesired(principal.Username, proposal, targets)
	if parserBundle != nil {
		s.markParserSyncRolloutDesired(r.Context(), principal.Username, doc.ID, *parserBundle, targets)
	}
	s.auditAction(principal, "rollout.approve", "rollout-proposals/"+id, "success", map[string]string{"live_rollout_id": live.ID})
	jsonOK(w, documentView(saved))
}
func (s *Server) cancelRollout(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	principal := s.authorize(w, r, middleware.PermissionCancelRollout)
	if principal == nil {
		return
	}
	doc, err := s.metadata.Get(r.Context(), "rollout-proposals", id, 0)
	if err != nil {
		if live, ok := s.rollouts.Get(id); ok && live != nil {
			if err := s.rollouts.Cancel(id); err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, map[string]string{"status": "cancelled"})
			return
		}
		jsonError(w, "rollout not found", http.StatusNotFound)
		return
	}
	var proposal map[string]any
	_ = json.Unmarshal(doc.Payload, &proposal)
	if liveID := fmt.Sprint(proposal["live_rollout_id"]); liveID != "" && liveID != "<nil>" {
		_ = s.rollouts.Cancel(liveID)
	}
	proposal["status"] = "cancelled"
	proposal["cancelled_by"] = principal.Username
	payload, _ := json.Marshal(proposal)
	saved, _ := s.metadata.Put(r.Context(), metastore.Document{Collection: "rollout-proposals", ID: id, State: "cancelled", Payload: payload, CreatedBy: doc.CreatedBy})
	s.auditAction(principal, "rollout.cancel", "rollout-proposals/"+id, "success", nil)
	jsonOK(w, documentView(saved))
}

func (s *Server) updateSyncFromHeartbeat(request protocol.HeartbeatRequest) {
	doc, err := s.metadata.Get(context.Background(), "fleet-sync", request.NodeID, 0)
	state := map[string]any{"node_id": request.NodeID}
	creator := "node:" + request.NodeID
	if err == nil {
		_ = json.Unmarshal(doc.Payload, &state)
		creator = doc.CreatedBy
	}
	state["active_config_hash"] = request.ConfigHash
	state["active_asset_bundle_hash"] = request.ActiveAssetBundleHash
	state["active_asset_revisions"] = request.ActiveAssetRevisions
	state["last_seen"] = time.Now().UTC()
	desired := fmt.Sprint(state["desired_config_hash"])
	state["drifted"] = desired != "" && desired != request.ConfigHash
	desiredAssetHash := fmt.Sprint(state["desired_asset_bundle_hash"])
	state["asset_drifted"] = desiredAssetHash != "" && desiredAssetHash != request.ActiveAssetBundleHash
	if desiredAssetHash != "" {
		if desiredAssetHash == request.ActiveAssetBundleHash {
			state["asset_sync_status"] = "applied"
			state["asset_sync_error"] = ""
		} else {
			state["asset_sync_status"] = "drifted"
		}
	}
	payload, _ := json.Marshal(state)
	_, _ = s.metadata.Put(context.Background(), metastore.Document{Collection: "fleet-sync", ID: safeID(request.NodeID), State: "reported", Payload: payload, CreatedBy: creator})
}
func (s *Server) handleNodeState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		NodeID            string `json:"node_id"`
		ActivePackHash    string `json:"active_pack_hash"`
		ActiveConfigHash  string `json:"active_config_hash"`
		ActiveReleaseHash string `json:"active_release_hash"`
		StagedPackHash    string `json:"staged_pack_hash,omitempty"`
		Status            string `json:"status"`
	}
	if err := decodeJSON(r, &request, 64<<10); err != nil || !s.authorizeNode(r, request.NodeID) {
		jsonError(w, "unauthorized node state", http.StatusUnauthorized)
		return
	}
	doc, _ := s.metadata.Get(r.Context(), "fleet-sync", safeID(request.NodeID), 0)
	state := map[string]any{"node_id": request.NodeID}
	_ = json.Unmarshal(doc.Payload, &state)
	state["active_pack_hash"] = request.ActivePackHash
	state["active_config_hash"] = request.ActiveConfigHash
	state["active_release_hash"] = request.ActiveReleaseHash
	state["staged_pack_hash"] = request.StagedPackHash
	state["status"] = request.Status
	state["last_sync_at"] = time.Now().UTC()
	state["drifted"] = (fmt.Sprint(state["desired_pack_hash"]) != "" && fmt.Sprint(state["desired_pack_hash"]) != request.ActivePackHash) || (fmt.Sprint(state["desired_config_hash"]) != "" && fmt.Sprint(state["desired_config_hash"]) != request.ActiveConfigHash)
	payload, _ := json.Marshal(state)
	_, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "fleet-sync", ID: safeID(request.NodeID), State: "reported", Payload: payload, CreatedBy: "node:" + request.NodeID})
	if err != nil {
		jsonError(w, "node state persistence failed", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "recorded"})
}
func (s *Server) handleFleetSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	documents, _ := s.metadata.List(r.Context(), "fleet-sync", metastore.Query{Limit: 1000})
	byID := map[string]any{}
	for _, doc := range documents {
		var state map[string]any
		_ = json.Unmarshal(doc.Payload, &state)
		byID[doc.ID] = state
	}
	out := []map[string]any{}
	for _, node := range s.fleet.All() {
		state, _ := byID[safeID(node.ID)].(map[string]any)
		if state == nil {
			state = map[string]any{"node_id": node.ID, "active_config_hash": node.ConfigHash, "drifted": false, "status": "unmanaged"}
		}
		out = append(out, map[string]any{"node": node, "sync": state})
	}
	jsonOK(w, map[string]any{"nodes": out, "count": len(out)})
}
func (s *Server) handleFleetResource(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/fleet/"), "/")
	parts := strings.Split(path, "/")
	nodeID := parts[0]
	if nodeID == "" {
		jsonError(w, "node id required", http.StatusBadRequest)
		return
	}
	if len(parts) > 1 && parts[1] == "desired" {
		principal := s.authorize(w, r, middleware.PermissionManageFleet)
		if principal == nil {
			return
		}
		if r.Method != http.MethodPut {
			jsonError(w, "PUT required", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := s.fleet.Get(nodeID); !ok {
			jsonError(w, "node not found", http.StatusNotFound)
			return
		}
		var desired struct {
			PackHash    string `json:"pack_hash"`
			ConfigHash  string `json:"config_hash"`
			ReleaseHash string `json:"release_hash"`
		}
		if err := decodeJSON(r, &desired, 32<<10); err != nil {
			jsonError(w, "invalid desired state", http.StatusBadRequest)
			return
		}
		doc, _ := s.metadata.Get(r.Context(), "fleet-sync", safeID(nodeID), 0)
		state := map[string]any{"node_id": nodeID}
		_ = json.Unmarshal(doc.Payload, &state)
		state["desired_pack_hash"] = desired.PackHash
		state["desired_config_hash"] = desired.ConfigHash
		state["desired_release_hash"] = desired.ReleaseHash
		state["desired_at"] = time.Now().UTC()
		state["drifted"] = true
		payload, _ := json.Marshal(state)
		saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "fleet-sync", ID: safeID(nodeID), State: "desired", Payload: payload, CreatedBy: principal.Username})
		if err != nil {
			jsonError(w, "desired state persistence failed", http.StatusInternalServerError)
			return
		}
		s.auditAction(principal, "fleet.desired", "fleet/"+nodeID, "success", nil)
		jsonOK(w, documentView(saved))
		return
	}
	if s.authorize(w, r, middleware.PermissionReadFleet) == nil {
		return
	}
	node, ok := s.fleet.Get(nodeID)
	if !ok {
		jsonError(w, "node not found", http.StatusNotFound)
		return
	}
	syncDoc, _ := s.metadata.Get(r.Context(), "fleet-sync", safeID(nodeID), 0)
	jsonOK(w, map[string]any{"node": node, "sync": documentView(syncDoc)})
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	permission := middleware.PermissionReadUsers
	if r.Method != http.MethodGet {
		permission = middleware.PermissionManageUsers
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		jsonOK(w, map[string]any{"users": s.auth.ListUsers(), "count": len(s.auth.ListUsers())})
	case http.MethodPost:
		var request struct {
			Username string `json:"username"`
			Email    string `json:"email"`
			Role     string `json:"role"`
			Password string `json:"password"`
		}
		if err := decodeJSON(r, &request, 16<<10); err != nil {
			jsonError(w, "invalid user", http.StatusBadRequest)
			return
		}
		user, err := s.auth.CreateUser(principal.Role, request.Username, request.Email, request.Role, request.Password)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		publicUser := *user
		publicUser.PasswordHash = ""
		publicUser.Salt = ""
		if err := s.mirrorUserMetadata(r.Context(), user, principal.Username); err != nil {
			s.auditAction(principal, "user.create", "users/"+user.Username, "partial_failure", map[string]string{"role": user.Role, "metadata_sync": "failed"})
			jsonOK(w, map[string]any{"status": "local_created_sync_failed", "retryable": true, "message": "User was created in the controller but Supabase metadata synchronization failed; retry with POST /api/v1/users/{username}/sync.", "user": &publicUser})
			return
		}
		s.auditAction(principal, "user.create", "users/"+user.Username, "success", map[string]string{"role": user.Role})
		jsonCreated(w, &publicUser)
	default:
		jsonError(w, "GET or POST required", http.StatusMethodNotAllowed)
	}
}
func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	principal := s.authorize(w, r, middleware.PermissionManageUsers)
	if principal == nil {
		return
	}
	username := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/users/"), "/")
	if strings.HasSuffix(username, "/sync") {
		if r.Method != http.MethodPost {
			jsonError(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		username = strings.TrimSuffix(username, "/sync")
		var user *auth.User
		for _, candidate := range s.auth.ListUsers() {
			if candidate.Username == username {
				user = candidate
				break
			}
		}
		if user == nil {
			jsonError(w, "user not found", http.StatusNotFound)
			return
		}
		if err := s.mirrorUserMetadata(r.Context(), user, principal.Username); err != nil {
			s.auditAction(principal, "user.metadata_sync", "users/"+username, "failure", nil)
			jsonError(w, "Supabase metadata synchronization failed; retry is safe", http.StatusServiceUnavailable)
			return
		}
		s.auditAction(principal, "user.metadata_sync", "users/"+username, "success", nil)
		jsonOK(w, map[string]any{"status": "synced", "username": username})
		return
	}
	if r.Method != http.MethodDelete {
		jsonError(w, "DELETE required", http.StatusMethodNotAllowed)
		return
	}
	if username == principal.Username {
		jsonError(w, "cannot deactivate current account", http.StatusConflict)
		return
	}
	if err := s.auth.DeactivateUser(principal.Role, username); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	var deactivated *auth.User
	for _, candidate := range s.auth.ListUsers() {
		if candidate.Username == username {
			deactivated = candidate
			break
		}
	}
	if deactivated != nil {
		if err := s.mirrorUserMetadata(r.Context(), deactivated, principal.Username); err != nil {
			s.auditAction(principal, "user.deactivate", "users/"+username, "partial_failure", map[string]string{"metadata_sync": "failed"})
			jsonOK(w, map[string]any{"status": "deactivated_sync_failed", "retryable": true, "username": username, "message": "User is deactivated in the controller but Supabase metadata synchronization failed; retry with POST /api/v1/users/" + username + "/sync."})
			return
		}
	}
	s.auditAction(principal, "user.deactivate", "users/"+username, "success", nil)
	jsonOK(w, map[string]string{"status": "deactivated"})
}

func (s *Server) mirrorUserMetadata(ctx context.Context, user *auth.User, actor string) error {
	if s.metadata.Mode() != "supabase" {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"id": user.ID, "username": user.Username, "email": user.Email, "role": user.Role, "active": user.Active, "created_by": user.CreatedBy, "created_at": user.CreatedAt, "updated_at": time.Now().UTC()})
	if err != nil {
		return err
	}
	_, err = s.metadata.Put(ctx, metastore.Document{Collection: "users", ID: safeID(user.Username), State: map[bool]string{true: "active", false: "inactive"}[user.Active], Payload: payload, CreatedBy: actor})
	return err
}
func (s *Server) handleRoles(w http.ResponseWriter, r *http.Request) {
	roles := map[string]any{}
	for _, role := range auth.Roles() {
		roles[role] = middleware.RolePermissions[role]
	}
	jsonOK(w, map[string]any{"roles": roles})
}
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed > 0 && parsed <= 1000 {
		limit = parsed
	}
	verify := "ok"
	if err := s.audit.Verify(); err != nil {
		verify = "tampered"
	}
	jsonOK(w, map[string]any{"events": s.audit.List(limit), "verification": verify, "immutable": true})
}
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	storageStatus := "ok"
	storageError := ""
	if err := s.metadata.Health(ctx); err != nil {
		storageStatus = "degraded"
		storageError = err.Error()
	}
	auditStatus := "ok"
	if err := s.audit.Verify(); err != nil {
		auditStatus = "tampered"
	}
	artifactStorageMode, artifactStorageReady := "local", true
	if s.metadata.Mode() == "supabase" {
		artifactStorageMode = "supabase-private-storage"
		artifactStorageReady = s.artifactStorage != nil
	}
	quarantineStoreStatus := "ok"
	if s.quarantine == nil || s.quarantine.store == nil || s.quarantine.store.Health(ctx) != nil {
		quarantineStoreStatus = "degraded"
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	hostname, _ := os.Hostname()
	health := "ok"
	if storageStatus != "ok" || auditStatus != "ok" || quarantineStoreStatus != "ok" {
		health = "degraded"
	}
	startedAt := s.startedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	infrastructure := map[string]any{"hostname": hostname, "os": runtime.GOOS, "arch": runtime.GOARCH, "cpu_count": runtime.NumCPU(), "cpu_percent": nil, "cpu_percent_status": telemetry.FreshnessNotInstrumented, "goroutines": runtime.NumGoroutine(), "memory_bytes": memory.Alloc, "memory_sys_bytes": memory.Sys, "started_at": startedAt, "uptime_seconds": time.Since(startedAt).Seconds(), "restart_count": nil, "restart_count_status": telemetry.FreshnessNotInstrumented, "disk_usage_bytes": nil, "disk_usage_status": telemetry.FreshnessNotInstrumented, "network_bytes": nil, "network_status": telemetry.FreshnessNotInstrumented, "edge": s.edgeInfrastructure()}
	if descriptors, err := os.ReadDir("/proc/self/fd"); err == nil {
		infrastructure["open_file_descriptors"] = len(descriptors)
		infrastructure["open_file_descriptors_status"] = "available"
	} else {
		infrastructure["open_file_descriptors_status"] = "unavailable"
	}
	clientMetrics := make([]map[string]any, 0)
	for _, node := range s.fleet.All() {
		if node.Metrics == nil || !node.Metrics.HostMetricsAvailable {
			continue
		}
		metric := node.Metrics
		clientMetrics = append(clientMetrics, map[string]any{"node_id": node.ID, "hostname": node.Hostname, "updated_at": metric.UpdatedAt, "cpu_percent": metric.CPUPercent, "memory_bytes": metric.MemoryBytes, "memory_percent": metric.MemoryPercent, "load_1m": metric.Load1, "load_5m": metric.Load5, "load_15m": metric.Load15})
	}
	jsonOK(w, map[string]any{"version": engine.Version, "health": health, "infrastructure": infrastructure, "client_metrics": clientMetrics, "storage": map[string]any{"mode": s.metadata.Mode(), "status": storageStatus, "error": storageError, "diagnostic": storageDiagnostic(storageError), "warning": s.storageWarning, "credentials_exposed": false}, "artifact_storage": map[string]any{"mode": artifactStorageMode, "ready": artifactStorageReady, "buckets": []string{"nli-assurance-fixtures", "nli-rollout-artifacts", "nli-geoip-databases"}, "private": true}, "retention": map[string]any{"normal_raw": false, "normal_json": false, "assurance_enabled": s.assurance.enabled, "assurance_max": s.assurance.max, "assurance_ttl_hours": int(s.assurance.ttl.Hours()), "quarantine_metadata_mode": "local", "quarantine_metadata_status": quarantineStoreStatus, "quarantine_raw_enabled": s.quarantine != nil && s.quarantine.enabled, "quarantine_raw_max_records": s.cfg.QuarantineRawRetentionMaxRecords, "quarantine_raw_ttl_hours": s.cfg.QuarantineRawRetentionHours, "encrypted": s.assurance.enabled || (s.quarantine != nil && s.quarantine.enabled)}, "security": map[string]any{"roles": auth.Roles(), "audit_chain": auditStatus, "artifact_signing_keys": len(s.artifactKeys), "csrf": true, "strict_content_types": true}, "fleet": s.fleet.Stats()})
}

func (s *Server) edgeInfrastructure() map[string]any {
	result := map[string]any{"manifest_available": false, "certificate_available": false}
	data, err := os.ReadFile(s.cfg.EdgeManifestPath)
	if err != nil {
		return result
	}
	var manifest struct {
		Mode               any      `json:"mode"`
		ProxyService       string   `json:"proxy_service"`
		PublicOrigin       string   `json:"public_origin"`
		ControllerListener string   `json:"controller_listener"`
		EdgeListeners      []string `json:"edge_listeners"`
		TLSCertPath        string   `json:"tls_cert_path"`
		TLSCAPath          string   `json:"tls_ca_path"`
		ConfiguredAt       string   `json:"configured_at"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return result
	}
	result["manifest_available"] = true
	result["mode"], result["proxy_service"], result["public_origin"] = manifest.Mode, manifest.ProxyService, manifest.PublicOrigin
	result["controller_listener"], result["edge_listeners"], result["configured_at"] = manifest.ControllerListener, manifest.EdgeListeners, manifest.ConfiguredAt
	if manifest.TLSCAPath != "" {
		result["tls_ca_configured"] = true
	}
	certPath := manifest.TLSCertPath
	if certPath == "" {
		certPath = s.cfg.TLSCert
	}
	if certPath == "" {
		return result
	}
	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		return result
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return result
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return result
	}
	fingerprint := sha256.Sum256(cert.Raw)
	result["certificate_available"] = true
	result["certificate"] = map[string]any{"subject": cert.Subject.String(), "dns_names": cert.DNSNames, "ip_addresses": cert.IPAddresses, "issuer": cert.Issuer.String(), "not_before": cert.NotBefore.UTC(), "not_after": cert.NotAfter.UTC(), "sha256_fingerprint": fmt.Sprintf("%x", fingerprint)}
	return result
}
func (s *Server) auditAction(principal *middleware.SecurityContext, action, resource, outcome string, details map[string]string) {
	if principal == nil {
		return
	}
	event, err := s.audit.Append(auditlog.Event{Action: action, Actor: principal.Username, Role: principal.Role, Resource: resource, Outcome: outcome, RemoteIP: principal.RemoteIP, Details: details})
	if err == nil {
		s.dispatchAuditNotification(event)
	}
}
