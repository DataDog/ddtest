// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"slices"
	"strings"
)

// A Jenkinsfile is a Groovy program. ddtest never runs it: this file reads
// the subset Jenkins Pipeline uses into a syntax tree whose nodes keep their
// byte spans. Declarative directives and literal step calls become calls,
// closures and literals; control flow becomes a groovyControl node, and
// everything else an explicit groovyUnknown, so the interpreter reports it
// instead of guessing (see ci_provider_jenkins_pipeline.go). Only text the
// lexer cannot read (an unterminated string or comment) or a file over the
// limits fails the read.

const (
	// groovySizeLimit and groovyTokenLimit bound what ddtest reads.
	groovySizeLimit  = 2 << 20
	groovyTokenLimit = 100000
	// groovyDepthLimit bounds nesting, so a hostile file cannot exhaust the
	// stack.
	groovyDepthLimit = 200
	// groovyInterpolationLimit bounds GStrings nested in interpolations,
	// whose source each level reads again.
	groovyInterpolationLimit = 8
)

type groovyTokenKind int

const (
	groovyEOF groovyTokenKind = iota
	groovyNewline
	groovyIdent
	groovyNumber
	groovyStringToken
	groovyPunct
)

type groovyToken struct {
	kind groovyTokenKind
	// text is an identifier, number or punctuation as written.
	text       string
	start, end int
	// str is a string literal's content.
	str *groovyStr
	// spaced reports whitespace (not a newline) before the token.
	spaced bool
}

// groovyStr is a string literal: its quote and its parts, literal text
// (escapes decoded) and interpolations ${…} or $name.path in GStrings.
type groovyStr struct {
	quote string
	parts []groovyStrPart
}

type groovyStrPart struct {
	text string
	// expr is an interpolation's source, without ${ and }; braced marks a
	// ${…} one, whose source starts at exprStart in the file.
	expr         string
	interpolated bool
	braced       bool
	exprStart    int
}

// literal is the string's value when nothing is interpolated.
func (s *groovyStr) literal() (string, bool) {
	var text strings.Builder
	for _, part := range s.parts {
		if part.interpolated {
			return "", false
		}
		text.WriteString(part.text)
	}
	return text.String(), true
}

// groovyPunctuation lists the operators, longest first.
var groovyPunctuation = []string{
	"..<", "<=>", "==~", "?.", "*.", ".&", ".@", "?:", "->", "=~", "==", "!=", "<=", ">=", "&&", "||", "++", "--", "+=", "-=", "*=", "/=", "%=", "<<", ">>", "**", "..", "::",
	"{", "}", "(", ")", "[", "]", ",", ";", ":", ".", "=", "+", "-", "*", "/", "%", "<", ">", "!", "?", "&", "|", "^", "~", "@", "#", "\\",
}

type groovyLexer struct {
	source string
	pos    int
	tokens []groovyToken
	spaced bool
}

// lexGroovy splits source into tokens.
func lexGroovy(source string) ([]groovyToken, error) {
	if len(source) > groovySizeLimit {
		return nil, fmt.Errorf("the Jenkinsfile is larger than %d bytes", groovySizeLimit)
	}
	l := &groovyLexer{source: source}
	if strings.HasPrefix(source, "#!") {
		l.skipLine()
	}
	for {
		if len(l.tokens) > groovyTokenLimit {
			return nil, fmt.Errorf("the Jenkinsfile has more than %d tokens", groovyTokenLimit)
		}
		if err := l.next(); err != nil {
			return nil, err
		}
		if l.tokens[len(l.tokens)-1].kind == groovyEOF {
			return l.tokens, nil
		}
	}
}

func (l *groovyLexer) skipLine() {
	for l.pos < len(l.source) && l.source[l.pos] != '\n' {
		l.pos++
	}
}

func (l *groovyLexer) emit(kind groovyTokenKind, start int, text string, str *groovyStr) {
	l.tokens = append(l.tokens, groovyToken{kind: kind, text: text, start: start, end: l.pos, str: str, spaced: l.spaced})
	l.spaced = false
}

// line reports the 1-based line of a byte offset.
func groovyLine(source string, offset int) int {
	return strings.Count(source[:min(offset, len(source))], "\n") + 1
}

// valueBefore reports whether the previous token ends a value, so a / that
// follows divides instead of starting a slashy string.
func (l *groovyLexer) valueBefore() bool {
	if len(l.tokens) == 0 {
		return false
	}
	last := l.tokens[len(l.tokens)-1]
	switch last.kind {
	case groovyIdent:
		return !slices.Contains([]string{"return", "case", "in", "else", "assert", "throw"}, last.text)
	case groovyNumber, groovyStringToken:
		return true
	case groovyPunct:
		return last.text == ")" || last.text == "]" || last.text == "}"
	}
	return false
}

func (l *groovyLexer) next() error {
	for l.pos < len(l.source) {
		c := l.source[l.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\f':
			l.pos++
			l.spaced = true
		case c == '\\' && l.pos+1 < len(l.source) && (l.source[l.pos+1] == '\n' || l.source[l.pos+1] == '\r'):
			// A line continuation joins the lines.
			l.pos += 2
			if l.pos < len(l.source) && l.source[l.pos-1] == '\r' && l.source[l.pos] == '\n' {
				l.pos++
			}
			l.spaced = true
		case strings.HasPrefix(l.source[l.pos:], "//"):
			l.skipLine()
		case strings.HasPrefix(l.source[l.pos:], "/*"):
			end := strings.Index(l.source[l.pos+2:], "*/")
			if end < 0 {
				return fmt.Errorf("line %d: unterminated comment", groovyLine(l.source, l.pos))
			}
			l.pos += end + 4
			l.spaced = true
		case c == '\n':
			start := l.pos
			l.pos++
			if n := len(l.tokens); n == 0 || l.tokens[n-1].kind != groovyNewline {
				l.emit(groovyNewline, start, "\n", nil)
				return nil
			}
		default:
			return l.token()
		}
	}
	l.emit(groovyEOF, l.pos, "", nil)
	return nil
}

func groovyIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func groovyIdentPart(c byte) bool {
	return groovyIdentStart(c) || c >= '0' && c <= '9'
}

func (l *groovyLexer) token() error {
	start := l.pos
	c := l.source[l.pos]
	rest := l.source[l.pos:]
	switch {
	case strings.HasPrefix(rest, "'''") || strings.HasPrefix(rest, `"""`):
		return l.quoted(rest[:3])
	case c == '\'' || c == '"':
		return l.quoted(rest[:1])
	case strings.HasPrefix(rest, "$/"):
		return l.dollarSlashy()
	case c == '/' && !l.valueBefore():
		return l.slashy()
	case groovyIdentStart(c):
		for l.pos < len(l.source) && groovyIdentPart(l.source[l.pos]) {
			l.pos++
		}
		l.emit(groovyIdent, start, l.source[start:l.pos], nil)
		return nil
	case c >= '0' && c <= '9':
		for l.pos < len(l.source) && (groovyIdentPart(l.source[l.pos]) || l.source[l.pos] == '.' && l.pos+1 < len(l.source) && l.source[l.pos+1] >= '0' && l.source[l.pos+1] <= '9') {
			l.pos++
		}
		l.emit(groovyNumber, start, l.source[start:l.pos], nil)
		return nil
	}
	for _, punct := range groovyPunctuation {
		if strings.HasPrefix(rest, punct) {
			l.pos += len(punct)
			l.emit(groovyPunct, start, punct, nil)
			return nil
		}
	}
	return fmt.Errorf("line %d: unexpected character %q", groovyLine(l.source, l.pos), rune(c))
}

// quoted reads a '…', "…", ”'…”' or """…""" string; double quotes
// interpolate.
func (l *groovyLexer) quoted(quote string) error {
	start := l.pos
	l.pos += len(quote)
	str := &groovyStr{quote: quote}
	gstring := quote[0] == '"'
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			str.parts = append(str.parts, groovyStrPart{text: text.String()})
			text.Reset()
		}
	}
	for {
		if l.pos >= len(l.source) {
			return fmt.Errorf("line %d: unterminated string", groovyLine(l.source, start))
		}
		if strings.HasPrefix(l.source[l.pos:], quote) {
			l.pos += len(quote)
			break
		}
		c := l.source[l.pos]
		if c == '\n' && len(quote) == 1 {
			return fmt.Errorf("line %d: unterminated string", groovyLine(l.source, start))
		}
		if c == '\\' && l.pos+1 < len(l.source) {
			l.pos++
			escaped := l.source[l.pos]
			l.pos++
			switch escaped {
			case 'n':
				text.WriteByte('\n')
			case 't':
				text.WriteByte('\t')
			case 'r':
				text.WriteByte('\r')
			case 'b':
				text.WriteByte('\b')
			case 'f':
				text.WriteByte('\f')
			case '\n':
				// A backslash at the end of a line joins it with the next.
			case 'u':
				if l.pos+4 <= len(l.source) {
					var r rune
					if _, err := fmt.Sscanf(l.source[l.pos:l.pos+4], "%04x", &r); err == nil {
						text.WriteRune(r)
						l.pos += 4
						continue
					}
				}
				text.WriteString(`\u`)
			default:
				text.WriteByte(escaped)
			}
			continue
		}
		if c == '$' && gstring {
			at := l.pos
			expr, ok, err := l.interpolation()
			if err != nil {
				return err
			}
			if ok {
				flush()
				braced := l.source[at+1] == '{'
				str.parts = append(str.parts, groovyStrPart{expr: expr, interpolated: true, braced: braced, exprStart: at + 2})
				continue
			}
		}
		text.WriteByte(c)
		l.pos++
	}
	flush()
	l.emit(groovyStringToken, start, l.source[start:l.pos], str)
	return nil
}

// interpolation reads ${…} or $name(.name)* at l.pos (a $); ok is false
// for a $ that interpolates nothing.
func (l *groovyLexer) interpolation() (string, bool, error) {
	start := l.pos
	if l.pos+1 >= len(l.source) {
		return "", false, nil
	}
	if l.source[l.pos+1] == '{' {
		end, err := groovyClosingBrace(l.source, l.pos+2)
		if err != nil {
			return "", false, fmt.Errorf("line %d: %w", groovyLine(l.source, start), err)
		}
		l.pos = end + 1
		return l.source[start+2 : end], true, nil
	}
	if !groovyIdentStart(l.source[l.pos+1]) || l.source[l.pos+1] == '$' {
		return "", false, nil
	}
	l.pos++
	for {
		for l.pos < len(l.source) && groovyIdentPart(l.source[l.pos]) && l.source[l.pos] != '$' {
			l.pos++
		}
		// $a.b reads a property; a trailing dot is text.
		if l.pos+1 < len(l.source) && l.source[l.pos] == '.' && groovyIdentStart(l.source[l.pos+1]) && l.source[l.pos+1] != '$' {
			l.pos++
			continue
		}
		break
	}
	return l.source[start+1 : l.pos], true, nil
}

// groovyClosingBrace finds the } that closes a ${ whose body starts at
// from, skipping nested braces and strings.
func groovyClosingBrace(source string, from int) (int, error) {
	depth := 0
	for i := from; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return i, nil
			}
			depth--
		case '\'', '"':
			quote := source[i : i+1]
			if strings.HasPrefix(source[i:], strings.Repeat(quote, 3)) {
				quote = strings.Repeat(quote, 3)
			}
			j := i + len(quote)
			for ; j < len(source) && !strings.HasPrefix(source[j:], quote); j++ {
				if source[j] == '\\' {
					j++
				}
			}
			if j >= len(source) {
				return 0, fmt.Errorf("unterminated string in an interpolation")
			}
			i = j + len(quote) - 1
		}
	}
	return 0, fmt.Errorf("unterminated interpolation")
}

// slashy reads a /…/ string, used for regular expressions.
func (l *groovyLexer) slashy() error {
	start := l.pos
	l.pos++
	var text strings.Builder
	interpolated := false
	for {
		if l.pos >= len(l.source) {
			return fmt.Errorf("line %d: unterminated slashy string", groovyLine(l.source, start))
		}
		c := l.source[l.pos]
		if c == '/' {
			l.pos++
			break
		}
		if c == '\\' && l.pos+1 < len(l.source) && l.source[l.pos+1] == '/' {
			text.WriteByte('/')
			l.pos += 2
			continue
		}
		if c == '$' && l.pos+1 < len(l.source) && (l.source[l.pos+1] == '{' || groovyIdentStart(l.source[l.pos+1])) {
			interpolated = true
		}
		text.WriteByte(c)
		l.pos++
	}
	l.emit(groovyStringToken, start, l.source[start:l.pos], &groovyStr{quote: "/", parts: []groovyStrPart{{text: text.String(), interpolated: interpolated, expr: text.String()}}})
	return nil
}

// dollarSlashy reads a $/…/$ string.
func (l *groovyLexer) dollarSlashy() error {
	start := l.pos
	end := strings.Index(l.source[l.pos+2:], "/$")
	if end < 0 {
		return fmt.Errorf("line %d: unterminated dollar-slashy string", groovyLine(l.source, start))
	}
	body := l.source[l.pos+2 : l.pos+2+end]
	l.pos += end + 4
	interpolated := strings.Contains(body, "$")
	l.emit(groovyStringToken, start, l.source[start:l.pos], &groovyStr{quote: "$/", parts: []groovyStrPart{{text: body, interpolated: interpolated, expr: body}}})
	return nil
}

// groovyNode is a node of the syntax tree; span is its byte range.
type groovyNode interface {
	span() (start, end int)
}

type groovySpan struct{ Start, End int }

func (s groovySpan) span() (int, int) { return s.Start, s.End }

// groovyBlock is a statement list: a file or a closure's body.
type groovyBlock struct {
	groovySpan
	Statements []groovyNode
}

// groovyCall is a method call: name(args) closure, the command form
// name args, or name closure. A call on a dotted name keeps the whole
// name (docker.image); a call on another call's result has a Receiver.
type groovyCall struct {
	groovySpan
	Receiver groovyNode
	Name     string
	Args     []groovyArg
	Closure  *groovyClosure
	// Parens reports name(…); NameStart is where the name starts.
	Parens    bool
	NameStart int
}

// groovyArg is a positional argument (Name empty) or a named one.
type groovyArg struct {
	Name  string
	Value groovyNode
}

type groovyClosure struct {
	groovySpan
	Params []string
	Body   *groovyBlock
}

type groovyString struct {
	groovySpan
	Value *groovyStr
	// Interpolations are the expressions of its ${…} interpolations, which
	// run when the string is evaluated; one ddtest cannot read is a
	// groovyUnknown.
	Interpolations []groovyNode
}

// groovyLiteral is a number, true, false or null.
type groovyLiteral struct {
	groovySpan
	Text string
}

// groovyName is a variable or a property path: env.HOME, params.X.
type groovyName struct {
	groovySpan
	Path []string
}

type groovyList struct {
	groovySpan
	Items []groovyNode
}

type groovyMap struct {
	groovySpan
	Entries []groovyArg
}

// groovyAssign is target = value (or +=, …); Declared marks def or a type.
type groovyAssign struct {
	groovySpan
	Target   groovyNode
	Op       string
	Value    groovyNode
	Declared bool
}

// groovyFunction is a def name(…) { … } method in the script.
type groovyFunction struct {
	groovySpan
	Name string
	Body *groovyBlock
}

// groovyControl is if/else, for, while, switch or try/catch/finally.
// Blocks are, by Kind: if: then[, else]; for and while: the body; switch:
// one block per case, in order; try: the body, then Catches, then Finally.
type groovyControl struct {
	groovySpan
	Kind string
	// Condition is an if's or while's condition, a for loop's iterable (for
	// x in items) or condition (for (init; condition; update)), or a
	// switch's subject.
	Condition groovyNode
	Blocks    []*groovyBlock
	// Labels are a switch's case labels, one per block: nil for default.
	Labels  []groovyNode
	Catches []*groovyBlock
	Finally *groovyBlock
	// Init and Update are a for (init; condition; update) loop's statement
	// and update expression; an Init that is a groovyUnknown is a loop
	// header ddtest cannot read.
	Init, Update groovyNode
	// Variables are the names a for loop's header binds or changes on every
	// iteration.
	Variables []string
}

// groovyJump is break or continue.
type groovyJump struct {
	groovySpan
	Kind string
}

// groovyIndex is receiver[index], such as env['SUITE'].
type groovyIndex struct {
	groovySpan
	Receiver, Index groovyNode
}

// groovyReturn is return [value].
type groovyReturn struct {
	groovySpan
	Value groovyNode
}

// groovyAnnotation is @Name(args), such as @Library('lib') _.
type groovyAnnotation struct {
	groovySpan
	Name string
	Args []groovyArg
}

// groovyBinary is an operator expression.
type groovyBinary struct {
	groovySpan
	Op          string
	Left, Right groovyNode
}

// groovyAssert is assert condition [: message], or throw value: an
// expression that runs, after which the path may fail.
type groovyAssert struct {
	groovySpan
	Kind           string
	Value, Message groovyNode
}

// groovyTernary is condition ? then : otherwise; either branch may run.
type groovyTernary struct {
	groovySpan
	Condition, Then, Else groovyNode
}

// groovyUnknown is syntax ddtest does not interpret.
type groovyUnknown struct {
	groovySpan
}

type groovyParser struct {
	tokens []groovyToken
	pos    int
	depth  int
	source string
	// nested counts the interpolations around the tokens.
	nested int
}

// parseGroovy reads a Jenkinsfile into its syntax tree.
func parseGroovy(source string) (*groovyBlock, error) {
	tokens, err := lexGroovy(source)
	if err != nil {
		return nil, err
	}
	p := &groovyParser{tokens: tokens, source: source}
	block := p.block(false)
	block.Start, block.End = 0, len(source)
	return block, nil
}

func (p *groovyParser) peek() groovyToken { return p.tokens[p.pos] }

// peekAt is the token offset positions ahead, or EOF.
func (p *groovyParser) peekAt(offset int) groovyToken {
	if p.pos+offset < len(p.tokens) {
		return p.tokens[p.pos+offset]
	}
	return p.tokens[len(p.tokens)-1]
}

func (p *groovyParser) advance() groovyToken {
	token := p.tokens[p.pos]
	if token.kind != groovyEOF {
		p.pos++
	}
	return token
}

func (p *groovyParser) is(text string) bool {
	token := p.peek()
	return (token.kind == groovyPunct || token.kind == groovyIdent) && token.text == text
}

func (p *groovyParser) take(text string) bool {
	if p.is(text) {
		p.advance()
		return true
	}
	return false
}

func (p *groovyParser) skipNewlines() {
	for p.peek().kind == groovyNewline || p.is(";") {
		p.advance()
	}
}

// nextSignificant is the first token at or after pos that is not a newline.
func (p *groovyParser) nextSignificant() groovyToken {
	for i := p.pos; i < len(p.tokens); i++ {
		if p.tokens[i].kind != groovyNewline {
			return p.tokens[i]
		}
	}
	return p.tokens[len(p.tokens)-1]
}

// lastEnd is the end of the last token consumed.
func (p *groovyParser) lastEnd() int {
	if p.pos == 0 {
		return 0
	}
	return p.tokens[p.pos-1].end
}

// block reads statements until the closing } (consumed by the caller) or
// the end of the file.
func (p *groovyParser) block(closed bool) *groovyBlock {
	block := &groovyBlock{groovySpan: groovySpan{Start: p.peek().start}}
	for {
		p.skipNewlines()
		token := p.peek()
		if token.kind == groovyEOF || closed && p.is("}") {
			break
		}
		if !closed && p.is("}") {
			// A stray brace: skip it as unknown.
			p.advance()
			block.Statements = append(block.Statements, &groovyUnknown{groovySpan{token.start, token.end}})
			continue
		}
		before := p.pos
		if statement := p.statement(); statement != nil {
			block.Statements = append(block.Statements, statement)
		}
		if p.pos == before {
			// Never loop without progress.
			skipped := p.advance()
			block.Statements = append(block.Statements, &groovyUnknown{groovySpan{skipped.start, skipped.end}})
		}
	}
	block.End = p.peek().start
	return block
}

// recover skips to the end of the statement: a newline, ; or } outside
// brackets.
func (p *groovyParser) recover(start int) groovyNode {
	depth := 0
	for {
		token := p.peek()
		if token.kind == groovyEOF {
			break
		}
		if depth == 0 && (token.kind == groovyNewline || p.is(";") || p.is("}")) {
			break
		}
		if token.kind == groovyPunct {
			switch token.text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
			}
		}
		p.advance()
	}
	return &groovyUnknown{groovySpan{start, max(start, p.lastEnd())}}
}

func (p *groovyParser) statement() groovyNode {
	if p.depth > groovyDepthLimit {
		return p.recover(p.peek().start)
	}
	p.depth++
	defer func() { p.depth-- }()
	token := p.peek()
	start := token.start
	if token.kind == groovyPunct && token.text == "@" {
		return p.annotation()
	}
	if token.kind == groovyIdent {
		switch token.text {
		case "import", "package":
			p.recover(start)
			return nil
		case "def":
			p.advance()
			return p.declaration(start)
		case "if":
			return p.ifStatement()
		case "for":
			return p.forStatement()
		case "while":
			p.advance()
			condition := p.parenthesized()
			body := p.body()
			return &groovyControl{groovySpan: groovySpan{start, p.lastEnd()}, Kind: "while", Condition: condition, Blocks: []*groovyBlock{body}}
		case "switch":
			return p.switchStatement()
		case "case", "default":
			// A switch label: skip to its colon.
			p.advance()
			for !p.is(":") && p.peek().kind != groovyNewline && p.peek().kind != groovyEOF {
				p.advance()
			}
			p.take(":")
			return nil
		case "try":
			return p.tryStatement()
		case "return":
			p.advance()
			control := &groovyReturn{groovySpan: groovySpan{Start: start}}
			if next := p.peek(); next.kind != groovyNewline && next.kind != groovyEOF && !p.is("}") && !p.is(";") {
				control.Value = p.expression()
			}
			control.End = p.lastEnd()
			return control
		case "break", "continue":
			p.recover(start)
			return &groovyJump{groovySpan: groovySpan{start, p.lastEnd()}, Kind: token.text}
		case "throw", "assert":
			// Their expressions run: assert sh(script: …, returnStatus: true) == 0.
			p.advance()
			node := &groovyAssert{groovySpan: groovySpan{Start: start}, Kind: token.text, Value: p.expression()}
			if token.text == "assert" && p.take(":") {
				p.skipNewlines()
				node.Message = p.expression()
			}
			node.End = p.lastEnd()
			return p.finish(node, start)
		case "static", "final", "private", "public", "protected":
			// A modifier before a declaration.
			p.advance()
			if p.is("def") {
				p.advance()
			}
			return p.declaration(start)
		case "synchronized", "class", "interface", "enum":
			return p.recover(start)
		}
		// A typed declaration: String name = …, or void name(…) { … }.
		if next := p.peekAt(1); next.kind == groovyIdent && !slices.Contains([]string{"in", "instanceof", "as"}, next.text) {
			after := p.peekAt(2)
			if after.kind == groovyPunct && (after.text == "=" || after.text == "(" && p.functionAhead(2)) {
				p.advance()
				return p.declaration(start)
			}
		}
	}
	return p.expressionStatement()
}

// functionAhead reports name(…) { at offset: a method definition.
func (p *groovyParser) functionAhead(offset int) bool {
	depth := 0
	for i := p.pos + offset; i < len(p.tokens); i++ {
		token := p.tokens[i]
		if token.kind == groovyEOF {
			return false
		}
		if token.kind == groovyPunct {
			switch token.text {
			case "(":
				depth++
			case ")":
				depth--
				if depth == 0 {
					for j := i + 1; j < len(p.tokens); j++ {
						if p.tokens[j].kind != groovyNewline {
							return p.tokens[j].kind == groovyPunct && p.tokens[j].text == "{"
						}
					}
					return false
				}
			}
		}
	}
	return false
}

// declaration reads what follows def or a type: a method, or a variable
// with an optional value.
func (p *groovyParser) declaration(start int) groovyNode {
	// A type after def: def String x.
	if p.peek().kind == groovyIdent && p.peekAt(1).kind == groovyIdent {
		p.advance()
	}
	name := p.peek()
	if name.kind != groovyIdent {
		return p.recover(start)
	}
	if p.peekAt(1).kind == groovyPunct && p.peekAt(1).text == "(" && p.functionAhead(1) {
		p.advance()
		p.parenthesized()
		p.skipNewlines()
		body := p.body()
		return &groovyFunction{groovySpan: groovySpan{start, p.lastEnd()}, Name: name.text, Body: body}
	}
	p.advance()
	target := &groovyName{groovySpan: groovySpan{name.start, name.end}, Path: []string{name.text}}
	if !p.take("=") {
		return &groovyAssign{groovySpan: groovySpan{start, p.lastEnd()}, Target: target, Op: "=", Declared: true}
	}
	p.skipNewlines()
	value := p.value()
	return p.finish(&groovyAssign{groovySpan: groovySpan{start, p.lastEnd()}, Target: target, Op: "=", Value: value, Declared: true}, start)
}

// value reads an assigned value, which may be a command call: tool 'X'.
func (p *groovyParser) value() groovyNode {
	start := p.peek().start
	expression := p.expression()
	if name, ok := expression.(*groovyName); ok && p.commandArgumentAhead() {
		call := &groovyCall{groovySpan: groovySpan{Start: start}, Name: strings.Join(name.Path, "."), NameStart: name.Start}
		call.Args = p.commandArguments()
		if p.is("{") {
			call.Closure = p.closure()
		}
		call.End = p.lastEnd()
		return call
	}
	return expression
}

// parenthesized reads ( … ) and returns its expression when it is one.
func (p *groovyParser) parenthesized() groovyNode {
	start := p.peek().start
	if !p.take("(") {
		return &groovyUnknown{groovySpan{start, start}}
	}
	p.skipNewlines()
	var node groovyNode
	if !p.is(")") {
		node = p.expression()
	}
	p.skipNewlines()
	if !p.take(")") {
		// for (a; b; c) and anything else: skip to the closing parenthesis.
		depth := 1
		for depth > 0 && p.peek().kind != groovyEOF {
			switch {
			case p.is("("):
				depth++
			case p.is(")"):
				depth--
			}
			p.advance()
		}
		return &groovyUnknown{groovySpan{start, p.lastEnd()}}
	}
	return node
}

// body reads a { … } block, or a single statement.
func (p *groovyParser) body() *groovyBlock {
	p.skipNewlines()
	start := p.peek().start
	if p.take("{") {
		block := p.block(true)
		block.Start = start
		p.take("}")
		block.End = p.lastEnd()
		return block
	}
	block := &groovyBlock{groovySpan: groovySpan{Start: start}}
	if statement := p.statement(); statement != nil {
		block.Statements = []groovyNode{statement}
	}
	block.End = p.lastEnd()
	return block
}

func (p *groovyParser) ifStatement() groovyNode {
	start := p.advance().start
	condition := p.parenthesized()
	control := &groovyControl{groovySpan: groovySpan{Start: start}, Kind: "if", Condition: condition, Blocks: []*groovyBlock{p.body()}}
	if next := p.nextSignificant(); next.kind == groovyIdent && next.text == "else" {
		p.skipNewlines()
		p.advance()
		if p.is("if") {
			nested := p.ifStatement()
			control.Blocks = append(control.Blocks, &groovyBlock{groovySpan: groovySpan{nested.(*groovyControl).Start, p.lastEnd()}, Statements: []groovyNode{nested}})
		} else {
			control.Blocks = append(control.Blocks, p.body())
		}
	}
	control.End = p.lastEnd()
	return control
}

// forStatement reads for (x in items), for (Type x : items) and
// for (init; condition; update) loops. A header it cannot read becomes an
// unknown Init.
func (p *groovyParser) forStatement() groovyNode {
	start := p.advance().start
	control := &groovyControl{groovySpan: groovySpan{Start: start}, Kind: "for"}
	p.skipNewlines()
	open, openStart := p.pos, p.peek().start
	if !p.take("(") {
		control.Init = &groovyUnknown{groovySpan{start, p.lastEnd()}}
	} else if !p.forInHeader(control) && !p.forClassicHeader(control) {
		// Skip to the closing parenthesis; the loop reads an unknown header.
		p.pos = open
		p.parenthesized()
		control.Init, control.Condition, control.Update, control.Variables = &groovyUnknown{groovySpan{openStart, p.lastEnd()}}, nil, nil, nil
	}
	control.Blocks = []*groovyBlock{p.body()}
	control.End = p.lastEnd()
	return control
}

// forInHeader reads x in items) or Type x : items), after the (.
func (p *groovyParser) forInHeader(control *groovyControl) bool {
	before := p.pos
	p.skipNewlines()
	offset := 0
	if p.peek().kind == groovyIdent && p.peekAt(1).kind == groovyIdent && p.peekAt(1).text != "in" {
		offset = 1 // def x, String x
	}
	name, separator := p.peekAt(offset), p.peekAt(offset+1)
	in := separator.kind == groovyIdent && separator.text == "in" || separator.kind == groovyPunct && separator.text == ":"
	if name.kind != groovyIdent || !in {
		p.pos = before
		return false
	}
	p.pos += offset + 2
	p.skipNewlines()
	control.Variables = []string{name.text}
	control.Condition = p.expression()
	p.skipNewlines()
	if !p.take(")") {
		p.pos = before
		control.Variables, control.Condition = nil, nil
		return false
	}
	return true
}

// forClassicHeader reads init; condition; update), after the (.
func (p *groovyParser) forClassicHeader(control *groovyControl) bool {
	before := p.pos
	p.skipNewlines()
	if !p.is(";") {
		control.Init = p.statement()
	}
	if !p.take(";") {
		p.pos = before
		return false
	}
	p.skipNewlines()
	if !p.is(";") {
		control.Condition = p.expression()
	}
	if !p.take(";") {
		p.pos = before
		return false
	}
	p.skipNewlines()
	if !p.is(")") {
		control.Update = p.expression()
	}
	p.skipNewlines()
	if !p.take(")") {
		p.pos = before
		return false
	}
	// The loop's counters: what the init declares or assigns, and what the
	// update changes.
	if assign, ok := control.Init.(*groovyAssign); ok {
		if target, ok := assign.Target.(*groovyName); ok && len(target.Path) == 1 {
			control.Variables = append(control.Variables, target.Path[0])
		}
	}
	if update, ok := control.Update.(*groovyBinary); ok && (update.Op == "++" || update.Op == "--") {
		for _, operand := range []groovyNode{update.Left, update.Right} {
			if target, ok := operand.(*groovyName); ok && len(target.Path) == 1 {
				control.Variables = append(control.Variables, target.Path[0])
			}
		}
	}
	if control.Update != nil && len(control.Variables) == 0 {
		// An update ddtest cannot tell the changes of.
		p.pos = before
		return false
	}
	return true
}

// switchStatement reads switch (subject) { case label: … default: … }:
// one block per label, holding the statements up to the next label.
func (p *groovyParser) switchStatement() groovyNode {
	start := p.advance().start
	control := &groovyControl{groovySpan: groovySpan{Start: start}, Kind: "switch", Condition: p.parenthesized()}
	p.skipNewlines()
	if !p.is("{") {
		// Not a switch body ddtest reads: one case it cannot tell.
		body := p.body()
		control.Blocks, control.Labels = []*groovyBlock{body}, []groovyNode{&groovyUnknown{groovySpan{body.Start, body.Start}}}
		control.End = p.lastEnd()
		return control
	}
	p.advance()
	var current *groovyBlock
	open := func(label groovyNode, at int) {
		if current != nil {
			current.End = at
		}
		current = &groovyBlock{groovySpan: groovySpan{Start: at}}
		control.Blocks = append(control.Blocks, current)
		control.Labels = append(control.Labels, label)
	}
	for {
		p.skipNewlines()
		token := p.peek()
		if token.kind == groovyEOF || p.is("}") {
			break
		}
		if token.kind == groovyIdent && (token.text == "case" || token.text == "default") {
			p.advance()
			var label groovyNode
			if token.text == "case" {
				label = p.expression()
			}
			for !p.is(":") && p.peek().kind != groovyNewline && p.peek().kind != groovyEOF {
				p.advance()
			}
			p.take(":")
			open(label, token.start)
			continue
		}
		if current == nil {
			// Statements before any label: a case ddtest cannot tell.
			open(&groovyUnknown{groovySpan{token.start, token.start}}, token.start)
		}
		before := p.pos
		if statement := p.statement(); statement != nil {
			current.Statements = append(current.Statements, statement)
		}
		if p.pos == before {
			skipped := p.advance()
			current.Statements = append(current.Statements, &groovyUnknown{groovySpan{skipped.start, skipped.end}})
		}
	}
	if current != nil {
		current.End = p.peek().start
	}
	p.take("}")
	control.End = p.lastEnd()
	return control
}

func (p *groovyParser) tryStatement() groovyNode {
	start := p.advance().start
	control := &groovyControl{groovySpan: groovySpan{Start: start}, Kind: "try", Blocks: []*groovyBlock{p.body()}}
	for {
		next := p.nextSignificant()
		if next.kind != groovyIdent || next.text != "catch" && next.text != "finally" {
			break
		}
		p.skipNewlines()
		p.advance()
		if next.text == "catch" {
			p.parenthesized()
			control.Catches = append(control.Catches, p.body())
			continue
		}
		control.Finally = p.body()
		break
	}
	control.End = p.lastEnd()
	return control
}

func (p *groovyParser) annotation() groovyNode {
	start := p.advance().start
	name := ""
	for p.peek().kind == groovyIdent {
		name += p.advance().text
		if !p.take(".") {
			break
		}
		name += "."
	}
	annotation := &groovyAnnotation{groovySpan: groovySpan{Start: start}, Name: name}
	if p.is("(") && !p.peek().spaced {
		annotation.Args = p.arguments()
	}
	// @Library('x') _ applies to the placeholder _.
	if next := p.peek(); next.kind == groovyIdent && next.text == "_" {
		p.advance()
	}
	annotation.End = p.lastEnd()
	return annotation
}

// assignmentOperators are =, +=, …
var groovyAssignmentOperators = []string{"=", "+=", "-=", "*=", "/=", "%="}

func (p *groovyParser) expressionStatement() groovyNode {
	start, before := p.peek().start, p.pos
	expression := p.expression()
	if p.pos == before {
		return p.recover(start)
	}
	if token := p.peek(); token.kind == groovyPunct && slices.Contains(groovyAssignmentOperators, token.text) {
		p.advance()
		p.skipNewlines()
		value := p.value()
		return p.finish(&groovyAssign{groovySpan: groovySpan{start, p.lastEnd()}, Target: expression, Op: token.text, Value: value}, start)
	}
	// The command form: name args, as in sh 'x' or agent any.
	if p.commandArgumentAhead() {
		var callee *groovyCall
		switch node := expression.(type) {
		case *groovyName:
			callee = &groovyCall{groovySpan: groovySpan{Start: start}, Name: strings.Join(node.Path, "."), NameStart: node.Start}
		case *groovyCall:
			if node.Closure == nil {
				// a.b(x) y: a command chain; keep the first call.
				callee = &groovyCall{groovySpan: groovySpan{Start: start}, Receiver: node, NameStart: node.Start}
			}
		}
		if callee != nil {
			callee.Args = p.commandArguments()
			if p.is("{") {
				callee.Closure = p.closure()
			}
			callee.End = p.lastEnd()
			return p.finish(callee, start)
		}
	}
	// A bare name is a call without arguments only in a few step forms, such
	// as checkout scm's scm; the interpreter decides.
	return p.finish(expression, start)
}

// finish makes anything left on the statement's line part of an unknown
// node, so trailing syntax is never dropped silently.
func (p *groovyParser) finish(node groovyNode, start int) groovyNode {
	token := p.peek()
	if token.kind == groovyNewline || token.kind == groovyEOF || p.is(";") || p.is("}") {
		return node
	}
	p.recover(start)
	return &groovyUnknown{groovySpan{start, p.lastEnd()}}
}

// commandArgumentAhead reports a token on the same line that starts a
// command argument.
func (p *groovyParser) commandArgumentAhead() bool {
	token := p.peek()
	switch token.kind {
	case groovyStringToken, groovyNumber:
		return true
	case groovyIdent:
		return !slices.Contains([]string{"in", "instanceof", "as", "else", "catch", "finally"}, token.text)
	case groovyPunct:
		// [ after a space starts a list argument; without one it indexes.
		return token.text == "[" && token.spaced || token.text == "-" && token.spaced && p.peekAt(1).kind == groovyNumber && !p.peekAt(1).spaced
	}
	return false
}

// commandArguments reads the comma-separated arguments of a command call.
func (p *groovyParser) commandArguments() []groovyArg {
	var args []groovyArg
	for {
		args = append(args, p.argument())
		if !p.take(",") {
			break
		}
		p.skipNewlines()
	}
	return args
}

// argument reads one argument: name: value, or a value.
func (p *groovyParser) argument() groovyArg {
	token := p.peek()
	if (token.kind == groovyIdent || token.kind == groovyStringToken) && p.peekAt(1).kind == groovyPunct && p.peekAt(1).text == ":" {
		name := token.text
		if token.kind == groovyStringToken {
			// An interpolated key keeps its source text.
			if literal, ok := token.str.literal(); ok {
				name = literal
			}
		}
		p.advance()
		p.advance()
		p.skipNewlines()
		return groovyArg{Name: name, Value: p.expression()}
	}
	return groovyArg{Value: p.expression()}
}

// arguments reads ( args ) after a name, allowing newlines inside.
func (p *groovyParser) arguments() []groovyArg {
	p.advance()
	var args []groovyArg
	p.skipNewlines()
	for !p.is(")") && p.peek().kind != groovyEOF {
		before := p.pos
		args = append(args, p.argument())
		p.skipNewlines()
		if !p.take(",") {
			if p.pos == before {
				p.advance()
			}
			if !p.is(")") {
				// Something ddtest cannot read: keep it as unknown.
				start := p.peek().start
				p.skipTo(",", ")")
				args = append(args, groovyArg{Value: &groovyUnknown{groovySpan{start, p.lastEnd()}}})
				p.take(",")
			}
		}
		p.skipNewlines()
	}
	p.take(")")
	return args
}

// skipTo skips tokens, with any brackets they open, up to one of stops
// outside brackets.
func (p *groovyParser) skipTo(stops ...string) {
	depth := 0
	for p.peek().kind != groovyEOF {
		token := p.peek()
		if depth == 0 && token.kind == groovyPunct && slices.Contains(stops, token.text) {
			return
		}
		if token.kind == groovyPunct {
			switch token.text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				if depth == 0 {
					return
				}
				depth--
			}
		}
		p.advance()
	}
}

// binaryOperators continue an expression.
var groovyBinaryOperators = []string{"+", "-", "*", "/", "%", "**", "==", "!=", "<", ">", "<=", ">=", "<=>", "&&", "||", "&", "|", "^", "<<", ">>", "=~", "==~", "..", "..<", "?:", "in", "instanceof", "as"}

func (p *groovyParser) expression() groovyNode {
	if p.depth > groovyDepthLimit {
		start := p.peek().start
		p.recover(start)
		return &groovyUnknown{groovySpan{start, p.lastEnd()}}
	}
	p.depth++
	defer func() { p.depth-- }()
	start := p.peek().start
	left := p.unary()
	for {
		token := p.peek()
		if (token.kind == groovyPunct || token.kind == groovyIdent) && slices.Contains(groovyBinaryOperators, token.text) {
			p.advance()
			p.skipNewlines()
			right := p.unary()
			left = &groovyBinary{groovySpan: groovySpan{start, p.lastEnd()}, Op: token.text, Left: left, Right: right}
			continue
		}
		if p.is("?") {
			// a ? b : c
			p.advance()
			p.skipNewlines()
			then := p.expression()
			p.skipNewlines()
			if !p.take(":") {
				return &groovyUnknown{groovySpan{start, p.lastEnd()}}
			}
			p.skipNewlines()
			otherwise := p.expression()
			return &groovyTernary{groovySpan: groovySpan{start, p.lastEnd()}, Condition: left, Then: then, Else: otherwise}
		}
		return left
	}
}

func (p *groovyParser) unary() groovyNode {
	start := p.peek().start
	if token := p.peek(); token.kind == groovyPunct && slices.Contains([]string{"!", "-", "+", "~", "++", "--"}, token.text) {
		p.advance()
		operand := p.unary()
		return &groovyBinary{groovySpan: groovySpan{start, p.lastEnd()}, Op: token.text, Right: operand}
	}
	return p.postfix(p.primary())
}

func (p *groovyParser) primary() groovyNode {
	token := p.peek()
	switch token.kind {
	case groovyStringToken:
		p.advance()
		return &groovyString{groovySpan: groovySpan{token.start, token.end}, Value: token.str, Interpolations: p.interpolations(token.str)}
	case groovyNumber:
		p.advance()
		return &groovyLiteral{groovySpan: groovySpan{token.start, token.end}, Text: token.text}
	case groovyIdent:
		switch token.text {
		case "true", "false", "null":
			p.advance()
			return &groovyLiteral{groovySpan: groovySpan{token.start, token.end}, Text: token.text}
		case "new":
			p.advance()
			for p.peek().kind == groovyIdent || p.is(".") {
				p.advance()
			}
			if p.is("(") {
				p.arguments()
			}
			return &groovyUnknown{groovySpan{token.start, p.lastEnd()}}
		}
		p.advance()
		return &groovyName{groovySpan: groovySpan{token.start, token.end}, Path: []string{token.text}}
	case groovyPunct:
		switch token.text {
		case "(":
			node := p.parenthesized()
			if node == nil {
				return &groovyUnknown{groovySpan{token.start, p.lastEnd()}}
			}
			return &groovyBinary{groovySpan: groovySpan{token.start, p.lastEnd()}, Op: "()", Right: node}
		case "[":
			return p.collection()
		case "{":
			return p.closure()
		}
	}
	return &groovyUnknown{groovySpan{token.start, token.start}}
}

// interpolations parses the expressions of a GString's ${…} interpolations.
func (p *groovyParser) interpolations(str *groovyStr) []groovyNode {
	var nodes []groovyNode
	for _, part := range str.parts {
		if !part.braced || strings.TrimSpace(part.expr) == "" {
			continue
		}
		start, end := part.exprStart, part.exprStart+len(part.expr)
		unknown := &groovyUnknown{groovySpan{start, end}}
		if p.depth > groovyDepthLimit || p.nested >= groovyInterpolationLimit {
			nodes = append(nodes, unknown)
			continue
		}
		tokens, err := lexGroovy(part.expr)
		if err != nil {
			nodes = append(nodes, unknown)
			continue
		}
		for i := range tokens {
			tokens[i].start += start
			tokens[i].end += start
			if tokens[i].str != nil {
				for j := range tokens[i].str.parts {
					tokens[i].str.parts[j].exprStart += start
				}
			}
		}
		inner := &groovyParser{tokens: tokens, source: p.source, depth: p.depth + 1, nested: p.nested + 1}
		inner.skipNewlines()
		node := inner.expression()
		inner.skipNewlines()
		if inner.peek().kind != groovyEOF {
			// More than one expression, such as ${a; b}.
			node = unknown
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// postfix reads property access, calls, indexes and closure arguments.
func (p *groovyParser) postfix(node groovyNode) groovyNode {
	start, _ := node.span()
	for {
		token := p.peek()
		// A chain may continue on the next line: .method().
		if token.kind == groovyNewline {
			next := p.nextSignificant()
			if next.kind == groovyPunct && (next.text == "." || next.text == "?.") {
				p.skipNewlines()
				continue
			}
			// An Allman-style closure: stage('x') on one line, { on the next.
			call, isCall := node.(*groovyCall)
			if _, isName := node.(*groovyName); next.kind == groovyPunct && next.text == "{" && (isName || isCall && call.Parens && call.Closure == nil) {
				p.skipNewlines()
				continue
			}
			return node
		}
		switch {
		case token.kind == groovyPunct && (token.text == "." || token.text == "?." || token.text == "*." || token.text == ".&" || token.text == ".@"):
			p.advance()
			p.skipNewlines()
			member := p.peek()
			if member.kind != groovyIdent && member.kind != groovyStringToken {
				return &groovyUnknown{groovySpan{start, p.lastEnd()}}
			}
			p.advance()
			memberName := member.text
			if member.kind == groovyStringToken {
				memberName, _ = member.str.literal()
			}
			if name, ok := node.(*groovyName); ok && token.text == "." {
				name.Path = append(name.Path, memberName)
				name.End = member.end
				continue
			}
			node = &groovyCall{groovySpan: groovySpan{start, member.end}, Receiver: node, Name: memberName, NameStart: member.start}
			if token.text != "." {
				node = &groovyUnknown{groovySpan{start, member.end}}
			}
		case token.kind == groovyPunct && token.text == "(":
			args := p.arguments()
			call := &groovyCall{groovySpan: groovySpan{start, p.lastEnd()}, Args: args, Parens: true}
			switch callee := node.(type) {
			case *groovyName:
				call.Name, call.NameStart = strings.Join(callee.Path, "."), callee.Start
			case *groovyCall:
				if callee.Parens || callee.Closure != nil || callee.Receiver == nil {
					// f(a)(b): calling a result.
					return &groovyUnknown{groovySpan{start, p.lastEnd()}}
				}
				call.Receiver, call.Name, call.NameStart = callee.Receiver, callee.Name, callee.NameStart
			default:
				return &groovyUnknown{groovySpan{start, p.lastEnd()}}
			}
			if p.is("{") {
				call.Closure = p.closure()
				call.End = p.lastEnd()
			}
			node = call
		case token.kind == groovyPunct && token.text == "{":
			// name { … } passes a closure: pipeline { … }, steps { … }.
			call := &groovyCall{groovySpan: groovySpan{Start: start}}
			switch callee := node.(type) {
			case *groovyName:
				call.Name, call.NameStart = strings.Join(callee.Path, "."), callee.Start
			case *groovyCall:
				if callee.Closure != nil {
					return node
				}
				call = callee
			default:
				return node
			}
			call.Closure = p.closure()
			call.End = p.lastEnd()
			node = call
		case token.kind == groovyPunct && token.text == "[" && !token.spaced:
			list, isList := p.collection().(*groovyList)
			if !isList || len(list.Items) != 1 {
				node = &groovyUnknown{groovySpan{start, p.lastEnd()}}
				continue
			}
			node = &groovyIndex{groovySpan: groovySpan{start, p.lastEnd()}, Receiver: node, Index: list.Items[0]}
		case token.kind == groovyPunct && (token.text == "++" || token.text == "--"):
			p.advance()
			node = &groovyBinary{groovySpan: groovySpan{start, p.lastEnd()}, Op: token.text, Left: node}
		default:
			return node
		}
	}
}

// collection reads a list [a, b] or a map [k: v] ([:] is empty).
func (p *groovyParser) collection() groovyNode {
	start := p.advance().start
	p.skipNewlines()
	if p.take("]") {
		return &groovyList{groovySpan: groovySpan{start, p.lastEnd()}}
	}
	if p.is(":") && p.peekAt(1).kind == groovyPunct && p.peekAt(1).text == "]" {
		p.advance()
		p.advance()
		return &groovyMap{groovySpan: groovySpan{start, p.lastEnd()}}
	}
	var items []groovyArg
	isMap := false
	for !p.is("]") && p.peek().kind != groovyEOF {
		before := p.pos
		if p.is("(") {
			// A computed key: (expr): value.
			p.parenthesized()
			if p.take(":") {
				p.skipNewlines()
				p.expression()
				items = append(items, groovyArg{Name: "\x00", Value: &groovyUnknown{groovySpan{start, p.lastEnd()}}})
				isMap = true
			}
		} else {
			item := p.argument()
			isMap = isMap || item.Name != ""
			items = append(items, item)
		}
		p.skipNewlines()
		if !p.take(",") && !p.is("]") {
			if p.pos == before {
				p.advance()
			}
			p.skipTo(",", "]")
			p.take(",")
		}
		p.skipNewlines()
	}
	p.take("]")
	if isMap {
		return &groovyMap{groovySpan: groovySpan{start, p.lastEnd()}, Entries: items}
	}
	list := &groovyList{groovySpan: groovySpan{start, p.lastEnd()}}
	for _, item := range items {
		list.Items = append(list.Items, item.Value)
	}
	return list
}

// closure reads { params -> statements }.
func (p *groovyParser) closure() *groovyClosure {
	start := p.advance().start
	closure := &groovyClosure{groovySpan: groovySpan{Start: start}}
	// Parameters: idents (with optional types) and commas before ->.
	for i := p.pos; i < len(p.tokens); i++ {
		token := p.tokens[i]
		if token.kind == groovyNewline && i == p.pos {
			continue
		}
		if token.kind == groovyPunct && token.text == "->" {
			for j := p.pos; j < i; j++ {
				if p.tokens[j].kind == groovyIdent && (j+1 == i || p.tokens[j+1].kind == groovyPunct) {
					closure.Params = append(closure.Params, p.tokens[j].text)
				}
			}
			p.pos = i + 1
			break
		}
		if token.kind != groovyIdent && (token.kind != groovyPunct || token.text != ",") && token.kind != groovyNewline {
			break
		}
	}
	closure.Body = p.block(true)
	p.take("}")
	closure.End = p.lastEnd()
	closure.Body.End = closure.End
	return closure
}

// groovyText is a node's source text.
func groovyText(source string, node groovyNode) string {
	start, end := node.span()
	if start < 0 || end > len(source) || start > end {
		return ""
	}
	return source[start:end]
}
