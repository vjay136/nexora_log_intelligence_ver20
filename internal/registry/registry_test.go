package registry_test

import (
	"os"
	"path/filepath"
	"testing"

	"nexora.local/nli-v20/internal/registry"
)

func testParser() *registry.ParserDef {
	return &registry.ParserDef{
		ID: "test.kv", Version: 1, Format: "kvpairs",
		Fields: []registry.FieldDef{
			{Name: "user", Source: "kv:user", Type: "string", Required: true},
			{Name: "count", Source: "kv:count", Type: "integer"},
		},
		Tests: []registry.RegressionTest{{Input: "user=alice count=2", Expected: map[string]interface{}{"user": "alice", "count": float64(2), "nested": map[string]interface{}{"stable": true}}}},
	}
}

func TestRegistryOwnsParserSnapshots(t *testing.T) {
	r := registry.New()
	original := testParser()
	r.Register(original)
	original.Fields[0].Name = "mutated"
	original.Tests[0].Expected["nested"].(map[string]interface{})["stable"] = false

	got, ok := r.Get("test.kv")
	if !ok || got.Fields[0].Name != "user" {
		t.Fatalf("stored parser = %#v", got)
	}
	if stable := got.Tests[0].Expected["nested"].(map[string]interface{})["stable"]; stable != true {
		t.Fatalf("nested expected value was not copied: %#v", got.Tests[0].Expected)
	}
	got.Fields[0].Name = "changed-after-get"
	again, _ := r.Get("test.kv")
	if again.Fields[0].Name != "user" {
		t.Fatalf("Get exposed registry state: %#v", again)
	}
}

func TestValidateApplyRegressionAndPersistence(t *testing.T) {
	p := testParser()
	delete(p.Tests[0].Expected, "nested")
	if err := registry.Validate(p); err != nil {
		t.Fatal(err)
	}
	fields, failures := registry.Apply(p, `user="alice smith" count=2`)
	if len(failures) != 0 || fields["user"] != "alice smith" || fields["count"] != int64(2) {
		t.Fatalf("fields=%#v failures=%#v", fields, failures)
	}
	if failures := registry.RunRegressionTests(p); len(failures) != 0 {
		t.Fatalf("regression failures = %#v", failures)
	}

	dir := t.TempDir()
	if err := registry.New().SaveParser(dir, p); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "test.kv-v1.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	loaded := registry.New()
	if err := loaded.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if got, ok := loaded.Get("test.kv"); !ok || got.Version != 1 {
		t.Fatalf("loaded parser = %#v, ok=%v", got, ok)
	}
}

func TestValidationRejectsUnsafeDefinitions(t *testing.T) {
	tests := []*registry.ParserDef{
		nil,
		{ID: "../escape", Version: 1, Fields: []registry.FieldDef{{Name: "x", Source: "kv:x"}}},
		{ID: "valid.id", Version: 0, Fields: []registry.FieldDef{{Name: "x", Source: "kv:x"}}},
		{ID: "valid.id", Version: 1, Fields: []registry.FieldDef{{Name: "x", Source: "exec:command"}}},
		{ID: "valid.id", Version: 1, PatternType: "regex", Pattern: "[", Fields: []registry.FieldDef{{Name: "x", Source: "group:x"}}},
	}
	for i, p := range tests {
		if err := registry.Validate(p); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestBuiltinParserIDsAreUniqueAndValid(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range registry.BuiltinParsers() {
		if seen[p.ID] {
			t.Fatalf("duplicate built-in parser id %q", p.ID)
		}
		seen[p.ID] = true
		if err := registry.Validate(p); err != nil {
			t.Fatalf("built-in parser %q invalid: %v", p.ID, err)
		}
	}
}
