package controlplane

import (
	"context"
	"encoding/json"
	"errors"

	parsercatalog "nexora.local/nli-v20/configs/catalog"
	metastore "nexora.local/nli-v20/internal/store"
)

var catalogCollections = map[string]string{
	"kv": "kv-config", "regex": "regex-patterns", "known-keys": "known-keys",
	"static-patterns": "static-patterns", "templates": "templates", "projections": "projections",
	"semantic-extraction": "semantic-extraction",
}

func seedBuiltinCatalog(ctx context.Context, store metastore.Store) error {
	assets, err := parsercatalog.Assets()
	if err != nil {
		return err
	}
	for _, asset := range assets {
		collection, ok := catalogCollections[asset.Collection]
		if !ok || safeID(asset.ID) == "" || asset.Name == "" || len(asset.Config) == 0 {
			return errors.New("embedded parser catalog has an invalid asset")
		}
		if _, err := store.Get(ctx, collection, asset.ID, 0); err == nil {
			continue // Preserve user revisions and edits across restarts.
		}
		payload, err := json.Marshal(map[string]any{
			"name": asset.Name, "config": asset.Config,
			"review_state": "active", "approved_by": "nli-v20-release",
			"published_by": "nli-v20-release",
		})
		if err != nil {
			return err
		}
		// The embedded catalog is part of the tested, signed release. Treat it as
		// pre-approved so a fresh controller immediately has a deployable baseline.
		if _, err := store.Put(ctx, metastore.Document{Collection: collection, ID: asset.ID, State: "active", Payload: payload, CreatedBy: "nli-v20-release"}); err != nil {
			return err
		}
	}
	return nil
}
