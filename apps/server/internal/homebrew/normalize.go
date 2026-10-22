package homebrew

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var tokenPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+_.@-]*$`)
var ignoredFields = []string{"tap_git_head", "generated_date", "analytics", "installed", "installed_time", "outdated", "pinned", "pinned_version"}
var rubyObjectAddressPattern = regexp.MustCompile(`#<([A-Z][A-Za-z0-9_]*(?:::[A-Z][A-Za-z0-9_]*)*):0x[0-9a-fA-F]+>`)

func RawHash(raw json.RawMessage) (string, error) {
	if !json.Valid(raw) {
		return "", errors.New("invalid package JSON")
	}
	var data map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil {
		return "", fmt.Errorf("decode raw package: %w", err)
	}
	if data == nil {
		return "", errors.New("package must be a JSON object")
	}
	for _, field := range ignoredFields {
		delete(data, field)
	}
	normalizeRubyObjectAddresses(data)
	canonical, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("encode canonical package: %w", err)
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
}

// Homebrew can serialize Ruby requirements as inspect strings containing a
// process-local address. Keep their class, and leave every other value and key
// intact so existing hashes (including JSON number spellings) remain compatible.
func normalizeRubyObjectAddresses(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			v[key] = normalizeRubyObjectAddresses(child)
		}
	case []any:
		for i, child := range v {
			v[i] = normalizeRubyObjectAddresses(child)
		}
	case string:
		if strings.Contains(v, "#<") {
			return rubyObjectAddressPattern.ReplaceAllString(v, "#<${1}>")
		}
	}
	return value
}

func Normalize(kind string, raw json.RawMessage) (Package, error) {
	var out Package
	switch kind {
	case "cask":
		var cask Cask
		if err := json.Unmarshal(raw, &cask); err != nil {
			return out, fmt.Errorf("decode cask: %w", err)
		}
		var err error
		out, err = normalizeCask(cask, raw)
		if err != nil {
			return out, err
		}
	case "formula":
		var formula Formula
		if err := json.Unmarshal(raw, &formula); err != nil {
			return out, fmt.Errorf("decode formula: %w", err)
		}
		out = normalizeFormula(formula)
	default:
		return out, errors.New("invalid package kind")
	}
	if !tokenPattern.MatchString(out.Token) {
		return out, errors.New("invalid Homebrew token")
	}
	out.Kind, out.Raw = kind, bytes.Clone(raw)
	out.VersionBase, _, _ = strings.Cut(out.Version, ",")
	out.DeprecationReplacement = replacement(out.DeprecationReplacementCask, out.DeprecationReplacementFormula)
	out.DisableReplacement = replacement(out.DisableReplacementCask, out.DisableReplacementFormula)
	if out.FullToken == "" {
		out.FullToken = out.Token
	}
	if out.Tap == "" {
		if kind == "cask" {
			out.Tap = "homebrew/cask"
		} else {
			out.Tap = "homebrew/core"
		}
	}
	hash, err := RawHash(raw)
	if err != nil {
		return out, err
	}
	out.RawHash = hash
	return out, nil
}

func replacement(cask, formula string) string {
	if cask != "" {
		return "cask:" + cask
	}
	if formula != "" {
		return "formula:" + formula
	}
	return ""
}

func unique(values ...[]string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, group := range values {
		for _, value := range group {
			if value != "" && !seen[value] {
				seen[value] = true
				result = append(result, value)
			}
		}
	}
	return result
}

func marshal(value any) json.RawMessage {
	// Encode only decoded JSON and primitive types, with no custom Marshaler.
	encoded, _ := json.Marshal(value)
	return encoded
}

func objectOrEmpty(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || string(value) == "null" {
		return json.RawMessage(`{}`)
	}
	return value
}

func normalizeCask(cask Cask, raw json.RawMessage) (Package, error) {
	out := Package{Metadata: cask.Metadata, Token: cask.Token, FullToken: cask.FullToken, Name: cask.Token,
		Names: unique(cask.Names, cask.OldTokens, []string{cask.Token}), OldTokens: unique(cask.OldTokens), Version: cask.Version,
		AutoUpdates: cask.AutoUpdates, DownloadURL: cask.URL, IsFont: strings.HasPrefix(cask.Token, "font-"),
		ConflictsWith: objectOrEmpty(cask.ConflictsWith)}
	if len(cask.Names) > 0 {
		out.Name = cask.Names[0]
	}
	for _, name := range cask.Names {
		if strings.ContainsFunc(name, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			out.ChineseName = name
			break
		}
	}
	metadata, err := caskPlatforms(cask, raw)
	if err != nil {
		return out, err
	}
	for _, platform := range metadata.Platforms {
		out.SupportsArm64 = out.SupportsArm64 || platform.Arch == "arm64"
		out.SupportsX8664 = out.SupportsX8664 || platform.Arch == "x86_64"
	}
	if cask.SupportedPlatforms == nil {
		out.SupportsArm64, out.SupportsX8664 = declaredArchitectures(cask.DependsOn)
	}
	out.MinMacos = minimumMacos(cask.DependsOn)
	// A single field cannot represent requirements across platforms; expose differences in the full platform list.
	for _, platform := range metadata.Platforms {
		if stringValue(platform.MinMacos) != out.MinMacos {
			out.MinMacos = ""
			break
		}
	}
	out.Artifacts = marshal(normalizeArtifacts(cask.Artifacts))
	out.Dependencies = marshal(metadata)
	return out, nil
}

func normalizeFormula(formula Formula) Package {
	out := Package{Metadata: formula.Metadata, Token: formula.Name, FullToken: formula.FullName, Name: formula.Name,
		Names: unique([]string{formula.Name}, formula.Aliases, formula.OldNames), OldTokens: unique(formula.OldNames),
		Version: formula.Versions.Stable, DownloadURL: formula.URLs.Stable.URL, KegOnly: formula.KegOnly}
	if out.Version == "" && formula.Versions.Head != "" {
		out.Version = formula.Versions.Head
	}
	if json.Unmarshal(formula.License, &out.License) != nil {
		out.License = string(formula.License)
	}
	if out.License == "null" {
		out.License = ""
	}
	out.Artifacts = marshal(map[string]any{"binaries": unique(formula.Executables)})
	uses := formula.UsesFromMacos
	if len(uses) == 0 || string(uses) == "null" {
		uses = json.RawMessage(`[]`)
	}
	out.Dependencies = marshal(map[string]any{"runtime": unique(formula.Dependencies), "build": unique(formula.BuildDependencies),
		"test": unique(formula.TestDependencies), "optional": unique(formula.OptionalDependencies), "recommended": unique(formula.RecommendedDependencies), "usesFromMacos": uses})
	out.ConflictsWith = marshal(map[string]any{"formulae": unique(formula.ConflictsWith), "reasons": unique(formula.ConflictsWithReasons)})
	files := formula.Bottle.Stable.Files
	if len(files) == 0 {
		out.SupportsArm64, out.SupportsX8664 = true, true
	}
	for tag := range files {
		if strings.HasPrefix(tag, "arm64_") {
			out.SupportsArm64 = true
		} else if !strings.HasSuffix(tag, "_linux") {
			out.SupportsX8664 = true
		}
	}
	for _, requirement := range formula.Requirements {
		if requirement.Name == "macos" {
			out.MinMacos = requirement.Version
			break
		}
	}
	return out
}
