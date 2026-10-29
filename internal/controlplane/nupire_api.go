package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"nexora.local/nli-v20/internal/middleware"
	"nexora.local/nli-v20/internal/nupire"
)

// nupireController is the boundary between HTTP/RBAC and the NUPIRE domain.
// The runtime package can replace this adapter without changing route contracts.
type nupireController interface {
	Snapshot(context.Context) (map[string]any, error)
	List(context.Context) ([]map[string]any, error)
	Get(context.Context, string) (map[string]any, error)
	CreateDiscovery(string, nupire.Provenance, string) (map[string]any, error)
	SubmitArtifact(string, json.RawMessage, string) (map[string]any, error)
	Evaluate(string, string, nupire.Metrics, []string) (map[string]any, error)
	Transition(context.Context, string, string, string) (map[string]any, error)
}

type persistentNUPIREController struct{ store *nupire.Store }

func (c persistentNUPIREController) List(ctx context.Context) ([]map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	records := c.store.List()
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, nupireRecordView(record))
	}
	return items, nil
}
func (c persistentNUPIREController) Snapshot(ctx context.Context) (map[string]any, error) {
	items, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"candidates": items, "count": len(items), "evolution_state": "available"}, nil
}
func (c persistentNUPIREController) Get(ctx context.Context, id string) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	record, ok := c.store.Get(id)
	if !ok {
		return nil, nupire.ErrNotFound
	}
	return nupireRecordView(record), nil
}
func (c persistentNUPIREController) CreateDiscovery(id string, provenance nupire.Provenance, actor string) (map[string]any, error) {
	provenance.CreatedBy = actor
	record, err := c.store.CreateDiscovery(id, provenance)
	if err != nil {
		return nil, err
	}
	return nupireRecordView(record), nil
}
func (c persistentNUPIREController) SubmitArtifact(id string, artifact json.RawMessage, actor string) (map[string]any, error) {
	record, err := c.store.SubmitCandidate(id, artifact, actor)
	if err != nil {
		return nil, err
	}
	return nupireRecordView(record), nil
}
func (c persistentNUPIREController) Evaluate(id, actor string, metrics nupire.Metrics, heldOutIDs []string) (map[string]any, error) {
	record, err := c.store.Evaluate(id, actor, metrics, heldOutIDs)
	if err != nil {
		return nil, err
	}
	return nupireRecordView(record), nil
}
func (c persistentNUPIREController) Transition(_ context.Context, id, action, actor string) (map[string]any, error) {
	state, ok := map[string]nupire.State{"shadow": nupire.Shadow, "approve": nupire.Approval, "canary": nupire.Canary, "promote": nupire.Promoted, "drift": nupire.Drifted, "rollback": nupire.RolledBack}[action]
	if !ok {
		return nil, nupire.ErrTransition
	}
	record, err := c.store.Transition(id, state, actor, "controller API action: "+action)
	if err != nil {
		return nil, err
	}
	return nupireRecordView(record), nil
}

func nupireRecordView(record nupire.Record) map[string]any {
	return map[string]any{
		"id": record.ID, "state": record.State, "artifact": record.Artifact,
		"digest": record.Digest, "digest_valid": record.DigestIsValid(),
		"provenance": record.Provenance, "metrics": record.Metrics,
		"approved_by": record.ApprovedBy, "events": record.Events,
		"created_at": record.CreatedAt, "updated_at": record.UpdatedAt,
	}
}

func (s *Server) nupireAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/nupire"), "/")
	if path == "" {
		path = "snapshot"
	}
	if r.Method == http.MethodGet {
		if s.authorize(w, r, middleware.PermissionReadParsers) == nil {
			return
		}
		var result map[string]any
		var err error
		switch {
		case path == "snapshot":
			result, err = s.nupire.Snapshot(r.Context())
		case path == "candidates":
			var items []map[string]any
			items, err = s.nupire.List(r.Context())
			result = map[string]any{"candidates": items, "count": len(items), "evolution_state": "available"}
		case strings.HasPrefix(path, "candidates/"):
			parts := strings.Split(path, "/")
			if len(parts) != 2 || parts[0] != "candidates" {
				jsonError(w, "GET is not supported for NUPIRE mutations", http.StatusMethodNotAllowed)
				return
			}
			id := safeID(parts[1])
			if id == "" {
				jsonError(w, "invalid candidate id", http.StatusBadRequest)
				return
			}
			result, err = s.nupire.Get(r.Context(), id)
		case path == "discoveries":
			jsonError(w, "POST required", http.StatusMethodNotAllowed)
			return
		default:
			jsonError(w, "NUPIRE route not found", http.StatusNotFound)
			return
		}
		if err != nil {
			if errors.Is(err, nupire.ErrNotFound) {
				jsonError(w, "NUPIRE candidate not found", http.StatusNotFound)
				return
			}
			jsonError(w, "NUPIRE state unavailable", http.StatusServiceUnavailable)
			return
		}
		jsonOK(w, result)
		return
	}
	if r.Method == http.MethodPost {
		if path == "discoveries" {
			s.nupireCreateDiscovery(w, r)
			return
		}
		if strings.HasPrefix(path, "candidates/") {
			s.nupireCandidateMutation(w, r, path)
			return
		}
	}
	jsonError(w, "GET or POST required", http.StatusMethodNotAllowed)
}

type nupireDiscoveryInput struct {
	ID          string   `json:"id"`
	Source      string   `json:"source"`
	Generator   string   `json:"generator"`
	TrainingIDs []string `json:"training_ids"`
	HeldOutIDs  []string `json:"held_out_ids"`
}

type nupireArtifactInput struct {
	Artifact json.RawMessage `json:"artifact"`
}
type nupireEvaluationInput struct {
	Metrics    nupire.Metrics `json:"metrics"`
	HeldOutIDs []string       `json:"held_out_ids"`
}

func (s *Server) nupireCreateDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	p := s.authorize(w, r, middleware.PermissionManageParsers)
	if p == nil {
		return
	}
	var input nupireDiscoveryInput
	if decodeJSON(r, &input, 1<<20) != nil || safeID(input.ID) != input.ID || strings.TrimSpace(input.Source) == "" || len(input.Source) > 256 || strings.TrimSpace(input.Generator) == "" || len(input.Generator) > 128 {
		s.auditAction(p, "nupire.discovery.create", "nupire-candidates/"+safeID(input.ID), "failure", map[string]string{"reason": "invalid_request"})
		jsonError(w, "id, source, generator, training_ids and held_out_ids are required", http.StatusBadRequest)
		return
	}
	result, err := s.nupire.CreateDiscovery(input.ID, nupire.Provenance{Source: input.Source, Generator: input.Generator, TrainingIDs: input.TrainingIDs, HeldOutIDs: input.HeldOutIDs}, p.Username)
	if err != nil {
		s.auditAction(p, "nupire.discovery.create", "nupire-candidates/"+safeID(input.ID), "failure", nil)
		jsonError(w, "invalid or duplicate NUPIRE discovery", http.StatusUnprocessableEntity)
		return
	}
	s.auditAction(p, "nupire.discovery.create", "nupire-candidates/"+safeID(input.ID), "success", nil)
	jsonCreated(w, result)
}

func (s *Server) nupireCandidateMutation(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] != "candidates" || safeID(parts[1]) == "" {
		jsonError(w, "invalid NUPIRE candidate route", http.StatusBadRequest)
		return
	}
	action := parts[2]
	permission := middleware.PermissionManageParsers
	if action == "actions" {
		if len(parts) != 4 {
			jsonError(w, "invalid NUPIRE action route", http.StatusBadRequest)
			return
		}
		action = "action:" + parts[3]
		if parts[3] == "approve" {
			permission = middleware.PermissionReviewParsers
		}
	} else if len(parts) != 3 {
		jsonError(w, "invalid NUPIRE candidate operation", http.StatusBadRequest)
		return
	}
	p := s.authorize(w, r, permission)
	if p == nil {
		return
	}
	id := safeID(parts[1])
	var result map[string]any
	var err error
	switch action {
	case "artifact":
		var input nupireArtifactInput
		if decodeJSON(r, &input, 2<<20) != nil {
			s.auditAction(p, "nupire.artifact", "nupire-candidates/"+id, "failure", map[string]string{"reason": "invalid_request"})
			jsonError(w, "invalid declarative artifact request", http.StatusBadRequest)
			return
		}
		var value map[string]any
		if len(input.Artifact) == 0 || json.Unmarshal(input.Artifact, &value) != nil || value == nil || unsafeDeclarative(value) != nil {
			s.auditAction(p, "nupire.artifact", "nupire-candidates/"+id, "failure", map[string]string{"reason": "unsafe_or_non_object"})
			jsonError(w, "artifact must be a safe declarative JSON object", http.StatusBadRequest)
			return
		}
		result, err = s.nupire.SubmitArtifact(id, input.Artifact, p.Username)
	case "evaluation":
		var input nupireEvaluationInput
		if decodeJSON(r, &input, 1<<20) != nil {
			s.auditAction(p, "nupire.evaluation", "nupire-candidates/"+id, "failure", map[string]string{"reason": "invalid_request"})
			jsonError(w, "invalid evaluation request", http.StatusBadRequest)
			return
		}
		result, err = s.nupire.Evaluate(id, p.Username, input.Metrics, input.HeldOutIDs)
	default:
		if strings.HasPrefix(action, "action:") {
			result, err = s.nupire.Transition(r.Context(), id, strings.TrimPrefix(action, "action:"), p.Username)
		} else {
			s.auditAction(p, "nupire.operation", "nupire-candidates/"+id, "failure", map[string]string{"reason": "unknown_operation"})
			jsonError(w, "unknown NUPIRE candidate operation", http.StatusNotFound)
			return
		}
	}
	auditAction := strings.ReplaceAll(action, ":", ".")
	outcome := "success"
	if err != nil {
		outcome = "failure"
	}
	s.auditAction(p, "nupire."+auditAction, "nupire-candidates/"+id, outcome, nil)
	if err != nil {
		if errors.Is(err, nupire.ErrNotFound) {
			jsonError(w, "NUPIRE candidate not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, nupire.ErrTransition) {
			jsonError(w, "NUPIRE lifecycle transition is not allowed", http.StatusConflict)
			return
		}
		jsonError(w, "NUPIRE operation rejected by evidence policy", http.StatusUnprocessableEntity)
		return
	}
	jsonOK(w, result)
}
