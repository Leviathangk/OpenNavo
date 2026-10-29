package homebrew

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Architecture-dependent requirements cannot be represented by the ARM base's
// shared supported_platforms list. Reject them before either pass loses a CPU.
func (p *caskParser) rejectArchitectureRequirement(name rbToken, start int) error {
	if name.text != "depends_on" {
		return nil
	}
	for _, token := range p.tokens[start:p.pos] {
		for _, part := range token.parts {
			if !part.interp {
				continue
			}
			expression, err := lexRuby(part.text)
			if err == nil && expression[0].kind == rbIdent && expression[0].text == "arch" {
				return p.fail(part.line, "arch interpolation in depends_on is not supported")
			}
		}
	}
	return nil
}

// architectureValue accepts only complete literal selectors. Validate both values even
// when only one will be used, so unsupported Ruby cannot hide in the other architecture.
func (p *caskParser) architectureValue(name rbToken, args rbArgs, checksum bool) (rbValue, error) {
	if len(args.positional) != 0 || len(args.options.keys) != 2 {
		return rbValue{}, p.fail(name.line, "%s expects arm: and intel: literal values", name.text)
	}
	for _, arch := range []string{"arm", "intel"} {
		value, ok := args.options.get(arch)
		valid := ok && value.kind == vString && value.text != ""
		if checksum {
			valid = ok && (value.kind == vString && checksumPattern.MatchString(value.text) || value.kind == vSymbol && value.text == "no_check")
		}
		if !valid {
			return rbValue{}, p.fail(name.line, "invalid %s %s value", name.text, arch)
		}
	}
	p.hasArchitectureValues = true
	value, _ := args.options.get(p.architecture)
	return value, nil
}

// Render an ARM base plus Intel overrides in the existing Homebrew API variation
// format. Casks without selectors take the original rendering path unchanged.
func (p *caskParser) renderArchitectures() (json.RawMessage, error) {
	base, err := p.render()
	if err != nil || !p.hasArchitectureValues {
		return base, err
	}
	intel := &caskParser{tokens: p.tokens, ref: p.ref, today: p.today, architecture: "intel", dependsOn: map[string]any{}, seen: map[string]bool{}}
	if err := intel.parse(); err != nil {
		return nil, err
	}
	other, err := intel.render()
	if err != nil {
		return nil, err
	}
	var out, variant map[string]json.RawMessage
	if err := json.Unmarshal(base, &out); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(other, &variant); err != nil {
		return nil, err
	}
	overrides := map[string]json.RawMessage{}
	for key, value := range variant {
		if !bytes.Equal(value, out[key]) {
			overrides[key] = value
		}
	}
	variations := map[string]map[string]json.RawMessage{}
	if len(overrides) > 0 {
		for _, tag := range intel.supportedPlatforms() {
			if !strings.HasPrefix(tag, "arm64_") {
				variations[tag] = overrides
			}
		}
	}
	out["variations"], err = json.Marshal(variations)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}
