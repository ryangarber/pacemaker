// Package clike is a shared, tolerant lexer and structural parser for
// C-family languages (Dart, JavaScript, TypeScript, ...). It is not a full
// parser: it recovers declarations (classes, functions, methods, fields) and
// their line spans, which is what drift analysis needs. Language modules
// configure it with a Dialect.
package clike

import (
	"strings"
)

// TokKind classifies tokens.
type TokKind uint8

const (
	Ident TokKind = iota
	Number
	String
	Regex
	Punct
	Comment
)

// Token is a lexical token with its 1-based line span.
type Token struct {
	Kind    TokKind
	Text    string
	Line    int
	EndLine int
	NL      bool // preceded by a newline
	Off     int  // byte offset of the token start
	EndOff  int  // byte offset just past the token
}

func (t Token) Is(s string) bool { return t.Kind == Punct && t.Text == s }

// Dialect configures lexing and parsing for a specific language.
type Dialect struct {
	Name                string
	Backtick            bool // `template ${literals}`
	TripleQuotes        bool // '''multi-line''' strings
	RawStrings          bool // r'raw' strings
	RegexLiterals       bool // /regex/ literals
	NestedComments      bool // /* /* nested */ */
	DollarInterpolation bool // "${expr}" inside ordinary strings
	ASI                 bool // automatic semicolon insertion heuristics
	// TypeFirst: declarations put the type before the name ("int x").
	TypeFirst bool
	// TopLevelNeedsKeyword: top-level statements are only declarations when
	// they contain a declaration keyword (JS allows arbitrary statements).
	TopLevelNeedsKeyword bool
	// Containers maps keywords that introduce a body of member declarations
	// to the symbol kind they produce.
	Containers map[string]string
	// DeclKeywords introduce declarations (used with TopLevelNeedsKeyword).
	DeclKeywords map[string]bool
	// Modifiers are skipped when looking for names.
	Modifiers map[string]bool
	// Visibility are the modifiers that only change who can see a symbol
	// (export, public, ...). Compare reports a change in them once, as a
	// visibility change, rather than also as a signature change.
	Visibility map[string]bool
	// Imports are statement-leading keywords for import-like statements.
	Imports map[string]bool
	// Skip are statement-leading keywords that never declare symbols.
	Skip map[string]bool
	// Exported decides visibility of a symbol.
	Exported func(header []Token, name string, inContainer bool) bool

	// MemberAssign enables JavaScript-style symbols that are declared by
	// assignment or registration rather than by a keyword (AngularJS 1.x,
	// ES5, CommonJS):
	//   - `$scope.x = ...`, `this.x = ...` and `vm.x = ...` (where
	//     `var vm = this`) inside function bodies;
	//   - `Foo.prototype.x = function`, `Foo.x = function`, `exports.x = ...`
	//     at top level;
	//   - declarations inside top-level IIFEs (`(function () { ... })()`);
	//   - registrations such as `.controller('Name', fn)` (see Registrations).
	MemberAssign bool
	// ScopeRoots are receivers whose assigned members are symbols inside
	// function bodies, besides "this" and its aliases (e.g. "$scope").
	ScopeRoots map[string]bool
	// Registrations maps call names to the symbol kind of the unit they
	// register: `.controller('LoginCtrl', fn)` → LoginCtrl (controller).
	Registrations map[string]string
	// WeakRegistrations are registration names that are also common method
	// names (filter, value, ...); they only count when the call chain also
	// contains a `module(...)` call or the second argument is a function.
	WeakRegistrations map[string]bool
}

var puncts = []string{
	"?..", ">>>=", "...", "===", "!==", "**=", "<<=", ">>=", ">>>", "??=", "&&=", "||=",
	"=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "..", "::", "++", "--",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<", "**", "~/",
}

var regexPrecedingWords = map[string]bool{
	"return": true, "typeof": true, "case": true, "do": true, "else": true, "in": true,
	"instanceof": true, "new": true, "delete": true, "void": true, "throw": true,
	"yield": true, "await": true, "of": true,
}

// bom is the UTF-8 byte-order mark, U+FEFF: whitespace wherever it appears.
const bom = "\xEF\xBB\xBF"

type lexer struct {
	src  string
	i    int
	line int
	d    *Dialect
	toks []Token
	nl   bool
}

// Lex tokenises src. It never fails; malformed input yields best-effort tokens.
func Lex(src []byte, d *Dialect) []Token {
	lx := &lexer{src: string(src), line: 1, d: d, nl: true}
	lx.run()
	return lx.toks
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}
func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }
func isDigit(c byte) bool     { return c >= '0' && c <= '9' }

func (lx *lexer) emit(kind TokKind, start, startLine int) {
	lx.toks = append(lx.toks, Token{Kind: kind, Text: lx.src[start:lx.i], Line: startLine, EndLine: lx.line, NL: lx.nl, Off: start, EndOff: lx.i})
	lx.nl = false
}

func (lx *lexer) advance(n int) {
	for k := 0; k < n && lx.i < len(lx.src); k++ {
		if lx.src[lx.i] == '\n' {
			lx.line++
		}
		lx.i++
	}
}

func (lx *lexer) peek(off int) byte {
	if lx.i+off < len(lx.src) {
		return lx.src[lx.i+off]
	}
	return 0
}

func (lx *lexer) run() {
	for lx.i < len(lx.src) {
		c := lx.src[lx.i]
		start, startLine := lx.i, lx.line
		switch {
		case strings.HasPrefix(lx.src[lx.i:], bom):
			// Byte-order mark: whitespace. isIdentStart accepts any byte >= 0x80,
			// so without this it would start an identifier and hide whatever
			// follows (a leading IIFE). Offsets are kept.
			lx.i += 3
		case c == '\n':
			lx.advance(1)
			lx.nl = true
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			lx.advance(1)
		case c == '/' && lx.peek(1) == '/':
			for lx.i < len(lx.src) && lx.src[lx.i] != '\n' {
				lx.i++
			}
			nl := lx.nl
			lx.emit(Comment, start, startLine)
			lx.nl = nl
		case c == '/' && lx.peek(1) == '*':
			lx.skipBlockComment()
			nl := lx.nl
			lx.emit(Comment, start, startLine)
			lx.nl = nl
		case lx.d.RawStrings && c == 'r' && (lx.peek(1) == '\'' || lx.peek(1) == '"'):
			lx.advance(1)
			lx.skipString(true)
			lx.emit(String, start, startLine)
		case c == '\'' || c == '"' || (c == '`' && lx.d.Backtick):
			lx.skipString(false)
			lx.emit(String, start, startLine)
		case isDigit(c) || (c == '.' && isDigit(lx.peek(1))):
			for lx.i < len(lx.src) {
				ch := lx.src[lx.i]
				if isIdentPart(ch) || (ch == '.' && isDigit(lx.peek(1))) ||
					((ch == '+' || ch == '-') && (lx.src[lx.i-1] == 'e' || lx.src[lx.i-1] == 'E') && !strings.HasPrefix(lx.src[start:], "0x")) {
					lx.i++
					continue
				}
				break
			}
			lx.emit(Number, start, startLine)
		case isIdentStart(c):
			for lx.i < len(lx.src) && isIdentPart(lx.src[lx.i]) && !strings.HasPrefix(lx.src[lx.i:], bom) {
				lx.i++
			}
			lx.emit(Ident, start, startLine)
		case c == '/' && lx.d.RegexLiterals && lx.regexAllowed():
			if lx.skipRegex() {
				lx.emit(Regex, start, startLine)
			} else {
				lx.i = start + 1
				lx.emit(Punct, start, startLine)
			}
		default:
			n := 1
			for _, p := range puncts {
				if strings.HasPrefix(lx.src[lx.i:], p) {
					n = len(p)
					break
				}
			}
			lx.advance(n)
			lx.emit(Punct, start, startLine)
		}
	}
}

func (lx *lexer) skipBlockComment() {
	depth := 0
	for lx.i < len(lx.src) {
		if strings.HasPrefix(lx.src[lx.i:], "/*") {
			depth++
			lx.advance(2)
			if !lx.d.NestedComments && depth > 1 {
				depth = 1
			}
			continue
		}
		if strings.HasPrefix(lx.src[lx.i:], "*/") {
			lx.advance(2)
			depth--
			if depth <= 0 {
				return
			}
			continue
		}
		lx.advance(1)
	}
}

// skipString consumes a string literal starting at lx.i (the quote).
func (lx *lexer) skipString(raw bool) {
	q := lx.src[lx.i]
	triple := lx.d.TripleQuotes && q != '`' && strings.HasPrefix(lx.src[lx.i:], strings.Repeat(string(q), 3))
	if triple {
		lx.advance(3)
	} else {
		lx.advance(1)
	}
	multiline := triple || q == '`'
	for lx.i < len(lx.src) {
		c := lx.src[lx.i]
		switch {
		case c == '\\' && !raw:
			lx.advance(2)
		case triple && strings.HasPrefix(lx.src[lx.i:], strings.Repeat(string(q), 3)):
			lx.advance(3)
			return
		case !triple && c == q:
			lx.advance(1)
			return
		case c == '\n' && !multiline:
			return // unterminated; recover at end of line
		case !raw && c == '$' && lx.peek(1) == '{' && (q == '`' || lx.d.DollarInterpolation):
			lx.advance(2)
			lx.skipCode('}')
		default:
			lx.advance(1)
		}
	}
}

// skipCode consumes code inside an interpolation until the matching closer.
func (lx *lexer) skipCode(closer byte) {
	depth := 0
	for lx.i < len(lx.src) {
		c := lx.src[lx.i]
		switch {
		case c == '\'' || c == '"' || (c == '`' && lx.d.Backtick):
			lx.skipString(false)
		case c == '/' && lx.peek(1) == '*':
			lx.skipBlockComment()
		case c == '{':
			depth++
			lx.advance(1)
		case c == closer:
			lx.advance(1)
			if depth == 0 {
				return
			}
			depth--
		default:
			lx.advance(1)
		}
	}
}

func (lx *lexer) regexAllowed() bool {
	if len(lx.toks) == 0 {
		return true
	}
	// Find previous non-comment token.
	for k := len(lx.toks) - 1; k >= 0; k-- {
		t := lx.toks[k]
		if t.Kind == Comment {
			continue
		}
		switch t.Kind {
		case Ident:
			return regexPrecedingWords[t.Text]
		case Number, String, Regex:
			return false
		case Punct:
			return t.Text != ")" && t.Text != "]" && t.Text != "}" && t.Text != "++" && t.Text != "--"
		}
	}
	return true
}

func (lx *lexer) skipRegex() bool {
	j := lx.i + 1
	inClass := false
	for j < len(lx.src) {
		c := lx.src[j]
		switch {
		case c == '\n':
			return false
		case c == '\\':
			j += 2
			continue
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			j++
			for j < len(lx.src) && isIdentPart(lx.src[j]) {
				j++
			}
			lx.i = j
			return true
		}
		j++
	}
	return false
}
