package geoipdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	metastore "nexora.local/nli-v20/internal/store"
)

// PublishCandidate uploads the three immutable MMDB objects and stores only
// provenance metadata in Supabase. It is intentionally unavailable until the
// administrator accepts the upstream GeoLite2 EULA/CC BY-SA terms. It marks
// every upload pending approval; this function never initiates rollout.
func PublishCandidate(ctx context.Context, root, generation, createdBy string, licenseAccepted bool, blobs metastore.BlobStore, metadata metastore.Store) (metastore.Document, error) {
	if !licenseAccepted {
		return metastore.Document{}, errors.New("GeoLite2 license acceptance is required before uploading databases to Supabase")
	}
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" || blobs == nil || metadata == nil {
		return metastore.Document{}, errors.New("administrator identity, private blob storage, and metadata store are required")
	}
	manifest, err := ValidateGeneration(root, generation)
	if err != nil {
		return metastore.Document{}, err
	}
	objects := make([]map[string]any, 0, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		path := filepath.Join(root, "generations", generation, artifact.Name)
		file, err := os.Open(path)
		if err != nil {
			return metastore.Document{}, err
		}
		object := "sha256/" + artifact.SHA256 + "-" + artifact.Name
		err = blobs.PutVerified(ctx, Bucket, object, file, artifact.Size, "application/octet-stream", artifact.SHA256)
		closeErr := file.Close()
		if err != nil {
			return metastore.Document{}, fmt.Errorf("upload %s to private GeoIP Storage: %w", artifact.Name, err)
		}
		if closeErr != nil {
			return metastore.Document{}, closeErr
		}
		objects = append(objects, map[string]any{"name": artifact.Name, "database_type": artifact.Database, "sha256": artifact.SHA256, "size_bytes": artifact.Size, "storage_bucket": Bucket, "storage_path": object})
	}
	payload := map[string]any{
		"source": manifest.Source, "source_commit": manifest.SourceCommit, "fetched_at": manifest.FetchedAt,
		"license": manifest.License, "license_eula": manifest.LicenseEULA, "license_cc_by_sa": LicenseCCBYSA,
		"attribution": manifest.Attribution, "artifacts": objects,
		"status": "pending_approval", "approval_required": true, "auto_rollout": false,
		"candidate_only": true, "license_accepted_by": createdBy,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return metastore.Document{}, err
	}
	return metadata.Put(ctx, metastore.Document{Collection: "geoip-candidates", ID: "geolite-" + generation, State: "pending_approval", Payload: encoded, CreatedBy: createdBy})
}
