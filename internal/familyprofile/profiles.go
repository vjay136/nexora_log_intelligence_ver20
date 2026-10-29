// Package familyprofile loads immutable product profiles and bounded,
// controller-delivered ParserPacks for production parser routing.
package familyprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	parserassets "nexora.local/nli-v20/configs/parsers"
	parserpolicy "nexora.local/nli-v20/parser-policy"
)

const (
	// MaxDirectoryEntries bounds work even when most files are legacy parsers.
	MaxDirectoryEntries = 128
	// MaxManagedPacks bounds the number of controller-delivered candidates.
	MaxManagedPacks = 32
	// MaxManagedBytes bounds aggregate JSON read from one active generation.
	MaxManagedBytes = 32 * 1024 * 1024
	// MaxAttemptsPerPack combines with the adapter's pack bound to cap routing.
	MaxAttemptsPerPack = 4
)

var expectedBuiltinIDs = map[string]struct{}{
	"nli_reviewed_imap":           {},
	"nli_reviewed_mss_capd":       {},
	"nli_reviewed_mta":            {},
	"nli_reviewed_pop":            {},
	"nli_reviewed_pss_pabd":       {},
	"nli_reviewed_smtp_in":        {},
	"nli_reviewed_smtp_out":       {},
	"nli_reviewed_webtop_webmail": {},
}

// Profile is one validated runtime pack and its compiled executor.
type Profile struct {
	Pack     *parserpolicy.ParserPack
	Executor *parserpolicy.ParserPackExecutor
	Order    int
	Name     string
	Managed  bool
}

// Resolve returns embedded profiles merged with an optional active managed
// generation. A managed pack with the same ID replaces the embedded revision;
// this lets the controller's existing active/rollback directory swap select the
// generation without mutating or auto-activating pack state here.
func Resolve(directory string) ([]Profile, error) {
	profiles, err := Builtins()
	if err != nil {
		return nil, err
	}
	managed, err := LoadManagedDirectory(directory)
	if err != nil {
		return nil, err
	}

	byID := make(map[string]Profile, len(profiles)+len(managed))
	for _, profile := range profiles {
		byID[profile.Pack.ID] = profile
	}
	managedIDs := make(map[string]struct{}, len(managed))
	for _, profile := range managed {
		if _, duplicate := managedIDs[profile.Pack.ID]; duplicate {
			return nil, fmt.Errorf("managed parser generation repeats pack ID %q", profile.Pack.ID)
		}
		managedIDs[profile.Pack.ID] = struct{}{}
		byID[profile.Pack.ID] = profile
	}

	resolved := make([]Profile, 0, len(byID))
	for _, profile := range byID {
		resolved = append(resolved, profile)
	}
	sortProfiles(resolved)
	return resolved, nil
}

// Builtins validates and compiles all embedded product-owned profiles.
func Builtins() ([]Profile, error) {
	assets, err := parserassets.All()
	if err != nil {
		return nil, err
	}
	profiles := make([]Profile, 0, len(assets))
	seen := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		profile, err := compile(asset.Name, asset.Data, false, true)
		if err != nil {
			return nil, fmt.Errorf("embedded parser asset %s: %w", asset.Name, err)
		}
		if _, duplicate := seen[profile.Pack.ID]; duplicate {
			return nil, fmt.Errorf("embedded parser pack ID %q is duplicated", profile.Pack.ID)
		}
		seen[profile.Pack.ID] = struct{}{}
		profiles = append(profiles, profile)
	}
	if len(seen) != len(expectedBuiltinIDs) {
		return nil, fmt.Errorf("embedded parser profile count is %d, want %d", len(seen), len(expectedBuiltinIDs))
	}
	for id := range expectedBuiltinIDs {
		if _, ok := seen[id]; !ok {
			return nil, fmt.Errorf("embedded parser profile %q is missing", id)
		}
	}
	sortProfiles(profiles)
	return profiles, nil
}

// LoadManagedDirectory loads ParserPacks from one flat active generation. It
// ignores legacy parser JSON documents, rejects symlinks and writable assets on
// Unix, and fails closed for malformed, draft, pending, or tampered packs.
func LoadManagedDirectory(directory string) ([]Profile, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, nil
	}
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("managed parser directory must be absolute")
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect managed parser directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("managed parser path must be a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("managed parser directory must not be group/world writable")
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read managed parser directory: %w", err)
	}
	if len(entries) > MaxDirectoryEntries {
		return nil, fmt.Errorf("managed parser directory has %d entries; limit is %d", len(entries), MaxDirectoryEntries)
	}

	profiles := make([]Profile, 0, len(entries))
	totalBytes := int64(0)
	for _, entry := range entries {
		entryPath := filepath.Join(directory, entry.Name())
		entryInfo, err := os.Lstat(entryPath)
		if err != nil {
			return nil, fmt.Errorf("inspect managed parser entry %s: %w", entry.Name(), err)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("managed parser entry %s is a symlink", entry.Name())
		}
		if entryInfo.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		if !entryInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("managed parser entry %s is not a regular file", entry.Name())
		}
		if runtime.GOOS != "windows" && entryInfo.Mode().Perm()&0o022 != 0 {
			return nil, fmt.Errorf("managed parser entry %s must not be group/world writable", entry.Name())
		}
		if entryInfo.Size() <= 0 || entryInfo.Size() > parserpolicy.MaxPackSize {
			return nil, fmt.Errorf("managed parser entry %s has invalid size %d", entry.Name(), entryInfo.Size())
		}
		totalBytes += entryInfo.Size()
		if totalBytes > MaxManagedBytes {
			return nil, fmt.Errorf("managed parser generation exceeds %d bytes", MaxManagedBytes)
		}

		file, err := os.Open(entryPath)
		if err != nil {
			return nil, fmt.Errorf("open managed parser entry %s: %w", entry.Name(), err)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, parserpolicy.MaxPackSize+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read managed parser entry %s: %w", entry.Name(), readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close managed parser entry %s: %w", entry.Name(), closeErr)
		}
		if len(data) > parserpolicy.MaxPackSize {
			return nil, fmt.Errorf("managed parser entry %s exceeds %d bytes", entry.Name(), parserpolicy.MaxPackSize)
		}
		after, err := os.Lstat(entryPath)
		if err != nil || !os.SameFile(entryInfo, after) || after.Size() != int64(len(data)) {
			return nil, fmt.Errorf("managed parser entry %s changed while loading", entry.Name())
		}
		if !LooksLikePackDocument(data) {
			continue
		}
		profile, err := compile(entry.Name(), data, true, false)
		if err != nil {
			return nil, fmt.Errorf("managed parser entry %s: %w", entry.Name(), err)
		}
		profiles = append(profiles, profile)
		if len(profiles) > MaxManagedPacks {
			return nil, fmt.Errorf("managed parser generation exceeds %d packs", MaxManagedPacks)
		}
	}
	sortProfiles(profiles)
	return profiles, nil
}

// LooksLikePackDocument distinguishes ParserPacks from legacy ParserDef JSON in
// the shared parser generation directory. ParserPack-specific keys are treated
// as authoritative even when the document is malformed, so tampering fails
// closed rather than silently falling back to the legacy loader.
func LooksLikePackDocument(data []byte) bool {
	markers := [][]byte{
		[]byte(`"review_state"`), []byte(`"resource_limits"`),
		[]byte(`"source_type"`), []byte(`"regression_cases"`),
	}
	for _, marker := range markers {
		if bytes.Contains(data, marker) {
			return true
		}
	}
	return false
}

func compile(name string, data []byte, managed, builtin bool) (Profile, error) {
	pack, err := parserpolicy.LoadParserPackBytes(data)
	if err != nil {
		return Profile{}, err
	}
	if pack.ReviewState != parserpolicy.ReviewStateApproved && pack.ReviewState != parserpolicy.ReviewStateActivated {
		return Profile{}, fmt.Errorf("pack %q is %s; only approved or activated packs may run", pack.ID, pack.ReviewState)
	}
	if len(pack.RegressionCases) == 0 {
		return Profile{}, fmt.Errorf("pack %q has no reviewed regression cases", pack.ID)
	}
	if pack.ResourceLimits.MaxAttempts > MaxAttemptsPerPack {
		return Profile{}, fmt.Errorf("pack %q permits %d attempts; runtime limit is %d", pack.ID, pack.ResourceLimits.MaxAttempts, MaxAttemptsPerPack)
	}
	if builtin {
		if pack.ReviewState != parserpolicy.ReviewStateActivated || pack.SourceType != "manual" || pack.SourceHash == "" {
			return Profile{}, fmt.Errorf("product profile %q lacks activated manual provenance", pack.ID)
		}
		if pack.Metadata["profile_kind"] != "product-reviewed-family-contract" ||
			pack.Metadata["profile_provenance"] != "explicit-product-owned-contract-not-importer-derived" ||
			pack.Metadata["key_contract_sha256"] == "" {
			return Profile{}, fmt.Errorf("product profile %q lacks reviewed contract provenance", pack.ID)
		}
	}
	order, err := profileOrder(pack)
	if err != nil {
		return Profile{}, err
	}
	executor, err := parserpolicy.NewParserPackExecutor(pack)
	if err != nil {
		return Profile{}, err
	}
	if pack.ReviewState == parserpolicy.ReviewStateApproved {
		if err := runApprovedRegressions(pack, executor); err != nil {
			return Profile{}, fmt.Errorf("approved pack regression failed: %w", err)
		}
	}
	return Profile{Pack: pack, Executor: executor, Order: order, Name: name, Managed: managed}, nil
}

func runApprovedRegressions(pack *parserpolicy.ParserPack, executor *parserpolicy.ParserPackExecutor) error {
	for _, regression := range pack.RegressionCases {
		result, err := executor.Execute(&parserpolicy.ExecutionRequest{
			Input: regression.Input, Service: regression.Service, SourceType: regression.SourceType,
			NodeID: "profile-validation", Context: context.Background(),
		})
		if err != nil {
			return fmt.Errorf("%s: %w", regression.Name, err)
		}
		if result.Quarantined || result.EventPayload == nil || result.ParserID != regression.ExpectedParser {
			return fmt.Errorf("%s: parser result mismatch", regression.Name)
		}
		if regression.ExpectedRoute != "" && result.ParseTelemetry.Route != regression.ExpectedRoute {
			return fmt.Errorf("%s: route mismatch", regression.Name)
		}
		actual, err := json.Marshal(result.EventPayload.Fields)
		if err != nil {
			return err
		}
		expected, err := json.Marshal(regression.ExpectedPayload)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, expected) {
			return fmt.Errorf("%s: payload mismatch", regression.Name)
		}
	}
	return nil
}

func profileOrder(pack *parserpolicy.ParserPack) (int, error) {
	value := strings.TrimSpace(pack.Metadata["runtime_order"])
	if value == "" {
		return 10000, nil
	}
	order, err := strconv.Atoi(value)
	if err != nil || order < 1 || order > 1000000 {
		return 0, fmt.Errorf("pack %q has invalid runtime_order %q", pack.ID, value)
	}
	return order, nil
}

func sortProfiles(profiles []Profile) {
	sort.SliceStable(profiles, func(i, j int) bool {
		if profiles[i].Order != profiles[j].Order {
			return profiles[i].Order < profiles[j].Order
		}
		if profiles[i].Pack.ID != profiles[j].Pack.ID {
			return profiles[i].Pack.ID < profiles[j].Pack.ID
		}
		return profiles[i].Pack.Revision > profiles[j].Pack.Revision
	})
}
