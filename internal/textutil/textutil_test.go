package textutil

import (
	"os"
	"strings"
	"testing"

	"github.com/ryangarber/pacemaker/internal/model"
)

var (
	on  = Painter{On: true}
	off = Painter{}
)

func TestDetect(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out") // a regular file, never a terminal
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	closed, _ := os.CreateTemp(t.TempDir(), "closed")
	closed.Close()

	cases := []struct {
		name                 string
		noColor, term, force string
		file                 *os.File
		want                 bool
	}{
		{"plain file", "", "xterm", "", f, false},
		{"force color", "", "xterm", "1", f, true},
		{"no color beats force", "1", "xterm", "1", f, false},
		{"dumb terminal beats force", "", "dumb", "1", f, false},
		{"stat error", "", "xterm", "", closed, false},
		{"force with stat error", "", "xterm", "1", closed, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", c.noColor)
			t.Setenv("TERM", c.term)
			t.Setenv("FORCE_COLOR", c.force)
			if got := Detect(c.file).On; got != c.want {
				t.Errorf("Detect = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPainterS(t *testing.T) {
	cases := []struct {
		name   string
		p      Painter
		text   string
		styles []Style
		want   string
	}{
		{"off", off, "hi", []Style{Red}, "hi"},
		{"no styles", on, "hi", nil, "hi"},
		{"empty text", on, "", []Style{Red}, ""},
		{"one style", on, "hi", []Style{Red}, "\x1b[31mhi\x1b[0m"},
		{"stacked", on, "hi", []Style{Bold, Green}, "\x1b[1m\x1b[32mhi\x1b[0m"},
	}
	for _, c := range cases {
		if got := c.p.S(c.text, c.styles...); got != c.want {
			t.Errorf("%s: S = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"héllo", 5},
		{"✓ ok", 4},
		{"\x1b[31mred\x1b[0m", 3},
		{"\x1b[1;38;5;208mx\x1b[0m", 1},
		{on.S("ab", Bold, Cyan) + "c", 3},
		{"\ufeffa", 2}, // BOM is a rune like any other
	}
	for _, c := range cases {
		if got := Width(c.in); got != c.want {
			t.Errorf("Width(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPad(t *testing.T) {
	styled := on.S("ab", Red)
	cases := []struct {
		name, got, want string
	}{
		{"pad", Pad("ab", 4), "ab  "},
		{"exact", Pad("abcd", 4), "abcd"},
		{"longer untouched", Pad("abcdef", 4), "abcdef"},
		{"zero width", Pad("ab", 0), "ab"},
		{"negative width", Pad("ab", -3), "ab"},
		{"empty", Pad("", 3), "   "},
		{"unicode", Pad("é", 3), "é  "},
		{"ansi ignored", Pad(styled, 4), styled + "  "},
		{"left pad", PadLeft("ab", 4), "  ab"},
		{"left exact", PadLeft("abcd", 4), "abcd"},
		{"left longer", PadLeft("abcdef", 2), "abcdef"},
		{"left ansi", PadLeft(styled, 3), " " + styled},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		name, got, want string
	}{
		{"fits", Truncate("hello", 5), "hello"},
		{"short", Truncate("hi", 5), "hi"},
		{"cut", Truncate("hello world", 5), "hell…"},
		{"w=2", Truncate("hello", 2), "h…"},
		{"w=1 unchanged", Truncate("hello", 1), "hello"},
		{"w=0 unchanged", Truncate("hello", 0), "hello"},
		{"empty", Truncate("", 3), ""},
		{"unicode safe", Truncate("ééééé", 3), "éé…"},
		{"left fits", TruncateLeft("a/b", 3), "a/b"},
		{"left cut", TruncateLeft("lib/src/auth.dart", 9), "…uth.dart"},
		{"left w=2", TruncateLeft("abc", 2), "…c"},
		{"left w=1 unchanged", TruncateLeft("abc", 1), "abc"},
		{"left unicode safe", TruncateLeft("ééééé", 3), "…éé"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
	long := strings.Repeat("x", 100000)
	if got := Truncate(long, 10); Width(got) != 10 {
		t.Errorf("long Truncate width %d, want 10", Width(got))
	}
	if got := TruncateLeft(long, 10); Width(got) != 10 {
		t.Errorf("long TruncateLeft width %d, want 10", Width(got))
	}
}

func TestTableRender(t *testing.T) {
	tb := Table{Header: []string{"NAME", "N", "NOTE"}, Right: map[int]bool{1: true}, Indent: "  "}
	tb.Add("alpha", "1", "x")
	tb.Add("b", "1234", "longer note")
	tb.Add("é", on.S("22", Red), "")
	var b strings.Builder
	tb.Render(&b, off)
	want := "" +
		"  NAME      N  NOTE\n" +
		"  alpha     1  x\n" +
		"  b      1234  longer note\n" +
		"  é       " + " " + on.S("22", Red) + "  \n"
	if b.String() != want {
		t.Errorf("Render\n got %q\nwant %q", b.String(), want)
	}
}

func TestTableRenderHeaderStyled(t *testing.T) {
	tb := Table{Header: []string{on.S("A", Red), "B"}}
	tb.Add("1", "2")
	var b strings.Builder
	tb.Render(&b, on)
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	// The header's own styles are stripped and replaced by Bold+Gray.
	if want := "\x1b[1m\x1b[90mA  B\x1b[0m"; lines[0] != want {
		t.Errorf("header = %q, want %q", lines[0], want)
	}
	if lines[1] != "1  2" {
		t.Errorf("row = %q", lines[1])
	}
}

func TestTableRenderRaggedAndHeaderless(t *testing.T) {
	// Rows wider than the header grow the widths; no header line is written.
	tb := Table{}
	tb.Add("a", "bb", "c")
	tb.Add("aaa")
	tb.Add()
	var b strings.Builder
	tb.Render(&b, off)
	if want := "a    bb  c\naaa\n\n"; b.String() != want {
		t.Errorf("Render\n got %q\nwant %q", b.String(), want)
	}
	// Last column right-aligned is still padded.
	tb = Table{Right: map[int]bool{1: true}}
	tb.Add("x", "1")
	tb.Add("y", "100")
	b.Reset()
	tb.Render(&b, off)
	if want := "x    1\ny  100\n"; b.String() != want {
		t.Errorf("right-aligned last column\n got %q\nwant %q", b.String(), want)
	}
}

func TestBar(t *testing.T) {
	cases := []struct {
		name          string
		pct, waived   float64
		w             int
		full, wv, rst int
	}{
		{"empty", 0, 0, 10, 0, 0, 10},
		{"full", 100, 0, 10, 10, 0, 0},
		{"half", 50, 0, 10, 5, 0, 5},
		{"rounds", 44, 0, 10, 4, 0, 6},
		{"rounds up", 45, 0, 10, 5, 0, 5},
		{"waived", 40, 20, 10, 4, 2, 4},
		{"waived clamped", 100, 30, 10, 10, 0, 0},
		{"rounding overflow clamped", 55, 45, 10, 6, 4, 0},
		{"default width", 50, 0, 0, 10, 0, 10},
		{"negative width default", 0, 0, -5, 0, 0, 20},
	}
	for _, c := range cases {
		got := Bar(off, c.pct, c.waived, c.w)
		want := strings.Repeat("█", c.full) + strings.Repeat("▒", c.wv) + strings.Repeat("░", c.rst)
		if got != want {
			t.Errorf("%s: Bar(%v,%v,%d) = %q, want %q", c.name, c.pct, c.waived, c.w, got, want)
		}
	}
}

func TestBarColors(t *testing.T) {
	cases := []struct {
		pct, waived float64
		color       Style
	}{
		{80, 0, Green}, {60, 20, Green}, {79, 0, Yellow}, {50, 0, Yellow}, {49, 0, Red}, {0, 0, Red},
	}
	for _, c := range cases {
		got := Bar(on, c.pct, c.waived, 10)
		if c.pct > 0 && !strings.HasPrefix(got, string(c.color)+"█") {
			t.Errorf("Bar(%v,%v) = %q, want filled part in %q", c.pct, c.waived, got, c.color)
		}
		if c.pct == 0 && strings.Contains(got, "█") {
			t.Errorf("Bar(0) should have no filled part: %q", got)
		}
	}
}

func TestBarOutOfRange(t *testing.T) {
	for _, tc := range []struct{ pct, waived float64 }{{150, 0}, {-20, 0}, {-20, 50}, {90, 40}, {0, 150}, {50, -30}} {
		if got := Width(Bar(off, tc.pct, tc.waived, 10)); got != 10 {
			t.Errorf("Bar(%v, %v) is %d wide, want 10", tc.pct, tc.waived, got)
		}
	}
}

func TestStatusIcon(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range model.Statuses {
		icon, c := StatusIcon(s)
		if icon == "?" || c == "" {
			t.Errorf("status %s has no icon", s)
		}
		if seen[icon] {
			t.Errorf("icon %q reused for %s", icon, s)
		}
		seen[icon] = true
	}
	if icon, c := StatusIcon("bogus"); icon != "?" || c != Gray {
		t.Errorf("unknown status icon = %q %q", icon, c)
	}
	if got := off.StatusLabel(model.Drifted); got != "▲ drifted" {
		t.Errorf("StatusLabel = %q", got)
	}
	if got := on.StatusLabel(model.OK); got != string(Green)+"✓ ok"+string(Reset) {
		t.Errorf("styled StatusLabel = %q", got)
	}
}

func TestPercent(t *testing.T) {
	cases := []struct {
		pct   float64
		text  string
		color Style
	}{
		{100, "100.0%", Green},
		{80, " 80.0%", Green},
		{79.96, " 80.0%", Yellow}, // colour from the value, not the rounded text
		{50, " 50.0%", Yellow},
		{49.9, " 49.9%", Red},
		{0, "  0.0%", Red},
	}
	for _, c := range cases {
		if got := off.Percent(c.pct); got != c.text {
			t.Errorf("Percent(%v) = %q, want %q", c.pct, got, c.text)
		}
		if got := on.Percent(c.pct); got != string(c.color)+c.text+string(Reset) {
			t.Errorf("Percent(%v) styled = %q", c.pct, got)
		}
	}
}

func TestDiff(t *testing.T) {
	d := "--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n ctx\n-old\n+new\n"
	if got := off.Diff(d); got != d {
		t.Errorf("Diff off should be identity, got %q", got)
	}
	want := on.S("--- a/x", Bold) + "\n" +
		on.S("+++ b/x", Bold) + "\n" +
		on.S("@@ -1,2 +1,2 @@", Cyan) + "\n" +
		" ctx\n" +
		on.S("-old", Red) + "\n" +
		on.S("+new", Green) + "\n"
	if got := on.Diff(d); got != want {
		t.Errorf("Diff\n got %q\nwant %q", got, want)
	}
	// Missing trailing newline gets one; CRLF content is passed through.
	if got := on.Diff("+a"); got != on.S("+a", Green)+"\n" {
		t.Errorf("Diff without newline = %q", got)
	}
	if got := on.Diff("-a\r\n"); got != on.S("-a\r", Red)+"\n" {
		t.Errorf("Diff CRLF = %q", got)
	}
}

func TestShortHash(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "-"},
		{"abc", "abc"},
		{"0123456789", "0123456789"},
		{"0123456789abcdef", "0123456789"},
	}
	for _, c := range cases {
		if got := ShortHash(c.in); got != c.want {
			t.Errorf("ShortHash(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
