package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/fleet"
	"nexora.local/nli-v20/internal/middleware"
	metastore "nexora.local/nli-v20/internal/store"
)

// handleInputPathProfileRollout proposes the published profile as a validated
// client config patch for one or more selected clients, or every eligible
// client. The client replaces only its inputs array after a separate approval.
func (s *Server) handleInputPathProfileRollout(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		jsonError(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}
	permission := middleware.PermissionReadFleet
	if r.Method == http.MethodPost {
		permission = middleware.PermissionTriggerRollout
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	doc, err := s.metadata.Get(r.Context(), "input-paths", id, 0)
	if err != nil || doc.Deleted {
		jsonError(w, "input path profile not found", http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		s.listInputPathRollouts(w, r, id, doc)
		return
	}
	if doc.State != "active" {
		jsonError(w, "input path profile must be submitted, independently approved, and published before rollout", http.StatusConflict)
		return
	}
	var profile map[string]any
	if json.Unmarshal(doc.Payload, &profile) != nil {
		jsonError(w, "input path profile is invalid", http.StatusInternalServerError)
		return
	}
	config, _ := profile["config"].(map[string]any)
	inputs, ok := config["inputs"].([]any)
	if !ok || validateCatalogConfig("input-paths", config) != nil {
		jsonError(w, "input path profile failed validation", http.StatusConflict)
		return
	}
	var request struct {
		NodeIDs []string `json:"node_ids"`
	}
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		jsonError(w, "invalid input path rollout request", http.StatusBadRequest)
		return
	}
	mode := "all_eligible"
	targets := []string{}
	if len(request.NodeIDs) > 0 {
		mode = "selected"
		known := map[string]bool{}
		for _, node := range s.fleet.All() {
			known[node.ID] = true
		}
		seen := map[string]bool{}
		for _, nodeID := range request.NodeIDs {
			nodeID = strings.TrimSpace(nodeID)
			if nodeID == "" || seen[nodeID] {
				continue
			}
			if !known[nodeID] {
				jsonError(w, "selected node does not exist: "+nodeID, http.StatusBadRequest)
				return
			}
			seen[nodeID] = true
			targets = append(targets, nodeID)
		}
	} else {
		for _, node := range s.fleet.All() {
			if node.Status != fleet.StatusOffline {
				targets = append(targets, node.ID)
			}
		}
	}
	if len(targets) == 0 {
		jsonError(w, "no target clients are available", http.StatusConflict)
		return
	}
	sort.Strings(targets)
	profileHash := fmt.Sprintf("%x", hashBytes(mustJSON(inputs)))
	proposal := map[string]any{
		"name":               "Roll out input paths: " + fmt.Sprint(profile["name"]),
		"type":               rolloutTypeConfigPush,
		"target_nodes":       targets,
		"payload":            map[string]any{"config_patch": map[string]any{"inputs": inputs}, "input_path_profile": map[string]any{"id": id, "revision": doc.Revision, "profile_hash": profileHash}},
		"input_path_profile": map[string]any{"id": id, "revision": doc.Revision, "name": profile["name"], "profile_hash": profileHash, "target_mode": mode},
		"approval_reason":    "Input path changes require independent rollout approval and client-side config validation.",
	}
	s.createRolloutProposal(w, r, principal, proposal)
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func (s *Server) listInputPathRollouts(w http.ResponseWriter, r *http.Request, profileID string, profile metastore.Document) {
	docs, err := s.metadata.List(r.Context(), "rollout-proposals", metastore.Query{Limit: 1000})
	if err != nil {
		jsonError(w, "input path rollout history unavailable", http.StatusServiceUnavailable)
		return
	}
	history := []map[string]any{}
	for _, doc := range docs {
		var payload map[string]any
		if json.Unmarshal(doc.Payload, &payload) != nil {
			continue
		}
		pathProfile, _ := payload["input_path_profile"].(map[string]any)
		if fmt.Sprint(pathProfile["id"]) != profileID {
			continue
		}
		item := map[string]any{"id": doc.ID, "state": doc.State, "created_by": doc.CreatedBy, "created_at": doc.CreatedAt, "target_nodes": payload["target_nodes"], "profile": pathProfile}
		if liveID := strings.TrimSpace(fmt.Sprint(payload["live_rollout_id"])); liveID != "" && liveID != "<nil>" {
			if live, ok := s.rollouts.Get(liveID); ok {
				item["rollout"] = live
			}
		}
		history = append(history, item)
	}
	sort.Slice(history, func(i, j int) bool {
		return fmt.Sprint(history[i]["created_at"]) > fmt.Sprint(history[j]["created_at"])
	})
	clients := []map[string]any{}
	for _, node := range s.fleet.All() {
		stateDoc, stateErr := s.metadata.Get(r.Context(), "fleet-sync", safeID(node.ID), 0)
		state := map[string]any{"node_id": node.ID, "status": "not_targeted"}
		if stateErr == nil {
			var stored map[string]any
			if json.Unmarshal(stateDoc.Payload, &stored) == nil {
				for _, key := range []string{"desired_input_path_profile", "applied_input_path_profile"} {
					if target, ok := stored[key].(map[string]any); ok && fmt.Sprint(target["id"]) == profileID {
						state[key] = target
					}
				}
				if state["desired_input_path_profile"] != nil || state["applied_input_path_profile"] != nil {
					for _, key := range []string{"input_path_sync_status", "input_path_sync_message", "input_path_sync_updated_at"} {
						if stored[key] != nil {
							state[key] = stored[key]
						}
					}
				}
			}
		}
		clients = append(clients, map[string]any{"node_id": node.ID, "hostname": node.Hostname, "status": node.Status, "input_path_sync": state})
	}
	jsonOK(w, map[string]any{"profile_id": profileID, "profile_revision": profile.Revision, "profile_state": profile.State, "history": history, "count": len(history), "clients": clients, "updated_at": time.Now().UTC()})
}

func (s *Server) markInputPathRolloutDesired(ctxPrincipal string, proposal map[string]any, targets []string) {
	pathProfile, _ := proposal["input_path_profile"].(map[string]any)
	if pathProfile == nil {
		return
	}
	for _, nodeID := range targets {
		id := safeID(nodeID)
		doc, _ := s.metadata.Get(context.Background(), "fleet-sync", id, 0)
		state := map[string]any{"node_id": nodeID}
		_ = json.Unmarshal(doc.Payload, &state)
		state["desired_input_path_profile"] = pathProfile
		state["input_path_sync_status"] = "pending"
		state["input_path_sync_updated_at"] = time.Now().UTC()
		payload, _ := json.Marshal(state)
		_, _ = s.metadata.Put(context.Background(), metastore.Document{Collection: "fleet-sync", ID: id, State: "desired", Payload: payload, CreatedBy: ctxPrincipal})
	}
}

func (s *Server) recordInputPathRolloutResult(commandID, nodeID, status, message string) {
	rollout, ok := s.rollouts.Get(commandID)
	if !ok || rollout == nil {
		return
	}
	var payload map[string]any
	if json.Unmarshal(rollout.Payload, &payload) != nil {
		return
	}
	profile, _ := payload["input_path_profile"].(map[string]any)
	if profile == nil {
		return
	}
	id := safeID(nodeID)
	doc, _ := s.metadata.Get(context.Background(), "fleet-sync", id, 0)
	state := map[string]any{"node_id": nodeID}
	_ = json.Unmarshal(doc.Payload, &state)
	resultStatus := "applied"
	if strings.EqualFold(status, "failed") || strings.EqualFold(status, "error") {
		resultStatus = "failed"
	}
	state["input_path_sync_status"] = resultStatus
	state["input_path_sync_message"] = message
	state["input_path_sync_updated_at"] = time.Now().UTC()
	state["applied_input_path_profile"] = profile
	data, _ := json.Marshal(state)
	_, _ = s.metadata.Put(context.Background(), metastore.Document{Collection: "fleet-sync", ID: id, State: "applied", Payload: data, CreatedBy: doc.CreatedBy})
}
