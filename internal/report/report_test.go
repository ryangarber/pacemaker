package report

import (
	"io"
	"strings"
	"testing"

	"github.com/ryangarber/pacemaker/internal/model"
)

// fake is a minimal renderer used to exercise the registry without
// depending on the real renderer sub-packages.
type fake struct {
	name string
	exts []string
}

func (f fake) Name() string                                   { return f.name }
func (f fake) Description() string                            { return "fake " + f.name }
func (f fake) Extensions() []string                           { return f.exts }
func (f fake) Render(io.Writer, *model.Report, Options) error { return nil }

// The registry is package-global; names are prefixed so they cannot clash
// with anything else registered in this test binary.
func init() {
	Register(fake{"zz-beta", []string{".zzb", ".zzshared"}}, "zzb", "ZZ-UPPER-ALIAS")
	Register(fake{"zz-alpha", []string{".zza", ".zzshared"}}, "zza")
}

func TestGetByNameAndAlias(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"zz-alpha", "zz-alpha"},
		{"ZZ-Alpha", "zz-alpha"}, // case-insensitive
		{"zza", "zz-alpha"},
		{"zzb", "zz-beta"},
		{"ZZB", "zz-beta"},
	} {
		r, err := Get(tc.in)
		if err != nil {
			t.Fatalf("Get(%q): %v", tc.in, err)
		}
		if r.Name() != tc.want {
			t.Errorf("Get(%q) = %q, want %q", tc.in, r.Name(), tc.want)
		}
	}
}

func TestGetAliasRegisteredInUpperCaseIsUnreachable(t *testing.T) {
	// Get lower-cases its input before the alias lookup, so an alias
	// registered with capitals can never match. Documents current behaviour.
	if _, err := Get("ZZ-UPPER-ALIAS"); err == nil {
		t.Fatal("expected upper-case alias to be unreachable")
	}
}

func TestGetUnknownListsAvailable(t *testing.T) {
	_, err := Get("nope")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"nope"`) {
		t.Errorf("error does not name the format: %s", msg)
	}
	a, b := strings.Index(msg, "zz-alpha"), strings.Index(msg, "zz-beta")
	if a < 0 || b < 0 || a > b {
		t.Errorf("error should list available formats sorted: %s", msg)
	}
}

func TestForFile(t *testing.T) {
	r, ok := ForFile("out/REPORT.ZZB")
	if !ok || r.Name() != "zz-beta" {
		t.Fatalf("ForFile(.ZZB) = %v, %v", r, ok)
	}
	// Two renderers claim .zzshared: the first by sorted name wins, every time.
	for range 5 {
		r, ok = ForFile("x.zzshared")
		if !ok || r.Name() != "zz-alpha" {
			t.Fatalf("ForFile(.zzshared) = %v, %v; want zz-alpha", r, ok)
		}
	}
	if _, ok := ForFile("x.unknownext"); ok {
		t.Error("unknown extension matched")
	}
	if _, ok := ForFile("noext"); ok {
		t.Error("extension-less path matched")
	}
}

func TestAllSorted(t *testing.T) {
	all := All()
	for i := 1; i < len(all); i++ {
		if all[i-1].Name() >= all[i].Name() {
			t.Fatalf("All not sorted: %q before %q", all[i-1].Name(), all[i].Name())
		}
	}
	var seen int
	for _, r := range all {
		if strings.HasPrefix(r.Name(), "zz-") {
			seen++
		}
	}
	if seen != 2 {
		t.Errorf("All returned %d fake renderers, want 2", seen)
	}
}

func TestVisible(t *testing.T) {
	r := &model.Report{Links: []*model.Link{
		{Block: "a", Status: model.Drifted},
		{Block: "b", Status: model.OK},
		{Block: "c", Status: model.New},
		{Block: "d", Status: model.OK},
	}}
	blocks := func(ls []*model.Link) string {
		var s []string
		for _, l := range ls {
			s = append(s, l.Block)
		}
		return strings.Join(s, ",")
	}
	if got := blocks(Visible(r, Options{})); got != "a,c" {
		t.Errorf("hidden ok: got %s", got)
	}
	if got := blocks(Visible(r, Options{ShowOK: true})); got != "a,b,c,d" {
		t.Errorf("ShowOK: got %s", got)
	}
	// Visible filters; it hands back the report's own links, not copies.
	if v := Visible(r, Options{ShowOK: true}); v[1] != r.Links[1] {
		t.Error("Visible copied links")
	}
	if v := Visible(&model.Report{}, Options{}); len(v) != 0 {
		t.Errorf("empty report: %v", v)
	}
}
