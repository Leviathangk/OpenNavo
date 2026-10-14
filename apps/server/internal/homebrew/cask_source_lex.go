package homebrew

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A lexer for the Ruby subset found in cask files. It only has to tokenize well enough to evaluate the
// declarative stanzas and to skip over blocks (livecheck, flight blocks) without misreading their contents.

type rbKind uint8

const (
	rbEOF rbKind = iota
	rbNewline
	rbIdent   // local names and method calls, including a trailing ? or !, @ivars and $globals
	rbConst   // HOMEBREW_PREFIX, MacOS
	rbLabel   // hash shorthand key; text excludes the colon
	rbSymbol  // text excludes the colon
	rbString  // parts hold literal text and interpolations
	rbWords   // %w[] and %i[] arrays
	rbRegexp  // contents are never evaluated
	rbCommand // backtick shell command; dynamic, never a static string
	rbNumber  // text is the literal without underscores
	rbKeyword // reserved words
	rbPunct
)

type rbPart struct {
	text   string // literal text, or the Ruby source of an interpolation
	interp bool
	line   int
}

type rbToken struct {
	kind   rbKind
	text   string
	parts  []rbPart
	words  []string
	line   int
	spaced bool // whitespace precedes the token
}

var rbKeywords = map[string]bool{
	"BEGIN": true, "END": true, "alias": true, "and": true, "begin": true, "break": true, "case": true, "class": true,
	"def": true, "defined?": true, "do": true, "else": true, "elsif": true, "end": true, "ensure": true, "false": true,
	"for": true, "if": true, "in": true, "module": true, "next": true, "nil": true, "not": true, "or": true, "redo": true,
	"rescue": true, "retry": true, "return": true, "self": true, "super": true, "then": true, "true": true, "undef": true,
	"unless": true, "until": true, "when": true, "while": true, "yield": true,
}

// Longest first, so that the first prefix match wins.
var rbPunctuation = []string{
	"**=", "<=>", "===", "...", "<<=", ">>=", "&&=", "||=",
	"::", "=>", "==", "!=", "=~", "!~", "<=", ">=", "&&", "||", "<<", ">>", "+=", "-=", "*=", "/=", "%=", "|=", "&=",
	"^=", "->", "&.", "**", "..",
	",", "(", ")", "[", "]", "{", "}", ".", "|", "&", "=", "!", "<", ">", "+", "-", "*", "/", "%", "?", ":", "^", "~",
}

type rbHeredoc struct {
	index               int // token waiting for its body
	ident               string
	squiggly, dash, raw bool
}

type rbLexer struct {
	src     string
	pos     int
	line    int
	tokens  []rbToken
	pending []rbHeredoc
}

func lexRuby(src string) ([]rbToken, error) {
	if !utf8.ValidString(src) {
		return nil, &UnsupportedCaskError{Reason: "source is not valid UTF-8"}
	}
	lexer := &rbLexer{src: src, line: 1}
	if err := lexer.run(); err != nil {
		return nil, err
	}
	return lexer.tokens, nil
}

func (l *rbLexer) fail(format string, args ...any) error {
	return &UnsupportedCaskError{Line: l.line, Reason: fmt.Sprintf(format, args...)}
}

func (l *rbLexer) peek(offset int) byte {
	if l.pos+offset < len(l.src) {
		return l.src[l.pos+offset]
	}
	return 0
}

func (l *rbLexer) emit(token rbToken) {
	l.tokens = append(l.tokens, token)
}

func (l *rbLexer) last() *rbToken {
	if len(l.tokens) == 0 {
		return nil
	}
	return &l.tokens[len(l.tokens)-1]
}

// valueEnded reports whether the previous token ends an operand, which decides between
// division and a regexp, and between a heredoc and the << operator.
func (l *rbLexer) valueEnded() bool {
	last := l.last()
	if last == nil {
		return false
	}
	switch last.kind {
	case rbIdent, rbConst, rbString, rbWords, rbRegexp, rbNumber, rbSymbol:
		return true
	case rbKeyword:
		return last.text == "end" || last.text == "self" || last.text == "true" || last.text == "false" || last.text == "nil"
	case rbPunct:
		return last.text == ")" || last.text == "]" || last.text == "}"
	}
	return false
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9'
}

func (l *rbLexer) run() error {
	atLineStart := true
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		spaced := false
		for c == ' ' || c == '\t' || c == '\r' || c == '\\' && l.peek(1) == '\n' {
			if c == '\\' {
				l.pos++
				l.line++
			}
			l.pos++
			spaced = true
			if l.pos >= len(l.src) {
				return l.finish()
			}
			c = l.src[l.pos]
		}
		if atLineStart && strings.HasPrefix(l.src[l.pos:], "=begin") {
			end := strings.Index(l.src[l.pos:], "\n=end")
			if end < 0 {
				return l.fail("unterminated =begin comment")
			}
			l.line += strings.Count(l.src[l.pos:l.pos+end+1], "\n")
			l.pos += end + len("\n=end")
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
			continue
		}
		if atLineStart && strings.HasPrefix(l.src[l.pos:], "__END__") {
			break
		}
		atLineStart = false
		switch {
		case c == '#':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
		case c == '\n' || c == ';':
			l.pos++
			if c == '\n' {
				l.line++
				if err := l.readHeredocBodies(); err != nil {
					return err
				}
				atLineStart = true
				if l.continuesWithDot() {
					continue
				}
			}
			if last := l.last(); last != nil && last.kind != rbNewline {
				l.emit(rbToken{kind: rbNewline, text: "\n", line: l.line - 1})
			}
		case c == '"' || c == '`':
			l.pos++
			parts, err := l.readQuoted(c, 0, true)
			if err != nil {
				return err
			}
			if c == '`' {
				l.emit(rbToken{kind: rbCommand, text: "shell command", line: l.line, spaced: spaced})
			} else {
				l.emitString(parts, spaced)
			}
		case c == '\'':
			l.pos++
			parts, err := l.readQuoted('\'', 0, false)
			if err != nil {
				return err
			}
			l.emitString(parts, spaced)
		case c == ':' && l.peek(1) == '"':
			l.pos += 2
			parts, err := l.readQuoted('"', 0, true)
			if err != nil {
				return err
			}
			text, ok := literalText(parts)
			if !ok {
				return l.fail("interpolated symbols are not supported")
			}
			l.emit(rbToken{kind: rbSymbol, text: text, line: l.line, spaced: spaced})
		case c == ':' && isIdentStart(l.peek(1)) && (l.last() == nil || l.last().kind != rbIdent || spaced):
			start := l.pos + 1
			l.pos++
			for l.pos < len(l.src) && isIdentChar(l.src[l.pos]) {
				l.pos++
			}
			if l.pos < len(l.src) && (l.src[l.pos] == '?' || l.src[l.pos] == '!' || l.src[l.pos] == '=') && l.peek(1) != '=' && l.peek(1) != '>' {
				l.pos++
			}
			l.emit(rbToken{kind: rbSymbol, text: l.src[start:l.pos], line: l.line, spaced: spaced})
		case c == '%' && !l.valueEnded() || c == '%' && spaced && l.peek(1) != ' ' && l.peek(1) != '=':
			if err := l.readPercent(spaced); err != nil {
				return err
			}
		case c == '/' && (!l.valueEnded() || spaced && l.peek(1) != ' ' && l.peek(1) != '=' && l.last().kind == rbIdent):
			l.pos++
			if _, err := l.readQuoted('/', 0, true); err != nil {
				return err
			}
			l.skipRegexpFlags()
			l.emit(rbToken{kind: rbRegexp, line: l.line, spaced: spaced})
		case c == '<' && l.peek(1) == '<' && l.startsHeredoc():
			if err := l.readHeredocStart(spaced); err != nil {
				return err
			}
		case c >= '0' && c <= '9':
			start := l.pos
			for l.pos < len(l.src) && (isIdentChar(l.src[l.pos]) || l.src[l.pos] == '.' && l.peek(1) >= '0' && l.peek(1) <= '9') {
				l.pos++
			}
			l.emit(rbToken{kind: rbNumber, text: strings.ReplaceAll(l.src[start:l.pos], "_", ""), line: l.line, spaced: spaced})
		case isIdentStart(c) || c == '@' && (isIdentStart(l.peek(1)) || l.peek(1) == '@') || c == '$' && isIdentChar(l.peek(1)):
			l.readIdentifier(spaced)
		case c == '?' && isIdentChar(l.peek(1)) && !isIdentChar(l.peek(2)) && !l.valueEnded():
			l.emit(rbToken{kind: rbString, parts: []rbPart{{text: string(l.src[l.pos+1]), line: l.line}}, line: l.line, spaced: spaced})
			l.pos += 2
		default:
			matched := false
			for _, punct := range rbPunctuation {
				if strings.HasPrefix(l.src[l.pos:], punct) {
					l.emit(rbToken{kind: rbPunct, text: punct, line: l.line, spaced: spaced})
					l.pos += len(punct)
					matched = true
					break
				}
			}
			if !matched {
				return l.fail("unexpected character %q", c)
			}
		}
	}
	return l.finish()
}

func (l *rbLexer) finish() error {
	if len(l.pending) > 0 {
		return l.fail("unterminated heredoc %s", l.pending[0].ident)
	}
	if last := l.last(); last != nil && last.kind != rbNewline {
		l.emit(rbToken{kind: rbNewline, text: "\n", line: l.line})
	}
	l.emit(rbToken{kind: rbEOF, line: l.line})
	return nil
}

// A line that starts with `.method` continues the previous expression.
func (l *rbLexer) continuesWithDot() bool {
	index := l.pos
	for index < len(l.src) && (l.src[index] == ' ' || l.src[index] == '\t') {
		index++
	}
	if index+1 < len(l.src) && (l.src[index] == '.' && l.src[index+1] != '.' || l.src[index] == '&' && l.src[index+1] == '.') {
		return true
	}
	return false
}

func (l *rbLexer) emitString(parts []rbPart, spaced bool) {
	// `"key": value` is a label in hash and keyword arguments.
	if l.pos < len(l.src) && l.src[l.pos] == ':' && l.peek(1) != ':' {
		if text, ok := literalText(parts); ok && (l.peek(1) == ' ' || l.peek(1) == '\n') {
			l.pos++
			l.emit(rbToken{kind: rbLabel, text: text, line: l.line, spaced: spaced})
			return
		}
	}
	l.emit(rbToken{kind: rbString, parts: parts, line: l.line, spaced: spaced})
}

func (l *rbLexer) readIdentifier(spaced bool) {
	start := l.pos
	if l.src[l.pos] == '@' || l.src[l.pos] == '$' {
		l.pos++
		if l.pos < len(l.src) && l.src[l.pos] == '@' {
			l.pos++
		}
	}
	for l.pos < len(l.src) && isIdentChar(l.src[l.pos]) {
		l.pos++
	}
	if l.pos < len(l.src) && (l.src[l.pos] == '?' || l.src[l.pos] == '!') && l.peek(1) != '=' {
		l.pos++
	}
	text := l.src[start:l.pos]
	afterDot := false
	if last := l.last(); last != nil && last.kind == rbPunct && (last.text == "." || last.text == "&." || last.text == "::") {
		afterDot = true
	}
	// `name:` (but not `name::`) is a label; a method named like a keyword after a dot is an identifier.
	if l.pos < len(l.src) && l.src[l.pos] == ':' && l.peek(1) != ':' && text[0] != '@' && text[0] != '$' {
		if last := l.last(); last == nil || last.kind != rbPunct || last.text != "?" {
			l.pos++
			l.emit(rbToken{kind: rbLabel, text: text, line: l.line, spaced: spaced})
			return
		}
	}
	kind := rbIdent
	switch {
	case !afterDot && rbKeywords[text]:
		kind = rbKeyword
	case text[0] >= 'A' && text[0] <= 'Z':
		kind = rbConst
	}
	l.emit(rbToken{kind: kind, text: text, line: l.line, spaced: spaced})
}

func closing(open byte) byte {
	switch open {
	case '(':
		return ')'
	case '[':
		return ']'
	case '{':
		return '}'
	case '<':
		return '>'
	}
	return 0
}

// readQuoted reads up to the closing delimiter. A nonzero open byte allows nesting of bracket pairs.
func (l *rbLexer) readQuoted(end, open byte, interpolate bool) ([]rbPart, error) {
	var parts []rbPart
	var text strings.Builder
	depth := 0
	startLine := l.line
	flush := func() {
		if text.Len() > 0 {
			parts = append(parts, rbPart{text: text.String(), line: l.line})
			text.Reset()
		}
	}
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '\\' && l.pos+1 < len(l.src):
			next := l.src[l.pos+1]
			l.pos += 2
			if next == '\n' {
				l.line++
				if !interpolate {
					text.WriteString("\\\n")
				}
				continue
			}
			if !interpolate || end == '/' {
				// Single quotes keep backslashes except before the delimiter or another backslash; regexps are never evaluated.
				if next != end && next != '\\' && (open == 0 || next != open) || end == '/' {
					text.WriteByte('\\')
				}
				text.WriteByte(next)
				continue
			}
			if err := l.writeEscape(&text, next); err != nil {
				return nil, err
			}
		case interpolate && c == '#' && l.peek(1) == '{':
			flush()
			l.pos += 2
			line := l.line
			source, err := l.readInterpolation()
			if err != nil {
				return nil, err
			}
			parts = append(parts, rbPart{text: source, interp: true, line: line})
		case interpolate && c == '#' && (l.peek(1) == '@' || l.peek(1) == '$') && isIdentStart(l.peek(2)):
			flush()
			l.pos++
			start := l.pos
			l.pos++
			for l.pos < len(l.src) && isIdentChar(l.src[l.pos]) {
				l.pos++
			}
			parts = append(parts, rbPart{text: l.src[start:l.pos], interp: true, line: l.line})
		case open != 0 && c == open:
			depth++
			text.WriteByte(c)
			l.pos++
		case c == end && depth > 0:
			depth--
			text.WriteByte(c)
			l.pos++
		case c == end:
			l.pos++
			flush()
			if parts == nil {
				parts = []rbPart{{line: startLine}}
			}
			return parts, nil
		default:
			if c == '\n' {
				l.line++
			}
			text.WriteByte(c)
			l.pos++
		}
	}
	return nil, &UnsupportedCaskError{Line: startLine, Reason: "unterminated string or regexp"}
}

func (l *rbLexer) writeEscape(text *strings.Builder, next byte) error {
	switch next {
	case 'n':
		text.WriteByte('\n')
	case 't':
		text.WriteByte('\t')
	case 'r':
		text.WriteByte('\r')
	case 's':
		text.WriteByte(' ')
	case '0':
		text.WriteByte(0)
	case 'e':
		text.WriteByte(0x1b)
	case 'a':
		text.WriteByte(7)
	case 'b':
		text.WriteByte(8)
	case 'f':
		text.WriteByte(12)
	case 'v':
		text.WriteByte(11)
	case 'u':
		var digits string
		if l.peek(0) == '{' {
			end := strings.IndexByte(l.src[l.pos:], '}')
			if end < 0 {
				return l.fail("invalid unicode escape")
			}
			digits, l.pos = l.src[l.pos+1:l.pos+end], l.pos+end+1
		} else if l.pos+4 <= len(l.src) {
			digits, l.pos = l.src[l.pos:l.pos+4], l.pos+4
		}
		for _, field := range strings.Fields(digits) {
			code, err := strconv.ParseUint(field, 16, 32)
			if err != nil || code > unicode.MaxRune {
				return l.fail("invalid unicode escape")
			}
			char := rune(code) // #nosec G115 -- bounded by unicode.MaxRune above.
			if !utf8.ValidRune(char) {
				return l.fail("invalid unicode escape")
			}
			text.WriteRune(char)
		}
		if strings.TrimSpace(digits) == "" {
			return l.fail("invalid unicode escape")
		}
	case 'x':
		end := l.pos
		for end < len(l.src) && end < l.pos+2 && strings.IndexByte("0123456789abcdefABCDEF", l.src[end]) >= 0 {
			end++
		}
		code, err := strconv.ParseUint(l.src[l.pos:end], 16, 8)
		if err != nil || code >= 0x80 {
			return l.fail("invalid hex escape")
		}
		text.WriteByte(byte(code))
		l.pos = end
	default:
		if next >= '1' && next <= '7' || next == 'c' || next == 'C' || next == 'M' {
			return l.fail("unsupported string escape \\%c", next)
		}
		text.WriteByte(next)
	}
	return nil
}

// readInterpolation returns the Ruby source between #{ and the matching }.
func (l *rbLexer) readInterpolation() (string, error) {
	start, depth := l.pos, 0
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch c {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				source := l.src[start:l.pos]
				l.pos++
				return source, nil
			}
			depth--
		case '"', '\'':
			l.pos++
			if _, err := l.readQuoted(c, 0, c == '"'); err != nil {
				return "", err
			}
			continue
		case '\n':
			l.line++
		}
		l.pos++
	}
	return "", l.fail("unterminated interpolation")
}

func (l *rbLexer) skipRegexpFlags() {
	for l.pos < len(l.src) && strings.IndexByte("imxounse", l.src[l.pos]) >= 0 {
		l.pos++
	}
}

func (l *rbLexer) readPercent(spaced bool) error {
	kind := l.peek(1)
	delimiterAt := 2
	if (kind < 'a' || kind > 'z') && (kind < 'A' || kind > 'Z') {
		kind, delimiterAt = 'Q', 1
	}
	open := l.peek(delimiterAt)
	if open == 0 || isIdentChar(open) || open == ' ' || open == '\n' || strings.IndexByte("qQwWiIr", kind) < 0 {
		return l.fail("unsupported %% literal")
	}
	l.pos += delimiterAt + 1
	end, nesting := closing(open), open
	if end == 0 {
		end, nesting = open, 0
	}
	parts, err := l.readQuoted(end, nesting, kind == 'Q' || kind == 'W' || kind == 'I' || kind == 'r')
	if err != nil {
		return err
	}
	switch kind {
	case 'r':
		l.skipRegexpFlags()
		l.emit(rbToken{kind: rbRegexp, line: l.line, spaced: spaced})
	case 'w', 'W', 'i', 'I':
		text, ok := literalText(parts)
		if !ok {
			return l.fail("interpolated word arrays are not supported")
		}
		token := rbToken{kind: rbWords, words: strings.Fields(text), line: l.line, spaced: spaced}
		if kind == 'i' || kind == 'I' {
			token.text = "symbols"
		}
		l.emit(token)
	default:
		l.emit(rbToken{kind: rbString, parts: parts, line: l.line, spaced: spaced})
	}
	return nil
}

func (l *rbLexer) startsHeredoc() bool {
	rest := l.src[l.pos+2:]
	if rest == "" {
		return false
	}
	c := rest[0]
	if c == '~' || c == '-' {
		return len(rest) > 1 && (isIdentStart(rest[1]) || rest[1] == '"' || rest[1] == '\'')
	}
	if (c < 'A' || c > 'Z') && c != '_' && c != '"' && c != '\'' {
		return false
	}
	return !l.valueEnded() || l.last().kind == rbIdent && l.pos > 0 && l.src[l.pos-1] == ' '
}

func (l *rbLexer) readHeredocStart(spaced bool) error {
	l.pos += 2
	heredoc := rbHeredoc{index: len(l.tokens)}
	switch l.peek(0) {
	case '~':
		heredoc.squiggly = true
		l.pos++
	case '-':
		heredoc.dash = true
		l.pos++
	}
	if quote := l.peek(0); quote == '"' || quote == '\'' {
		end := strings.IndexByte(l.src[l.pos+1:], quote)
		if end < 0 {
			return l.fail("invalid heredoc")
		}
		heredoc.ident, heredoc.raw = l.src[l.pos+1:l.pos+1+end], quote == '\''
		l.pos += end + 2
	} else {
		start := l.pos
		for l.pos < len(l.src) && isIdentChar(l.src[l.pos]) {
			l.pos++
		}
		heredoc.ident = l.src[start:l.pos]
	}
	if heredoc.ident == "" {
		return l.fail("invalid heredoc")
	}
	l.emit(rbToken{kind: rbString, line: l.line, spaced: spaced})
	l.pending = append(l.pending, heredoc)
	return nil
}

func (l *rbLexer) readHeredocBodies() error {
	for _, heredoc := range l.pending {
		startLine := l.line
		var lines []string
		for {
			if l.pos >= len(l.src) {
				return &UnsupportedCaskError{Line: startLine, Reason: "unterminated heredoc " + heredoc.ident}
			}
			end := strings.IndexByte(l.src[l.pos:], '\n')
			var line string
			if end < 0 {
				line, l.pos = l.src[l.pos:], len(l.src)
			} else {
				line, l.pos = l.src[l.pos:l.pos+end], l.pos+end+1
			}
			l.line++
			terminator := line
			if heredoc.squiggly || heredoc.dash {
				terminator = strings.TrimLeft(line, " \t")
			}
			if strings.TrimRight(terminator, "\r") == heredoc.ident {
				break
			}
			lines = append(lines, line)
		}
		if heredoc.squiggly {
			lines = dedent(lines)
		}
		body := ""
		if len(lines) > 0 {
			body = strings.Join(lines, "\n") + "\n"
		}
		var parts []rbPart
		if heredoc.raw {
			parts = []rbPart{{text: body, line: startLine}}
		} else {
			inner := &rbLexer{src: body + "\x00", line: startLine}
			read, err := inner.readQuoted(0, 0, true)
			if err != nil {
				return err
			}
			parts = read
		}
		l.tokens[heredoc.index].parts = parts
	}
	l.pending = nil
	return nil
}

// dedent removes the indentation of the least indented line like a <<~ heredoc. Tabs advance to the next
// multiple of eight columns, and a tab that would cross the removed width is kept.
func dedent(lines []string) []string {
	width := func(line string) (int, bool) {
		column := 0
		for _, c := range []byte(line) {
			switch c {
			case ' ':
				column++
			case '\t':
				column = (column/8 + 1) * 8
			default:
				return column, true
			}
		}
		return column, false
	}
	indent := -1
	for _, line := range lines {
		if column, content := width(line); content && (indent < 0 || column < indent) {
			indent = column
		}
	}
	out := make([]string, len(lines))
	for index, line := range lines {
		column, cut := 0, 0
		for cut < len(line) && column < indent {
			next := column + 1
			if line[cut] == '\t' {
				next = (column/8 + 1) * 8
			} else if line[cut] != ' ' {
				break
			}
			if next > indent {
				break
			}
			column, cut = next, cut+1
		}
		out[index] = line[cut:]
	}
	return out
}

func literalText(parts []rbPart) (string, bool) {
	var text strings.Builder
	for _, part := range parts {
		if part.interp {
			return "", false
		}
		text.WriteString(part.text)
	}
	return text.String(), true
}
