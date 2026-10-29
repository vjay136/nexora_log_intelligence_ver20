package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	parserpolicy "nexora.local/nli-v20/parser-policy"
	staticlayout "nexora.local/nli-v20/static-layout"
)

const familyParserMaxInputBytes = 1 << 20

var familyProfileKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// FamilyProfileSpec describes a manually reviewed production profile. It is
// deliberately separate from MigrationResult: none of these stages are
// inferred from a filename or attributed to an imported Alloy/Vector source.
type FamilyProfileSpec struct {
	ID                     string
	Family                 string
	ContractKeys           []string
	ReviewedExtensions     []string
	AdditionalReviewedKeys []string
	ReviewReference        string
}

// BuildReviewedFamilyProfile creates a bounded manual profile from explicit
// key contracts. The profile remains pending until a human activates it.
func BuildReviewedFamilyProfile(spec FamilyProfileSpec) (*parserpolicy.ParserPack, error) {
	family, ok := canonicalReviewedFamily(spec.Family)
	if !ok {
		return nil, fmt.Errorf("unsupported reviewed family %q", spec.Family)
	}
	if strings.TrimSpace(spec.ReviewReference) == "" {
		return nil, fmt.Errorf("review reference is required")
	}
	contractKeys, err := normalizeReviewedKeys(spec.ContractKeys)
	if err != nil {
		return nil, fmt.Errorf("contract keys: %w", err)
	}
	extensions, err := normalizeReviewedKeys(spec.ReviewedExtensions)
	if err != nil {
		return nil, fmt.Errorf("reviewed extensions: %w", err)
	}
	additional, err := normalizeReviewedKeys(spec.AdditionalReviewedKeys)
	if err != nil {
		return nil, fmt.Errorf("additional reviewed keys: %w", err)
	}
	keys, err := mergeReviewedKeys(contractKeys, extensions, additional)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one reviewed key is required")
	}

	allowPatterns := exactKeyPatterns(keys)
	aliases := familyCollisionAliases(family, keys)
	rule := parserpolicy.ParserRule{
		ID: "reviewed_" + family, Name: "Reviewed " + family + " family profile", Priority: 1,
		ServiceSelectors: familyServiceSelectors(family), FailurePolicy: parserpolicy.FailurePolicyRejectRule,
		ProjectionMode: parserpolicy.ProjectionModeAll, CountPolicy: parserpolicy.CountPolicyAllowDerived,
	}
	switch family {
	case "imap", "pop", "mta", "smtp":
		rule.Stages = nativeFamilyStages(family, allowPatterns, aliases)
	case "webtop":
		rule.Stages = webtopFamilyStages(allowPatterns, aliases)
	case "pabd", "capd":
		rule.Stages = davFamilyStages(family, allowPatterns, aliases)
		if family == "capd" {
			rule.IncludePredicates = []parserpolicy.Predicate{{
				Type: "regex", Field: "input",
				Pattern: `;CalDavResponse\s+id=[^:]+:[A-Za-z_][A-Za-z0-9_.-]*=`,
			}}
		}
	}

	profileID := identifier(spec.ID)
	if strings.TrimSpace(spec.ID) == "" {
		profileID = "reviewed_" + family + "_profile"
	}
	metadata := map[string]string{
		"profile_kind":             "manual-reviewed-family-contract",
		"profile_provenance":       "explicit-contract-not-importer-derived",
		"importer_parity":          "not-applicable",
		"activation_review":        "pending",
		"review_reference":         spec.ReviewReference,
		"contract_key_count":       fmt.Sprint(len(contractKeys)),
		"reviewed_extension_count": fmt.Sprint(len(extensions)),
		"additional_key_count":     fmt.Sprint(len(additional)),
		"effective_key_count":      fmt.Sprint(len(keys)),
		"key_contract_sha256":      reviewedKeyDigest(contractKeys, extensions, additional),
	}
	for source, target := range aliases {
		metadata["field_alias."+source] = target
	}
	pack := &parserpolicy.ParserPack{
		ID: profileID, Version: parserpolicy.CurrentSchemaVersion, Revision: 1,
		SourceType: "manual", Author: "nli-migration-review", ReviewState: parserpolicy.ReviewStatePending,
		CreatedAt: time.Unix(0, 0).UTC(), Rules: []parserpolicy.ParserRule{rule},
		ResourceLimits: parserpolicy.ResourceLimits{
			MaxInputBytes: familyParserMaxInputBytes, MaxFields: 512, MaxCaptures: 256,
			MaxRegexLength: parserpolicy.MaxRegexLength, MaxStageCount: parserpolicy.MaxStages,
			MaxAttempts: 4, ExecutionDeadlineMs: 5000,
		},
		Metadata: metadata,
	}
	if err := parserpolicy.SealParserPack(pack); err != nil {
		return nil, fmt.Errorf("seal reviewed family profile: %w", err)
	}
	return pack, nil
}

func nativeFamilyStages(service string, allowPatterns []string, aliases map[string]string) []parserpolicy.Stage {
	kvConfig := familyKVConfig("_kv_tail", []string{":", "|"}, allowPatterns, aliases, true)
	if service == "smtp" {
		kvConfig["assignment_separators"] = []string{"=", ";"}
	}
	stages := []parserpolicy.Stage{
		{
			ID: "family_header", Name: "Reviewed native family header", Type: parserpolicy.StageTypeStaticLayout,
			Config: staticlayout.CreateSMTPConfig(), FailurePolicy: parserpolicy.FailurePolicyRejectRule,
		},
		{
			ID: "family_kv", Name: "Reviewed allowlisted family fields", Type: parserpolicy.StageTypeMixedKV,
			Config: kvConfig, FailurePolicy: parserpolicy.FailurePolicyContinue,
		},
	}
	if service == "smtp" {
		stages = append(stages, parserpolicy.Stage{
			ID: "family_types", Name: "Reviewed SMTP field types", Type: parserpolicy.StageTypeTypeConversion,
			Config: map[string]interface{}{"conversions": map[string]string{
				"auth_ok": "bool", "auth_fail": "int", "auth_tries": "int", "score": "int",
			}}, FailurePolicy: parserpolicy.FailurePolicyContinue,
		})
	}
	return stages
}

func webtopFamilyStages(allowPatterns []string, aliases map[string]string) []parserpolicy.Stage {
	return []parserpolicy.Stage{
		{
			ID: "family_header", Name: "Reviewed WEBTOP timestamp and tail", Type: parserpolicy.StageTypeStaticLayout,
			Config: staticlayout.Config{
				MaxInputBytes: familyParserMaxInputBytes, FailOnMissing: true,
				Tokens: []staticlayout.TokenSpec{
					{Index: 0, Name: "date", Type: staticlayout.TokenTypeString, Required: true},
					{Index: 1, Name: "time", Type: staticlayout.TokenTypeString, Required: true},
					{Index: 2, Name: "_kv_tail", Type: staticlayout.TokenTypeRestOfLine, Required: true},
				},
			}, FailurePolicy: parserpolicy.FailurePolicyRejectRule,
		},
		{
			ID: "family_kv", Name: "Reviewed WEBTOP logfmt fields", Type: parserpolicy.StageTypeLogfmt,
			Config:        familyKVConfig("_kv_tail", []string{" ", "\t"}, allowPatterns, aliases, false),
			FailurePolicy: parserpolicy.FailurePolicyRejectRule,
		},
	}
}

func davFamilyStages(service string, allowPatterns []string, aliases map[string]string) []parserpolicy.Stage {
	event := "CARDDAV"
	if service == "capd" {
		event = "CalDavResponse"
	}
	pattern := fmt.Sprintf(`^(?P<date>\d{8})\s+(?P<time>\d{9}[+-]\d{4})\s+(?P<hostname>\S+)\s+(?P<service>\S+)\s+(?P<major_pid>\d+)\s+(?P<pid_status>\d+)\s+(?P<minor_pid>\d+)\s+(?P<event_type>[A-Za-z]+);(?P<event>%s)\s+(?P<_kv_tail>.*)$`, event)
	return []parserpolicy.Stage{
		{
			ID: "family_header", Name: "Reviewed DAV family header", Type: parserpolicy.StageTypeRegex,
			Config: map[string]interface{}{"pattern": pattern}, FailurePolicy: parserpolicy.FailurePolicyRejectRule,
		},
		{
			ID: "family_kv", Name: "Reviewed allowlisted DAV fields", Type: parserpolicy.StageTypeMixedKV,
			Config:        familyKVConfig("_kv_tail", []string{":"}, allowPatterns, aliases, false),
			FailurePolicy: parserpolicy.FailurePolicyContinue,
		},
		{
			ID: "family_types", Name: "Reviewed DAV header numeric type", Type: parserpolicy.StageTypeTypeConversion,
			Config:        map[string]interface{}{"conversions": map[string]string{"pid_status": "int"}},
			FailurePolicy: parserpolicy.FailurePolicyRejectRule,
		},
	}
}

func familyKVConfig(input string, pairSeparators, allowPatterns []string, aliases map[string]string, allowLeadingText bool) map[string]interface{} {
	return map[string]interface{}{
		"input_field": input, "pair_separators": pairSeparators, "assignment_separators": []string{"="},
		"quote_characters": "\"'", "escape_character": "\\", "key_pattern": "^[A-Za-z_][A-Za-z0-9_.-]*$",
		"key_allow_patterns": allowPatterns, "key_aliases": aliases, "allow_leading_text": allowLeadingText,
		"duplicate_policy": "last", "max_input_bytes": familyParserMaxInputBytes, "max_fields": 256,
	}
}

func canonicalReviewedFamily(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "imap":
		return "imap", true
	case "pop":
		return "pop", true
	case "mta":
		return "mta", true
	case "smtp", "smtp-in", "smtp-out":
		return "smtp", true
	case "webtop", "webmail", "webtop/webmail":
		return "webtop", true
	case "pabd", "pss", "pss/pabd":
		return "pabd", true
	case "capd", "mss", "mss/capd":
		return "capd", true
	default:
		return "", false
	}
}

func familyServiceSelectors(service string) []string {
	switch service {
	case "imap":
		return []string{"imap", "imapserv"}
	case "pop":
		return []string{"pop", "popserv"}
	case "webtop":
		return []string{"webtop", "webmail"}
	case "pabd":
		return []string{"pabd", "pss"}
	case "capd":
		return []string{"capd", "mss"}
	default:
		return []string{service}
	}
}

func normalizeReviewedKeys(input []string) ([]string, error) {
	output := append([]string(nil), input...)
	sort.Strings(output)
	for index, key := range output {
		if !familyProfileKey.MatchString(key) {
			return nil, fmt.Errorf("invalid key %q", key)
		}
		if index > 0 && output[index-1] == key {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
	}
	return output, nil
}

func mergeReviewedKeys(groups ...[]string) ([]string, error) {
	seen := map[string]string{}
	labels := []string{"contract", "reviewed extension", "additional reviewed"}
	for groupIndex, group := range groups {
		for _, key := range group {
			if prior, exists := seen[key]; exists {
				return nil, fmt.Errorf("key %q appears in both %s and %s sets", key, prior, labels[groupIndex])
			}
			seen[key] = labels[groupIndex]
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func exactKeyPatterns(keys []string) []string {
	const maxPatternPayload = 3000
	patterns := make([]string, 0, 2)
	parts := make([]string, 0, len(keys))
	length := len(`^(?:)$`)
	flush := func() {
		if len(parts) == 0 {
			return
		}
		patterns = append(patterns, `^(?:`+strings.Join(parts, "|")+`)$`)
		parts = parts[:0]
		length = len(`^(?:)$`)
	}
	for _, key := range keys {
		quoted := regexp.QuoteMeta(key)
		additional := len(quoted)
		if len(parts) > 0 {
			additional++
		}
		if len(parts) > 0 && length+additional > maxPatternPayload {
			flush()
		}
		parts = append(parts, quoted)
		length += additional
	}
	flush()
	return patterns
}

func familyCollisionAliases(family string, keys []string) map[string]string {
	collisions := map[string]bool{
		"raw": true, "nli": true, "meta": true, "source": true, "envelope": true,
		"parser_id": true, "attempt_count": true, "ordered_attempts": true, "parse_telemetry": true,
	}
	for _, field := range familyHeaderFields(family) {
		collisions[field] = true
	}
	aliases := map[string]string{}
	for _, key := range keys {
		if collisions[strings.ToLower(key)] || collisions[key] {
			aliases[key] = "input." + key
		}
	}
	return aliases
}

func familyHeaderFields(family string) []string {
	switch family {
	case "webtop":
		return []string{"date", "time"}
	case "pabd", "capd":
		return []string{"date", "time", "hostname", "service", "major_pid", "pid_status", "minor_pid", "event_type", "event"}
	default:
		return []string{"date", "time", "hostname", "service", "major_pid", "pid_status", "minor_pid", "event_type", "event", "code"}
	}
}

func reviewedKeyDigest(contract, extensions, additional []string) string {
	material := "contract\x00" + strings.Join(contract, "\x00") + "\nextensions\x00" + strings.Join(extensions, "\x00") + "\nadditional\x00" + strings.Join(additional, "\x00")
	digest := sha256.Sum256([]byte(material))
	return hex.EncodeToString(digest[:])
}
