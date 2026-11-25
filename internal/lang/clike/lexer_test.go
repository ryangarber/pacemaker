package clike_test

import (
	"strings"
	"testing"

	"github.com/ryangarber/pacemaker/internal/lang/clike"
	"github.com/ryangarber/pacemaker/internal/lang/dart"
	"github.com/ryangarber/pacemaker/internal/lang/javascript"
)

var kindNames = map[clike.TokKind]string{
	clike.Ident: "id", clike.Number: "num", clike.String: "str", clike.Regex: "re",
	clike.Punct: "p", clike.Comment: "c",
}

// render lexes src and renders each token as kind:text, space separated.
func render(src string, d *clike.Dialect) string {
	var out []string
	for _, t := range clike.Lex([]byte(src), d) {
		out = append(out, kindNames[t.Kind]+":"+t.Text)
	}
	return strings.Join(out, " ")
}

func TestLexJavaScript(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"division is not a regex", "a / b / c", "id:a p:/ id:b p:/ id:c"},
		{"regex after =", "x = /a}b/g", "id:x p:= re:/a}b/g"},
		{"regex after return", "return /[/]x/i", "id:return re:/[/]x/i"},
		{"division after ) ] and ++", "(a) / 2; b[0] / 2; i++ / 2",
			"p:( id:a p:) p:/ num:2 p:; id:b p:[ num:0 p:] p:/ num:2 p:; id:i p:++ p:/ num:2"},
		{"regex at start of file", "/x/.test(s)", "re:/x/ p:. id:test p:( id:s p:)"},
		{"unterminated regex falls back to division", "a = /x\nb", "id:a p:= p:/ id:x id:b"},
		{"template literal with nested braces", "`a ${ {b: 1}.b } }` + c", "str:`a ${ {b: 1}.b } }` p:+ id:c"},
		{"strings with escapes", `'it\'s' "q\"q"`, `str:'it\'s' str:"q\"q"`},
		{"unterminated string stops at end of line", "'abc\nx", "str:'abc id:x"},
		{"comments", "a // line\n/* block */ b", "id:a c:// line c:/* block */ id:b"},
		{"numbers", "1e-5 0x1E-2 .5 1_000n", "num:1e-5 num:0x1E p:- num:2 num:.5 num:1_000n"},
		{"longest punctuation first", "a >>>= b?.c ?? d ...e", "id:a p:>>>= id:b p:?. id:c p:?? id:d p:... id:e"},
		{"non-ASCII identifier", "café = 1", "id:café p:= num:1"},
		{"byte-order mark is whitespace", "\xEF\xBB\xBF(function () {})()",
			"p:( id:function p:( p:) p:{ p:} p:) p:( p:)"},
		{"byte-order mark mid-file is whitespace", "a\xEF\xBB\xBFb", "id:a id:b"},
	} {
		if got := render(tc.src, javascript.Dialect); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}

func TestLexDart(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"nested block comments", "a /* x /* y */ z */ b", "id:a c:/* x /* y */ z */ id:b"},
		{"triple-quoted strings span lines", "'''a\n'b'\n''' x", "str:'''a\n'b'\n''' id:x"},
		{"raw strings ignore escapes", `r'\' x`, `str:r'\' id:x`},
		{"interpolation inside ordinary strings", `"a ${b("}")} c" d`, `str:"a ${b("}")} c" id:d`},
		{"no regex literals", "a = /b/", "id:a p:= p:/ id:b p:/"},
	} {
		if got := render(tc.src, dart.Dialect); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}

func TestLexPositions(t *testing.T) {
	src := "\xEF\xBB\xBFa\n`x\ny` b\n\n  c"
	toks := clike.Lex([]byte(src), javascript.Dialect)
	type pos struct {
		text        string
		line, end   int
		nl          bool
		off, endOff int
	}
	want := []pos{
		{"a", 1, 1, true, 3, 4}, // start of file counts as a new line
		{"`x\ny`", 2, 3, true, 5, 10},
		{"b", 3, 3, false, 11, 12},
		{"c", 5, 5, true, 16, 17},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d: %+v", len(toks), len(want), toks)
	}
	for i, w := range want {
		tk := toks[i]
		got := pos{tk.Text, tk.Line, tk.EndLine, tk.NL, tk.Off, tk.EndOff}
		if got != w {
			t.Errorf("token %d = %+v, want %+v", i, got, w)
		}
		if src[tk.Off:tk.EndOff] != tk.Text {
			t.Errorf("token %d offsets [%d,%d) do not slice to its text %q", i, tk.Off, tk.EndOff, tk.Text)
		}
	}
}

func TestLexCommentKeepsNewlineFlag(t *testing.T) {
	toks := clike.Lex([]byte("a\n// note\nb"), javascript.Dialect)
	if len(toks) != 3 || !toks[2].NL {
		t.Errorf("a token after a comment line must still be marked as starting a line: %+v", toks)
	}
}

func TestNormalize(t *testing.T) {
	same := [][2]string{
		{"a(1);\n", "\xEF\xBB\xBFa(1)\n"},
		{"f({ a: 'x', b: [1, 2,], });", "f({\n  a: \"x\",\n  b: [1, 2]\n})  /* c */"},
	}
	for _, p := range same {
		a, b := clike.Normalize([]byte(p[0]), javascript.Dialect), clike.Normalize([]byte(p[1]), javascript.Dialect)
		if string(a) != string(b) {
			t.Errorf("%q and %q should normalise equal:\n%s\n---\n%s", p[0], p[1], a, b)
		}
	}
	differ := [][2]string{
		{"a('x')", "a('y')"},
		{`a('it\'s')`, `a("it's")`}, // only escape-free strings are canonicalised
		{"a + b", "a - b"},
	}
	for _, p := range differ {
		a, b := clike.Normalize([]byte(p[0]), javascript.Dialect), clike.Normalize([]byte(p[1]), javascript.Dialect)
		if string(a) == string(b) {
			t.Errorf("%q and %q must not normalise equal", p[0], p[1])
		}
	}
	// Dart has no ASI: semicolons are significant.
	if string(clike.Normalize([]byte("a;"), dart.Dialect)) == string(clike.Normalize([]byte("a"), dart.Dialect)) {
		t.Error("Dart normalisation must keep semicolons")
	}
}

func TestSignificantLines(t *testing.T) {
	src := "\xEF\xBB\xBF// header\n\nconst a = `x\n\ny`;\n/* a\n   b */\n  b()\n"
	got := clike.SignificantLines([]byte(src), javascript.Dialect)
	want := []bool{false, false, true, true, true, false, false, true}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d significant = %v, want %v (all %v)", i+1, got[i], want[i], got)
		}
	}
	if n := len(clike.SignificantLines(nil, javascript.Dialect)); n != 1 {
		t.Errorf("empty source has %d lines, want 1", n)
	}
}
