package glob

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"lib/**/*.dart", "lib/src/a.dart", true},
		{"lib/**/*.dart", "lib/a.dart", true},
		{"lib/**/*.dart", "test/a.dart", false},
		{"*.g.dart", "lib/src/a.g.dart", true},
		{"*.g.dart", "lib/src/a.dart", false},
		{"node_modules/", "a/node_modules/x/y.js", true},
		{"build/**", "build/out.js", true},
		{"/src/*.ts", "src/a.ts", true},
		{"src/*.ts", "src/sub/a.ts", false},
		{"**", "anything/at/all", true},
	}
	for _, c := range cases {
		if got := Match(c.pat, c.name); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pat, c.name, got, c.want)
		}
	}
}

func TestSet(t *testing.T) {
	s := Set{Include: []string{"lib/**"}, Exclude: []string{"*.g.dart"}}
	if !s.Match("lib/a.dart") || s.Match("lib/a.g.dart") || s.Match("web/a.dart") {
		t.Fatal("unexpected set matching")
	}
	if !(Set{Exclude: []string{"node_modules/"}}).ExcludesDir("pkg/node_modules") {
		t.Fatal("expected dir exclusion")
	}
}
