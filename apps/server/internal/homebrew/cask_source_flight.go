package homebrew

import (
	"regexp"
	"strings"
)

// flightGuard accepts only a feature check selecting the modern and legacy forms of the same
// flight hook. Validate both branches before recording the modern marker, just as for an
// unconditional *_steps block. Never evaluate Ruby or choose metadata based on a brew version.
func (p *caskParser) flightGuard(start rbToken) error {
	condition := p.next()
	if condition.kind != rbIdent || condition.text != "respond_to?" {
		return p.fail(start.line, "conditionals are not supported")
	}
	args, block, err := p.parseCall(condition)
	if err != nil {
		return err
	}
	if len(args.positional) != 1 || args.positional[0].kind != vSymbol || len(args.options.keys) != 0 || block != "" || p.peek().kind != rbNewline {
		return p.fail(start.line, "expected a flight-only respond_to?(:*_steps) compatibility guard")
	}
	modern := args.positional[0].text
	legacy, ok := strings.CutSuffix(modern, "_steps")
	if !ok || !flightBlocks[legacy] {
		return p.fail(start.line, "unsupported flight feature %q", modern)
	}
	if err := p.flightGuardBranch(modern); err != nil {
		return err
	}
	p.skipNewlines()
	if token := p.next(); !isKeyword(token, "else") {
		return p.fail(token.line, "flight compatibility guard requires a matching legacy else branch")
	}
	if p.peek().kind != rbNewline {
		return p.fail(p.peek().line, "expected a newline after else")
	}
	if err := p.flightGuardBranch(legacy); err != nil {
		return err
	}
	p.skipNewlines()
	if token := p.next(); !isKeyword(token, "end") {
		return p.fail(token.line, "expected end of flight compatibility guard")
	}
	if token := p.peek(); token.kind != rbNewline && token.kind != rbEOF && !isKeyword(token, "end") {
		return p.fail(token.line, "unsupported expression after a flight compatibility guard")
	}
	p.addArtifact(map[string]any{modern: nil}, !strings.HasPrefix(modern, "uninstall"))
	return nil
}

var flightLocalName = regexp.MustCompile(`^[a-z_][a-zA-Z0-9_]*$`)

// flightGuardBranch allows literal string helpers followed by exactly one matching flight block.
// Helpers are validated but never stored: referencing them from catalog metadata still fails.
func (p *caskParser) flightGuardBranch(want string) error {
	for {
		p.skipNewlines()
		name := p.next()
		if name.kind != rbIdent {
			return p.fail(name.line, "expected %s block in flight compatibility guard", want)
		}
		if isPunct(p.peek(), "=") {
			if !flightLocalName.MatchString(name.text) {
				return p.fail(name.line, "flight helpers must be local variables")
			}
			// Ruby locals can shadow DSL methods in later interpolations, even when declared
			// in the unselected branch. Keep every root resolved by evaluate unambiguous.
			switch name.text {
			case "arch", "version", "token", "appdir", "staged_path", "caskroom_path":
				return p.fail(name.line, "flight helper shadows %s", name.text)
			}
			p.next()
			value, err := p.value()
			if err != nil {
				return err
			}
			if value.kind != vString || p.peek().kind != rbNewline {
				return p.fail(name.line, "flight helpers must be literal strings")
			}
			continue
		}
		if name.text != want {
			return p.fail(name.line, "only %s blocks and literal string helpers are supported in this flight branch", want)
		}
		args, block, err := p.parseCall(name)
		if err != nil {
			return err
		}
		if block == "" || len(args.positional) != 0 || len(args.options.keys) != 0 {
			return p.fail(name.line, "expected %s block without arguments", want)
		}
		if isPunct(p.tokens[p.pos-1], "|") {
			return p.fail(name.line, "block parameters are not supported in flight compatibility guards")
		}
		_, err = p.skipRubyBlock(block, true)
		return err
	}
}

// Guarded hooks must have unambiguous boundaries: a phantom or missing end could hide
// metadata or a second hook in the same branch. Keep this subset deliberately smaller
// than ordinary opaque flight bodies instead of guessing at arbitrary Ruby syntax.
// Supported constructs are balanced calls/arrays/hashes, method do blocks, and standalone
// multiline if/unless with else/elsif. Modifiers, inline conditionals, definitions, loops,
// left shifts, and heredocs are rejected because either form can hide a guard boundary.
func (p *caskParser) validateFlightGuardToken(token, previous rbToken, closer string) error {
	if token.heredoc || token.kind == rbPunct && strings.HasPrefix(token.text, "<<") {
		return p.fail(token.line, "ambiguous heredoc or left shift in flight compatibility guard")
	}
	if token.kind != rbKeyword {
		return nil
	}
	standalone := previous.kind == rbNewline && closer == "end"
	if standalone && p.pos >= 3 {
		// Ruby continues a line after an operator. A control expression on the next
		// line is still inline even though the lexer emitted a newline token.
		beforeNewline := p.tokens[p.pos-3]
		if beforeNewline.kind == rbPunct && beforeNewline.text != ")" && beforeNewline.text != "]" && beforeNewline.text != "}" {
			standalone = false
		}
	}
	switch token.text {
	case "true", "false", "nil", "self":
		return nil
	case "if", "unless", "else", "elsif", "end":
		if standalone {
			return nil
		}
	case "do":
		// Loops are rejected below, so every accepted do opens its own block.
		// Receiver methods named do are already lexed as identifiers.
		if previous.kind != rbNewline {
			return nil
		}
	}
	return p.fail(token.line, "unsupported %q construct in flight compatibility guard", token.text)
}
