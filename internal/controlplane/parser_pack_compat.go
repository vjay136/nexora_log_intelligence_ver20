package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	metastore "nexora.local/nli-v20/internal/store"
	parserpolicy "nexora.local/nli-v20/parser-policy"
)

// runtimeParserPackForDocument accepts both the current Assurance envelope and
// Parser Catalog records produced by earlier NLI releases. The returned bytes
// always pass the strict client-side ParserPack loader. Unsupported legacy
// shapes are rejected before publication instead of becoming an "active" pack
// that can never be synchronized.
func runtimeParserPackForDocument(doc metastore.Document) (*parserpolicy.ParserPack, json.RawMessage, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(doc.Payload, &envelope); err != nil {
		return nil, nil, fmt.Errorf("parser payload is not a JSON object")
	}
	if raw := envelope["runtime_pack"]; len(raw) != 0 && string(raw) != "null" {
		pack, err := parserpolicy.LoadParserPackBytes(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("published runtime pack is invalid: %w", err)
		}
		return normalizeRuntimeParserPack(doc, pack)
	}

	// Imported NLI packs are stored directly as the document payload. Decode
	// them strictly first so unknown top-level fields cannot be discarded.
	var direct parserpolicy.ParserPack
	if strictJSON(doc.Payload, &direct) == nil && direct.ID != "" && len(direct.Rules) != 0 {
		return normalizeRuntimeParserPack(doc, &direct)
	}

	// The original Parser Catalog editor stored a small envelope containing
	// id/name/version/priority/rules. Rules are still decoded with unknown-field
	// rejection and validated by the current runtime before they can publish.
	var rules []parserpolicy.ParserRule
	rawRules := envelope["rules"]
	if len(rawRules) == 0 || string(rawRules) == "null" {
		return nil, nil, fmt.Errorf("legacy parser has no runtime rules; edit and validate it in Assurance")
	}
	if err := strictJSON(rawRules, &rules); err != nil {
		return nil, nil, fmt.Errorf("legacy parser rules are incompatible with the current runtime: %w", err)
	}
	var id, version string
	_ = json.Unmarshal(envelope["id"], &id)
	_ = json.Unmarshal(envelope["version"], &version)
	if id == "" {
		id = doc.ID
	}
	priority := 0
	_ = json.Unmarshal(envelope["priority"], &priority)
	for index := range rules {
		if rules[index].Priority <= 0 && priority > 0 {
			rules[index].Priority = priority
		}
	}
	pack := &parserpolicy.ParserPack{
		ID: id, Version: version, Revision: doc.Revision, SourceType: "manual",
		Author: doc.CreatedBy, ReviewState: parserpolicy.ReviewStateApproved,
		CreatedAt: doc.CreatedAt, Rules: rules,
		ResourceLimits: parserpolicy.DefaultResourceLimits(),
		Metadata:       map[string]string{"compatibility_source": "legacy-parser-catalog"},
	}
	return normalizeRuntimeParserPack(doc, pack)
}

func normalizeRuntimeParserPack(doc metastore.Document, pack *parserpolicy.ParserPack) (*parserpolicy.ParserPack, json.RawMessage, error) {
	if pack == nil {
		return nil, nil, fmt.Errorf("runtime parser pack is missing")
	}
	copyOfPack := *pack
	if copyOfPack.ID == "" {
		copyOfPack.ID = doc.ID
	}
	if copyOfPack.ID != doc.ID {
		return nil, nil, fmt.Errorf("runtime parser id %q does not match catalog id %q", copyOfPack.ID, doc.ID)
	}
	if copyOfPack.Revision <= 0 {
		copyOfPack.Revision = doc.Revision
	}
	if copyOfPack.Revision <= 0 {
		copyOfPack.Revision = 1
	}
	if copyOfPack.Author == "" {
		copyOfPack.Author = doc.CreatedBy
	}
	if copyOfPack.CreatedAt.IsZero() {
		copyOfPack.CreatedAt = doc.CreatedAt
	}
	if copyOfPack.CreatedAt.IsZero() {
		copyOfPack.CreatedAt = time.Now().UTC()
	}
	if copyOfPack.SourceType == "" {
		copyOfPack.SourceType = "manual"
	}
	if copyOfPack.ResourceLimits == (parserpolicy.ResourceLimits{}) {
		copyOfPack.ResourceLimits = parserpolicy.DefaultResourceLimits()
	}
	if copyOfPack.ReviewState != parserpolicy.ReviewStateApproved && copyOfPack.ReviewState != parserpolicy.ReviewStateActivated {
		copyOfPack.ReviewState = parserpolicy.ReviewStateApproved
		copyOfPack.ActivatedAt = nil
	}
	if err := parserpolicy.SealParserPack(&copyOfPack); err != nil {
		return nil, nil, err
	}
	encoded, err := json.Marshal(&copyOfPack)
	if err != nil {
		return nil, nil, err
	}
	verified, err := parserpolicy.LoadParserPackBytes(encoded)
	if err != nil {
		return nil, nil, err
	}
	if verified.ReviewState != parserpolicy.ReviewStateApproved && verified.ReviewState != parserpolicy.ReviewStateActivated {
		return nil, nil, fmt.Errorf("runtime parser must be approved or activated")
	}
	return verified, encoded, nil
}

func strictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON value")
	}
	return nil
}

func parserDocumentView(doc metastore.Document) map[string]any {
	view := documentView(doc)
	status := "not_published"
	if doc.State == "approved" {
		status = "ready_to_publish"
	}
	if doc.State == "active" || doc.State == "published" || doc.State == "approved" {
		if pack, _, err := runtimeParserPackForDocument(doc); err != nil {
			status = "blocked"
			view["runtime_sync"] = map[string]any{"status": status, "reason": err.Error()}
			return view
		} else {
			view["runtime_sync"] = map[string]any{"status": status, "parser_id": pack.ID, "parser_version": pack.Version}
			if doc.State == "active" || doc.State == "published" {
				view["runtime_sync"].(map[string]any)["status"] = "published"
			}
			return view
		}
	}
	view["runtime_sync"] = map[string]any{"status": status}
	return view
}
