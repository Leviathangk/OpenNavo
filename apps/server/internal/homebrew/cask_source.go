package homebrew

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// CaskSource identifies a cask file read from a third-party tap repository.
type CaskSource struct {
	Tap     string // Homebrew tap name, e.g. swiftvm-app/tap
	Token   string
	Path    string // repository path, e.g. Casks/swiftvm.rb
	GitHead string // commit the file was read at
}

// UnsupportedCaskError reports Ruby the static reader cannot evaluate. Such casks are rejected rather than guessed.
type UnsupportedCaskError struct {
	Line   int
	Reason string
}

func (e *UnsupportedCaskError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Reason)
	}
	return e.Reason
}

// ParseCaskSource statically reads the declarative subset of the cask DSL and returns the same JSON shape as
// formulae.brew.sh/api/cask.json, so the result goes through Normalize like an official cask. Ruby is never
// executed: architecture selectors are evaluated for both CPUs; per-OS/architecture blocks, local variables,
// language blocks and unknown stanzas are rejected.
// Flight blocks, livecheck and caveats blocks only run inside brew on the user's Mac, so they are skipped.
func ParseCaskSource(source string, ref CaskSource, today time.Time) (json.RawMessage, error) {
	tokens, err := lexRuby(source)
	if err != nil {
		return nil, err
	}
	parser := &caskParser{tokens: tokens, ref: ref, today: today.UTC(), architecture: "arm", dependsOn: map[string]any{}, seen: map[string]bool{}}
	if err := parser.parse(); err != nil {
		return nil, err
	}
	return parser.renderArchitectures()
}

type rbValueKind uint8

const (
	vString rbValueKind = iota
	vSymbol
	vNumber
	vBool
	vNil
	vArray
	vHash
)

type rbValue struct {
	kind  rbValueKind
	text  string
	flag  bool
	items []rbValue
	keys  []string
	vals  []rbValue
	line  int
}

func (v rbValue) json() any {
	switch v.kind {
	case vString, vSymbol:
		return v.text
	case vNumber:
		return json.Number(v.text)
	case vBool:
		return v.flag
	case vArray:
		out := make([]any, len(v.items))
		for index, item := range v.items {
			out[index] = item.json()
		}
		return out
	case vHash:
		out := make(map[string]any, len(v.keys))
		for index, key := range v.keys {
			out[key] = v.vals[index].json()
		}
		return out
	}
	return nil
}

func (v rbValue) get(key string) (rbValue, bool) {
	for index, name := range v.keys {
		if name == key {
			return v.vals[index], true
		}
	}
	return rbValue{}, false
}

type caskParser struct {
	tokens                []rbToken
	pos                   int
	depth                 int
	ref                   CaskSource
	today                 time.Time
	architecture, arch    string
	hasArchitectureValues bool

	token, version, sha256, url, desc, homepage string
	hasVersion, hasSHA256, hasURL, rosetta      bool
	autoUpdates                                 *bool
	names, caveats                              []string
	urlSpecs                                    map[string]any
	artifacts                                   []map[string]any
	installable                                 bool
	dependsOn                                   map[string]any
	dependsKeys                                 []string
	macos, maximumMacos                         *macosRequirement
	arches                                      []string
	conflicts                                   map[string]any
	container                                   map[string]any
	deprecation, disable                        *lifecycleMark
	seen                                        map[string]bool
}

type macosRequirement struct {
	comparator string
	versions   []string
}

type lifecycleMark struct {
	date, reason, formula, cask string
	active                      bool
}

func (p *caskParser) peek() rbToken { return p.tokens[p.pos] }

func (p *caskParser) next() rbToken {
	token := p.tokens[p.pos]
	if token.kind != rbEOF {
		p.pos++
	}
	return token
}

func (p *caskParser) fail(line int, format string, args ...any) error {
	return &UnsupportedCaskError{Line: line, Reason: fmt.Sprintf(format, args...)}
}

func (p *caskParser) skipNewlines() {
	for p.peek().kind == rbNewline {
		p.pos++
	}
}

func isPunct(token rbToken, text string) bool { return token.kind == rbPunct && token.text == text }

func isKeyword(token rbToken, text string) bool { return token.kind == rbKeyword && token.text == text }

func (p *caskParser) parse() error {
	p.skipNewlines()
	head := p.next()
	if head.kind != rbIdent || head.text != "cask" {
		return p.fail(head.line, "expected a cask definition")
	}
	args, block, err := p.parseCall(head)
	if err != nil {
		return err
	}
	if len(args.positional) != 1 || args.positional[0].kind != vString || len(args.options.keys) > 0 || block != "do" {
		return p.fail(head.line, `expected cask "token" do`)
	}
	p.token = args.positional[0].text
	if p.token != p.ref.Token {
		return p.fail(head.line, "cask token %q does not match the file name %q", p.token, p.ref.Token)
	}
	for {
		p.skipNewlines()
		token := p.peek()
		if isKeyword(token, "end") {
			p.next()
			break
		}
		if token.kind == rbEOF {
			return p.fail(token.line, "missing end of cask block")
		}
		if err := p.statement(); err != nil {
			return err
		}
	}
	p.skipNewlines()
	if token := p.peek(); token.kind != rbEOF {
		return p.fail(token.line, "unexpected code after the cask block")
	}
	switch {
	case !p.hasVersion:
		return p.fail(0, "the cask has no version stanza")
	case !p.hasURL:
		return p.fail(0, "the cask has no url stanza")
	case len(p.names) == 0:
		return p.fail(0, "the cask has no name stanza")
	}
	return nil
}

type rbArgs struct {
	positional []rbValue
	options    rbValue
}

// parseCall reads the arguments and an optional block opener after a method name.
// The returned block is "do", "{" or "" and is left unconsumed apart from the opener and block parameters.
func (p *caskParser) parseCall(name rbToken) (rbArgs, string, error) {
	args := rbArgs{options: rbValue{kind: vHash, line: name.line}}
	token := p.peek()
	parenthesized := isPunct(token, "(") && !token.spaced
	if parenthesized {
		p.next()
	}
	for {
		if parenthesized {
			p.skipNewlines()
			if isPunct(p.peek(), ")") {
				p.next()
				break
			}
		} else {
			token = p.peek()
			if token.kind == rbNewline || token.kind == rbEOF || isKeyword(token, "do") || isKeyword(token, "end") ||
				isPunct(token, "{") && len(args.positional) == 0 && len(args.options.keys) == 0 {
				break
			}
		}
		token = p.peek()
		if token.kind == rbLabel {
			p.next()
			p.skipNewlines()
			value, err := p.value()
			if err != nil {
				return args, "", err
			}
			args.options.keys, args.options.vals = append(args.options.keys, token.text), append(args.options.vals, value)
		} else {
			value, err := p.value()
			if err != nil {
				return args, "", err
			}
			if isPunct(p.peek(), "=>") {
				p.next()
				p.skipNewlines()
				if value.kind != vString && value.kind != vSymbol {
					return args, "", p.fail(value.line, "unsupported hash key")
				}
				option, err := p.value()
				if err != nil {
					return args, "", err
				}
				args.options.keys, args.options.vals = append(args.options.keys, value.text), append(args.options.vals, option)
			} else {
				if len(args.options.keys) > 0 {
					return args, "", p.fail(value.line, "positional argument after keyword arguments")
				}
				args.positional = append(args.positional, value)
			}
		}
		if isPunct(p.peek(), ",") {
			p.next()
			p.skipNewlines()
			continue
		}
		if parenthesized {
			p.skipNewlines()
			if !isPunct(p.peek(), ")") {
				return args, "", p.fail(p.peek().line, "expected , or )")
			}
			continue
		}
		break
	}
	token = p.peek()
	if isKeyword(token, "do") || isPunct(token, "{") {
		p.next()
		if isPunct(p.peek(), "|") {
			p.next()
			for !isPunct(p.peek(), "|") {
				if p.peek().kind == rbEOF || p.peek().kind == rbNewline {
					return args, "", p.fail(token.line, "invalid block parameters")
				}
				p.next()
			}
			p.next()
		}
		return args, token.text, nil
	}
	if token.kind != rbNewline && token.kind != rbEOF && !isKeyword(token, "end") {
		return args, "", p.fail(token.line, "unsupported expression after %s", name.text)
	}
	return args, "", nil
}

// maxValueDepth bounds nested arrays and hashes; real casks nest two or three levels.
const maxValueDepth = 32

// value evaluates a literal: strings (with supported interpolation), symbols, numbers, booleans, nil, arrays and hashes.
func (p *caskParser) value() (rbValue, error) {
	token := p.next()
	if p.depth >= maxValueDepth {
		return rbValue{}, p.fail(token.line, "values are nested too deeply")
	}
	p.depth++
	defer func() { p.depth-- }()
	switch token.kind {
	case rbString:
		text, err := p.interpolate(token)
		if err != nil {
			return rbValue{}, err
		}
		// Adjacent string literals are concatenated.
		for p.peek().kind == rbString {
			more, err := p.interpolate(p.next())
			if err != nil {
				return rbValue{}, err
			}
			text += more
		}
		return rbValue{kind: vString, text: text, line: token.line}, nil
	case rbSymbol:
		return rbValue{kind: vSymbol, text: token.text, line: token.line}, nil
	case rbNumber:
		if !numberLiteral.MatchString(token.text) {
			return rbValue{}, p.fail(token.line, "unsupported number %s", token.text)
		}
		return rbValue{kind: vNumber, text: token.text, line: token.line}, nil
	case rbWords:
		out := rbValue{kind: vArray, line: token.line, items: []rbValue{}}
		for _, word := range token.words {
			kind := vString
			if token.text == "symbols" {
				kind = vSymbol
			}
			out.items = append(out.items, rbValue{kind: kind, text: word, line: token.line})
		}
		return out, nil
	case rbKeyword:
		switch token.text {
		case "true", "false":
			return rbValue{kind: vBool, flag: token.text == "true", line: token.line}, nil
		case "nil":
			return rbValue{kind: vNil, line: token.line}, nil
		}
	case rbPunct:
		switch token.text {
		case "[":
			out := rbValue{kind: vArray, line: token.line, items: []rbValue{}}
			for {
				p.skipNewlines()
				if isPunct(p.peek(), "]") {
					p.next()
					return out, nil
				}
				item, err := p.value()
				if err != nil {
					return rbValue{}, err
				}
				out.items = append(out.items, item)
				p.skipNewlines()
				if isPunct(p.peek(), ",") {
					p.next()
				} else if !isPunct(p.peek(), "]") {
					return rbValue{}, p.fail(p.peek().line, "expected , or ] in array")
				}
			}
		case "{":
			out := rbValue{kind: vHash, line: token.line}
			for {
				p.skipNewlines()
				if isPunct(p.peek(), "}") {
					p.next()
					return out, nil
				}
				var key string
				if label := p.peek(); label.kind == rbLabel {
					p.next()
					key = label.text
				} else {
					name, err := p.value()
					if err != nil {
						return rbValue{}, err
					}
					if (name.kind != vString && name.kind != vSymbol) || !isPunct(p.peek(), "=>") {
						return rbValue{}, p.fail(name.line, "unsupported hash key")
					}
					p.next()
					key = name.text
				}
				p.skipNewlines()
				item, err := p.value()
				if err != nil {
					return rbValue{}, err
				}
				out.keys, out.vals = append(out.keys, key), append(out.vals, item)
				p.skipNewlines()
				if isPunct(p.peek(), ",") {
					p.next()
				} else if !isPunct(p.peek(), "}") {
					return rbValue{}, p.fail(p.peek().line, "expected , or } in hash")
				}
			}
		}
	}
	if token.kind == rbIdent || token.kind == rbConst {
		return rbValue{}, p.fail(token.line, "expressions such as %s are not supported; only literal values are", token.text)
	}
	return rbValue{}, p.fail(token.line, "unsupported value %q", token.text)
}

func (p *caskParser) interpolate(token rbToken) (string, error) {
	var out strings.Builder
	for _, part := range token.parts {
		if !part.interp {
			out.WriteString(part.text)
			continue
		}
		value, err := p.evaluate(part)
		if err != nil {
			return "", err
		}
		out.WriteString(value)
	}
	return out.String(), nil
}

var numberLiteral = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

var majorMinorPatch = regexp.MustCompile(`^([^.,:]+)(?:.([^.,:]+)(?:.([^.,:]+))?)?`)

var dividerNames = map[string]string{"dots": ".", "hyphens": "-", "underscores": "_"}

// evaluate supports the interpolations casks use for paths and download URLs: version (with the
// Cask::DSL::Version helpers), token, appdir, staged_path, caskroom_path and HOMEBREW_PREFIX.
func (p *caskParser) evaluate(part rbPart) (string, error) {
	tokens, err := lexRuby(part.text)
	if err != nil {
		return "", p.fail(part.line, "unsupported interpolation #{%s}", part.text)
	}
	unsupported := func() error {
		return p.fail(part.line, "unsupported interpolation #{%s}", part.text)
	}
	index := 0
	root := tokens[index]
	index++
	var scalar string
	var list []string
	isList := false
	switch {
	case root.kind == rbIdent && root.text == "arch":
		if p.arch == "" {
			return "", p.fail(part.line, "arch is used before the arch stanza")
		}
		scalar = p.arch
	case root.kind == rbIdent && root.text == "version":
		if !p.hasVersion {
			return "", p.fail(part.line, "version is used before the version stanza")
		}
		scalar = p.version
	case root.kind == rbIdent && root.text == "token":
		scalar = p.token
	case root.kind == rbIdent && root.text == "appdir":
		scalar = "$APPDIR"
	case root.kind == rbConst && root.text == "HOMEBREW_PREFIX":
		scalar = "$HOMEBREW_PREFIX"
	case root.kind == rbIdent && root.text == "caskroom_path":
		scalar = "$HOMEBREW_PREFIX/Caskroom/" + p.token
	case root.kind == rbIdent && root.text == "staged_path":
		if !p.hasVersion {
			return "", p.fail(part.line, "staged_path is used before the version stanza")
		}
		scalar = "$HOMEBREW_PREFIX/Caskroom/" + p.token + "/" + p.version
	default:
		return "", unsupported()
	}
	versionValue := root.text == "version"
	for {
		token := tokens[index]
		index++
		if token.kind == rbEOF {
			break
		}
		if token.kind == rbNewline {
			// A statement separator cannot truncate the interpolation to its first expression.
			for index < len(tokens) && tokens[index].kind == rbNewline {
				index++
			}
			if index >= len(tokens) || tokens[index].kind != rbEOF {
				return "", unsupported()
			}
			break
		}
		if isPunct(token, "[") && isList {
			number, closer := tokens[index], tokens[index+1]
			if number.kind != rbNumber || !isPunct(closer, "]") || strings.Contains(number.text, ".") {
				return "", unsupported()
			}
			index += 2
			var position int
			_, _ = fmt.Sscan(number.text, &position)
			scalar, isList = "", false
			if position < len(list) {
				scalar = list[position]
			}
			continue
		}
		if !isPunct(token, ".") || !versionValue {
			return "", unsupported()
		}
		method := tokens[index]
		index++
		if method.kind != rbIdent && method.kind != rbKeyword {
			return "", unsupported()
		}
		if isList {
			ordinals := map[string]int{"first": 0, "second": 1, "third": 2, "fourth": 3, "fifth": 4}
			position, ok := ordinals[method.text]
			if method.text == "last" {
				position, ok = len(list)-1, true
			}
			if !ok {
				return "", unsupported()
			}
			scalar, isList = "", false
			if position >= 0 && position < len(list) {
				scalar = list[position]
			}
			continue
		}
		if method.text == "chomp" && isPunct(tokens[index], "(") {
			argument, closer := tokens[index+1], tokens[index+2]
			text, ok := literalText(argument.parts)
			if argument.kind != rbString || !ok || !isPunct(closer, ")") {
				return "", unsupported()
			}
			index += 3
			if scalar != "" && scalar != "latest" {
				scalar = strings.TrimSuffix(scalar, text)
			}
			continue
		}
		if method.text == "csv" {
			list, isList = rubySplit(scalar, ","), true
			continue
		}
		result, ok := versionMethod(scalar, method.text)
		if !ok {
			return "", unsupported()
		}
		scalar = result
	}
	if isList {
		return "", unsupported()
	}
	return scalar, nil
}

// versionMethod mirrors Cask::DSL::Version; helpers return the version itself when it is empty or :latest.
func versionMethod(version, method string) (string, bool) {
	if method == "to_s" {
		return version, true
	}
	group := func(index int) string {
		match := majorMinorPatch.FindStringSubmatchIndex(version)
		if match == nil || match[2*index] < 0 {
			return ""
		}
		return version[match[2*index]:match[2*index+1]]
	}
	join := func(values ...string) string {
		return strings.Join(slices.DeleteFunc(values, func(value string) bool { return value == "" }), ".")
	}
	var result string
	switch method {
	case "major":
		result = group(1)
	case "minor":
		result = group(2)
	case "patch":
		result = group(3)
	case "major_minor":
		result = join(group(1), group(2))
	case "major_minor_patch":
		result = join(group(1), group(2), group(3))
	case "minor_patch":
		result = join(group(2), group(3))
	case "before_comma":
		result, _, _ = strings.Cut(version, ",")
	case "after_comma":
		_, result, _ = strings.Cut(version, ",")
	case "no_dividers":
		result = strings.NewReplacer(".", "", "-", "", "_", "").Replace(version)
	case "chomp":
		result = strings.TrimSuffix(strings.TrimSuffix(version, "\n"), "\r")
	default:
		if from, ok := strings.CutPrefix(method, "no_"); ok && dividerNames[from] != "" {
			result = strings.ReplaceAll(version, dividerNames[from], "")
			break
		}
		from, to, ok := strings.Cut(method, "_to_")
		if !ok || dividerNames[from] == "" || dividerNames[to] == "" || from == to {
			return "", false
		}
		result = strings.ReplaceAll(version, dividerNames[from], dividerNames[to])
	}
	if version == "" || version == "latest" {
		return version, true
	}
	return result, true
}

// rubySplit matches String#split with a string separator, which drops trailing empty fields.
func rubySplit(value, separator string) []string {
	fields := strings.Split(value, separator)
	for len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	return fields
}

// Skipped blocks only run inside brew on the user's Mac and do not affect catalog metadata.
var skippedBlocks = map[string]bool{
	"livecheck": true, "preflight": true, "postflight": true, "uninstall_preflight": true, "uninstall_postflight": true,
	"preflight_steps": true, "postflight_steps": true, "uninstall_preflight_steps": true, "uninstall_postflight_steps": true,
	"caveats": true,
}

var flightBlocks = map[string]bool{"preflight": true, "postflight": true, "uninstall_preflight": true, "uninstall_postflight": true}

var uniqueStanzas = map[string]bool{"version": true, "sha256": true, "url": true, "desc": true, "homepage": true, "auto_updates": true, "container": true}

// Default install locations as they appear in Homebrew's generated API JSON.
var relocatedDirs = map[string]string{
	"app": "/Applications", "suite": "/Applications", "binary": "$HOMEBREW_PREFIX/bin",
	"keyboard_layout": "/Library/Keyboard Layouts", "colorpicker": "/$HOME/Library/ColorPickers",
	"prefpane": "/$HOME/Library/PreferencePanes", "qlplugin": "/$HOME/Library/QuickLook",
	"mdimporter": "/$HOME/Library/Spotlight", "dictionary": "/$HOME/Library/Dictionaries",
	"font": "/$HOME/Library/Fonts", "service": "/$HOME/Library/Services",
	"input_method": "/$HOME/Library/Input Methods", "internet_plugin": "/$HOME/Library/Internet Plug-Ins",
	"audio_unit_plugin": "/$HOME/Library/Audio/Plug-Ins/Components", "vst_plugin": "/$HOME/Library/Audio/Plug-Ins/VST",
	"vst3_plugin": "/$HOME/Library/Audio/Plug-Ins/VST3", "screen_saver": "/$HOME/Library/Screen Savers",
	"artifact": "",
}

var completionDirs = map[string]string{
	"bash_completion": "$HOMEBREW_PREFIX/etc/bash_completion.d",
	"zsh_completion":  "$HOMEBREW_PREFIX/share/zsh/site-functions",
	"fish_completion": "$HOMEBREW_PREFIX/share/fish/vendor_completions.d",
}

var manpageSection = regexp.MustCompile(`\.([1-8]|n|l)(?:\.gz)?$`)

var versionCharacters = regexp.MustCompile(`^[0-9a-zA-Z.,:\-_+ ]+$`)

func (p *caskParser) statement() error {
	name := p.next()
	if name.kind == rbKeyword && (name.text == "if" || name.text == "unless" || name.text == "case") {
		return p.fail(name.line, "conditionals are not supported")
	}
	if name.kind != rbIdent {
		return p.fail(name.line, "unsupported statement %q", name.text)
	}
	if next := p.peek(); next.kind == rbPunct && (next.text == "=" || next.text == "||=" || next.text == "+=") {
		return p.fail(name.line, "local variables are not supported")
	}
	if blocked := unsupportedStanza(name.text); blocked != "" {
		return p.fail(name.line, "%s", blocked)
	}
	if uniqueStanzas[name.text] {
		if p.seen[name.text] {
			return p.fail(name.line, "%s is declared more than once", name.text)
		}
		p.seen[name.text] = true
	}
	start := p.pos
	args, block, err := p.parseCall(name)
	if err != nil {
		return err
	}
	if err := p.rejectArchitectureRequirement(name, start); err != nil {
		return err
	}
	if block != "" {
		if !skippedBlocks[name.text] || len(args.positional) > 0 || len(args.options.keys) > 0 {
			return p.fail(name.line, "%s blocks are not supported", name.text)
		}
		identifiers, err := p.skipBlock(block)
		if err != nil {
			return err
		}
		switch {
		case flightBlocks[name.text] || strings.HasSuffix(name.text, "_steps"):
			p.addArtifact(map[string]any{name.text: nil}, !strings.HasPrefix(name.text, "uninstall"))
		case name.text == "caveats":
			p.rosetta = p.rosetta || slices.Contains(identifiers, "requires_rosetta")
		}
		return nil
	}
	if skippedBlocks[name.text] && name.text != "caveats" {
		return p.fail(name.line, "%s requires a block", name.text)
	}
	return p.apply(name, args)
}

func unsupportedStanza(name string) string {
	switch {
	case name == "os" || name == "on_arch_conditional" || name == "on_system_conditional":
		return "`" + name + "` (per-architecture or per-OS values) is not supported"
	case strings.HasPrefix(name, "on_"):
		return "`" + name + "` blocks (per-architecture or per-OS values) are not supported"
	case name == "language":
		return "`language` blocks are not supported"
	}
	return ""
}

// skipBlock consumes tokens up to the end matching an already consumed opener and
// returns the identifiers that start statements directly inside the block.
func (p *caskParser) skipBlock(opener string) ([]string, error) {
	closers := []string{"end"}
	if opener == "{" {
		closers = []string{"}"}
	}
	var identifiers []string
	loopLine := -1
	for len(closers) > 0 {
		// The opener was consumed before the call, so a previous token always exists.
		previous := p.tokens[p.pos-1]
		token := p.next()
		startsStatement := previous.kind == rbNewline ||
			previous.kind == rbPunct && slices.Contains([]string{"(", "[", "{", ",", "=", "|", "&&", "||", "=>", "+=", "-=", "||=", "&&=", "!", "?", ":"}, previous.text) ||
			previous.kind == rbKeyword && slices.Contains([]string{"do", "then", "else", "elsif", "begin", "and", "or", "not", "when", "in", "ensure", "rescue"}, previous.text)
		switch {
		case token.kind == rbEOF:
			return nil, p.fail(token.line, "unterminated block")
		case token.kind == rbIdent && startsStatement && len(closers) == 1:
			identifiers = append(identifiers, token.text)
		case token.kind == rbKeyword:
			switch token.text {
			case "do":
				if loopLine == token.line {
					loopLine = -1
				} else {
					closers = append(closers, "end")
				}
			case "begin", "case", "def", "class", "module":
				closers = append(closers, "end")
			case "if", "unless", "while", "until", "for":
				if startsStatement {
					closers = append(closers, "end")
					if token.text != "if" && token.text != "unless" {
						loopLine = token.line
					}
				}
			case "end":
				if closers[len(closers)-1] != "end" {
					return nil, p.fail(token.line, "mismatched end")
				}
				closers = closers[:len(closers)-1]
			}
		case token.kind == rbPunct:
			switch token.text {
			case "(":
				closers = append(closers, ")")
			case "[":
				closers = append(closers, "]")
			case "{":
				closers = append(closers, "}")
			case ")", "]", "}":
				if closers[len(closers)-1] != token.text {
					return nil, p.fail(token.line, "mismatched %s", token.text)
				}
				closers = closers[:len(closers)-1]
			}
		}
	}
	if token := p.peek(); token.kind != rbNewline && token.kind != rbEOF && !isKeyword(token, "end") {
		return nil, p.fail(token.line, "unsupported expression after a block")
	}
	return identifiers, nil
}

func (p *caskParser) stringArg(name rbToken, args rbArgs) (string, error) {
	if len(args.positional) != 1 || args.positional[0].kind != vString || len(args.options.keys) > 0 {
		return "", p.fail(name.line, "%s expects one string", name.text)
	}
	return args.positional[0].text, nil
}

func (p *caskParser) apply(name rbToken, args rbArgs) error {
	switch name.text {
	case "arch":
		if p.arch != "" {
			return p.fail(name.line, "arch is declared more than once")
		}
		value, err := p.architectureValue(name, args, false)
		if err != nil {
			return err
		}
		p.arch = value.text
	case "version":
		if len(args.positional) == 1 && args.positional[0].kind == vSymbol && args.positional[0].text == "latest" && len(args.options.keys) == 0 {
			p.version, p.hasVersion = "latest", true
			return nil
		}
		version, err := p.stringArg(name, args)
		if err != nil {
			return err
		}
		if !versionCharacters.MatchString(version) {
			return p.fail(name.line, "version %q contains invalid characters", version)
		}
		p.version, p.hasVersion = version, true
	case "sha256":
		if len(args.options.keys) > 0 {
			value, err := p.architectureValue(name, args, true)
			if err != nil {
				return err
			}
			args = rbArgs{positional: []rbValue{value}}
		}
		if len(args.positional) == 1 && args.positional[0].kind == vSymbol && args.positional[0].text == "no_check" {
			p.sha256, p.hasSHA256 = "no_check", true
			return nil
		}
		sha256, err := p.stringArg(name, args)
		if err != nil {
			return err
		}
		if !checksumPattern.MatchString(sha256) {
			return p.fail(name.line, "invalid sha256")
		}
		p.sha256, p.hasSHA256 = strings.ToLower(sha256), true
	case "url":
		if len(args.positional) != 1 || args.positional[0].kind != vString {
			return p.fail(name.line, "url expects one string")
		}
		p.url, p.hasURL = args.positional[0].text, true
		p.urlSpecs = map[string]any{}
		for index, key := range args.options.keys {
			p.urlSpecs[key] = args.options.vals[index].json()
		}
	case "name":
		if len(args.positional) == 0 || len(args.options.keys) > 0 {
			return p.fail(name.line, "name expects strings")
		}
		for _, value := range args.positional {
			if value.kind != vString {
				return p.fail(name.line, "name expects strings")
			}
			p.names = append(p.names, value.text)
		}
	case "desc":
		value, err := p.stringArg(name, args)
		if err != nil {
			return err
		}
		p.desc = value
	case "homepage":
		if len(args.positional) != 1 || args.positional[0].kind != vString {
			return p.fail(name.line, "homepage expects one string")
		}
		p.homepage = args.positional[0].text
	case "auto_updates":
		if len(args.positional) != 1 || args.positional[0].kind != vBool || len(args.options.keys) > 0 {
			return p.fail(name.line, "auto_updates expects true or false")
		}
		value := args.positional[0].flag
		p.autoUpdates = &value
	case "caveats":
		if len(args.positional) == 0 || len(args.options.keys) > 0 {
			return p.fail(name.line, "caveats expects strings")
		}
		for _, value := range args.positional {
			if value.kind != vString {
				return p.fail(name.line, "caveats expects strings")
			}
			p.caveats = append(p.caveats, value.text)
		}
	case "depends_on":
		return p.dependsOnStanza(name, args)
	case "conflicts_with":
		if len(args.positional) > 0 || len(args.options.keys) == 0 {
			return p.fail(name.line, "conflicts_with expects cask: or formula:")
		}
		if p.conflicts == nil {
			p.conflicts = map[string]any{}
		}
		for index, key := range args.options.keys {
			names, err := stringList(args.options.vals[index])
			if key != "cask" && key != "formula" || err != nil {
				return p.fail(name.line, "unsupported conflicts_with %s", key)
			}
			existing, _ := p.conflicts[key].([]string)
			p.conflicts[key] = append(existing, names...)
		}
	case "container":
		if len(args.positional) > 0 || len(args.options.keys) == 0 {
			return p.fail(name.line, "container expects nested: or type:")
		}
		p.container = map[string]any{}
		for index, key := range args.options.keys {
			value := args.options.vals[index]
			if key != "nested" && key != "type" || value.kind != vString && value.kind != vSymbol {
				return p.fail(name.line, "unsupported container option %s", key)
			}
			p.container[key] = value.text
		}
	case "deprecate!", "disable!":
		return p.lifecycle(name, args)
	case "no_autobump!":
		return nil
	case "pkg":
		if len(args.positional) != 1 || args.positional[0].kind != vString {
			return p.fail(name.line, "pkg expects one file name")
		}
		entry := []any{args.positional[0].text}
		if len(args.options.keys) > 0 {
			entry = append(entry, args.options.json())
		}
		p.addArtifact(map[string]any{"pkg": entry}, true)
	case "installer":
		if len(args.positional) > 0 || len(args.options.keys) != 1 || (args.options.keys[0] != "manual" && args.options.keys[0] != "script") {
			return p.fail(name.line, "installer expects manual: or script:")
		}
		options := args.options.json().(map[string]any)
		if script, ok := options["script"].(string); ok {
			options["script"] = map[string]any{"executable": script}
		}
		p.addArtifact(map[string]any{"installer": []any{options}}, true)
	case "uninstall", "zap":
		if len(args.positional) > 0 || len(args.options.keys) == 0 {
			return p.fail(name.line, "%s expects keyword arguments", name.text)
		}
		p.addArtifact(map[string]any{name.text: []any{args.options.json()}}, false)
	case "stage_only":
		if len(args.positional) != 1 || args.positional[0].kind != vBool || !args.positional[0].flag {
			return p.fail(name.line, "stage_only expects true")
		}
		p.addArtifact(map[string]any{"stage_only": []any{true}}, true)
	case "manpage":
		source, err := p.stringArg(name, args)
		if err != nil {
			return err
		}
		section := manpageSection.FindStringSubmatch(source)
		if section == nil {
			return p.fail(name.line, "%q is not a valid man page name", source)
		}
		p.addArtifact(map[string]any{"manpage": []any{source},
			"target": "$HOMEBREW_PREFIX/share/man/man" + section[1] + "/" + path.Base(source)}, true)
	default:
		if dir, ok := completionDirs[name.text]; ok {
			return p.relocated(name, args, func(target string) string { return dir + "/" + completionName(name.text, target) })
		}
		if dir, ok := relocatedDirs[name.text]; ok {
			return p.relocated(name, args, func(target string) string {
				switch {
				case target == "~" || strings.HasPrefix(target, "~/"):
					return "/$HOME" + target[1:]
				case name.text == "artifact" || strings.HasPrefix(target, "/"):
					return target
				}
				return dir + "/" + target
			})
		}
		return p.fail(name.line, "`%s` is not supported", name.text)
	}
	return nil
}

func (p *caskParser) addArtifact(entry map[string]any, installs bool) {
	p.artifacts = append(p.artifacts, entry)
	p.installable = p.installable || installs
}

func (p *caskParser) relocated(name rbToken, args rbArgs, resolve func(string) string) error {
	if len(args.positional) != 1 || args.positional[0].kind != vString {
		return p.fail(name.line, "%s expects one source path", name.text)
	}
	source := args.positional[0].text
	entry := []any{source}
	target := path.Base(source)
	if len(args.options.keys) > 0 {
		value, ok := args.options.get("target")
		if len(args.options.keys) != 1 || !ok || value.kind != vString {
			return p.fail(name.line, "%s only supports target:", name.text)
		}
		target = value.text
		entry = append(entry, map[string]any{"target": target})
	} else if name.text == "artifact" {
		return p.fail(name.line, "artifact requires target:")
	}
	p.addArtifact(map[string]any{name.text: entry, "target": resolve(target)}, true)
	return nil
}

func completionName(kind, target string) string {
	base := path.Base(target)
	extension := rubyExtname(base)
	switch kind {
	case "zsh_completion":
		if strings.HasPrefix(target, "_") {
			return target
		}
		return "_" + strings.TrimSuffix(base, extension)
	case "fish_completion":
		if strings.HasSuffix(target, ".fish") {
			return target
		}
		return strings.TrimSuffix(base, extension) + ".fish"
	}
	return strings.TrimSuffix(base, extension)
}

// rubyExtname mirrors File.extname, which ignores leading dots and a trailing dot.
func rubyExtname(base string) string {
	trimmed := strings.TrimLeft(base, ".")
	index := strings.LastIndexByte(trimmed, '.')
	if index <= 0 || index == len(trimmed)-1 {
		return ""
	}
	return trimmed[index:]
}

func stringList(value rbValue) ([]string, error) {
	switch value.kind {
	case vString, vSymbol:
		return []string{value.text}, nil
	case vArray:
		out := make([]string, 0, len(value.items))
		for _, item := range value.items {
			if item.kind != vString && item.kind != vSymbol {
				return nil, fmt.Errorf("expected strings")
			}
			out = append(out, item.text)
		}
		return out, nil
	}
	return nil, fmt.Errorf("expected strings")
}

func (p *caskParser) dependsOnStanza(name rbToken, args rbArgs) error {
	if len(args.positional) == 1 && args.positional[0].kind == vSymbol && args.positional[0].text == "macos" && len(args.options.keys) == 0 {
		// `depends_on :macos` marks a macOS-only cask; it never replaces a versioned requirement.
		args.options = rbValue{kind: vHash, keys: []string{"macos"}, vals: []rbValue{{kind: vSymbol, text: "any"}}}
		if p.macos != nil && len(p.macos.versions) > 0 {
			return nil
		}
	} else if len(args.positional) > 0 || len(args.options.keys) == 0 {
		return p.fail(name.line, "depends_on expects :macos or keyword arguments such as macos: or arch:")
	}
	for index, key := range args.options.keys {
		value := args.options.vals[index]
		if !slices.Contains(p.dependsKeys, key) {
			p.dependsKeys = append(p.dependsKeys, key)
		}
		switch key {
		case "macos", "maximum_macos":
			requirement, err := parseMacosRequirement(value, key == "maximum_macos")
			if err != nil {
				return p.fail(name.line, "unsupported depends_on %s: %v", key, err)
			}
			if key == "macos" {
				p.macos = requirement
			} else {
				p.maximumMacos = requirement
			}
			p.dependsOn[key] = requirement.json()
		case "arch":
			arches, err := stringList(value)
			if err != nil {
				return p.fail(name.line, "unsupported depends_on arch")
			}
			list, _ := p.dependsOn["arch"].([]any)
			for _, arch := range arches {
				switch strings.ReplaceAll(strings.ToLower(arch), "-", "_") {
				case "arm64":
					p.arches = append(p.arches, "arm64")
					list = append(list, map[string]any{"type": "arm", "bits": 64})
				case "intel", "x86_64":
					p.arches = append(p.arches, "x86_64")
					list = append(list, map[string]any{"type": "intel", "bits": 64})
				default:
					return p.fail(name.line, "invalid depends_on arch %q", arch)
				}
			}
			p.dependsOn["arch"] = list
		case "cask", "formula":
			names, err := stringList(value)
			if err != nil {
				return p.fail(name.line, "unsupported depends_on %s", key)
			}
			existing, _ := p.dependsOn[key].([]string)
			p.dependsOn[key] = append(existing, names...)
		default:
			return p.fail(name.line, "unsupported depends_on %s", key)
		}
	}
	return nil
}

var macosComparison = regexp.MustCompile(`^\s*(<|>|[=<>]=)\s*:(\S+)\s*$`)

// parseMacosRequirement follows MacOSRequirement.parse: a bare symbol means ">=" (or "<=" for maximum_macos),
// several symbols mean "==", and the legacy string form carries its own comparator.
func parseMacosRequirement(value rbValue, maximum bool) (*macosRequirement, error) {
	comparator := ">="
	if maximum {
		comparator = "<="
	}
	release := func(symbol string) (string, error) {
		version, ok := macosReleases[symbol]
		if !ok {
			return "", fmt.Errorf("unknown macOS release %q", symbol)
		}
		return version, nil
	}
	switch value.kind {
	case vSymbol:
		if value.text == "any" && !maximum {
			return &macosRequirement{}, nil
		}
		version, err := release(value.text)
		if err != nil {
			return nil, err
		}
		return &macosRequirement{comparator: comparator, versions: []string{version}}, nil
	case vString:
		match := macosComparison.FindStringSubmatch(value.text)
		if match == nil {
			return nil, fmt.Errorf("unsupported macOS comparison %q", value.text)
		}
		version, err := release(match[2])
		if err != nil {
			return nil, err
		}
		if maximum && match[1] != "<=" {
			return nil, fmt.Errorf("maximum_macos must use <=")
		}
		return &macosRequirement{comparator: match[1], versions: []string{version}}, nil
	case vArray:
		if maximum {
			return nil, fmt.Errorf("maximum_macos accepts one release")
		}
		symbols, err := stringList(value)
		if err != nil || len(symbols) == 0 {
			return nil, fmt.Errorf("expected macOS release symbols")
		}
		requirement := &macosRequirement{comparator: "=="}
		for _, symbol := range symbols {
			version, err := release(symbol)
			if err != nil {
				return nil, err
			}
			requirement.versions = append(requirement.versions, version)
		}
		if len(symbols) == 1 {
			requirement.comparator = comparator
		}
		return requirement, nil
	}
	return nil, fmt.Errorf("unsupported value")
}

func (r *macosRequirement) json() map[string]any {
	if r == nil || len(r.versions) == 0 {
		return map[string]any{}
	}
	return map[string]any{r.comparator: r.versions}
}

// allows mirrors MacOSRequirement#allows?: only >= and <= compare; everything else is equality.
func (r *macosRequirement) allows(version string) bool {
	if r == nil || len(r.versions) == 0 {
		return true
	}
	comparison, _ := CompareVersion(version, r.versions[0])
	switch r.comparator {
	case ">=":
		return comparison >= 0
	case "<=":
		return comparison <= 0
	}
	return slices.Contains(r.versions, version)
}

func (p *caskParser) lifecycle(name rbToken, args rbArgs) error {
	if len(args.positional) > 0 {
		return p.fail(name.line, "%s expects keyword arguments", name.text)
	}
	mark := &lifecycleMark{}
	for index, key := range args.options.keys {
		value := args.options.vals[index]
		if value.kind != vString && value.kind != vSymbol {
			return p.fail(name.line, "unsupported %s %s", name.text, key)
		}
		switch key {
		case "date":
			mark.date = value.text
		case "because":
			mark.reason = value.text
		case "replacement_formula":
			mark.formula = value.text
		case "replacement_cask":
			mark.cask = value.text
		case "replacement":
			mark.formula, mark.cask = value.text, value.text
		default:
			return p.fail(name.line, "unsupported %s %s", name.text, key)
		}
	}
	date, err := time.Parse(time.DateOnly, mark.date)
	if err != nil || mark.reason == "" {
		return p.fail(name.line, "%s needs date: and because:", name.text)
	}
	if name.text == "disable!" && mark.reason == "unsigned" {
		mark.reason = "fails_gatekeeper_check"
	}
	mark.active = !date.After(time.Date(p.today.Year(), p.today.Month(), p.today.Day(), 0, 0, 0, 0, time.UTC))
	if name.text == "deprecate!" {
		p.deprecation = mark
	} else {
		p.disable = mark
	}
	return nil
}

// Homebrew's API lists macOS platforms from the newest release down to its oldest supported one.
var apiMacosTags = []string{"golden_gate", "tahoe", "sequoia", "sonoma", "ventura", "monterey", "big_sur"}

func (p *caskParser) supportedPlatforms() []string {
	platforms := []string{}
	if p.version == "" || p.sha256 == "" || p.url == "" || !p.installable {
		return platforms
	}
	allowsArch := func(arch string) bool { return len(p.arches) == 0 || slices.Contains(p.arches, arch) }
	for _, tag := range apiMacosTags {
		version := macosReleases[tag]
		if !p.macos.allows(version) || !p.maximumMacos.allows(version) {
			continue
		}
		if allowsArch("x86_64") {
			platforms = append(platforms, tag)
		}
		if allowsArch("arm64") {
			platforms = append(platforms, "arm64_"+tag)
		}
	}
	return platforms
}

// artifactOrder follows Cask::Artifact::AbstractArtifact#sort_order, which decides install order.
func artifactOrder(entry map[string]any) int {
	groups := [][]string{
		{"preflight_steps"}, {"uninstall_preflight_steps"}, {"preflight", "uninstall_preflight"}, {"uninstall"},
		{"generated_script"}, {"installer"}, {"pkg"},
		{"app", "app_image", "suite", "artifact", "colorpicker", "prefpane", "qlplugin", "mdimporter", "dictionary", "font",
			"service", "input_method", "internet_plugin", "keyboard_layout", "audio_unit_plugin", "vst_plugin", "vst3_plugin",
			"screen_saver", "stage_only"},
		{"binary", "command_wrapper"}, {"manpage"}, {"bash_completion", "zsh_completion", "fish_completion", "pwsh_completion"},
		{"generate_completions_from_executable"}, {"postflight_steps"}, {"uninstall_postflight_steps"},
		{"postflight", "uninstall_postflight"}, {"zap"},
	}
	for kind := range entry {
		if kind == "target" {
			continue
		}
		for index, group := range groups {
			if slices.Contains(group, kind) {
				return index
			}
		}
	}
	return len(groups)
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (p *caskParser) render() (json.RawMessage, error) {
	dependsOn := map[string]any{}
	for _, key := range p.dependsKeys {
		dependsOn[key] = p.dependsOn[key]
	}
	var autoUpdates any
	if p.autoUpdates != nil {
		autoUpdates = *p.autoUpdates
	}
	var caveats, rosetta, conflicts, container any
	if len(p.caveats) > 0 {
		caveats = strings.Join(p.caveats, "\n")
	}
	if p.rosetta {
		rosetta = true
	}
	if p.conflicts != nil {
		conflicts = p.conflicts
	}
	if p.container != nil {
		container = p.container
	}
	artifacts := slices.Clone(p.artifacts)
	if artifacts == nil {
		artifacts = []map[string]any{}
	}
	slices.SortStableFunc(artifacts, func(a, b map[string]any) int { return artifactOrder(a) - artifactOrder(b) })
	urlSpecs := p.urlSpecs
	if urlSpecs == nil {
		urlSpecs = map[string]any{}
	}
	out := map[string]any{
		"token": p.token, "full_token": p.ref.Tap + "/" + p.token, "old_tokens": []string{}, "tap": p.ref.Tap,
		"name": p.names, "desc": nullable(p.desc), "homepage": nullable(p.homepage), "url": p.url, "url_specs": urlSpecs,
		"version": p.version, "sha256": nullable(p.sha256), "artifacts": artifacts, "caveats": caveats,
		"caveats_rosetta": rosetta, "depends_on": dependsOn, "conflicts_with": conflicts, "container": container,
		"rename": []any{}, "auto_updates": autoUpdates, "languages": []string{}, "ruby_source_path": p.ref.Path,
		"tap_git_head": nullable(p.ref.GitHead), "supported_platforms": p.supportedPlatforms(), "variations": map[string]any{},
		"deprecated": false, "deprecation_date": nil, "deprecation_reason": nil, "deprecation_replacement_formula": nil,
		"deprecation_replacement_cask": nil, "disabled": false, "disable_date": nil, "disable_reason": nil,
		"disable_replacement_formula": nil, "disable_replacement_cask": nil,
	}
	if mark := p.deprecation; mark != nil {
		out["deprecation_date"] = mark.date
		if mark.active {
			out["deprecated"], out["deprecation_reason"] = true, mark.reason
			out["deprecation_replacement_formula"], out["deprecation_replacement_cask"] = nullable(mark.formula), nullable(mark.cask)
		}
	}
	if mark := p.disable; mark != nil {
		out["disable_date"] = mark.date
		if mark.active {
			out["disabled"], out["disable_reason"] = true, mark.reason
			out["disable_replacement_formula"], out["disable_replacement_cask"] = nullable(mark.formula), nullable(mark.cask)
		} else {
			// A future disable date deprecates the cask until then.
			out["deprecated"], out["deprecation_reason"] = true, mark.reason
			out["deprecation_replacement_formula"], out["deprecation_replacement_cask"] = nullable(mark.formula), nullable(mark.cask)
		}
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode cask: %w", err)
	}
	return encoded, nil
}
