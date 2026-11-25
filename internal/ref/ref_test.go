package ref

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Ref
		str  string
	}{
		{"lib/a.dart", Ref{Path: "lib/a.dart"}, "lib/a.dart"},
		{"up:lib/a.dart#L3-L9", Ref{Target: "up", Path: "lib/a.dart", Lines: Range{3, 9}}, "up:lib/a.dart#L3-L9"},
		{"lib/a.dart#L4", Ref{Path: "lib/a.dart", Lines: Range{4, 4}}, "lib/a.dart#L4"},
		{"lib/a.dart#L4-12", Ref{Path: "lib/a.dart", Lines: Range{4, 12}}, "lib/a.dart#L4-L12"},
		{"web:src/c.ts#Client.get", Ref{Target: "web", Path: "src/c.ts", Symbol: "Client.get"}, "web:src/c.ts#Client.get"},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
		if got.String() != c.str {
			t.Errorf("String() = %q, want %q", got.String(), c.str)
		}
	}
	for _, bad := range []string{"", "a#L9-L3", "up:"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}
