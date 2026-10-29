package controlclient

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/protocol"
	parserpolicy "nexora.local/nli-v20/parser-policy"
)

type ArtifactFetcher interface {
	Fetch(context.Context, protocol.ArtifactRef) ([]byte, error)
}

type HTTPArtifactFetcher struct {
	Client            *http.Client
	ControllerBaseURL string
	AllowedHosts      []string
	AllowInsecureHTTP bool
	MaxArtifactBytes  int64
}

func (fetcher HTTPArtifactFetcher) Fetch(ctx context.Context, artifact protocol.ArtifactRef) ([]byte, error) {
	base, err := url.Parse(fetcher.ControllerBaseURL)
	if err != nil || base.Hostname() == "" {
		return nil, fmt.Errorf("controller base URL is invalid")
	}
	location, err := url.Parse(artifact.URL)
	if err != nil {
		return nil, fmt.Errorf("artifact URL is invalid")
	}
	location = base.ResolveReference(location)
	if location.Scheme != "https" && !fetcher.AllowInsecureHTTP {
		return nil, fmt.Errorf("artifact URL must use HTTPS")
	}
	if !hostAllowed(location.Hostname(), fetcher.AllowedHosts) && !strings.EqualFold(location.Hostname(), base.Hostname()) {
		return nil, fmt.Errorf("artifact host is not controller-authorized")
	}
	limit := fetcher.MaxArtifactBytes
	if limit <= 0 || limit > maxPackageBytes {
		limit = maxPackageBytes
	}
	if artifact.Size > limit {
		return nil, fmt.Errorf("artifact exceeds client limit")
	}
	client := fetcher.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifact download HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("artifact exceeds client limit")
	}
	return data, nil
}

type StagedArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Type   string `json:"type"`
}

type ManifestState struct {
	ManifestID string                                `json:"manifest_id,omitempty"`
	Revision   uint64                                `json:"revision"`
	Desired    protocol.HashState                    `json:"desired"`
	Staged     protocol.HashState                    `json:"staged"`
	Active     protocol.HashState                    `json:"active"`
	Rollback   protocol.HashState                    `json:"rollback"`
	Status     string                                `json:"status"`
	Artifacts  map[string]StagedArtifact             `json:"artifacts,omitempty"`
	Journal    map[string]protocol.SyncCommandStatus `json:"journal,omitempty"`
}

type ManifestClient struct {
	mu            sync.Mutex
	root          string
	statePath     string
	clientVersion string
	trustedKeys   map[string]ed25519.PublicKey
	fetcher       ArtifactFetcher
	state         ManifestState
}

func NewManifestClient(root, clientVersion string, trustedKeys map[string]ed25519.PublicKey, fetcher ArtifactFetcher) (*ManifestClient, error) {
	if !filepath.IsAbs(root) || clientVersion == "" || len(trustedKeys) == 0 || fetcher == nil {
		return nil, fmt.Errorf("absolute root, client version, trusted keys, and fetcher are required")
	}
	client := &ManifestClient{root: root, statePath: filepath.Join(root, "manifest-state.json"), clientVersion: clientVersion, trustedKeys: trustedKeys, fetcher: fetcher}
	client.state.Artifacts = map[string]StagedArtifact{}
	client.state.Journal = map[string]protocol.SyncCommandStatus{}
	client.loadState()
	return client, nil
}

func (client *ManifestClient) Stage(ctx context.Context, manifest protocol.ImmutableManifest, now time.Time) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if err := manifest.Verify(client.trustedKeys, now); err != nil {
		return err
	}
	comparison, err := protocol.CompareSemver(client.clientVersion, manifest.MinimumClientVersion)
	if err != nil || comparison < 0 {
		return fmt.Errorf("client %s is below required %s", client.clientVersion, manifest.MinimumClientVersion)
	}
	if client.state.Revision > manifest.Revision {
		return fmt.Errorf("manifest revision regression")
	}
	if client.state.Revision == manifest.Revision && client.state.ManifestID != "" && client.state.ManifestID != manifest.ID {
		return fmt.Errorf("manifest revision conflict")
	}
	if client.state.ManifestID == manifest.ID && client.state.Revision == manifest.Revision && (client.state.Staged.Equal(manifest.Desired) || client.state.Active.Equal(manifest.Desired)) {
		return nil
	}
	generation := safeGeneration(manifest.ID, manifest.Revision)
	temporary := filepath.Join(client.root, "staging", generation+".tmp")
	final := filepath.Join(client.root, "staging", generation)
	_ = os.RemoveAll(temporary)
	if err := os.MkdirAll(temporary, 0o750); err != nil {
		return err
	}
	artifacts := map[string]StagedArtifact{}
	for _, artifact := range manifest.Artifacts {
		if filepath.Base(artifact.Name) != artifact.Name || artifact.Name == "." || artifact.Name == ".." {
			_ = os.RemoveAll(temporary)
			return fmt.Errorf("unsafe artifact name %q", artifact.Name)
		}
		data, err := client.fetcher.Fetch(ctx, artifact)
		if err != nil {
			_ = os.RemoveAll(temporary)
			return err
		}
		if int64(len(data)) != artifact.Size {
			_ = os.RemoveAll(temporary)
			return fmt.Errorf("artifact %q size mismatch", artifact.Name)
		}
		digest := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(digest[:]), artifact.SHA256) {
			_ = os.RemoveAll(temporary)
			return fmt.Errorf("artifact %q checksum mismatch", artifact.Name)
		}
		if err := validateArtifactContent(artifact.Type, data); err != nil {
			_ = os.RemoveAll(temporary)
			return fmt.Errorf("artifact %q validation: %w", artifact.Name, err)
		}
		path := filepath.Join(temporary, artifact.Name)
		if err := atomicWrite(path, data, 0o600); err != nil {
			_ = os.RemoveAll(temporary)
			return err
		}
		artifacts[artifact.Name] = StagedArtifact{Path: filepath.Join(final, artifact.Name), SHA256: artifact.SHA256, Size: artifact.Size, Type: artifact.Type}
	}
	_ = os.RemoveAll(final)
	if err := os.Rename(temporary, final); err != nil {
		_ = os.RemoveAll(temporary)
		return err
	}
	client.state.ManifestID, client.state.Revision = manifest.ID, manifest.Revision
	client.state.Desired, client.state.Staged = manifest.Desired, manifest.Desired
	client.state.Rollback = manifest.Rollback
	client.state.Status, client.state.Artifacts = "staged", artifacts
	return client.saveStateLocked()
}

func (client *ManifestClient) ValidateStaged() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.state.Staged.Empty() || !client.state.Staged.Equal(client.state.Desired) {
		return fmt.Errorf("staged generation does not match desired state")
	}
	for name, artifact := range client.state.Artifacts {
		data, err := os.ReadFile(artifact.Path)
		if err != nil {
			return fmt.Errorf("staged artifact %q missing: %w", name, err)
		}
		if int64(len(data)) != artifact.Size {
			return fmt.Errorf("staged artifact %q size changed", name)
		}
		digest := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(digest[:]), artifact.SHA256) {
			return fmt.Errorf("staged artifact %q checksum changed", name)
		}
		if err := validateArtifactContent(artifact.Type, data); err != nil {
			return fmt.Errorf("staged artifact %q validation: %w", name, err)
		}
	}
	client.state.Status = "validated"
	return client.saveStateLocked()
}

func (client *ManifestClient) Activate() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.state.Status != "validated" || !client.state.Staged.Equal(client.state.Desired) {
		return fmt.Errorf("validated desired generation is required")
	}
	generation := safeGeneration(client.state.ManifestID, client.state.Revision)
	staged := filepath.Join(client.root, "staging", generation)
	active, rollback := filepath.Join(client.root, "active"), filepath.Join(client.root, "rollback")
	if _, err := os.Stat(staged); err != nil {
		return fmt.Errorf("staged generation missing: %w", err)
	}
	_ = os.RemoveAll(rollback)
	if _, err := os.Stat(active); err == nil {
		if err := os.Rename(active, rollback); err != nil {
			return err
		}
	}
	if err := os.Rename(staged, active); err != nil {
		_ = os.Rename(rollback, active)
		return err
	}
	if !client.state.Active.Empty() {
		client.state.Rollback = client.state.Active
	}
	client.state.Active, client.state.Staged = client.state.Desired, protocol.HashState{}
	client.state.Status = "active"
	client.state.Artifacts = map[string]StagedArtifact{}
	return client.saveStateLocked()
}

func (client *ManifestClient) Rollback() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.state.Rollback.Empty() {
		return fmt.Errorf("rollback generation is unavailable")
	}
	active, rollback := filepath.Join(client.root, "active"), filepath.Join(client.root, "rollback")
	if _, err := os.Stat(rollback); err != nil {
		return fmt.Errorf("rollback artifacts are unavailable")
	}
	failed := filepath.Join(client.root, "failed-active")
	_ = os.RemoveAll(failed)
	if _, err := os.Stat(active); err == nil {
		if err := os.Rename(active, failed); err != nil {
			return err
		}
	}
	if err := os.Rename(rollback, active); err != nil {
		_ = os.Rename(failed, active)
		return err
	}
	_ = os.RemoveAll(failed)
	client.state.Active, client.state.Staged = client.state.Rollback, protocol.HashState{}
	client.state.Status = "rolled_back"
	return client.saveStateLocked()
}

func (client *ManifestClient) RecordCommand(status protocol.SyncCommandStatus) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if previous, exists := client.state.Journal[status.CommandID]; exists {
		if previous.ManifestID == status.ManifestID && previous.Revision == status.Revision && previous.Status == status.Status {
			return nil
		}
		return fmt.Errorf("conflicting command replay")
	}
	client.state.Journal[status.CommandID] = status
	return client.saveStateLocked()
}

func (client *ManifestClient) State() ManifestState {
	client.mu.Lock()
	defer client.mu.Unlock()
	data, _ := json.Marshal(client.state)
	var copyOfState ManifestState
	_ = json.Unmarshal(data, &copyOfState)
	return copyOfState
}

func (client *ManifestClient) loadState() {
	data, err := os.ReadFile(client.statePath)
	if err == nil {
		_ = json.Unmarshal(data, &client.state)
	}
	if client.state.Artifacts == nil {
		client.state.Artifacts = map[string]StagedArtifact{}
	}
	if client.state.Journal == nil {
		client.state.Journal = map[string]protocol.SyncCommandStatus{}
	}
}

func (client *ManifestClient) saveStateLocked() error {
	data, err := json.MarshalIndent(client.state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(client.statePath, data, 0o600)
}

func safeGeneration(id string, revision uint64) string {
	var builder strings.Builder
	for _, value := range id {
		if (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || (value >= '0' && value <= '9') || value == '-' || value == '_' {
			builder.WriteRune(value)
		}
	}
	if builder.Len() == 0 {
		builder.WriteString("manifest")
	}
	return fmt.Sprintf("%s-%d", builder.String(), revision)
}

func validateArtifactContent(kind string, data []byte) error {
	switch kind {
	case "config":
		if !json.Valid(data) {
			return fmt.Errorf("configuration is not valid JSON")
		}
	case "parser_pack":
		pack, err := parserpolicy.LoadParserPackBytes(data)
		if err != nil {
			return err
		}
		if pack.ReviewState != parserpolicy.ReviewStateApproved && pack.ReviewState != parserpolicy.ReviewStateActivated {
			return fmt.Errorf("parser pack must be approved or activated")
		}
		if _, err := parserpolicy.NewParserPackExecutor(pack); err != nil {
			return fmt.Errorf("parser pack regression validation: %w", err)
		}
	case "release":
		if len(data) == 0 {
			return fmt.Errorf("release artifact is empty")
		}
	default:
		return fmt.Errorf("unsupported artifact type %q", kind)
	}
	return nil
}
