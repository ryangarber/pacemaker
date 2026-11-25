package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(root), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultIsValid(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if c.Version != CurVersion || c.Marker != "@pace" || c.Drift.Cosmetic != "warn" ||
		c.Drift.Context != 3 || c.Drift.MaxCommits != 10 || c.Report.Format != "markdown" {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if want := []string{"drifted", "missing", "new", "error"}; !reflect.DeepEqual(c.Drift.FailOn, want) {
		t.Errorf("drift.fail_on = %v, want %v", c.Drift.FailOn, want)
	}
	if c.Targets == nil || c.Todo.FailOn == nil {
		t.Errorf("default maps/slices should be non-nil: %+v", c)
	}
	for _, ex := range []string{".git/", ".pacemaker/", "node_modules/"} {
		found := false
		for _, e := range c.Local.Exclude {
			found = found || e == ex
		}
		if !found {
			t.Errorf("default local.exclude missing %q: %v", ex, c.Local.Exclude)
		}
	}
	// Each call returns an independent value.
	Default().Drift.FailOn[0] = "mutated"
	if Default().Drift.FailOn[0] != "drifted" {
		t.Error("Default shares state between calls")
	}
}

func TestTargetLocationAndSpec(t *testing.T) {
	u := Target{URL: "https://example.com/r.git", Rev: "v1"}
	if u.Location() != u.URL {
		t.Errorf("Location = %q", u.Location())
	}
	p := Target{Path: "../up"}
	if p.Location() != "../up" {
		t.Errorf("Location = %q", p.Location())
	}
	s := u.Spec("up")
	if s.Name != "up" || s.URL != u.URL || s.Rev != "v1" || s.Path != "" {
		t.Errorf("Spec = %+v", s)
	}
	f := Filter{Include: []string{"lib/**"}, Exclude: []string{"*.g.dart"}}
	gs := f.Set()
	if !reflect.DeepEqual(gs.Include, f.Include) || !reflect.DeepEqual(gs.Exclude, f.Exclude) {
		t.Errorf("Set = %+v", gs)
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "version: 1\n")
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, deep} {
		got, err := Find(dir)
		if err != nil {
			t.Fatalf("Find(%s): %v", dir, err)
		}
		if got != root {
			t.Errorf("Find(%s) = %s, want %s", dir, got, root)
		}
	}

	// A .pacemaker directory without config.yaml (or with config.yaml as a
	// directory) is not a project root.
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, Dir, File), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(other); err == nil || !strings.Contains(err.Error(), "pacemaker init") {
		t.Errorf("Find without config: err = %v", err)
	}
}

func TestLoadDefaultsAndOverrides(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `version: 1
marker: "@sync"
default_target: up
targets:
  up:
    path: ../upstream
    include: [lib/**]
drift:
  cosmetic: fail
  fail_on: [missing]
`)
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Marker != "@sync" || c.DefaultTarget != "up" || c.Drift.Cosmetic != "fail" {
		t.Errorf("overrides not applied: %+v", c)
	}
	if !reflect.DeepEqual(c.Drift.FailOn, []string{"missing"}) {
		t.Errorf("drift.fail_on = %v, want replaced by [missing]", c.Drift.FailOn)
	}
	// Unset fields keep their defaults.
	if c.Drift.Context != 3 || c.Drift.MaxCommits != 10 || c.Report.Format != "markdown" {
		t.Errorf("defaults lost: %+v", c)
	}
	up := c.Targets["up"]
	if up.Path != "../upstream" || !reflect.DeepEqual(up.Include, []string{"lib/**"}) {
		t.Errorf("target = %+v", up)
	}
	// Local filters are not defaulted on load: only what the file says.
	if len(c.Local.Include) != 0 || len(c.Local.Exclude) != 0 {
		t.Errorf("local should be empty when absent from file: %+v", c.Local)
	}
}

func TestLoadMinimal(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "version: 1\ntargets:\n")
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Targets == nil {
		t.Error("Targets should be non-nil after load")
	}
	if c.Marker != "@pace" {
		t.Errorf("marker = %q", c.Marker)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"bad yaml", "version: [\n", "config.yaml"},
		{"wrong type", "drift:\n  context: lots\n", "config.yaml"},
		{"both url and path", "targets:\n  up:\n    url: https://x/y.git\n    path: ../y\n", `target "up": exactly one of url or path must be set`},
		{"neither url nor path", "targets:\n  up:\n    rev: main\n", `target "up": exactly one of url or path must be set`},
		{"unknown default target", "default_target: nope\n", `default_target "nope" is not a configured target`},
		{"bad cosmetic", "drift:\n  cosmetic: maybe\n", `drift.cosmetic must be ok, warn or fail (got "maybe")`},
		{"empty cosmetic", "drift:\n  cosmetic: \"\"\n", `drift.cosmetic must be ok, warn or fail (got "")`},
		{"bad todo fail_on", "todo:\n  fail_on: [high, urgent]\n", `todo.fail_on: unknown value "urgent" (use any, blocker, high, normal, low, stale, review)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, tc.body)
			_, err := Load(root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}

	if _, err := Load(t.TempDir()); !os.IsNotExist(err) {
		t.Errorf("missing file: err = %v, want not-exist", err)
	}
}

func TestValidateAccepts(t *testing.T) {
	for _, cos := range []string{"ok", "warn", "fail"} {
		c := Default()
		c.Drift.Cosmetic = cos
		c.Todo.FailOn = append([]string(nil), TodoFailOnValues...)
		if err := c.Validate(); err != nil {
			t.Errorf("cosmetic %q: %v", cos, err)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	c := Default()
	c.Targets["up"] = Target{URL: "https://example.com/u.git", Rev: "main",
		Filter: Filter{Include: []string{"lib/**"}, Exclude: []string{"*_test.go"}}}
	c.Targets["local"] = Target{Path: "../other"}
	c.DefaultTarget = "up"
	c.Todo.FailOn = []string{"blocker", "stale"}
	c.Report.Title = "Drift"
	if err := c.Save(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "# pacemaker configuration") {
		t.Errorf("missing header comment:\n%s", b)
	}
	if !strings.Contains(string(b), "\n  up:\n") {
		t.Errorf("expected 2-space indentation:\n%s", b)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, c)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	root := t.TempDir()
	c := Default()
	c.Drift.Cosmetic = "loud"
	if err := c.Save(root); err == nil {
		t.Fatal("expected validation error")
	}
	if _, err := os.Stat(Path(root)); !os.IsNotExist(err) {
		t.Errorf("invalid config should not be written (stat err %v)", err)
	}
}

func TestResolveTarget(t *testing.T) {
	one := Default()
	one.Targets["only"] = Target{Path: "x"}
	two := Default()
	two.Targets["a"] = Target{Path: "a"}
	two.Targets["b"] = Target{Path: "b"}
	twoDef := Default()
	twoDef.Targets["a"] = Target{Path: "a"}
	twoDef.Targets["b"] = Target{Path: "b"}
	twoDef.DefaultTarget = "b"

	cases := []struct {
		name    string
		c       *Config
		arg     string
		want    string
		wantErr string
	}{
		{"single implicit", one, "", "only", ""},
		{"single explicit", one, "only", "only", ""},
		{"single unknown", one, "nope", "", `unknown target "nope"`},
		{"none", Default(), "", "", "no default target configured"},
		{"ambiguous", two, "", "", "no default target configured"},
		{"explicit", two, "a", "a", ""},
		{"default", twoDef, "", "b", ""},
		{"explicit beats default", twoDef, "a", "a", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.c.ResolveTarget(tc.arg)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestTargetNamesSorted(t *testing.T) {
	c := Default()
	if len(c.TargetNames()) != 0 {
		t.Errorf("TargetNames on empty = %v", c.TargetNames())
	}
	for _, n := range []string{"zeta", "alpha", "mid"} {
		c.Targets[n] = Target{Path: n}
	}
	if got := c.TargetNames(); !reflect.DeepEqual(got, []string{"alpha", "mid", "zeta"}) {
		t.Errorf("TargetNames = %v", got)
	}
}

func TestGet(t *testing.T) {
	c := Default()
	c.Targets["up"] = Target{Path: "../up", Rev: "main"}
	cases := []struct {
		key     string
		want    any
		wantErr string
	}{
		{"marker", "@pace", ""},
		{"drift.context", 3, ""},
		{".drift.max_commits.", 10, ""},
		{"targets.up.rev", "main", ""},
		{"drift.fail_on", []any{"drifted", "missing", "new", "error"}, ""},
		{"targets.up.url", nil, "targets.up.url: not set"},
		{"nope", nil, "nope: not set"},
		{"marker.sub", nil, "marker.sub: not a map"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got, err := c.Get(tc.key)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Get(%q) = %#v, %v; want %#v", tc.key, got, err, tc.want)
			}
		})
	}
}

func TestSet(t *testing.T) {
	cases := []struct {
		name, key, value string
		check            func(*Config) bool
	}{
		{"string", "marker", "@sync", func(c *Config) bool { return c.Marker == "@sync" }},
		{"int", "drift.context", "7", func(c *Config) bool { return c.Drift.Context == 7 }},
		{"enum", "drift.cosmetic", "ok", func(c *Config) bool { return c.Drift.Cosmetic == "ok" }},
		{"list comma", "drift.fail_on", "drifted, missing", func(c *Config) bool {
			return reflect.DeepEqual(c.Drift.FailOn, []string{"drifted", "missing"})
		}},
		{"list single", "drift.fail_on", "error", func(c *Config) bool {
			return reflect.DeepEqual(c.Drift.FailOn, []string{"error"})
		}},
		{"list bracketed", "todo.fail_on", "[high,stale]", func(c *Config) bool {
			return reflect.DeepEqual(c.Todo.FailOn, []string{"high", "stale"})
		}},
		{"list empty", "drift.fail_on", "[]", func(c *Config) bool { return len(c.Drift.FailOn) == 0 }},
		{"new target creates map", "targets.up.path", "../up", func(c *Config) bool {
			return c.Targets["up"].Path == "../up"
		}},
		{"target filter list", "local.include", "[lib/**, src/**]", func(c *Config) bool {
			return reflect.DeepEqual(c.Local.Include, []string{"lib/**", "src/**"})
		}},
		{"bool-looking string into string field", "report.title", "true", func(c *Config) bool {
			return c.Report.Title == "true"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			if err := c.Set(tc.key, tc.value); err != nil {
				t.Fatalf("Set(%q, %q): %v", tc.key, tc.value, err)
			}
			if !tc.check(c) {
				t.Errorf("Set(%q, %q) gave %+v", tc.key, tc.value, c)
			}
		})
	}
}

func TestSetErrorsLeaveConfigUnchanged(t *testing.T) {
	cases := []struct {
		name, key, value, want string
	}{
		{"unknown key", "drift.colour", "red", "colour"},
		{"unknown top key", "bogus", "1", "bogus"},
		{"wrong type", "drift.context", "many", "cannot unmarshal"},
		{"invalid enum", "drift.cosmetic", "loud", "drift.cosmetic must be"},
		{"invalid todo", "todo.fail_on", "urgent", `unknown value "urgent"`},
		{"target without location", "targets.up.rev", "main", "exactly one of url or path"},
		{"dangling default", "default_target", "ghost", `default_target "ghost"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			before := *Default()
			err := c.Set(tc.key, tc.value)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
			if !reflect.DeepEqual(*c, before) {
				t.Errorf("config modified on error: %+v", c)
			}
		})
	}
}

func TestSetThenSaveLoad(t *testing.T) {
	root := t.TempDir()
	c := Default()
	for _, kv := range [][2]string{
		{"targets.up.url", "git@example.com:org/up.git"},
		{"targets.up.rev", "v2.1"},
		{"default_target", "up"},
		{"drift.max_commits", "25"},
	} {
		if err := c.Set(kv[0], kv[1]); err != nil {
			t.Fatalf("Set %v: %v", kv, err)
		}
	}
	if err := c.Save(root); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Errorf("mismatch:\n got %+v\nwant %+v", got, c)
	}
}

func TestUnset(t *testing.T) {
	c := Default()
	c.Targets["up"] = Target{Path: "../up", Rev: "main"}
	c.DefaultTarget = "up"
	if err := c.Unset("targets.up.rev"); err != nil {
		t.Fatal(err)
	}
	if c.Targets["up"].Rev != "" || c.Targets["up"].Path != "../up" {
		t.Errorf("after unset rev: %+v", c.Targets["up"])
	}
	// Removing a target still named as the default is refused.
	if err := c.Unset("targets.up"); err == nil {
		t.Error("expected error removing the default target")
	}
	if _, ok := c.Targets["up"]; !ok {
		t.Error("target removed despite error")
	}
	if err := c.Unset("default_target"); err != nil {
		t.Fatal(err)
	}
	if err := c.Unset("targets.up"); err != nil {
		t.Fatal(err)
	}
	if len(c.Targets) != 0 || c.Targets == nil {
		t.Errorf("targets = %#v", c.Targets)
	}
	// Unsetting a key with a default reverts it to the value Load would give
	// a config.yaml without it.
	c.Drift = Drift{Cosmetic: "fail", FailOn: []string{"drifted"}, Context: 9, MaxCommits: 1}
	c.Local = Filter{Exclude: []string{"x/"}}
	c.Report.Format = "html"
	for _, k := range []string{"drift.cosmetic", "drift.fail_on", "drift.context", "drift", "local.exclude", "report.format"} {
		if err := c.Unset(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
	}
	if d := Default(); !reflect.DeepEqual(c.Drift, d.Drift) || c.Report.Format != d.Report.Format {
		t.Errorf("after unset: drift %+v report %+v, want the defaults", c.Drift, c.Report)
	}
	if len(c.Local.Exclude) != 0 {
		t.Errorf("local.exclude after unset = %q; Load does not default it, so neither does Unset", c.Local.Exclude)
	}
	// Unsetting something absent is a no-op.
	if err := c.Unset("report.output"); err != nil {
		t.Errorf("unset absent key: %v", err)
	}
}

func TestKeys(t *testing.T) {
	c := Default()
	c.Targets["up"] = Target{Path: "../up", Rev: "main"}
	keys := c.Keys()
	has := map[string]bool{}
	for _, k := range keys {
		has[k] = true
	}
	for _, k := range []string{"version", "marker", "local.exclude", "targets.up.path", "targets.up.rev",
		"drift.cosmetic", "drift.fail_on", "drift.context", "drift.max_commits", "todo.fail_on", "report.format"} {
		if !has[k] {
			t.Errorf("Keys missing %q: %v", k, keys)
		}
	}
	for _, k := range []string{"default_target", "targets.up.url", "report.output", "targets", "drift"} {
		if has[k] {
			t.Errorf("Keys unexpectedly has %q", k)
		}
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Fatalf("Keys not sorted: %v", keys)
		}
	}
	// An empty map is itself a leaf key.
	if k := Default().Keys(); !contains(k, "targets") {
		t.Errorf("empty targets should be listed as a leaf: %v", k)
	}
	// Every listed key can be read back.
	for _, k := range keys {
		if _, err := c.Get(k); err != nil {
			t.Errorf("Get(%q): %v", k, err)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestSetNumericLookingString(t *testing.T) {
	// A numeric-looking rev (e.g. an all-digit short hash with a leading
	// zero) must survive as typed.
	c := Default()
	if err := c.Set("targets.up.path", "../up"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("targets.up.rev", "0123456"); err != nil {
		t.Fatal(err)
	}
	if got := c.Targets["up"].Rev; got != "0123456" {
		t.Errorf("rev = %q, want %q", got, "0123456")
	}
}
