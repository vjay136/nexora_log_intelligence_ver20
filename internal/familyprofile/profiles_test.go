package familyprofile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	parserpolicy "nexora.local/nli-v20/parser-policy"
)

func TestBuiltinProfilesHaveReviewedImmutableProvenance(t *testing.T) {
	expectedHashes := map[string]string{
		"nli_reviewed_imap":           "d30d24a86af32d4609356683e1fb6918f9767adf282ec00ce9114b0033bec2ee",
		"nli_reviewed_mss_capd":       "3ce8f380b35d61019cd41579e12f7741c198d5c7c1c63b7ee871fa675fecd249",
		"nli_reviewed_mta":            "8eadf560e3b90c34fad2892bf2a2d4b60f88fabd1553265b5a1876578c2cfed4",
		"nli_reviewed_pop":            "54fd87de8338cb1aa74be5d9ff6c411cce6a5b0fd49cadbc683a597f8bddde95",
		"nli_reviewed_pss_pabd":       "34ced22638e86ac62541902567a8ac9d7afd499ab1d46ed0e26cf6950784c9ab",
		"nli_reviewed_smtp_in":        "528887f964ed9f7547e7c48af3bcc58f674e588cde6997c4910d9e618344b824",
		"nli_reviewed_smtp_out":       "f1248d40f34c731fbc31a3b215d70e5d769e3448cab38548468947c66d31a198",
		"nli_reviewed_webtop_webmail": "af6f9be537288a7d41cf6eec468be5ede0171452526db17ac69a8e1957afb6d6",
	}
	profiles, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != len(expectedHashes) {
		t.Fatalf("builtin profile count = %d, want %d", len(profiles), len(expectedHashes))
	}
	for _, profile := range profiles {
		pack := profile.Pack
		if pack.SHA256 != expectedHashes[pack.ID] {
			t.Errorf("%s hash = %s", pack.ID, pack.SHA256)
		}
		canonical, err := parserpolicy.CanonicalParserPackHash(pack)
		if err != nil || canonical != pack.SHA256 {
			t.Errorf("%s canonical hash mismatch: %s / %v", pack.ID, canonical, err)
		}
		if pack.ReviewState != parserpolicy.ReviewStateActivated || pack.ActivatedAt == nil || pack.SourceType != "manual" || len(pack.SourceHash) != 64 {
			t.Errorf("%s activation/provenance is incomplete", pack.ID)
		}
		if pack.Metadata["profile_provenance"] != "explicit-product-owned-contract-not-importer-derived" ||
			pack.Metadata["corpus_archive_sha256"] != "29ed50f36785fdf0f5693ab2a44d3613348b0898ec078567836286fac1781fa6" ||
			len(pack.Metadata["key_contract_sha256"]) != 64 {
			t.Errorf("%s reviewed metadata is incomplete", pack.ID)
		}
		if len(pack.Rules) != 1 || len(pack.Rules[0].ServiceSelectors) == 0 || len(pack.RegressionCases) != 1 {
			t.Errorf("%s runtime selectors/regressions are incomplete", pack.ID)
		}
	}
	smtpIn := findProfile(t, profiles, "nli_reviewed_smtp_in").Pack
	if smtpIn.Metadata["field_alias.source"] != "input.source" || smtpIn.Metadata["field_alias.envelope"] != "input.envelope" {
		t.Error("SMTP-IN lacks reserved source/envelope aliases")
	}
	smtpOut := findProfile(t, profiles, "nli_reviewed_smtp_out").Pack
	if smtpOut.Metadata["field_alias.source"] != "input.source" {
		t.Error("SMTP-OUT lacks the reviewed source alias")
	}
	if _, invented := smtpOut.Metadata["field_alias.envelope"]; invented {
		t.Error("SMTP-OUT invented an envelope alias outside its supplied contract")
	}
}

func TestManagedDirectoryPreservesApprovedStateAndRejectsUnsafePacks(t *testing.T) {
	builtin := findProfile(t, mustBuiltins(t), "nli_reviewed_imap").Pack

	t.Run("approved remains approved", func(t *testing.T) {
		directory := t.TempDir()
		pack := clonePack(t, builtin)
		pack.ReviewState = parserpolicy.ReviewStateApproved
		pack.ActivatedAt = nil
		pack.Revision++
		sealAndWrite(t, directory, pack)
		profiles, err := LoadManagedDirectory(directory)
		if err != nil {
			t.Fatal(err)
		}
		if len(profiles) != 1 || profiles[0].Pack.ReviewState != parserpolicy.ReviewStateApproved || profiles[0].Pack.ActivatedAt != nil {
			t.Fatalf("approved pack was mutated or omitted: %+v", profiles)
		}
	})

	for _, state := range []parserpolicy.ReviewState{parserpolicy.ReviewStateDraft, parserpolicy.ReviewStatePending} {
		t.Run("reject "+string(state), func(t *testing.T) {
			directory := t.TempDir()
			pack := clonePack(t, builtin)
			pack.ReviewState = state
			pack.ActivatedAt = nil
			sealAndWrite(t, directory, pack)
			if _, err := LoadManagedDirectory(directory); err == nil || !strings.Contains(err.Error(), "only approved or activated") {
				t.Fatalf("%s pack was not rejected: %v", state, err)
			}
		})
	}

	t.Run("reject canonical hash tamper", func(t *testing.T) {
		directory := t.TempDir()
		pack := clonePack(t, builtin)
		pack.Author = "tampered-author"
		writePack(t, directory, pack)
		if _, err := LoadManagedDirectory(directory); err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
			t.Fatalf("tampered pack was not rejected: %v", err)
		}
	})
}

func TestManagedGenerationReplacementAndRollbackAreDeterministic(t *testing.T) {
	builtins := mustBuiltins(t)
	builtin := findProfile(t, builtins, "nli_reviewed_pop").Pack
	directory := t.TempDir()

	updated := clonePack(t, builtin)
	updated.ReviewState = parserpolicy.ReviewStateApproved
	updated.ActivatedAt = nil
	updated.Revision = 2
	updated.Author = "controller-reviewed-update"
	sealAndWrite(t, directory, updated)
	resolved, err := Resolve(directory)
	if err != nil {
		t.Fatal(err)
	}
	active := findProfile(t, resolved, builtin.ID)
	if !active.Managed || active.Pack.Revision != 2 || active.Pack.ReviewState != parserpolicy.ReviewStateApproved {
		t.Fatalf("managed active generation was not selected: %+v", active)
	}

	// The controller rollback is an atomic directory-generation swap. Loading
	// the restored prior bytes must therefore restore the prior immutable pack.
	writePack(t, directory, builtin)
	resolved, err = Resolve(directory)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack := findProfile(t, resolved, builtin.ID)
	if !rolledBack.Managed || rolledBack.Pack.Revision != 1 || rolledBack.Pack.SHA256 != builtin.SHA256 {
		t.Fatalf("rollback generation was not selected: %+v", rolledBack)
	}
}

func mustBuiltins(t *testing.T) []Profile {
	t.Helper()
	profiles, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	return profiles
}

func findProfile(t *testing.T, profiles []Profile, id string) Profile {
	t.Helper()
	for _, profile := range profiles {
		if profile.Pack.ID == id {
			return profile
		}
	}
	t.Fatalf("profile %s not found", id)
	return Profile{}
}

func clonePack(t *testing.T, pack *parserpolicy.ParserPack) *parserpolicy.ParserPack {
	t.Helper()
	data, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := parserpolicy.LoadParserPackBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	return clone
}

func sealAndWrite(t *testing.T, directory string, pack *parserpolicy.ParserPack) {
	t.Helper()
	if err := parserpolicy.SealParserPack(pack); err != nil {
		t.Fatal(err)
	}
	writePack(t, directory, pack)
}

func writePack(t *testing.T, directory string, pack *parserpolicy.ParserPack) {
	t.Helper()
	data, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "managed.parserpack.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
