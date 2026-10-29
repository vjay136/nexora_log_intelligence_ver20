// Package catalog provides the audited, read-only parser inventory embedded
// into V20 binaries so the Controller can seed editable catalog tabs at startup.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed nli-v20-parser-catalog.json
var asset []byte

type Asset struct {
	Collection string         `json:"collection"`
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Config     map[string]any `json:"config"`
}

type document struct {
	SchemaVersion string  `json:"schema_version"`
	Product       string  `json:"product"`
	UIAssets      []Asset `json:"ui_assets"`
}

func Assets() ([]Asset, error) {
	var decoded document
	if err := json.Unmarshal(asset, &decoded); err != nil {
		return nil, fmt.Errorf("decode embedded parser catalog: %w", err)
	}
	if decoded.SchemaVersion != "1" || decoded.Product != "Nexora Log Intelligence Ver20" || len(decoded.UIAssets) == 0 {
		return nil, fmt.Errorf("embedded parser catalog identity or assets are invalid")
	}
	assets := append([]Asset(nil), decoded.UIAssets...)
	return assets, nil
}
