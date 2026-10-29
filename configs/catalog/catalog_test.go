package catalog_test

import (
	"testing"

	"nexora.local/nli-v20/configs/catalog"
)

func TestEmbeddedCatalogIdentityAndFreshCopies(t *testing.T) {
	assets, err := catalog.Assets()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) == 0 {
		t.Fatal("embedded catalog is empty")
	}
	seen := make(map[string]bool, len(assets))
	for _, asset := range assets {
		if asset.Collection == "" || asset.ID == "" || asset.Name == "" || asset.Config == nil {
			t.Fatalf("incomplete embedded catalog asset: %#v", asset)
		}
		key := asset.Collection + "/" + asset.ID
		if seen[key] {
			t.Fatalf("duplicate embedded catalog identity %q", key)
		}
		seen[key] = true
	}

	assets[0].Name = "mutated"
	again, err := catalog.Assets()
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Name == "mutated" {
		t.Fatal("Assets returned shared mutable state")
	}
}
