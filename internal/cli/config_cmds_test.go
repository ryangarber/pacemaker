package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// project creates a configured project with one path target "upstream" and
// returns its root and the upstream directory.
func project(t *testing.T) (proj, up string) {
	t.Helper()
	dir := t.TempDir()
	up, proj = filepath.Join(dir, "up"), filepath.Join(dir, "proj")
	if err := os.MkdirAll(up, 0o755); err != nil {
		t.Fatal(err)
	}
	must(t, 0, "-C", proj, "init", up)
	return proj, up
}

func get(t *testing.T, proj, key string) string {
	t.Helper()
	return pm(t, proj, 0, "config", "get", key).out
}

// ---------------------------------------------------------------- config

func TestConfigShowAndPath(t *testing.T) {
	proj, up := project(t)
	r := pm(t, proj, 0, "config", "show")
	has(t, "show", r.out, "marker: '@pace'", "default_target: upstream", "path: "+up, "cosmetic: warn")
	if strings.HasPrefix(r.out, "#") {
		t.Error("show prints the effective config, not the file with its header")
	}
	r = pm(t, proj, 0, "config", "path")
	if r.out != filepath.Join(proj, ".pacemaker", "config.yaml")+"\n" {
		t.Errorf("path = %q", r.out)
	}
	// The project is found from a subdirectory.
	sub := filepath.Join(proj, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := pm(t, sub, 0, "config", "path"); r.out != filepath.Join(proj, ".pacemaker", "config.yaml")+"\n" {
		t.Errorf("path from subdir = %q", r.out)
	}
	// --color keys are highlighted.
	r = must(t, 0, "-C", proj, "--color", "config", "show")
	has(t, "colored show", r.out, "\x1b[36mmarker\x1b[0m:")
}

func TestConfigGet(t *testing.T) {
	proj, _ := project(t)
	tests := []struct {
		key, want string
	}{
		{"drift.cosmetic", "warn\n"},
		{"drift.context", "3\n"},
		{"drift.fail_on", "- drifted\n- missing\n- new\n- error\n"},
		{".drift.cosmetic.", "warn\n"}, // surrounding dots are trimmed
		{"todo.fail_on", "[]\n"},
	}
	for _, tt := range tests {
		if got := get(t, proj, tt.key); got != tt.want {
			t.Errorf("config get %s = %q, want %q", tt.key, got, tt.want)
		}
	}
	r := pm(t, proj, 0, "config", "get", "drift")
	has(t, "map value", r.out, "cosmetic: warn\n", "context: 3\n")

	for _, tt := range []struct{ key, err string }{
		{"nope.x", "nope.x: not set"},
		{"drift.cosmetic.x", "drift.cosmetic.x: not a map"},
	} {
		r := pm(t, proj, 2, "config", "get", tt.key)
		has(t, tt.key, r.err, "error: "+tt.err)
	}
}

func TestConfigSet(t *testing.T) {
	proj, _ := project(t)
	cfgFile := filepath.Join(proj, ".pacemaker", "config.yaml")

	r := pm(t, proj, 0, "config", "set", "drift.cosmetic", "fail")
	has(t, "set", r.out, "✓ drift.cosmetic = fail")
	if get(t, proj, "drift.cosmetic") != "fail\n" {
		t.Error("drift.cosmetic not saved")
	}

	pm(t, proj, 0, "config", "set", "drift.fail_on", "drifted,missing")
	if got := get(t, proj, "drift.fail_on"); got != "- drifted\n- missing\n" {
		t.Errorf("list set = %q", got)
	}
	pm(t, proj, 0, "config", "set", "drift.context", "7")
	if got := get(t, proj, "drift.context"); got != "7\n" {
		t.Errorf("int set = %q", got)
	}
	// Remaining args are joined into one value.
	pm(t, proj, 0, "config", "set", "report.title", "Port", "status")
	if got := get(t, proj, "report.title"); got != "Port status\n" {
		t.Errorf("multi-word set = %q", got)
	}
	pm(t, proj, 0, "config", "set", "targets.upstream.rev", "main")
	if got := get(t, proj, "targets.upstream.rev"); got != "main\n" {
		t.Errorf("target rev = %q", got)
	}

	// Invalid values are rejected and nothing is written.
	before := read(t, cfgFile)
	for _, tt := range []struct {
		args []string
		err  string
	}{
		{[]string{"drift.cosmetic", "bogus"}, `drift.cosmetic must be ok, warn or fail (got "bogus")`},
		{[]string{"todo.fail_on", "sometimes"}, `todo.fail_on: unknown value "sometimes"`},
		{[]string{"drift.nope", "1"}, "field nope not found"},
		{[]string{"default_target", "ghost"}, `default_target "ghost" is not a configured target`},
		{[]string{"drift.context", "many"}, "cannot unmarshal"}, // the message does not name the key
	} {
		r := pm(t, proj, 2, append([]string{"config", "set"}, tt.args...)...)
		has(t, strings.Join(tt.args, " "), r.err, tt.err)
	}
	if read(t, cfgFile) != before {
		t.Error("a rejected set changed the config file")
	}
}

func TestConfigUnset(t *testing.T) {
	proj, _ := project(t)
	pm(t, proj, 0, "config", "set", "report.title", "X")
	r := pm(t, proj, 0, "config", "unset", "report.title")
	has(t, "unset", r.out, "✓ unset report.title")
	pm(t, proj, 2, "config", "get", "report.title")
}

func TestConfigUnsetRevertsToDefault(t *testing.T) {
	proj, _ := project(t)
	pm(t, proj, 0, "config", "set", "drift.cosmetic", "fail")
	pm(t, proj, 0, "config", "unset", "drift.cosmetic")
	if got := get(t, proj, "drift.cosmetic"); got != "warn\n" {
		t.Errorf("drift.cosmetic after unset = %q, want default warn", got)
	}
	pm(t, proj, 0, "config", "set", "drift.fail_on", "drifted")
	pm(t, proj, 0, "config", "unset", "drift.fail_on")
	if got := get(t, proj, "drift.fail_on"); got != "- drifted\n- missing\n- new\n- error\n" {
		t.Errorf("drift.fail_on after unset = %q, want the default list", got)
	}
}

func TestConfigKeys(t *testing.T) {
	proj, up := project(t)
	r := pm(t, proj, 0, "config", "keys")
	has(t, "keys", r.out, "drift.cosmetic", `"warn"`, "drift.context", "3", `"drifted","missing","new","error"`,
		"targets.upstream.path", `"`+up+`"`)
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	for i := 1; i < len(lines); i++ {
		if strings.Fields(lines[i-1])[0] > strings.Fields(lines[i])[0] {
			t.Errorf("keys not sorted: %q before %q", lines[i-1], lines[i])
		}
	}
}

func TestConfigArgCounts(t *testing.T) {
	proj, _ := project(t)
	for _, tt := range []struct {
		args  []string
		usage string
	}{
		{[]string{"config", "get"}, "error: config get KEY"},
		{[]string{"config", "get", "a", "b"}, "error: config get KEY"},
		{[]string{"config", "set", "a"}, "error: config set KEY VALUE"},
		{[]string{"config", "unset"}, "error: config unset KEY"},
		{[]string{"target", "add", "x"}, "error: target add NAME URL|PATH"},
		{[]string{"target", "set"}, "error: target set NAME [URL|PATH] [--rev REV]"},
		{[]string{"target", "set", "a", "b", "c"}, "error: target set NAME"},
		{[]string{"target", "rm"}, "error: target rm NAME"},
		{[]string{"target", "default"}, "error: target default NAME"},
		{[]string{"filter", "add", "include"}, "error: filter add|rm include|exclude PATTERN..."},
		{[]string{"filter", "rm", "local", "x"}, "error: filter add|rm include|exclude PATTERN..."},
	} {
		r := pm(t, proj, 2, tt.args...)
		has(t, strings.Join(tt.args, " "), r.err, tt.usage, "Usage:")
	}
}

func TestConfigCommandsNeedProject(t *testing.T) {
	empty := t.TempDir()
	for _, args := range [][]string{
		{"config", "show"}, {"config", "set", "a", "b"}, {"target", "list"}, {"filter", "list"},
	} {
		r := must(t, 2, append([]string{"-C", empty}, args...)...)
		has(t, strings.Join(args, " "), r.err, "no .pacemaker/config.yaml found")
	}
}

// ---------------------------------------------------------------- target

func TestTargetCommands(t *testing.T) {
	proj, up := project(t)
	other := filepath.Join(filepath.Dir(up), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}

	r := pm(t, proj, 0, "target", "add", "second", other, "--rev", "v2", "--include", "a/**,b/**", "--exclude", "x")
	has(t, "add", r.out, "✓ added target second → "+other)
	if get(t, proj, "default_target") != "upstream\n" {
		t.Error("adding a target without --default must keep the default")
	}
	pm(t, proj, 0, "target", "add", "remote", "https://example.com/r.git")

	r = pm(t, proj, 0, "target", "list")
	has(t, "list", r.out, "NAME", "KIND", "second", "path", "v2", "a/**, b/**", "remote", "url", "https://example.com/r.git", "default")
	lines := strings.Split(r.out, "\n")
	for _, l := range lines {
		if strings.Contains(l, "upstream") && !strings.HasPrefix(l, "*") {
			t.Errorf("default target should be starred: %q", l)
		}
		if strings.Contains(l, "second") && strings.HasPrefix(l, "*") {
			t.Errorf("non-default target starred: %q", l)
		}
	}
	if strings.Index(r.out, "remote") > strings.Index(r.out, "second") || strings.Index(r.out, "second") > strings.Index(r.out, "upstream") {
		t.Errorf("targets should be listed by name:\n%s", r.out)
	}
	pm(t, proj, 0, "target", "ls")

	for _, tt := range []struct {
		args []string
		err  string
	}{
		{[]string{"target", "add", "bad.name", other}, "target names may only contain letters, digits, '-' and '_'"},
		{[]string{"target", "add", "second", other}, `target "second" already exists (use ` + "`target set`)"},
		{[]string{"target", "add", "x", filepath.Join(proj, "missing")}, "is neither an existing directory nor a URL"},
		{[]string{"target", "set", "ghost"}, `unknown target "ghost"`},
		{[]string{"target", "rm", "ghost"}, `unknown target "ghost"`},
		{[]string{"target", "default", "ghost"}, `unknown target "ghost"`},
		{[]string{"target", "set", "second", filepath.Join(proj, "missing")}, "is neither an existing directory nor a URL"},
	} {
		r := pm(t, proj, 2, tt.args...)
		has(t, strings.Join(tt.args, " "), r.err, tt.err)
	}

	// set: rev, rev clearing, and switching path → url keeps the rev.
	r = pm(t, proj, 0, "target", "set", "second", "--rev", "v3")
	has(t, "set", r.out, "✓ updated target second")
	if get(t, proj, "targets.second.rev") != "v3\n" {
		t.Error("rev not updated")
	}
	pm(t, proj, 0, "target", "set", "second", "https://example.com/s.git")
	if get(t, proj, "targets.second.url") != "https://example.com/s.git\n" || get(t, proj, "targets.second.rev") != "v3\n" {
		t.Error("set location should switch to the url and keep the rev")
	}
	pm(t, proj, 2, "config", "get", "targets.second.path")
	if get(t, proj, "targets.second.include") != "- a/**\n- b/**\n" {
		t.Error("set location must keep the filter")
	}
	pm(t, proj, 0, "target", "set", "second", "--rev", "-")
	pm(t, proj, 2, "config", "get", "targets.second.rev")

	// default
	r = pm(t, proj, 0, "target", "default", "second")
	has(t, "default", r.out, "default target is now second")
	if get(t, proj, "default_target") != "second\n" {
		t.Error("default not saved")
	}
	pm(t, proj, 0, "target", "add", "third", other, "--default")
	if get(t, proj, "default_target") != "third\n" {
		t.Error("--default not honoured")
	}

	// rm removes the target and its cache; removing the default clears it.
	cache := filepath.Join(proj, ".pacemaker", "cache", "repos", "third")
	write(t, filepath.Join(cache, "x"), "cached")
	r = pm(t, proj, 0, "target", "remove", "third")
	has(t, "rm", r.out, "✓ removed target third")
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Errorf("cache dir should be removed: %v", err)
	}
	pm(t, proj, 2, "config", "get", "default_target")
	pm(t, proj, 2, "config", "get", "targets.third")
}

func TestTargetAddFirstBecomesDefault(t *testing.T) {
	dir := t.TempDir()
	proj, up := filepath.Join(dir, "proj"), filepath.Join(dir, "up")
	if err := os.MkdirAll(up, 0o755); err != nil {
		t.Fatal(err)
	}
	pm(t, proj, 0, "init")
	pm(t, proj, 0, "target", "add", "first", up)
	if get(t, proj, "default_target") != "first\n" {
		t.Error("the first target should become the default")
	}
}

// ---------------------------------------------------------------- filter

func TestFilterCommands(t *testing.T) {
	proj, up := project(t)

	r := pm(t, proj, 0, "filter", "add", "include", "lib/**", "src/**")
	has(t, "add", r.out, "✓ lib/**, src/** added to target upstream include")
	pm(t, proj, 0, "filter", "add", "include", "lib/**") // duplicates are ignored
	if got := get(t, proj, "targets.upstream.include"); got != "- lib/**\n- src/**\n" {
		t.Errorf("include = %q", got)
	}
	pm(t, proj, 0, "filter", "add", "exclude", "*.g.dart")
	r = pm(t, proj, 0, "filter", "rm", "include", "lib/**", "never-there")
	has(t, "rm", r.out, "removed from target upstream include")
	if got := get(t, proj, "targets.upstream.include"); got != "- src/**\n" {
		t.Errorf("include after rm = %q", got)
	}

	// --local edits the local scan filter; flags may follow positionals.
	r = pm(t, proj, 0, "filter", "add", "exclude", "tmp/", "--local")
	has(t, "local", r.out, "tmp/ added to local exclude")
	has(t, "local exclude", get(t, proj, "local.exclude"), "- tmp/\n")

	// Explicit --target, and an unknown one.
	other := filepath.Join(filepath.Dir(up), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	pm(t, proj, 0, "target", "add", "second", other)
	pm(t, proj, 0, "filter", "add", "--target", "second", "include", "only/**")
	if got := get(t, proj, "targets.second.include"); got != "- only/**\n" {
		t.Errorf("second include = %q", got)
	}
	r = pm(t, proj, 2, "filter", "add", "--target", "ghost", "include", "x")
	has(t, "unknown target", r.err, `unknown target "ghost"`)

	r = pm(t, proj, 0, "filter", "list")
	has(t, "list", r.out, "local\n", "exclude tmp/", "target second\n  include only/**", "target upstream\n  include src/**\n  exclude *.g.dart")
	r = pm(t, proj, 0, "filter", "ls", "--local")
	has(t, "list --local", r.out, "local\n", "include (everything)", "exclude tmp/")
	hasNot(t, "list --local", r.out, "target")
	r = pm(t, proj, 0, "filter", "list", "--target", "second")
	has(t, "list --target", r.out, "target second\n  include only/**")
	hasNot(t, "list --target", r.out, "local", "upstream")
	r = pm(t, proj, 2, "filter", "list", "--target", "ghost")
	has(t, "list ghost", r.err, `unknown target "ghost"`)
}

func TestFilterNeedsDefaultTarget(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	pm(t, proj, 0, "init")
	r := pm(t, proj, 2, "filter", "add", "include", "x")
	has(t, "no target", r.err, "no default target configured")

	// With exactly one target and no default, that target is used.
	up := filepath.Join(dir, "up")
	if err := os.MkdirAll(up, 0o755); err != nil {
		t.Fatal(err)
	}
	pm(t, proj, 0, "target", "add", "only", up)
	pm(t, proj, 0, "target", "rm", "only")
	pm(t, proj, 0, "target", "add", "a", up)
	pm(t, proj, 0, "target", "add", "b", up)
	pm(t, proj, 0, "config", "unset", "default_target")
	r = pm(t, proj, 2, "filter", "add", "include", "x")
	has(t, "two targets, no default", r.err, "no default target configured")
}
