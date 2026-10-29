package parserassets_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	parserassets "nexora.local/nli-v20/configs/parsers"
)

func TestEmbeddedParserPacksAreOrderedValidAndDefensive(t *testing.T) {
	assets, err := parserassets.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 8 {
		t.Fatalf("embedded parser-pack count = %d, want 8", len(assets))
	}
	names := make([]string, len(assets))
	for i, asset := range assets {
		names[i] = asset.Name
		if !strings.HasSuffix(asset.Name, ".parserpack.json") || !json.Valid(asset.Data) {
			t.Fatalf("invalid embedded parser pack %q", asset.Name)
		}
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("parser packs are not sorted: %#v", names)
	}

	original := append([]byte(nil), assets[0].Data...)
	assets[0].Data[0] ^= 0xff
	again, err := parserassets.All()
	if err != nil {
		t.Fatal(err)
	}
	if string(again[0].Data) != string(original) {
		t.Fatal("All returned shared mutable parser-pack bytes")
	}
}
