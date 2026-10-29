package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nexora.local/nli-v20/internal/nupire"
)

// registerNAPICandidate binds the already-tested NAPI draft to the persistent
// EGPE lifecycle. Only hashes of the discovery and held-out records enter the
// lifecycle store; raw customer records remain governed by Assurance storage.
func (s *Server) registerNAPICandidate(ctx context.Context, parserID, version, actor string, candidate napiCandidate, discovery, heldOut []assuranceRequest) (map[string]any, error) {
	artifact, err := json.Marshal(candidate.Definition)
	if err != nil {
		return nil, fmt.Errorf("marshal declarative candidate: %w", err)
	}
	artifactHash := napiJSONHash(candidate.Definition)
	candidateID := safeID(parserID + "-" + strings.ReplaceAll(version, ".", "-") + "-" + artifactHash[:12])
	if candidateID == "" {
		return nil, fmt.Errorf("invalid NUPIRE candidate identity")
	}
	if existing, err := s.nupire.Get(ctx, candidateID); err == nil {
		return existing, nil
	}

	trainingIDs := napiEvidenceIDs(discovery)
	heldOutIDs := napiEvidenceIDs(heldOut)
	if _, err = s.nupire.CreateDiscovery(candidateID, nupire.Provenance{
		Source:      "assurance/napi/" + parserID,
		Generator:   "napi-bounded-v1",
		TrainingIDs: trainingIDs,
		HeldOutIDs:  heldOutIDs,
	}, actor); err != nil {
		return nil, fmt.Errorf("create NUPIRE discovery: %w", err)
	}
	if _, err = s.nupire.SubmitArtifact(candidateID, artifact, actor); err != nil {
		return nil, fmt.Errorf("submit NUPIRE artifact: %w", err)
	}
	metrics, err := napiNUPIREMetrics(candidate.Evaluation)
	if err != nil {
		return nil, err
	}
	record, err := s.nupire.Evaluate(candidateID, actor, metrics, heldOutIDs)
	if err != nil {
		return nil, fmt.Errorf("evaluate NUPIRE candidate: %w", err)
	}
	return record, nil
}

func napiEvidenceIDs(samples []assuranceRequest) []string {
	result := make([]string, 0, len(samples))
	for _, sample := range samples {
		result = append(result, napiJSONHash(map[string]any{"raw": sample.Raw, "desired": sample.Desired}))
	}
	return result
}

func napiNUPIREMetrics(evaluation map[string]any) (nupire.Metrics, error) {
	heldOut, ok := evaluation["held_out"].(map[string]any)
	if !ok {
		return nupire.Metrics{}, fmt.Errorf("NAPI held-out evidence is missing")
	}
	total, totalOK := numberAsInt(heldOut["total"])
	exact, exactOK := numberAsInt(heldOut["exact"])
	failed, failedOK := numberAsInt(heldOut["failed"])
	precision, precisionOK := numberAsFloat(heldOut["field_precision"])
	recall, recallOK := numberAsFloat(heldOut["field_recall"])
	if !totalOK || !exactOK || !failedOK || !precisionOK || !recallOK || total < 1 {
		return nupire.Metrics{}, fmt.Errorf("NAPI held-out evidence has invalid metric types")
	}
	return nupire.Metrics{
		Samples:        total,
		ExactMatches:   exact,
		FieldPrecision: precision,
		FieldRecall:    recall,
		ErrorRate:      float64(failed) / float64(total),
		Ambiguous:      false,
	}, nil
}

func numberAsInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	default:
		return 0, false
	}
}

func numberAsFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	default:
		return 0, false
	}
}
