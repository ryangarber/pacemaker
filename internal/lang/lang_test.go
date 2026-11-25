package lang

import (
	"reflect"
	"sync"
	"testing"
)

// fake modules exercising the optional capability interfaces.
type bare struct {
	id   string
	exts []string
}

func (b bare) ID() string           { return b.id }
func (b bare) Extensions() []string { return b.exts }

type full struct{ bare }

func (full) Normalize(src []byte) []byte             { return src }
func (full) SignificantLines(src []byte) []bool      { return nil }
func (full) Symbols(src []byte) []Symbol             { return nil }
func (full) Compare(oldSrc, newSrc []byte) []Finding { return nil }

type symbolsOnly struct{ bare }

func (symbolsOnly) Symbols(src []byte) []Symbol { return nil }

// withRegistry swaps in an empty registry for the duration of a test.
func withRegistry(t *testing.T) {
	t.Helper()
	mu.Lock()
	savedReg, savedFb := registry, fallback
	registry, fallback = nil, nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		registry, fallback = savedReg, savedFb
		mu.Unlock()
	})
}

func TestForPath(t *testing.T) {
	withRegistry(t)
	if got := ForPath("a.go"); got != nil {
		t.Fatalf("ForPath with empty registry and no fallback = %v, want nil", got)
	}
	fb := bare{id: "fallback"}
	dart := bare{id: "dart", exts: []string{".dart"}}
	js := bare{id: "js", exts: []string{".js", ".mjs", ".ts"}}
	SetFallback(fb)
	Register(dart)
	Register(js)

	cases := []struct {
		path string
		want string
	}{
		{"lib/a.dart", "dart"},
		{"a.dart", "dart"},
		{"LIB/A.DART", "dart"}, // extension match is case-insensitive
		{"web/app.Mjs", "js"},
		{"src/x.ts", "js"},
		{"a.dart.bak", "fallback"}, // only the last extension counts
		{"Makefile", "fallback"},
		{"", "fallback"},
		{"dir.dart/file", "fallback"}, // directory extension is ignored
		{".dart", "dart"},             // dotfile: path.Ext sees ".dart"
		{"a.", "fallback"},
	}
	for _, c := range cases {
		got := ForPath(c.path)
		if got == nil || got.ID() != c.want {
			t.Errorf("ForPath(%q) = %v, want %s", c.path, got, c.want)
		}
	}
}

func TestForPathLaterRegistrationWins(t *testing.T) {
	withRegistry(t)
	Register(bare{id: "first", exts: []string{".x"}})
	Register(bare{id: "second", exts: []string{".y", ".x"}})
	if got := ForPath("f.x").ID(); got != "second" {
		t.Errorf("shared extension resolved to %s, want later registration", got)
	}
	Register(bare{id: "third", exts: []string{".x"}})
	if got := ForPath("f.x").ID(); got != "third" {
		t.Errorf("shared extension resolved to %s, want third", got)
	}
	if got := ForPath("f.y").ID(); got != "second" {
		t.Errorf("unshared extension resolved to %s, want second", got)
	}
}

func TestAllAndFallback(t *testing.T) {
	withRegistry(t)
	if got := All(); len(got) != 0 {
		t.Fatalf("All on empty registry = %v", got)
	}
	if Fallback() != nil {
		t.Fatal("Fallback should be nil before SetFallback")
	}
	fb := bare{id: "generic"}
	SetFallback(fb)
	Register(bare{id: "zeta"})
	Register(bare{id: "alpha"})
	Register(bare{id: "mid"})
	var ids []string
	for _, l := range All() {
		ids = append(ids, l.ID())
	}
	if want := []string{"alpha", "mid", "zeta"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("All ids = %v, want %v (sorted, fallback excluded)", ids, want)
	}
	if Fallback().ID() != "generic" {
		t.Errorf("Fallback = %v", Fallback())
	}
	// All returns a copy: mutating it must not touch the registry.
	a := All()
	a[0] = bare{id: "hijack"}
	if All()[0].ID() != "alpha" {
		t.Error("All exposes the internal registry slice")
	}
	// SetFallback replaces.
	SetFallback(bare{id: "other"})
	if Fallback().ID() != "other" {
		t.Error("SetFallback did not replace the fallback")
	}
}

func TestRegistryConcurrent(t *testing.T) {
	withRegistry(t)
	SetFallback(bare{id: "fb"})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); Register(bare{id: "m", exts: []string{".m"}}) }()
		go func() { defer wg.Done(); _ = ForPath("a.m"); _ = All() }()
	}
	wg.Wait()
	if len(All()) != 20 {
		t.Errorf("registered %d modules, want 20", len(All()))
	}
}

func TestCapabilities(t *testing.T) {
	cases := []struct {
		name string
		l    Language
		want []string
	}{
		{"bare", bare{id: "b"}, nil},
		{"symbols only", symbolsOnly{bare{id: "s"}}, []string{"symbols"}},
		{"full, in fixed order", full{bare{id: "f"}}, []string{"normalize", "lines", "symbols", "semantic-diff"}},
	}
	for _, c := range cases {
		if got := Capabilities(c.l); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Capabilities = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSeverityRank(t *testing.T) {
	cases := []struct {
		s    Severity
		want int
	}{
		{SevBreaking, 3}, {SevMajor, 2}, {SevMinor, 1}, {SevInfo, 0}, {"", 0}, {"bogus", 0}, {"Breaking", 0},
	}
	for _, c := range cases {
		if got := c.s.Rank(); got != c.want {
			t.Errorf("Severity(%q).Rank() = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestFindSymbol(t *testing.T) {
	syms := []Symbol{
		{Name: "Auth", Kind: "class", Start: 3, End: 20},
		{Name: "Auth.login", Kind: "method", Start: 5, End: 8},
		{Name: "Auth.login", Kind: "method", Start: 10, End: 12}, // overload
		{Name: "Auth.token", Kind: "getter", Start: 14, End: 14},
		{Name: "Session.token", Kind: "getter", Start: 30, End: 31},
		{Name: "main", Kind: "function", Start: 40, End: 42},
		{Name: "Outer.Inner.run", Kind: "method", Start: 50, End: 55},
		{Name: "logout", Kind: "function", Start: 60, End: 61},
	}
	cases := []struct {
		name       string
		ok         bool
		start, end int
		kind       string
	}{
		{"Auth", true, 3, 20, "class"},
		{"main", true, 40, 42, "function"},
		{"Auth.login", true, 5, 12, "method"}, // overloads merged into one span
		{"login", true, 5, 12, "method"},      // unique suffix, overloads merged
		{"token", false, 0, 0, ""},            // ambiguous suffix
		{"Session.token", true, 30, 31, "getter"},
		{"run", true, 50, 55, "method"},
		{"Inner.run", true, 50, 55, "method"},
		{"out", false, 0, 0, ""},  // suffix must follow a dot
		{"ogin", false, 0, 0, ""}, // no partial-identifier match
		{"Nope", false, 0, 0, ""},
		{"auth", false, 0, 0, ""}, // case-sensitive
	}
	for _, c := range cases {
		got, ok := FindSymbol(syms, c.name)
		if ok != c.ok {
			t.Errorf("FindSymbol(%q) ok = %v, want %v (got %+v)", c.name, ok, c.ok, got)
			continue
		}
		if !ok {
			if !reflect.DeepEqual(got, Symbol{}) {
				t.Errorf("FindSymbol(%q) miss returned non-zero %+v", c.name, got)
			}
			continue
		}
		if got.Start != c.start || got.End != c.end || got.Kind != c.kind {
			t.Errorf("FindSymbol(%q) = %+v, want %d-%d %s", c.name, got, c.start, c.end, c.kind)
		}
	}
	if _, ok := FindSymbol(nil, "x"); ok {
		t.Error("FindSymbol on nil list should miss")
	}
}

func TestFindSymbolExactBeatsSuffix(t *testing.T) {
	syms := []Symbol{
		{Name: "A.run", Start: 1, End: 2},
		{Name: "run", Start: 10, End: 12},
	}
	got, ok := FindSymbol(syms, "run")
	if !ok || got.Start != 10 || got.End != 12 {
		t.Errorf("exact match should win over suffix match, got %+v ok=%v", got, ok)
	}
}

func TestFindSymbolOverloadSpanOrderIndependent(t *testing.T) {
	syms := []Symbol{
		{Name: "f", Start: 20, End: 25, Signature: "f(int)"},
		{Name: "f", Start: 2, End: 4, Signature: "f()"},
		{Name: "f", Start: 10, End: 30},
	}
	got, ok := FindSymbol(syms, "f")
	if !ok || got.Start != 2 || got.End != 30 {
		t.Errorf("merged span = %d-%d, want 2-30", got.Start, got.End)
	}
	if got.Signature != "f(int)" {
		t.Errorf("merged symbol should keep the first hit's other fields, got %+v", got)
	}
}
