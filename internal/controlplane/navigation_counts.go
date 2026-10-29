package controlplane

import (
	"context"
	"net/http"
	"time"

	metastore "nexora.local/nli-v20/internal/store"
)

type navigationActionCount struct {
	Total     int            `json:"total"`
	States    map[string]int `json:"states"`
	Available bool           `json:"available"`
}

func countActionableDocuments(ctx context.Context, store metastore.Store, collection string, actionable map[string]struct{}) (navigationActionCount, error) {
	result := navigationActionCount{States: map[string]int{}, Available: true}
	documents, err := store.List(ctx, collection, metastore.Query{Limit: 1000})
	if err != nil {
		return result, err
	}
	for _, document := range documents {
		if _, ok := actionable[document.State]; !ok {
			continue
		}
		result.Total++
		result.States[document.State]++
	}
	return result, nil
}

// handleNavigationCounts returns one bounded, canonical snapshot for sidebar
// work indicators. Only current states that require an operator decision are
// counted; completed work, historical failures, and browser notifications are
// deliberately excluded.
func (s *Server) handleNavigationCounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "GET required", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	workflowStates := map[string]struct{}{"draft": {}, "rejected": {}, "in_review": {}, "approved": {}}
	pageCollections := map[string]string{
		"parsers":          "parser-packs",
		"parser-workspace": "parser-packs",
		"input-paths":      "input-paths",
		"outputs":          "output-destinations",
		"kv":               "kv-config",
		"static":           "static-patterns",
		"projection":       "projections",
		"studio":           "templates",
		"regex":            "regex-patterns",
		"knownkeys":        "known-keys",
		"semantic":         "semantic-extraction",
	}

	counts := make(map[string]navigationActionCount, len(pageCollections)+5)
	unavailable := make(map[string]string)
	cache := map[string]navigationActionCount{}
	for page, collection := range pageCollections {
		count, ok := cache[collection]
		if !ok {
			var err error
			count, err = countActionableDocuments(ctx, s.metadata, collection, workflowStates)
			if err != nil {
				count = navigationActionCount{States: map[string]int{}, Available: false}
				unavailable[collection] = "lifecycle store temporarily unavailable"
			}
			cache[collection] = count
		}
		counts[page] = count
	}

	rolloutCount, err := countActionableDocuments(ctx, s.metadata, "rollout-proposals", map[string]struct{}{"pending_approval": {}})
	if err != nil {
		rolloutCount = navigationActionCount{States: map[string]int{}, Available: false}
		unavailable["rollout-proposals"] = "lifecycle store temporarily unavailable"
	}
	counts["rollouts"] = rolloutCount

	assuranceCount, err := countActionableDocuments(ctx, s.metadata, "assurance-runs", map[string]struct{}{"queued": {}, "running": {}})
	if err != nil {
		assuranceCount = navigationActionCount{States: map[string]int{}, Available: false}
		unavailable["assurance-runs"] = "lifecycle store temporarily unavailable"
	}
	counts["assurance"] = assuranceCount

	quarantineCount := navigationActionCount{States: map[string]int{}, Available: false}
	if s.quarantine == nil || s.quarantine.store == nil {
		unavailable[quarantineRecordsCollection] = "quarantine store is not configured"
	} else if count, countErr := countActionableDocuments(ctx, s.quarantine.store, quarantineRecordsCollection, map[string]struct{}{"waiting": {}, "replay_failed": {}}); countErr != nil {
		unavailable[quarantineRecordsCollection] = "quarantine store temporarily unavailable"
	} else {
		quarantineCount = count
	}
	counts["quarantine-wizard"] = quarantineCount
	counts["unknowns"] = quarantineCount
	counts["upes"] = quarantineCount

	jsonOK(w, map[string]any{
		"generated_at": time.Now().UTC(),
		"counts":       counts,
		"unavailable":  unavailable,
		"source":       "controller lifecycle stores",
	})
}
