package engine

import (
	"strings"

	"nexora.local/nli-v20/internal/mixedkv"
)

var defaultMixedKVParser = mixedkv.MustCompile(mixedkv.DefaultConfig())

func parseMixedKVFields(input string) (map[string]interface{}, error) {
	result, err := defaultMixedKVParser.Parse(strings.TrimSpace(input))
	if err != nil {
		return nil, err
	}
	return result.Fields, nil
}
