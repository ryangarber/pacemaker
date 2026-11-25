package diff

import (
	"strings"
	"testing"
)

func TestMapRange(t *testing.T) {
	old := strings.Split("a b c d e f g", " ")
	// Insert two lines at top, modify "e".
	cur := strings.Split("x y a b c d E f g", " ")
	ed := Lines(old, cur)
	if s, e, ok := MapRange(ed, len(old), len(cur), 3, 4); !ok || s != 5 || e != 6 {
		t.Errorf("moved range = %d-%d %v, want 5-6", s, e, ok)
	}
	// Range whose last line was modified extends over the modification.
	if s, e, ok := MapRange(ed, len(old), len(cur), 4, 5); !ok || s != 6 || e != 7 {
		t.Errorf("edited range = %d-%d %v, want 6-7", s, e, ok)
	}
	// Deleted range.
	cur2 := strings.Split("a b f g", " ")
	if _, _, ok := MapRange(Lines(old, cur2), len(old), len(cur2), 3, 5); ok {
		t.Error("expected deleted range to be reported missing")
	}
}

func TestUnified(t *testing.T) {
	d := Unified([]string{"a", "b", "c"}, []string{"a", "B", "c"}, "old", "new", 10, 20, 1)
	want := "--- old\n+++ new\n@@ -10,3 +20,3 @@\n a\n-b\n+B\n c\n"
	if d != want {
		t.Errorf("got:\n%s\nwant:\n%s", d, want)
	}
	if Unified([]string{"a"}, []string{"a"}, "", "", 1, 1, 3) != "" {
		t.Error("identical input should give empty diff")
	}
}

func TestLinesRoundTrip(t *testing.T) {
	a := strings.Split("the quick brown fox jumps over the lazy dog", " ")
	b := strings.Split("a quick red fox leaps over the dog today", " ")
	var out []string
	for _, e := range Lines(a, b) {
		if e.Op != Delete {
			out = append(out, b[e.B])
		}
	}
	if strings.Join(out, " ") != strings.Join(b, " ") {
		t.Fatalf("edit script does not reproduce b: %v", out)
	}
}
