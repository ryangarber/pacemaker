package generic

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ryangarber/pacemaker/internal/lang"
)

func TestRegistersAsFallback(t *testing.T) {
	fb := lang.Fallback()
	if fb == nil || fb.ID() != "generic" {
		t.Fatalf("Fallback = %v, want generic module", fb)
	}
	if got := lang.ForPath("no/known/extension.zzz"); got == nil || got.ID() != "generic" {
		t.Errorf("ForPath unknown ext = %v, want generic", got)
	}
	for _, l := range lang.All() {
		if l.ID() == "generic" {
			t.Error("generic must not be in the extension registry")
		}
	}
	if len(Module{}.Extensions()) != 0 {
		t.Error("generic should claim no extensions")
	}
	want := []string{"normalize", "lines"}
	if got := lang.Capabilities(Module{}); !reflect.DeepEqual(got, want) {
		t.Errorf("Capabilities = %v, want %v", got, want)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"only whitespace", " \n\t\n\n", ""},
		{"collapse runs", "a   b\t\tc", "a b c\n"},
		{"trim edges", "   x = 1   \n", "x = 1\n"},
		{"drop blank lines", "a\n\n\n b\n", "a\nb\n"},
		{"no trailing newline", "a\nb", "a\nb\n"},
		{"crlf", "a  b\r\nc\r\n\r\n", "a b\nc\n"},
		{"unicode kept", "  héllo   wörld ✓ \n", "héllo wörld ✓\n"},
		{"nbsp is whitespace", "a\u00a0\u00a0b", "a b\n"},
	}
	for _, c := range cases {
		if got := string(Module{}.Normalize([]byte(c.in))); got != c.want {
			t.Errorf("%s: Normalize(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestNormalizeCosmeticEquivalence(t *testing.T) {
	a := "func f() {\n    return 1\n}\n"
	b := "func  f()  {\r\n\r\n\treturn   1\r\n}"
	if string(Module{}.Normalize([]byte(a))) != string(Module{}.Normalize([]byte(b))) {
		t.Error("whitespace/CRLF-only changes should normalize identically")
	}
	c := "func f() {\n    return 2\n}\n"
	if string(Module{}.Normalize([]byte(a))) == string(Module{}.Normalize([]byte(c))) {
		t.Error("content change must not normalize away")
	}
	// Whitespace inside a token boundary is significant: "ab" != "a b".
	if string(Module{}.Normalize([]byte("ab"))) == string(Module{}.Normalize([]byte("a b"))) {
		t.Error("joining tokens must not normalize away")
	}
}

func TestNormalizeLongLine(t *testing.T) {
	in := strings.Repeat("word  ", 100000)
	got := string(Module{}.Normalize([]byte(in)))
	want := strings.TrimSuffix(strings.Repeat("word ", 100000), " ") + "\n"
	if got != want {
		t.Errorf("long line normalized to %d bytes, want %d", len(got), len(want))
	}
}

func TestSignificantLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []bool
	}{
		{"empty", "", []bool{false}},
		{"single newline", "\n", []bool{false}},
		{"one line no newline", "x", []bool{true}},
		{"one line with newline", "x\n", []bool{true}},
		{"blank between", "a\n\n  \nb\n", []bool{true, false, false, true}},
		{"tabs only", "\t\n\tx\n", []bool{false, true}},
		{"crlf", "a\r\n\r\nb\r\n", []bool{true, false, true}},
		{"two trailing newlines", "a\n\n", []bool{true, false}},
		{"comment counts (not language aware)", "// c\n", []bool{true}},
		{"unicode", "é\n \u00a0\n", []bool{true, false}},
	}
	for _, c := range cases {
		if got := (Module{}).SignificantLines([]byte(c.in)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: SignificantLines(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}
