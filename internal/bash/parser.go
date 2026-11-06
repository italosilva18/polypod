package bash

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TokenType identifies the type of a lexer token.
type TokenType int

const (
	TokWord         TokenType = iota // Regular word
	TokNumber                        // Integer literal
	TokOp                            // Operator (|, &&, ||, ;, etc.)
	TokNewline                       // \n
	TokComment                       // # comment
	TokDQuote                        // "
	TokSQuote                        // '...'
	TokAnsiC                         // $'...'
	TokDollar                        // $
	TokDollarParen                   // $(
	TokDollarBrace                   // ${
	TokDollarDParen                  // $((
	TokBacktick                      // `
	TokLTParen                       // <(
	TokGTParen                       // >(
	TokEOF                           // End of input
	TokHeredocBody                   // Content of a heredoc
)

// Token is a lexer token.
type Token struct {
	Type  TokenType
	Value string
	Start int // Byte offset
	End   int // Byte offset
}

// Lexer tokenizes bash input.
type Lexer struct {
	src      string
	pos      int // Current byte position
	tokens   []Token
	heredocs []heredocPending
}

type heredocPending struct {
	delim     string
	stripTabs bool
	quoted    bool
}

// NewLexer creates a new lexer for the given source.
func NewLexer(src string) *Lexer {
	return &Lexer{src: src}
}

func (l *Lexer) remaining() string {
	if l.pos >= len(l.src) {
		return ""
	}
	return l.src[l.pos:]
}

func (l *Lexer) peek() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *Lexer) peekAt(offset int) byte {
	p := l.pos + offset
	if p >= len(l.src) || p < 0 {
		return 0
	}
	return l.src[p]
}

func (l *Lexer) advance() {
	if l.pos < len(l.src) {
		l.pos++
	}
}

func (l *Lexer) skipBlanks() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == ' ' || c == '\t' || c == '\r' {
			l.advance()
		} else if c == '\\' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '\n' {
			// Line continuation
			l.advance()
			l.advance()
		} else {
			break
		}
	}
}

func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '_' || c == '/' || c == '.' ||
		c == '-' || c == '+' || c == ':' || c == '@' || c == '%' ||
		c == ',' || c == '~' || c == '^' || c == '?' || c == '*' ||
		c == '!' || c == '=' || c == '[' || c == ']'
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// NextToken scans the next token. Context-sensitive: cmd position treats
// [ [[ { as operators; arg position treats them as word chars.
func (l *Lexer) NextToken(ctx string) Token {
	l.skipBlanks()
	start := l.pos
	if l.pos >= len(l.src) {
		return Token{Type: TokEOF, Value: "", Start: start, End: start}
	}

	c := l.src[l.pos]
	c1 := l.peekAt(1)
	c2 := l.peekAt(2)

	// Newline
	if c == '\n' {
		l.advance()
		return Token{Type: TokNewline, Value: "\n", Start: start, End: l.pos}
	}

	// Comment
	if c == '#' {
		si := l.pos
		for l.pos < len(l.src) && l.src[l.pos] != '\n' {
			l.advance()
		}
		return Token{Type: TokComment, Value: l.src[si:l.pos], Start: start, End: l.pos}
	}

	// Multi-char operators (longest match first)
	if c == '&' && c1 == '&' {
		l.pos += 2
		return Token{Type: TokOp, Value: "&&", Start: start, End: l.pos}
	}
	if c == '|' && c1 == '|' {
		l.pos += 2
		return Token{Type: TokOp, Value: "||", Start: start, End: l.pos}
	}
	if c == '|' && c1 == '&' {
		l.pos += 2
		return Token{Type: TokOp, Value: "|&", Start: start, End: l.pos}
	}
	if c == ';' && c1 == ';' && c2 == '&' {
		l.pos += 3
		return Token{Type: TokOp, Value: ";;&", Start: start, End: l.pos}
	}
	if c == ';' && c1 == ';' {
		l.pos += 2
		return Token{Type: TokOp, Value: ";;", Start: start, End: l.pos}
	}
	if c == ';' && c1 == '&' {
		l.pos += 2
		return Token{Type: TokOp, Value: ";&", Start: start, End: l.pos}
	}
	if c == '>' && c1 == '>' {
		l.pos += 2
		return Token{Type: TokOp, Value: ">>", Start: start, End: l.pos}
	}
	if c == '>' && c1 == '&' {
		l.pos += 2
		return Token{Type: TokOp, Value: ">&", Start: start, End: l.pos}
	}
	if c == '>' && c1 == '|' {
		l.pos += 2
		return Token{Type: TokOp, Value: ">|", Start: start, End: l.pos}
	}
	if c == '&' && c1 == '>' && c2 == '>' {
		l.pos += 3
		return Token{Type: TokOp, Value: "&>>", Start: start, End: l.pos}
	}
	if c == '&' && c1 == '>' {
		l.pos += 2
		return Token{Type: TokOp, Value: "&>", Start: start, End: l.pos}
	}
	if c == '<' && c1 == '<' && c2 == '<' {
		l.pos += 3
		return Token{Type: TokOp, Value: "<<<", Start: start, End: l.pos}
	}
	if c == '<' && c1 == '<' && c2 == '-' {
		l.pos += 3
		return Token{Type: TokOp, Value: "<<-", Start: start, End: l.pos}
	}
	if c == '<' && c1 == '<' {
		l.pos += 2
		return Token{Type: TokOp, Value: "<<", Start: start, End: l.pos}
	}
	if c == '<' && c1 == '&' {
		l.pos += 2
		return Token{Type: TokOp, Value: "<&", Start: start, End: l.pos}
	}
	if c == '<' && c1 == '(' {
		l.pos += 2
		return Token{Type: TokLTParen, Value: "<(", Start: start, End: l.pos}
	}
	if c == '>' && c1 == '(' {
		l.pos += 2
		return Token{Type: TokGTParen, Value: ">(", Start: start, End: l.pos}
	}
	if c == '(' && c1 == '(' {
		l.pos += 2
		return Token{Type: TokOp, Value: "((", Start: start, End: l.pos}
	}
	if c == ')' && c1 == ')' {
		l.pos += 2
		return Token{Type: TokOp, Value: "))", Start: start, End: l.pos}
	}

	// Single-char operators
	if c == '|' || c == '&' || c == ';' || c == '>' || c == '<' {
		l.advance()
		return Token{Type: TokOp, Value: string(c), Start: start, End: l.pos}
	}
	if c == '(' || c == ')' {
		l.advance()
		return Token{Type: TokOp, Value: string(c), Start: start, End: l.pos}
	}

	// Cmd-position operators
	if ctx == "cmd" {
		if c == '[' && c1 == '[' {
			l.pos += 2
			return Token{Type: TokOp, Value: "[[", Start: start, End: l.pos}
		}
		if c == '[' {
			l.advance()
			return Token{Type: TokOp, Value: "[", Start: start, End: l.pos}
		}
		if c == '{' && (c1 == ' ' || c1 == '\t' || c1 == '\n') {
			l.advance()
			return Token{Type: TokOp, Value: "{", Start: start, End: l.pos}
		}
		if c == '}' {
			l.advance()
			return Token{Type: TokOp, Value: "}", Start: start, End: l.pos}
		}
		if c == '!' && (c1 == ' ' || c1 == '\t') {
			l.advance()
			return Token{Type: TokOp, Value: "!", Start: start, End: l.pos}
		}
	}

	// Double quote
	if c == '"' {
		l.advance()
		return Token{Type: TokDQuote, Value: "\"", Start: start, End: l.pos}
	}

	// Single quote — scan to closing '
	if c == '\'' {
		si := l.pos
		l.advance()
		for l.pos < len(l.src) && l.src[l.pos] != '\'' {
			l.advance()
		}
		if l.pos < len(l.src) {
			l.advance()
		}
		return Token{Type: TokSQuote, Value: l.src[si:l.pos], Start: start, End: l.pos}
	}

	// Dollar variants
	if c == '$' {
		if c1 == '(' && c2 == '(' {
			l.pos += 3
			return Token{Type: TokDollarDParen, Value: "$((", Start: start, End: l.pos}
		}
		if c1 == '(' {
			l.pos += 2
			return Token{Type: TokDollarParen, Value: "$(", Start: start, End: l.pos}
		}
		if c1 == '{' {
			l.pos += 2
			return Token{Type: TokDollarBrace, Value: "${", Start: start, End: l.pos}
		}
		if c1 == '\'' {
			// ANSI-C string $'...'
			si := l.pos
			l.pos += 2
			for l.pos < len(l.src) && l.src[l.pos] != '\'' {
				if l.src[l.pos] == '\\' && l.pos+1 < len(l.src) {
					l.advance()
				}
				l.advance()
			}
			if l.pos < len(l.src) {
				l.advance()
			}
			return Token{Type: TokAnsiC, Value: l.src[si:l.pos], Start: start, End: l.pos}
		}
		l.advance()
		return Token{Type: TokDollar, Value: "$", Start: start, End: l.pos}
	}

	// Backtick
	if c == '`' {
		l.advance()
		return Token{Type: TokBacktick, Value: "`", Start: start, End: l.pos}
	}

	// File descriptor before redirect
	if isDigit(c) {
		j := l.pos
		for j < len(l.src) && isDigit(l.src[j]) {
			j++
		}
		if j < len(l.src) && (l.src[j] == '>' || l.src[j] == '<') {
			val := l.src[l.pos:j]
			l.pos = j
			return Token{Type: TokWord, Value: val, Start: start, End: l.pos}
		}
	}

	// Word / number
	if isWordChar(c) || c == '\\' || c == '{' || c == '}' {
		si := l.pos
		for l.pos < len(l.src) {
			ch := l.src[l.pos]
			if ch == '\\' {
				if l.pos+1 >= len(l.src) {
					break
				}
				if l.src[l.pos+1] == '\n' {
					l.pos += 2
					continue
				}
				l.pos += 2
				continue
			}
			if !isWordChar(ch) && ch != '{' && ch != '}' {
				break
			}
			l.advance()
		}
		if l.pos > si {
			v := l.src[si:l.pos]
			if isNumberLiteral(v) {
				return Token{Type: TokNumber, Value: v, Start: start, End: l.pos}
			}
			return Token{Type: TokWord, Value: v, Start: start, End: l.pos}
		}
	}

	// Unknown char — consume as single-char word
	_, size := utf8.DecodeRuneInString(l.src[l.pos:])
	val := l.src[l.pos : l.pos+size]
	l.pos += size
	return Token{Type: TokWord, Value: val, Start: start, End: l.pos}
}

func isNumberLiteral(s string) bool {
	if len(s) == 0 {
		return false
	}
	start := 0
	if s[0] == '-' {
		start = 1
	}
	if start >= len(s) {
		return false
	}
	for i := start; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// Parser builds an AST from tokens.
type Parser struct {
	lexer     *Lexer
	src       string
	maxNodes  int
	nodeCount int
}

const (
	defaultMaxNodes = 50000
	maxCommandLen   = 10000
)

// Parse parses a bash command string and returns the AST root.
// Returns nil if parsing fails or input is empty/too long.
func Parse(source string) *Node {
	if len(source) == 0 || len(source) > maxCommandLen {
		return nil
	}
	p := &Parser{
		lexer:    NewLexer(source),
		src:      source,
		maxNodes: defaultMaxNodes,
	}
	return p.parseProgram()
}

func (p *Parser) checkBudget() bool {
	p.nodeCount++
	return p.nodeCount <= p.maxNodes
}

func (p *Parser) mk(typ NodeType, start, end int) *Node {
	if !p.checkBudget() {
		return nil
	}
	text := ""
	if start >= 0 && end <= len(p.src) && start <= end {
		text = p.src[start:end]
	}
	return &Node{
		Type:  typ,
		Text:  text,
		Start: start,
		End:   end,
		FD:    -1,
	}
}

func (p *Parser) save() int {
	return p.lexer.pos
}

func (p *Parser) restore(pos int) {
	p.lexer.pos = pos
}

func (p *Parser) parseProgram() *Node {
	p.lexer.skipBlanks()
	// Skip leading newlines
	for {
		save := p.save()
		t := p.lexer.NextToken("cmd")
		if t.Type == TokNewline {
			continue
		}
		p.restore(save)
		break
	}

	progStart := p.lexer.pos
	children := make([]*Node, 0, 8)

	for p.lexer.pos < len(p.src) {
		save := p.save()
		t := p.lexer.NextToken("cmd")
		if t.Type == TokEOF {
			break
		}
		if t.Type == TokNewline {
			continue
		}
		if t.Type == TokComment {
			node := p.mk(NodeComment, t.Start, t.End)
			if node != nil {
				node.Text = t.Value
				children = append(children, node)
			}
			continue
		}
		p.restore(save)
		stmts := p.parseStatements("")
		children = append(children, stmts...)
		if len(stmts) == 0 {
			// Couldn't parse — skip one token
			t2 := p.lexer.NextToken("cmd")
			if t2.Type == TokEOF {
				break
			}
		}
	}

	progEnd := p.lexer.pos
	if len(children) > 0 {
		progEnd = children[len(children)-1].End
	}
	prog := p.mk(NodeProgram, progStart, progEnd)
	if prog == nil {
		return nil
	}
	prog.Children = children
	return prog
}

// parseStatements parses a sequence of statements separated by ; & \n.
// Stops at terminator or EOF.
func (p *Parser) parseStatements(terminator string) []*Node {
	out := make([]*Node, 0, 4)
	for {
		p.lexer.skipBlanks()
		save := p.save()
		t := p.lexer.NextToken("cmd")
		if t.Type == TokEOF {
			p.restore(save)
			break
		}
		if t.Type == TokNewline {
			continue
		}
		if t.Type == TokComment {
			node := p.mk(NodeComment, t.Start, t.End)
			if node != nil {
				node.Text = t.Value
				out = append(out, node)
			}
			continue
		}
		if terminator != "" && t.Type == TokOp && t.Value == terminator {
			p.restore(save)
			break
		}
		// Stop at structural terminators
		if t.Type == TokOp && (t.Value == ")" || t.Value == "}" ||
			t.Value == ";;" || t.Value == ";&" || t.Value == ";;&" ||
			t.Value == "))" || t.Value == "]]" || t.Value == "]") {
			p.restore(save)
			break
		}
		if t.Type == TokWord && (t.Value == "then" || t.Value == "elif" ||
			t.Value == "else" || t.Value == "fi" || t.Value == "do" ||
			t.Value == "done" || t.Value == "esac") {
			p.restore(save)
			break
		}
		p.restore(save)

		stmt := p.parseAndOr()
		if stmt == nil {
			break
		}
		out = append(out, stmt)

		// Look for separator
		p.lexer.skipBlanks()
		save2 := p.save()
		sep := p.lexer.NextToken("cmd")
		if sep.Type == TokOp && (sep.Value == ";" || sep.Value == "&") {
			// Separator consumed, continue
			continue
		} else if sep.Type == TokNewline {
			continue
		} else {
			p.restore(save2)
		}
	}
	return out
}

// parseAndOr parses pipeline chains joined by && ||.
func (p *Parser) parseAndOr() *Node {
	left := p.parsePipeline()
	if left == nil {
		return nil
	}
	for {
		save := p.save()
		t := p.lexer.NextToken("cmd")
		if t.Type == TokOp && (t.Value == "&&" || t.Value == "||") {
			p.skipNewlines()
			right := p.parsePipeline()
			if right == nil {
				break
			}
			op := OpAnd
			if t.Value == "||" {
				op = OpOr
			}
			node := p.mk(NodeAndOr, left.Start, right.End)
			if node == nil {
				return left
			}
			node.Operator = op
			node.Children = []*Node{left, right}
			left = node
		} else {
			p.restore(save)
			break
		}
	}
	return left
}

func (p *Parser) skipNewlines() {
	for {
		save := p.save()
		t := p.lexer.NextToken("cmd")
		if t.Type != TokNewline {
			p.restore(save)
			break
		}
	}
}

// parsePipeline parses commands joined by | or |&.
func (p *Parser) parsePipeline() *Node {
	first := p.parseCommand()
	if first == nil {
		return nil
	}
	parts := []*Node{first}
	pipePos := 0
	for {
		save := p.save()
		t := p.lexer.NextToken("cmd")
		if t.Type == TokOp && (t.Value == "|" || t.Value == "|&") {
			p.skipNewlines()
			pipePos++
			next := p.parseCommand()
			if next == nil {
				break
			}
			next.PipelinePos = pipePos
			parts = append(parts, next)
		} else {
			p.restore(save)
			break
		}
	}
	if len(parts) == 1 {
		return parts[0]
	}
	node := p.mk(NodePipeline, parts[0].Start, parts[len(parts)-1].End)
	if node == nil {
		return first
	}
	node.Children = parts
	node.Operator = OpPipe
	return node
}

// parseCommand parses a single command: simple, compound, or control flow.
func (p *Parser) parseCommand() *Node {
	p.lexer.skipBlanks()
	save := p.save()
	t := p.lexer.NextToken("cmd")
	if t.Type == TokEOF {
		p.restore(save)
		return nil
	}

	// Negation
	if t.Type == TokOp && t.Value == "!" {
		inner := p.parseCommand()
		if inner == nil {
			p.restore(save)
			return nil
		}
		node := p.mk(NodeNegation, t.Start, inner.End)
		if node != nil {
			node.Children = []*Node{inner}
		}
		return node
	}

	// Subshell
	if t.Type == TokOp && t.Value == "(" {
		body := p.parseStatements(")")
		closeTok := p.lexer.NextToken("cmd")
		endPos := t.End
		if closeTok.Type == TokOp && closeTok.Value == ")" {
			endPos = closeTok.End
		}
		node := p.mk(NodeSubshell, t.Start, endPos)
		if node != nil {
			node.Body = body
		}
		return node
	}

	// Group command
	if t.Type == TokOp && t.Value == "{" {
		body := p.parseStatements("}")
		closeTok := p.lexer.NextToken("cmd")
		endPos := t.End
		if closeTok.Type == TokOp && closeTok.Value == "}" {
			endPos = closeTok.End
		}
		node := p.mk(NodeGroup, t.Start, endPos)
		if node != nil {
			node.Body = body
		}
		return node
	}

	// Test command
	if t.Type == TokOp && (t.Value == "[" || t.Value == "[[") {
		closer := "]"
		if t.Value == "[[" {
			closer = "]]"
		}
		args := p.parseTestArgs(closer)
		endPos := t.End
		closeSave := p.save()
		closeTok := p.lexer.NextToken("arg")
		if closeTok.Value == closer {
			endPos = closeTok.End
		} else {
			p.restore(closeSave)
		}
		node := p.mk(NodeTestCommand, t.Start, endPos)
		if node != nil {
			node.Args = args
			node.Name = t.Value
		}
		return node
	}

	// Keywords
	if t.Type == TokWord {
		switch t.Value {
		case "if":
			return p.parseIf(t)
		case "while", "until":
			return p.parseWhileUntil(t)
		case "for":
			return p.parseFor(t)
		case "case":
			return p.parseCase(t)
		case "function":
			return p.parseFunction(t)
		case "export", "declare", "typeset", "readonly", "local":
			return p.parseDeclaration(t)
		case "unset", "unsetenv":
			return p.parseUnset(t)
		}
	}

	p.restore(save)
	return p.parseSimpleCommand()
}

// parseSimpleCommand parses: [assignment]* word [arg|redirect]*
func (p *Parser) parseSimpleCommand() *Node {
	start := p.lexer.pos
	var assignments []Assignment
	var redirects []*Node

	// Leading assignments
	for {
		p.lexer.skipBlanks()
		a := p.tryParseAssignment()
		if a != nil {
			assignments = append(assignments, *a)
			continue
		}
		r := p.tryParseRedirect()
		if r != nil {
			redirects = append(redirects, r)
			continue
		}
		break
	}

	// Command name
	p.lexer.skipBlanks()
	save := p.save()
	tok := p.lexer.NextToken("cmd")
	if tok.Type == TokEOF || tok.Type == TokNewline || tok.Type == TokComment ||
		(tok.Type == TokOp && tok.Value != "{" && tok.Value != "[" && tok.Value != "[[") {
		p.restore(save)
		// Standalone assignment
		if len(assignments) == 1 && len(redirects) == 0 {
			a := assignments[0]
			node := p.mk(NodeAssignment, start, p.lexer.pos)
			if node != nil {
				node.VarName = a.Name
				node.VarValue = a.Value
				node.IsAppend = a.IsAppend
			}
			return node
		}
		if len(assignments) > 0 || len(redirects) > 0 {
			node := p.mk(NodeCommand, start, p.lexer.pos)
			if node != nil {
				node.EnvVars = assignments
				node.Redirects = redirects
			}
			return node
		}
		return nil
	}

	// Check for shell keywords that should stop here
	if tok.Type == TokWord && isShellKeyword(tok.Value) && tok.Value != "in" {
		p.restore(save)
		if len(assignments) == 1 {
			a := assignments[0]
			node := p.mk(NodeAssignment, start, p.lexer.pos)
			if node != nil {
				node.VarName = a.Name
				node.VarValue = a.Value
				node.IsAppend = a.IsAppend
			}
			return node
		}
		return nil
	}
	p.restore(save)

	// Parse the command name word
	nameWord := p.scanWord()
	if nameWord == "" {
		if len(assignments) == 1 {
			a := assignments[0]
			node := p.mk(NodeAssignment, start, p.lexer.pos)
			if node != nil {
				node.VarName = a.Name
				node.VarValue = a.Value
				node.IsAppend = a.IsAppend
			}
			return node
		}
		return nil
	}

	// Parse arguments and redirects
	var args []string
	args = append(args, nameWord)
	for {
		p.lexer.skipBlanks()
		r := p.tryParseRedirect()
		if r != nil {
			redirects = append(redirects, r)
			continue
		}
		save2 := p.save()
		pk := p.lexer.NextToken("arg")
		if pk.Type == TokEOF || pk.Type == TokNewline || pk.Type == TokComment ||
			(pk.Type == TokOp && isTerminatorOp(pk.Value)) {
			p.restore(save2)
			break
		}
		p.restore(save2)
		arg := p.scanWord()
		if arg == "" {
			break
		}
		args = append(args, arg)
	}

	endPos := p.lexer.pos
	if len(redirects) > 0 {
		endPos = redirects[len(redirects)-1].End
	}
	node := p.mk(NodeCommand, start, endPos)
	if node == nil {
		return nil
	}
	node.Name = args[0]
	node.Args = args
	node.EnvVars = assignments
	node.Redirects = redirects
	return node
}

func isTerminatorOp(op string) bool {
	switch op {
	case "|", "|&", "&&", "||", ";", ";;", ";&", ";;&", "&", ")", "}", "))":
		return true
	}
	return false
}

func isShellKeyword(word string) bool {
	switch word {
	case "if", "then", "elif", "else", "fi", "while", "until", "for",
		"in", "do", "done", "case", "esac", "function", "select":
		return true
	}
	return false
}

// scanWord scans a word (possibly with quotes, dollar, etc.) and returns
// the raw text. This is a simplified scanner — for security analysis,
// the full walkArgument logic in analysis.go is used instead.
func (p *Parser) scanWord() string {
	p.lexer.skipBlanks()
	start := p.lexer.pos
	for p.lexer.pos < len(p.src) {
		c := p.lexer.peek()
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == 0 ||
			c == '|' || c == '&' || c == ';' || c == '(' || c == ')' {
			break
		}
		if c == '<' || c == '>' {
			if p.lexer.peekAt(1) != '(' {
				break
			}
		}
		if c == '"' {
			// Scan to closing "
			p.lexer.advance()
			for p.lexer.pos < len(p.src) && p.lexer.peek() != '"' {
				if p.lexer.peek() == '\\' && p.lexer.pos+1 < len(p.src) {
					p.lexer.advance()
				}
				p.lexer.advance()
			}
			if p.lexer.pos < len(p.src) {
				p.lexer.advance()
			}
			continue
		}
		if c == '\'' {
			// Scan to closing '
			p.lexer.advance()
			for p.lexer.pos < len(p.src) && p.lexer.peek() != '\'' {
				p.lexer.advance()
			}
			if p.lexer.pos < len(p.src) {
				p.lexer.advance()
			}
			continue
		}
		if c == '$' {
			p.scanDollar()
			continue
		}
		if c == '`' {
			// Scan backtick substitution
			p.lexer.advance()
			for p.lexer.pos < len(p.src) && p.lexer.peek() != '`' {
				if p.lexer.peek() == '\\' && p.lexer.pos+1 < len(p.src) {
					p.lexer.advance()
				}
				p.lexer.advance()
			}
			if p.lexer.pos < len(p.src) {
				p.lexer.advance()
			}
			continue
		}
		if c == '\\' {
			if p.lexer.pos+1 < len(p.src) {
				p.lexer.advance()
				p.lexer.advance()
				continue
			}
			break
		}
		p.lexer.advance()
	}
	if p.lexer.pos == start {
		return ""
	}
	return p.src[start:p.lexer.pos]
}

func (p *Parser) scanDollar() {
	p.lexer.advance() // skip $
	if p.lexer.pos >= len(p.src) {
		return
	}
	c := p.lexer.peek()
	switch c {
	case '(':
		if p.lexer.peekAt(1) == '(' {
			// $(( ... ))
			p.lexer.advance()
			p.lexer.advance()
			depth := 1
			for p.lexer.pos < len(p.src) && depth > 0 {
				if p.lexer.peek() == ')' && p.lexer.peekAt(1) == ')' {
					depth--
					p.lexer.advance()
					p.lexer.advance()
				} else if p.lexer.peek() == '$' && p.lexer.peekAt(1) == '(' && p.lexer.peekAt(2) == '(' {
					depth++
					p.lexer.pos += 3
				} else {
					p.lexer.advance()
				}
			}
		} else {
			// $( ... )
			p.lexer.advance()
			depth := 1
			for p.lexer.pos < len(p.src) && depth > 0 {
				ch := p.lexer.peek()
				if ch == '(' {
					depth++
				} else if ch == ')' {
					depth--
				} else if ch == '\'' {
					p.lexer.advance()
					for p.lexer.pos < len(p.src) && p.lexer.peek() != '\'' {
						p.lexer.advance()
					}
				} else if ch == '"' {
					p.lexer.advance()
					for p.lexer.pos < len(p.src) && p.lexer.peek() != '"' {
						if p.lexer.peek() == '\\' && p.lexer.pos+1 < len(p.src) {
							p.lexer.advance()
						}
						p.lexer.advance()
					}
				}
				p.lexer.advance()
			}
		}
	case '{':
		// ${ ... }
		p.lexer.advance()
		depth := 1
		for p.lexer.pos < len(p.src) && depth > 0 {
			ch := p.lexer.peek()
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
			} else if ch == '\\' && p.lexer.pos+1 < len(p.src) {
				p.lexer.advance()
			}
			p.lexer.advance()
		}
	case '\'':
		// $'...' ANSI-C quoting
		p.lexer.advance()
		for p.lexer.pos < len(p.src) && p.lexer.peek() != '\'' {
			if p.lexer.peek() == '\\' && p.lexer.pos+1 < len(p.src) {
				p.lexer.advance()
			}
			p.lexer.advance()
		}
		if p.lexer.pos < len(p.src) {
			p.lexer.advance()
		}
	default:
		// $VAR or $?, $$, etc.
		if isIdentStart(c) {
			for p.lexer.pos < len(p.src) && isIdentChar(p.lexer.peek()) {
				p.lexer.advance()
			}
		} else if isDigit(c) || c == '?' || c == '$' || c == '!' || c == '#' ||
			c == '@' || c == '*' || c == '-' || c == '_' {
			p.lexer.advance()
		}
	}
}

// tryParseAssignment attempts to parse VAR=value. Returns nil if not found.
func (p *Parser) tryParseAssignment() *Assignment {
	save := p.save()
	p.lexer.skipBlanks()
	start := p.lexer.pos

	if p.lexer.pos >= len(p.src) || !isIdentStart(p.lexer.peek()) {
		p.restore(save)
		return nil
	}

	// Scan identifier
	for p.lexer.pos < len(p.src) && isIdentChar(p.lexer.peek()) {
		p.lexer.advance()
	}
	nameEnd := p.lexer.pos

	c := p.lexer.peek()
	c1 := p.lexer.peekAt(1)
	var op string
	if c == '=' && c1 != '=' {
		op = "="
	} else if c == '+' && c1 == '=' {
		op = "+="
	} else {
		p.restore(save)
		return nil
	}

	name := p.src[start:nameEnd]
	p.lexer.advance()
	if op == "+=" {
		p.lexer.advance()
	}

	// Parse value
	value := ""
	c = p.lexer.peek()
	if c != 0 && c != ' ' && c != '\t' && c != '\n' && c != ';' && c != '&' &&
		c != '|' && c != ')' && c != '}' {
		value = p.scanWord()
	}

	return &Assignment{
		Name:     name,
		Value:    value,
		IsAppend: op == "+=",
	}
}

// tryParseRedirect attempts to parse a redirect. Returns nil if not found.
func (p *Parser) tryParseRedirect() *Node {
	save := p.save()
	p.lexer.skipBlanks()

	// Optional file descriptor
	fd := -1
	fdStart := p.lexer.pos
	if p.lexer.pos < len(p.src) && isDigit(p.lexer.peek()) {
		j := p.lexer.pos
		for j < len(p.src) && isDigit(p.src[j]) {
			j++
		}
		if j < len(p.src) && (p.src[j] == '>' || p.src[j] == '<') {
			fdStr := p.src[p.lexer.pos:j]
			fd = 0
			for _, d := range fdStr {
				fd = fd*10 + int(d-'0')
			}
			p.lexer.pos = j
		}
	}

	t := p.lexer.NextToken("arg")
	if t.Type != TokOp {
		p.restore(save)
		return nil
	}

	var redirOp RedirectOp
	switch t.Value {
	case ">":
		redirOp = RedirectOutput
	case ">>":
		redirOp = RedirectAppend
	case "<":
		redirOp = RedirectInput
	case "<<":
		redirOp = RedirectHeredoc
	case "<<-":
		redirOp = RedirectHeredocTab
	case "<<<":
		redirOp = RedirectHerestring
	case ">&":
		redirOp = RedirectDupOutput
	case "<&":
		redirOp = RedirectDupInput
	case ">|":
		redirOp = RedirectClobber
	case "&>":
		redirOp = RedirectBothOut
	case "&>>":
		redirOp = RedirectBothAppend
	default:
		p.restore(save)
		return nil
	}

	// Heredoc: scan delimiter
	if redirOp == RedirectHeredoc || redirOp == RedirectHeredocTab {
		p.lexer.skipBlanks()
		delim, quoted := p.scanHeredocDelimiter()
		startPos := fdStart
		if fd == -1 {
			startPos = t.Start
		}
		node := p.mk(NodeHeredoc, startPos, p.lexer.pos)
		if node != nil {
			node.RedirOp = redirOp
			node.FD = fd
			node.HeredocDelim = delim
			node.HeredocQuoted = quoted
			node.HeredocStrip = redirOp == RedirectHeredocTab
		}
		return node
	}

	// Herestring
	if redirOp == RedirectHerestring {
		p.lexer.skipBlanks()
		target := p.scanWord()
		startPos := fdStart
		if fd == -1 {
			startPos = t.Start
		}
		node := p.mk(NodeHerestring, startPos, p.lexer.pos)
		if node != nil {
			node.RedirOp = redirOp
			node.FD = fd
			node.Target = target
		}
		return node
	}

	// Regular redirect — parse target
	p.lexer.skipBlanks()
	target := p.scanWord()
	startPos := fdStart
	if fd == -1 {
		startPos = t.Start
	}
	node := p.mk(NodeRedirect, startPos, p.lexer.pos)
	if node != nil {
		node.RedirOp = redirOp
		node.Target = target
		node.FD = fd
	}
	return node
}

func (p *Parser) scanHeredocDelimiter() (string, bool) {
	p.lexer.skipBlanks()
	if p.lexer.pos >= len(p.src) {
		return "", false
	}
	c := p.lexer.peek()

	// Quoted delimiter
	if c == '\'' || c == '"' {
		quote := c
		p.lexer.advance()
		start := p.lexer.pos
		for p.lexer.pos < len(p.src) && p.lexer.peek() != quote {
			p.lexer.advance()
		}
		delim := p.src[start:p.lexer.pos]
		if p.lexer.pos < len(p.src) {
			p.lexer.advance()
		}
		return delim, true
	}

	// Backslash-escaped delimiter
	if c == '\\' {
		p.lexer.advance()
		start := p.lexer.pos
		for p.lexer.pos < len(p.src) && isIdentChar(p.lexer.peek()) {
			p.lexer.advance()
		}
		return p.src[start:p.lexer.pos], true
	}

	// Unquoted delimiter
	start := p.lexer.pos
	for p.lexer.pos < len(p.src) {
		ch := p.lexer.peek()
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '<' || ch == '>' ||
			ch == '|' || ch == '&' || ch == ';' || ch == '(' || ch == ')' {
			break
		}
		p.lexer.advance()
	}
	return p.src[start:p.lexer.pos], false
}

func (p *Parser) parseTestArgs(closer string) []string {
	var args []string
	for {
		p.lexer.skipBlanks()
		save := p.save()
		t := p.lexer.NextToken("arg")
		if t.Type == TokEOF {
			p.restore(save)
			break
		}
		if t.Value == closer {
			p.restore(save)
			break
		}
		args = append(args, t.Value)
	}
	return args
}

func (p *Parser) parseIf(ifTok Token) *Node {
	start := ifTok.Start
	cond := p.parseStatements("")
	p.consumeKeyword("then")
	body := p.parseStatements("")

	var elifs []*Node
	var elseBody []*Node

	for {
		save := p.save()
		t := p.lexer.NextToken("cmd")
		if t.Type == TokWord && t.Value == "elif" {
			elifCond := p.parseStatements("")
			p.consumeKeyword("then")
			elifBody := p.parseStatements("")
			elifNode := p.mk(NodeIf, t.Start, p.lexer.pos)
			if elifNode != nil {
				elifNode.Condition = elifCond
				elifNode.Body = elifBody
			}
			elifs = append(elifs, elifNode)
		} else if t.Type == TokWord && t.Value == "else" {
			elseBody = p.parseStatements("")
		} else {
			p.restore(save)
			break
		}
	}
	p.consumeKeyword("fi")

	node := p.mk(NodeIf, start, p.lexer.pos)
	if node != nil {
		node.Condition = cond
		node.Body = body
		node.ElseIf = elifs
		node.Else = elseBody
	}
	return node
}

func (p *Parser) parseWhileUntil(kwTok Token) *Node {
	typ := NodeWhile
	if kwTok.Value == "until" {
		typ = NodeUntil
	}
	cond := p.parseStatements("")
	p.consumeKeyword("do")
	body := p.parseStatements("")
	p.consumeKeyword("done")

	node := p.mk(typ, kwTok.Start, p.lexer.pos)
	if node != nil {
		node.Condition = cond
		node.Body = body
	}
	return node
}

func (p *Parser) parseFor(forTok Token) *Node {
	p.lexer.skipBlanks()
	// Variable name
	varTok := p.lexer.NextToken("arg")
	varName := varTok.Value

	p.lexer.skipBlanks()
	save := p.save()
	inTok := p.lexer.NextToken("arg")

	var words []string
	if inTok.Type == TokWord && inTok.Value == "in" {
		for {
			p.lexer.skipBlanks()
			c := p.lexer.peek()
			if c == ';' || c == '\n' || c == 0 {
				break
			}
			w := p.scanWord()
			if w == "" {
				break
			}
			words = append(words, w)
		}
	} else {
		p.restore(save)
	}

	// Consume separator
	save2 := p.save()
	sep := p.lexer.NextToken("cmd")
	if sep.Type != TokOp || sep.Value != ";" {
		if sep.Type != TokNewline {
			p.restore(save2)
		}
	}

	p.skipNewlines()
	p.consumeKeyword("do")
	body := p.parseStatements("")
	p.consumeKeyword("done")

	node := p.mk(NodeFor, forTok.Start, p.lexer.pos)
	if node != nil {
		node.VarName = varName
		node.Args = words
		node.Body = body
	}
	return node
}

func (p *Parser) parseCase(caseTok Token) *Node {
	p.lexer.skipBlanks()
	word := p.scanWord()
	p.lexer.skipBlanks()
	p.consumeKeyword("in")
	p.skipNewlines()

	var items []*CaseItem
	for {
		p.lexer.skipBlanks()
		p.skipNewlines()
		save := p.save()
		t := p.lexer.NextToken("arg")
		if t.Type == TokWord && t.Value == "esac" {
			break
		}
		if t.Type == TokEOF {
			break
		}
		p.restore(save)
		item := p.parseCaseItem()
		if item == nil {
			break
		}
		items = append(items, item)
	}

	node := p.mk(NodeCase, caseTok.Start, p.lexer.pos)
	if node != nil {
		node.CaseWord = word
		node.CaseItems = items
	}
	return node
}

func (p *Parser) parseCaseItem() *CaseItem {
	p.lexer.skipBlanks()
	// Optional (
	if p.lexer.peek() == '(' {
		p.lexer.advance()
	}

	// Patterns separated by |
	var patterns []string
	for {
		p.lexer.skipBlanks()
		pat := p.scanCasePattern()
		if pat == "" {
			break
		}
		patterns = append(patterns, pat)
		p.lexer.skipBlanks()
		if p.lexer.peek() == '|' {
			p.lexer.advance()
		} else {
			break
		}
	}

	// )
	if p.lexer.peek() == ')' {
		p.lexer.advance()
	}

	body := p.parseStatements("")

	// Terminator
	term := ";;"
	save := p.save()
	t := p.lexer.NextToken("cmd")
	if t.Type == TokOp && (t.Value == ";;" || t.Value == ";&" || t.Value == ";;&") {
		term = t.Value
	} else {
		p.restore(save)
		term = ""
	}

	return &CaseItem{
		Patterns: patterns,
		Body:     body,
		Term:     term,
	}
}

func (p *Parser) scanCasePattern() string {
	p.lexer.skipBlanks()
	start := p.lexer.pos
	for p.lexer.pos < len(p.src) {
		c := p.lexer.peek()
		if c == ')' || c == '|' || c == 0 || c == '\n' {
			break
		}
		if c == '\\' && p.lexer.pos+1 < len(p.src) {
			p.lexer.advance()
			p.lexer.advance()
			continue
		}
		if c == '\'' {
			p.lexer.advance()
			for p.lexer.pos < len(p.src) && p.lexer.peek() != '\'' {
				p.lexer.advance()
			}
			if p.lexer.pos < len(p.src) {
				p.lexer.advance()
			}
			continue
		}
		if c == '"' {
			p.lexer.advance()
			for p.lexer.pos < len(p.src) && p.lexer.peek() != '"' {
				if p.lexer.peek() == '\\' && p.lexer.pos+1 < len(p.src) {
					p.lexer.advance()
				}
				p.lexer.advance()
			}
			if p.lexer.pos < len(p.src) {
				p.lexer.advance()
			}
			continue
		}
		p.lexer.advance()
	}
	result := strings.TrimSpace(p.src[start:p.lexer.pos])
	return result
}

func (p *Parser) parseFunction(kwTok Token) *Node {
	p.lexer.skipBlanks()
	name := p.scanWord()
	p.lexer.skipBlanks()
	// Optional ()
	if p.lexer.peek() == '(' {
		p.lexer.advance()
		p.lexer.skipBlanks()
		if p.lexer.peek() == ')' {
			p.lexer.advance()
		}
	}
	p.lexer.skipBlanks()
	p.skipNewlines()
	body := p.parseCommand()

	node := p.mk(NodeFunctionDef, kwTok.Start, p.lexer.pos)
	if node != nil {
		node.Name = name
		if body != nil {
			node.Body = []*Node{body}
		}
	}
	return node
}

func (p *Parser) parseDeclaration(kwTok Token) *Node {
	args := []string{kwTok.Value}
	start := kwTok.Start

	for {
		p.lexer.skipBlanks()
		save := p.save()
		t := p.lexer.NextToken("arg")
		if t.Type == TokEOF || t.Type == TokNewline || t.Type == TokComment ||
			(t.Type == TokOp && isTerminatorOp(t.Value)) {
			p.restore(save)
			break
		}
		p.restore(save)
		w := p.scanWord()
		if w == "" {
			break
		}
		args = append(args, w)
	}

	node := p.mk(NodeDeclaration, start, p.lexer.pos)
	if node != nil {
		node.Name = kwTok.Value
		node.Args = args
	}
	return node
}

func (p *Parser) parseUnset(kwTok Token) *Node {
	args := []string{kwTok.Value}
	start := kwTok.Start

	for {
		p.lexer.skipBlanks()
		save := p.save()
		t := p.lexer.NextToken("arg")
		if t.Type == TokEOF || t.Type == TokNewline || t.Type == TokComment ||
			(t.Type == TokOp && isTerminatorOp(t.Value)) {
			p.restore(save)
			break
		}
		p.restore(save)
		w := p.scanWord()
		if w == "" {
			break
		}
		args = append(args, w)
	}

	node := p.mk(NodeUnset, start, p.lexer.pos)
	if node != nil {
		node.Name = kwTok.Value
		node.Args = args
	}
	return node
}

func (p *Parser) consumeKeyword(kw string) {
	p.skipNewlines()
	p.lexer.skipBlanks()
	save := p.save()
	t := p.lexer.NextToken("cmd")
	if t.Type == TokWord && t.Value == kw {
		return
	}
	p.restore(save)
}

// ContainsControlChars checks for characters that cause parser differentials.
func ContainsControlChars(cmd string) bool {
	for _, r := range cmd {
		if r < 0x20 && r != '\n' && r != '\t' && r != '\r' {
			return true
		}
		if r == 0x7F {
			return true
		}
	}
	return false
}

// ContainsUnicodeWhitespace checks for non-ASCII whitespace that confuses parsers.
func ContainsUnicodeWhitespace(cmd string) bool {
	for _, r := range cmd {
		if r > 127 && unicode.IsSpace(r) {
			return true
		}
		// Specific Unicode whitespace characters
		switch r {
		case '\u00A0', '\u1680', '\u2028', '\u2029', '\u202F', '\u205F', '\u3000', '\uFEFF':
			return true
		}
		if r >= '\u2000' && r <= '\u200B' {
			return true
		}
	}
	return false
}

// FormatAST returns a string representation of the AST for debugging.
func FormatAST(node *Node, indent int) string {
	if node == nil {
		return "<nil>"
	}
	prefix := strings.Repeat("  ", indent)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s%s", prefix, node.Type))
	if node.Name != "" {
		b.WriteString(fmt.Sprintf(" name=%q", node.Name))
	}
	if node.VarName != "" {
		b.WriteString(fmt.Sprintf(" var=%q=%q", node.VarName, node.VarValue))
	}
	if node.Target != "" {
		b.WriteString(fmt.Sprintf(" target=%q", node.Target))
	}
	b.WriteString("\n")
	for _, child := range node.Children {
		b.WriteString(FormatAST(child, indent+1))
	}
	for _, child := range node.Body {
		b.WriteString(FormatAST(child, indent+1))
	}
	for _, child := range node.Condition {
		b.WriteString(FormatAST(child, indent+1))
	}
	return b.String()
}
