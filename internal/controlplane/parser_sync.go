package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/fleet"
	"nexora.local/nli-v20/internal/middleware"
	metastore "nexora.local/nli-v20/internal/store"
)

var parserAssetCollections = []string{"kv-config", "static-patterns", "projections", "templates", "regex-patterns", "known-keys", "semantic-extraction", "parser-packs"}

type parserBundleAsset struct {
	AssetClass string          `json:"asset_class"`
	AssetID    string          `json:"asset_id"`
	Name       string          `json:"name"`
	Revision   int             `json:"revision"`
	SHA256     string          `json:"sha256"`
	Payload    json.RawMessage `json:"payload"`
}
type parserAssetBundle struct {
	SchemaVersion int                 `json:"schema_version"`
	Assets        []parserBundleAsset `json:"assets"`
	BundleHash    string              `json:"bundle_hash"`
}

func bundleDigest(bundle parserAssetBundle) (string, []byte, error) {
	bundle.BundleHash = ""
	unsigned, err := json.Marshal(struct {
		SchemaVersion int                 `json:"schema_version"`
		Assets        []parserBundleAsset `json:"assets"`
	}{bundle.SchemaVersion, bundle.Assets})
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(unsigned)
	return hex.EncodeToString(digest[:]), unsigned, nil
}

func (s *Server) buildActiveParserBundle(ctx context.Context) (parserAssetBundle, error) {
	bundle := parserAssetBundle{SchemaVersion: 1, Assets: []parserBundleAsset{}}
	for _, collection := range parserAssetCollections {
		page, err := s.metadata.List(ctx, collection, metastore.Query{Limit: 1000})
		if err != nil {
			return bundle, err
		}
		for _, doc := range page {
			if doc.State != "active" || doc.Deleted {
				if collection != "parser-packs" || doc.State != "published" || doc.Deleted {
					continue
				}
			}
			var payload map[string]any
			if err := json.Unmarshal(doc.Payload, &payload); err != nil {
				return bundle, fmt.Errorf("invalid active parser asset %s/%s", collection, doc.ID)
			}
			if collection == "parser-packs" {
				// Assurance candidates are not runtime packs. Only the publish action
				// writes this strict, sealed ParserPack after independent approval.
				runtimePack, ok := payload["runtime_pack"]
				if !ok || payload["review_state"] != "published" {
					continue
				}
				encoded, err := json.Marshal(runtimePack)
				if err != nil {
					return bundle, fmt.Errorf("invalid published runtime parser %s", doc.ID)
				}
				digest := sha256.Sum256(encoded)
				name, _ := payload["name"].(string)
				bundle.Assets = append(bundle.Assets, parserBundleAsset{AssetClass: collection, AssetID: doc.ID, Name: name, Revision: doc.Revision, SHA256: hex.EncodeToString(digest[:]), Payload: encoded})
				continue
			}
			name, _ := payload["name"].(string)
			bundle.Assets = append(bundle.Assets, parserBundleAsset{AssetClass: collection, AssetID: doc.ID, Name: name, Revision: doc.Revision, SHA256: doc.Hash, Payload: append(json.RawMessage(nil), doc.Payload...)})
		}
	}
	// Stable order makes the same approved asset set produce the same bundle hash.
	sort.Slice(bundle.Assets, func(i, j int) bool {
		if bundle.Assets[i].AssetClass == bundle.Assets[j].AssetClass {
			return bundle.Assets[i].AssetID < bundle.Assets[j].AssetID
		}
		return bundle.Assets[i].AssetClass < bundle.Assets[j].AssetClass
	})
	hash, _, err := bundleDigest(bundle)
	bundle.BundleHash = hash
	return bundle, err
}

func (s *Server) materializeParserBundle(bundle parserAssetBundle) (string, error) {
	hash, _, err := bundleDigest(bundle)
	if err != nil {
		return "", err
	}
	if hash != bundle.BundleHash {
		return "", errors.New("parser bundle hash calculation failed")
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(s.cfg.StateDir, "parser-assets")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "bundle.json")
	temp := path + ".staging"
	file, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return "", err
	}
	if _, err = file.Write(encoded); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Rename(temp, path); err != nil {
		return "", err
	}
	readback, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var actual parserAssetBundle
	if err = json.Unmarshal(readback, &actual); err != nil {
		return "", err
	}
	readHash, _, err := bundleDigest(actual)
	if err != nil || readHash != bundle.BundleHash {
		return "", errors.New("controller parser bundle readback hash mismatch")
	}
	return readHash, nil
}

func (s *Server) readMaterializedParserBundleHash() (string, error) {
	data, err := os.ReadFile(filepath.Join(s.cfg.StateDir, "parser-assets", "bundle.json"))
	if err != nil {
		return "", err
	}
	var bundle parserAssetBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return "", err
	}
	hash, _, err := bundleDigest(bundle)
	if err != nil || hash != bundle.BundleHash {
		return "", errors.New("materialized parser bundle hash is invalid")
	}
	return hash, nil
}

func (s *Server) startParserBundleRollout(ctx context.Context, actor, name string, bundle parserAssetBundle, targetIDs []string) (*rolloutRecord, error) {
	if len(targetIDs) == 0 {
		for _, node := range s.fleet.All() {
			if node.Status != fleet.StatusOffline {
				targetIDs = append(targetIDs, node.ID)
			}
		}
	}
	if len(targetIDs) == 0 {
		return nil, nil
	}
	payload := map[string]any{"asset_bundle": bundle, "bundle_hash": bundle.BundleHash}
	rollout, err := s.rollouts.Create(rolloutTypeParserSync, name, actor, targetIDs, payload)
	if err != nil {
		return nil, err
	}
	if err := s.rollouts.Start(rollout.ID, targetIDs); err != nil {
		return nil, err
	}
	for _, nodeID := range targetIDs {
		s.updateDesiredNodeAssetSync(ctx, nodeID, bundle)
	}
	item, ok := s.rollouts.Get(rollout.ID)
	if !ok {
		return nil, errors.New("created parser sync rollout disappeared")
	}
	return item, nil
}

// proposeParserBundleRollout snapshots the exact published controller bundle
// and target set for independent approval. Clients do not receive the bundle
// until the separate rollout approval endpoint starts the live rollout.
func (s *Server) proposeParserBundleRollout(ctx context.Context, actor, name, reason, targetMode string, bundle parserAssetBundle, targetIDs []string) (metastore.Document, error) {
	if targetMode == "" {
		targetMode = "all_eligible"
	}
	if targetMode != "all_eligible" && targetMode != "selected" && targetMode != "origin" {
		return metastore.Document{}, errors.New("target_mode must be all_eligible, selected, or origin")
	}
	eligible := map[string]bool{}
	for _, node := range s.fleet.All() {
		if node.Status != fleet.StatusOffline {
			eligible[node.ID] = true
		}
	}
	if targetMode == "all_eligible" {
		targetIDs = targetIDs[:0]
		for id := range eligible {
			targetIDs = append(targetIDs, id)
		}
	}
	if len(targetIDs) > 1000 || (len(targetIDs) == 0 && targetMode != "all_eligible") {
		return metastore.Document{}, errors.New("parser sync rollout requires 1..1000 eligible target clients")
	}
	seen := map[string]bool{}
	for _, id := range targetIDs {
		if !eligible[id] || seen[id] {
			return metastore.Document{}, fmt.Errorf("parser sync target %q is not an eligible enrolled client", id)
		}
		seen[id] = true
	}
	sort.Strings(targetIDs)
	proposal := map[string]any{
		"name": name, "type": rolloutTypeParserSync, "target_nodes": targetIDs,
		"target_mode":       targetMode,
		"payload":           map[string]any{"asset_bundle": bundle, "bundle_hash": bundle.BundleHash},
		"approval_required": true, "approval_reason": reason,
		"bundle_hash": bundle.BundleHash,
	}
	encoded, err := json.Marshal(proposal)
	if err != nil {
		return metastore.Document{}, err
	}
	id := fmt.Sprintf("parser-sync-%d", time.Now().UnixNano())
	return s.metadata.Put(ctx, metastore.Document{Collection: "rollout-proposals", ID: id, State: "pending_approval", Payload: encoded, CreatedBy: actor})
}

func (s *Server) updateDesiredNodeAssetSync(ctx context.Context, nodeID string, bundle parserAssetBundle) {
	id := safeID(nodeID)
	doc, _ := s.metadata.Get(ctx, "fleet-sync", id, 0)
	state := map[string]any{"node_id": nodeID}
	_ = json.Unmarshal(doc.Payload, &state)
	revisions := map[string]int{}
	for _, asset := range bundle.Assets {
		key := asset.AssetClass + "/" + asset.AssetID
		revisions[key] = asset.Revision
	}
	state["desired_asset_bundle_hash"] = bundle.BundleHash
	state["desired_asset_revisions"] = revisions
	state["desired_asset_at"] = time.Now().UTC()
	state["asset_drifted"] = fmt.Sprint(state["active_asset_bundle_hash"]) != bundle.BundleHash
	state["asset_sync_status"] = "pending"
	payload, _ := json.Marshal(state)
	_, _ = s.metadata.Put(ctx, metastore.Document{Collection: "fleet-sync", ID: id, State: "desired", Payload: payload, CreatedBy: doc.CreatedBy})
}

func (s *Server) markParserSyncRolloutDesired(ctx context.Context, actor, proposalID string, bundle parserAssetBundle, targets []string) {
	for _, nodeID := range targets {
		s.updateDesiredNodeAssetSync(ctx, nodeID, bundle)
	}
	_, _ = s.metadata.Put(ctx, metastore.Document{
		Collection: "parser-sync-state", ID: "controller", State: "published",
		Payload: mustJSON(map[string]any{
			"desired_hash": bundle.BundleHash, "assets": bundleRevisionMap(bundle),
			"rollout_proposal_id": proposalID, "rollout_targets": targets,
			"rollout_approved_by": actor, "rollout_approved_at": time.Now().UTC(), "status": "rolling_out",
		}), CreatedBy: actor,
	})
}

func (s *Server) handleParserSync(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/parser-sync"), "/")
	if path == "" && r.Method == http.MethodGet {
		if s.authorize(w, r, middleware.PermissionReadFleet) == nil {
			return
		}
		s.listParserSync(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		jsonError(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}
	if path != "verify" && path != "retry" && path != "rollout" {
		jsonError(w, "action must be verify, retry, or rollout", http.StatusNotFound)
		return
	}
	permission := middleware.PermissionReadParsers
	if path == "retry" || path == "rollout" {
		permission = middleware.PermissionTriggerRollout
	}
	principal := s.authorize(w, r, permission)
	if principal == nil {
		return
	}
	var request struct {
		AssetClass string   `json:"asset_class"`
		AssetID    string   `json:"asset_id"`
		NodeIDs    []string `json:"node_ids,omitempty"`
		TargetMode string   `json:"target_mode,omitempty"`
		OriginNode string   `json:"origin_node_id,omitempty"`
		BundleHash string   `json:"bundle_hash,omitempty"`
	}
	if err := decodeJSON(r, &request, 64<<10); err != nil {
		jsonError(w, "invalid parser sync action", http.StatusBadRequest)
		return
	}
	bundle, err := s.buildActiveParserBundle(r.Context())
	if err != nil {
		jsonError(w, "active parser bundle unavailable", http.StatusServiceUnavailable)
		return
	}
	if request.AssetClass != "" || request.AssetID != "" {
		found := false
		for _, asset := range bundle.Assets {
			if asset.AssetClass == request.AssetClass && asset.AssetID == request.AssetID {
				found = true
				break
			}
		}
		if !found {
			jsonError(w, "active asset not found", http.StatusNotFound)
			return
		}
	}
	controllerHash, controllerStatus := "", "not_materialized"
	if path == "verify" {
		if applied, readErr := s.readMaterializedParserBundleHash(); readErr == nil {
			controllerHash = applied
			controllerStatus = "drifted"
			if applied == bundle.BundleHash {
				controllerStatus = "applied"
			}
		} else if !os.IsNotExist(readErr) {
			controllerStatus = "invalid"
		}
	} else {
		var materializeErr error
		controllerHash, materializeErr = s.materializeParserBundle(bundle)
		if materializeErr != nil {
			jsonError(w, "controller parser bundle materialization failed", http.StatusServiceUnavailable)
			return
		}
		controllerStatus = "applied"
	}
	if path == "verify" {
		if request.BundleHash != "" && request.BundleHash != bundle.BundleHash {
			jsonError(w, "requested parser bundle is no longer the current published bundle", http.StatusConflict)
			return
		}
		if len(request.NodeIDs) > 1000 {
			jsonError(w, "verification exceeds the 1000-client target limit", http.StatusBadRequest)
			return
		}
		selected := map[string]bool{}
		for _, id := range request.NodeIDs {
			selected[id] = true
		}
		if len(selected) > 0 {
			for id := range selected {
				if _, ok := s.fleet.Get(id); !ok {
					jsonError(w, "verification target is not an enrolled client: "+id, http.StatusBadRequest)
					return
				}
			}
		}
		states, stateErr := s.metadata.List(r.Context(), "fleet-sync", metastore.Query{Limit: 1000})
		if stateErr != nil {
			jsonError(w, "client parser acknowledgements are unavailable", http.StatusServiceUnavailable)
			return
		}
		byNode := map[string]map[string]any{}
		for _, doc := range states {
			state := map[string]any{}
			if json.Unmarshal(doc.Payload, &state) == nil {
				byNode[doc.ID] = state
			}
		}
		clients := []map[string]any{}
		for _, node := range s.fleet.All() {
			if len(selected) > 0 && !selected[node.ID] {
				continue
			}
			state := byNode[safeID(node.ID)]
			active := fmt.Sprint(state["active_asset_bundle_hash"])
			desired := fmt.Sprint(state["desired_asset_bundle_hash"])
			acknowledged := desired == bundle.BundleHash && active == bundle.BundleHash && fmt.Sprint(state["asset_sync_status"]) == "applied"
			status := "pending"
			if acknowledged {
				status = "applied"
			} else if active != "<nil>" && active != "" {
				status = "drifted"
			}
			clients = append(clients, map[string]any{"node_id": node.ID, "desired_hash": desired, "acknowledged_hash": active, "status": status, "exact_acknowledgement": acknowledged})
		}
		jsonOK(w, map[string]any{"desired_hash": bundle.BundleHash, "controller_applied_hash": controllerHash, "controller_status": controllerStatus, "client_acknowledgements": clients, "verified_at": time.Now().UTC(), "assets": bundle.Assets})
		return
	}
	if request.BundleHash != "" && request.BundleHash != bundle.BundleHash {
		jsonError(w, "requested parser bundle is no longer the current published bundle", http.StatusConflict)
		return
	}
	if path == "rollout" {
		mode := request.TargetMode
		if mode == "" {
			mode = "all_eligible"
		}
		targets := append([]string(nil), request.NodeIDs...)
		if mode == "origin" {
			if request.OriginNode == "" {
				jsonError(w, "origin_node_id is required for origin rollout", http.StatusBadRequest)
				return
			}
			targets = []string{request.OriginNode}
		}
		proposal, err := s.proposeParserBundleRollout(r.Context(), principal.Username, "Roll out published parser bundle", "Published parser bundles require independent rollout approval before client synchronization.", mode, bundle, targets)
		if err != nil {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		s.auditAction(principal, "parser_sync.rollout.propose", "rollout-proposals/"+proposal.ID, "success", map[string]string{"bundle_hash": bundle.BundleHash, "target_mode": mode})
		jsonCreated(w, map[string]any{"proposal": documentView(proposal), "bundle_hash": bundle.BundleHash, "approval_gate": "pending_approval"})
		return
	}
	targets := append([]string(nil), request.NodeIDs...)
	if len(targets) == 0 {
		for _, node := range s.fleet.All() {
			if node.Status != fleet.StatusOffline {
				targets = append(targets, node.ID)
			}
		}
	}
	if len(targets) == 0 {
		jsonError(w, "no eligible online clients to retry", http.StatusConflict)
		return
	}
	saved, err := s.proposeParserBundleRollout(r.Context(), principal.Username, "Retry parser asset synchronization", "Parser sync retries require an independent rollout approval.", "selected", bundle, targets)
	if err != nil {
		jsonError(w, "parser sync retry proposal could not be saved", http.StatusInternalServerError)
		return
	}
	s.auditAction(principal, "parser_sync.retry.propose", "rollout-proposals/"+saved.ID, "success", map[string]string{"bundle_hash": bundle.BundleHash})
	jsonCreated(w, map[string]any{"proposal": documentView(saved), "bundle_hash": bundle.BundleHash, "approval_gate": "pending_approval"})
}

func (s *Server) handleParserAssetTransition(w http.ResponseWriter, r *http.Request, principal *middleware.SecurityContext, collection, id, action string, doc metastore.Document) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	state, payload := doc.State, map[string]any{}
	_ = json.Unmarshal(doc.Payload, &payload)
	switch action {
	case "submit":
		if state != "draft" && state != "rejected" {
			jsonError(w, "only draft or rejected assets may be submitted", http.StatusConflict)
			return
		}
		state = "in_review"
		payload["submitted_by"] = principal.Username
		payload["submitted_at"] = time.Now().UTC()
	case "approve":
		if state != "in_review" {
			jsonError(w, "asset is not awaiting review", http.StatusConflict)
			return
		}
		if doc.CreatedBy == principal.Username {
			jsonError(w, "creator cannot approve own parser asset", http.StatusConflict)
			return
		}
		state = "approved"
		payload["approved_by"] = principal.Username
		payload["approved_at"] = time.Now().UTC()
	case "publish":
		if state != "approved" {
			jsonError(w, "only approved assets can be published", http.StatusConflict)
			return
		}
		state = "active"
		payload["published_by"] = principal.Username
		payload["published_at"] = time.Now().UTC()
	default:
		jsonError(w, "unknown asset transition", http.StatusNotFound)
		return
	}
	payload["review_state"] = state
	encoded, err := json.Marshal(payload)
	if err != nil {
		jsonError(w, "asset transition failed", http.StatusInternalServerError)
		return
	}
	saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: collection, ID: id, State: state, Payload: encoded, CreatedBy: doc.CreatedBy})
	if err != nil {
		jsonError(w, "asset transition persistence failed", http.StatusInternalServerError)
		return
	}
	response := map[string]any{"asset": documentView(saved), "state": state}
	if action == "publish" && isParserAssetCollection(collection) {
		bundle, buildErr := s.buildActiveParserBundle(r.Context())
		if buildErr != nil {
			jsonError(w, "published asset bundle could not be built", http.StatusServiceUnavailable)
			return
		}
		appliedHash, materializeErr := s.materializeParserBundle(bundle)
		if materializeErr != nil {
			jsonError(w, "published asset bundle could not be applied on controller", http.StatusServiceUnavailable)
			return
		}
		now := time.Now().UTC()
		controllerState := map[string]any{"desired_hash": bundle.BundleHash, "applied_hash": appliedHash, "applied_at": now, "status": "controller_applied_pending_fleet_approval", "assets": bundleRevisionMap(bundle)}
		stateBytes, _ := json.Marshal(controllerState)
		_, _ = s.metadata.Put(r.Context(), metastore.Document{Collection: "parser-sync-state", ID: "controller", State: "applied", Payload: stateBytes, CreatedBy: principal.Username})
		proposal, proposalErr := s.proposeParserBundleRollout(r.Context(), principal.Username, "Publish parser asset bundle", "Parser asset publication requires independent fleet rollout approval.", "all_eligible", bundle, nil)
		if proposalErr != nil {
			jsonError(w, "asset published and controller applied, but rollout proposal could not be created: "+proposalErr.Error(), http.StatusServiceUnavailable)
			return
		}
		response["bundle_hash"] = bundle.BundleHash
		response["controller_applied_hash"] = appliedHash
		response["rollout_proposal"] = documentView(proposal)
		response["auto_rollout_status"] = "pending_approval"
	}
	actionLabel, targetPrefix := "config_asset."+action, "config-assets/"
	if isParserAssetCollection(collection) {
		actionLabel, targetPrefix = "parser_asset."+action, "parser-assets/"
	}
	s.auditAction(principal, actionLabel, targetPrefix+collection+"/"+id, "success", map[string]string{"revision": fmt.Sprint(saved.Revision), "state": state})
	jsonOK(w, response)
}

func isParserAssetCollection(collection string) bool {
	for _, candidate := range parserAssetCollections {
		if collection == candidate {
			return true
		}
	}
	return false
}

func (s *Server) listParserSync(w http.ResponseWriter, r *http.Request) {
	bundle, err := s.buildActiveParserBundle(r.Context())
	if err != nil {
		jsonError(w, "parser sync metadata unavailable", http.StatusServiceUnavailable)
		return
	}
	controllerHash := ""
	controllerStatus := "not_materialized"
	if data, err := os.ReadFile(filepath.Join(s.cfg.StateDir, "parser-assets", "bundle.json")); err == nil {
		var applied parserAssetBundle
		if json.Unmarshal(data, &applied) == nil {
			hash, _, hashErr := bundleDigest(applied)
			if hashErr == nil && hash == bundle.BundleHash {
				controllerHash = hash
				controllerStatus = "applied"
			} else {
				controllerStatus = "drifted"
			}
		}
	}
	page, _ := s.metadata.List(r.Context(), "fleet-sync", metastore.Query{Limit: 1000})
	byNode := map[string]map[string]any{}
	for _, doc := range page {
		var state map[string]any
		if json.Unmarshal(doc.Payload, &state) == nil {
			byNode[doc.ID] = state
		}
	}
	clients := []map[string]any{}
	for _, node := range s.fleet.All() {
		state := byNode[safeID(node.ID)]
		if state == nil {
			state = map[string]any{"node_id": node.ID}
		}
		applied := fmt.Sprint(state["active_asset_bundle_hash"])
		desired := bundle.BundleHash
		drift := applied != desired
		acknowledged := !drift && fmt.Sprint(state["desired_asset_bundle_hash"]) == desired && fmt.Sprint(state["asset_sync_status"]) == "applied"
		clients = append(clients, map[string]any{"node_id": node.ID, "hostname": node.Hostname, "desired_revision": bundleRevisionMap(bundle), "desired_hash": desired, "applied_revision": state["active_asset_revisions"], "applied_hash": applied, "acknowledged_bundle_hash": applied, "exact_acknowledgement": acknowledged, "last_seen": state["last_seen"], "status": state["asset_sync_status"], "drifted": drift, "error": state["asset_sync_error"]})
	}
	var latest *rolloutRecord
	for _, item := range s.rollouts.List() {
		if item.Type != rolloutTypeParserSync {
			continue
		}
		var payload map[string]any
		if json.Unmarshal(item.Payload, &payload) == nil && fmt.Sprint(payload["bundle_hash"]) == bundle.BundleHash {
			latest = item
			break
		}
	}
	var pendingProposal *metastore.Document
	if proposals, err := s.metadata.List(r.Context(), "rollout-proposals", metastore.Query{State: "pending_approval", Limit: 1000}); err == nil {
		for i := range proposals {
			var proposal map[string]any
			if json.Unmarshal(proposals[i].Payload, &proposal) == nil && fmt.Sprint(proposal["type"]) == rolloutTypeParserSync && fmt.Sprint(proposal["bundle_hash"]) == bundle.BundleHash {
				pendingProposal = &proposals[i]
				break
			}
		}
	}
	rolloutState := rolloutStatus(latest)
	if latest == nil && pendingProposal != nil {
		rolloutState = "pending_approval"
	}
	var proposalView any
	if pendingProposal != nil {
		proposalView = documentView(*pendingProposal)
	}
	assetViews := []map[string]any{}
	for _, asset := range bundle.Assets {
		var controllerRevision any
		if controllerStatus == "applied" {
			controllerRevision = asset.Revision
		}
		desired := map[string]any{"revision": asset.Revision, "sha256": asset.SHA256, "state": "published"}
		controller := map[string]any{"revision": controllerRevision, "sha256": controllerHash, "status": controllerStatus}
		assetViews = append(assetViews, map[string]any{"asset_class": asset.AssetClass, "asset_id": asset.AssetID, "name": asset.Name, "desired": desired, "controller": controller, "desired_revision": asset.Revision, "desired_hash": asset.SHA256, "state": "published", "controller_applied_revision": controllerRevision, "controller_applied_hash": controllerHash, "controller_status": controllerStatus, "auto_rollout": map[string]any{"enabled": false, "status": rolloutState}, "rollout": rolloutSummary(latest), "rollout_proposal": proposalView, "clients": clients})
	}
	jsonOK(w, map[string]any{"assets": assetViews, "count": len(assetViews), "bundle": map[string]any{"revision": bundleRevisionMap(bundle), "desired_hash": bundle.BundleHash, "controller_applied_hash": controllerHash, "controller_status": controllerStatus}, "clients": clients, "rollout": rolloutSummary(latest), "rollout_proposal": proposalView, "automatic_rollout_policy": "approval_gated_parser_bundle_sync"})
}

func bundleRevisionMap(bundle parserAssetBundle) map[string]int {
	out := map[string]int{}
	for _, asset := range bundle.Assets {
		out[asset.AssetClass+"/"+asset.AssetID] = asset.Revision
	}
	return out
}
func rolloutStatus(item *rolloutRecord) string {
	if item == nil {
		return "not_started"
	}
	return item.Status
}
func rolloutSummary(item *rolloutRecord) any {
	if item == nil {
		return map[string]any{"status": "not_started"}
	}
	return map[string]any{"id": item.ID, "status": item.Status, "total_nodes": item.TotalNodes, "done_nodes": item.DoneNodes, "failed_nodes": item.FailedNodes, "target_nodes": item.TargetNodes, "node_results": item.NodeResults}
}
