package cli

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	_ "github.com/ryangarber/pacemaker/internal/lang/generic"
	_ "github.com/ryangarber/pacemaker/internal/report/html"
	_ "github.com/ryangarber/pacemaker/internal/report/jsonfmt"
	_ "github.com/ryangarber/pacemaker/internal/report/markdown"
)

// result is the outcome of one CLI invocation.
type result struct {
	code     int
	out, err string
}

// run invokes Main with colors off and captures stdout/stderr through files in
// a temp dir (Main takes *os.File).
func run(t *testing.T, args ...string) result {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "")
	// Keep accept and git (used to probe path targets) away from the user's setup.
	t.Setenv("PACEMAKER_USER", "tester")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	stdout, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	code := Main(args, stdout, stderr)
	stdout.Close()
	stderr.Close()
	o, _ := os.ReadFile(stdout.Name())
	e, _ := os.ReadFile(stderr.Name())
	return result{code: code, out: string(o), err: string(e)}
}

// must runs the CLI and fails the test unless it exits with want.
func must(t *testing.T, want int, args ...string) result {
	t.Helper()
	r := run(t, args...)
	if r.code != want {
		t.Fatalf("pacemaker %s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), r.code, want, r.out, r.err)
	}
	return r
}

func has(t *testing.T, label, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("%s: missing %q in:\n%s", label, sub, s)
		}
	}
}

func hasNot(t *testing.T, label, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			t.Errorf("%s: unexpected %q in:\n%s", label, sub, s)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---------------------------------------------------------------- dispatch

func TestMainDispatch(t *testing.T) {
	empty := t.TempDir()
	tests := []struct {
		name     string
		args     []string
		code     int
		out, err []string
		notOut   []string
		emptyErr bool
		emptyOut bool
		skip     string
	}{
		{name: "no args prints root help", args: nil, code: 0,
			out: []string{"pacemaker — keep ported code", "Usage:\n  pacemaker <command>", "Global flags:", "@pace:begin"}},
		{name: "help", args: []string{"help"}, code: 0, out: []string{"Commands:", "status", "version"}},
		{name: "--help", args: []string{"--help"}, code: 0, out: []string{"Commands:"}},
		{name: "-h", args: []string{"-h"}, code: 0, out: []string{"Commands:"}},
		{name: "help after global flag", args: []string{"--offline", "help"}, code: 0, out: []string{"Commands:"}},
		{name: "help for nested command", args: []string{"help", "target", "add"}, code: 0,
			out:    []string{"pacemaker target add — add a target", "pacemaker target add NAME URL|PATH [flags]", "--default", "--include VALUE", "--rev STRING"},
			notOut: []string{"Global flags:"}},
		{name: "command -h", args: []string{"status", "-h"}, code: 0, out: []string{"pacemaker status — summarise drift", "--all", "--target VALUE"}},
		{name: "flag help after positional", args: []string{"ls", "blk", "--help"}, code: 0, out: []string{"pacemaker ls — list links", "--json"}},
		{name: "alias resolves", args: []string{"help", "st"}, code: 0, out: []string{"pacemaker status —"}},
		{name: "group without subcommand prints help", args: []string{"target"}, code: 0,
			out: []string{"pacemaker target <command>", "add", "default", "list", "rm", "set"}},
		// The error goes to stderr; the help that follows it goes to stdout.
		{name: "unknown command", args: []string{"bogus"}, code: 2,
			err: []string{`pacemaker: unknown command "bogus"`}, out: []string{"Usage:\n  pacemaker <command>"}},
		{name: "unknown subcommand", args: []string{"target", "bogus"}, code: 2,
			err: []string{`pacemaker: unknown command "target bogus"`}, out: []string{"pacemaker target <command>"}},
		{name: "unknown global flag", args: []string{"--bogus", "status"}, code: 2,
			err: []string{"pacemaker: flag provided but not defined: -bogus"}, emptyOut: true},
		{name: "unknown command flag prints usage", args: []string{"status", "--bogus"}, code: 2,
			err: []string{"error: flag provided but not defined: -bogus", "pacemaker status [flags]"}, emptyOut: true},
		{name: "bad flag value has no usage", args: []string{"coverage", "--limit", "x"}, code: 2,
			err: []string{`error: invalid value "x" for flag -limit`}},
		{name: "usage error prints usage", args: []string{"-C", empty, "show"}, code: 2,
			err: []string{"error: show needs at least one block id or glob", "pacemaker show BLOCK-GLOB... [flags]"}},
		{name: "no project", args: []string{"-C", empty, "status"}, code: 2,
			err: []string{"error: no .pacemaker/config.yaml found (run `pacemaker init`)"}},
		{name: "--root is -C", args: []string{"--root", empty, "config", "path"}, code: 2,
			err: []string{"no .pacemaker/config.yaml found"}},
		{name: "version", args: []string{"version"}, code: 0, out: []string{"pacemaker " + Version + "\n"}, emptyErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip != "" {
				t.Skip(tt.skip)
			}
			r := run(t, tt.args...)
			if r.code != tt.code {
				t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, tt.code, r.out, r.err)
			}
			has(t, "stdout", r.out, tt.out...)
			has(t, "stderr", r.err, tt.err...)
			hasNot(t, "stdout", r.out, tt.notOut...)
			if tt.emptyOut && r.out != "" {
				t.Errorf("stdout should be empty, got:\n%s", r.out)
			}
			if tt.emptyErr && r.err != "" {
				t.Errorf("stderr should be empty, got:\n%s", r.err)
			}
		})
	}
}

func TestVersionVariable(t *testing.T) {
	old := Version
	Version = "1.2.3"
	defer func() { Version = old }()
	if r := must(t, 0, "version"); r.out != "pacemaker 1.2.3\n" {
		t.Errorf("version output = %q", r.out)
	}
}

func TestColorFlags(t *testing.T) {
	if r := must(t, 0, "help"); strings.Contains(r.out, "\x1b[") {
		t.Errorf("NO_COLOR output should be plain:\n%q", r.out)
	}
	if r := must(t, 0, "--color", "help"); !strings.Contains(r.out, "\x1b[") {
		t.Errorf("--color should force escape codes:\n%q", r.out)
	}
	// --color is applied after --no-color, whatever the order.
	if r := must(t, 0, "--color", "--no-color", "help"); !strings.Contains(r.out, "\x1b[") {
		t.Errorf("--color should win over --no-color")
	}
	if r := must(t, 0, "--no-color", "help"); strings.Contains(r.out, "\x1b[") {
		t.Errorf("--no-color output should be plain")
	}
}

func TestRootHelpListsEveryCommandInOrder(t *testing.T) {
	r := must(t, 0, "help")
	var names []string
	for _, c := range rootCommand().Sub {
		names = append(names, c.Name)
	}
	// The root help keeps the declared order (subgroups are sorted).
	last := -1
	for _, n := range names {
		i := strings.Index(r.out, "\n  "+n+" ")
		if i < 0 {
			t.Fatalf("command %q missing from root help", n)
		}
		if i < last {
			t.Errorf("command %q out of declared order", n)
		}
		last = i
	}
}

func TestEveryCommandHasHelp(t *testing.T) {
	var walk func(chain []string, c *Command)
	walk = func(chain []string, c *Command) {
		for _, s := range c.Sub {
			ch := append(append([]string(nil), chain...), s.Name)
			t.Run(strings.Join(ch, "_"), func(t *testing.T) {
				r := must(t, 0, append([]string{"help"}, ch...)...)
				has(t, "help", r.out, "pacemaker "+strings.Join(ch, " ")+" — "+s.Short, "Usage:")
			})
			walk(ch, s)
		}
	}
	walk(nil, rootCommand())
}

// ---------------------------------------------------------------- helpers

func TestParseInterspersed(t *testing.T) {
	tests := []struct {
		args    []string
		pos     []string
		json    bool
		status  []string
		wantErr bool
	}{
		{args: nil, pos: nil},
		{args: []string{"a", "b"}, pos: []string{"a", "b"}},
		{args: []string{"--json", "a"}, pos: []string{"a"}, json: true},
		{args: []string{"a", "--json", "b"}, pos: []string{"a", "b"}, json: true},
		{args: []string{"a", "--status", "new,ok", "b", "--status=moved"}, pos: []string{"a", "b"}, status: []string{"new", "ok", "moved"}},
		{args: []string{"a", "--", "--json"}, pos: []string{"a", "--json"}},
		{args: []string{"a", "--nope"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			fs := flag.NewFlagSet("x", flag.ContinueOnError)
			fs.SetOutput(new(strings.Builder))
			asJSON := fs.Bool("json", false, "")
			var st multi
			fs.Var(&st, "status", "")
			pos, err := parseInterspersed(fs, tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(pos, tt.pos) || *asJSON != tt.json || !reflect.DeepEqual([]string(st), tt.status) {
				t.Errorf("pos=%q json=%v status=%q; want pos=%q json=%v status=%q", pos, *asJSON, st, tt.pos, tt.json, tt.status)
			}
		})
	}
}

func TestParseInterspersedDashDashFirst(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.SetOutput(new(strings.Builder))
	asJSON := fs.Bool("json", false, "")
	pos, err := parseInterspersed(fs, []string{"--", "blk", "--json"})
	if err != nil || *asJSON || !reflect.DeepEqual(pos, []string{"blk", "--json"}) {
		t.Errorf("pos=%q json=%v err=%v", pos, *asJSON, err)
	}
}

func TestMultiFlag(t *testing.T) {
	var m multi
	for _, v := range []string{"a,b", " c , ,d ", "", "e"} {
		if err := m.Set(v); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"a", "b", "c", "d", "e"}; !reflect.DeepEqual([]string(m), want) {
		t.Errorf("multi = %q, want %q", m, want)
	}
	if m.String() != "a,b,c,d,e" {
		t.Errorf("String() = %q", m.String())
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pat, s string
		want   bool
	}{
		{"blk", "blk", true},
		{"b*", "blk", true},
		{"auth.*", "auth.login", true},
		{"auth.*", "auth", false},
		{"src/*.ts", "src/x.ts", true},
		{"src/*", "src/a/x.ts", false},
		{"[", "[", true},  // exact match wins over a bad pattern
		{"[", "x", false}, // bad pattern never matches
		{"?", "ab", false},
	}
	for _, tt := range tests {
		if got := globMatch(tt.pat, tt.s); got != tt.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pat, tt.s, got, tt.want)
		}
	}
}

func TestPickRenderer(t *testing.T) {
	tests := []struct {
		format, out, def string
		want             string
		wantErr          string
	}{
		{want: "markdown"},
		{def: "json", want: "json"},
		{out: "r.html", def: "json", want: "html"},
		{out: "r.unknown-ext", def: "json", want: "json"},
		{format: "text", out: "r.html", want: "text"},
		{format: "nope", wantErr: `unknown report format "nope"`},
	}
	for _, tt := range tests {
		r, err := pickRenderer(tt.format, tt.out, tt.def)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("pickRenderer(%q,%q,%q) err = %v, want %q", tt.format, tt.out, tt.def, err, tt.wantErr)
			}
			continue
		}
		if err != nil || r.Name() != tt.want {
			t.Errorf("pickRenderer(%q,%q,%q) = %v, %v; want %s", tt.format, tt.out, tt.def, r, err, tt.want)
		}
	}
}

func TestExitError(t *testing.T) {
	var ee ExitError
	if !errors.As(error(ExitError{3}), &ee) || ee.Code != 3 || ee.Error() != "exit 3" {
		t.Errorf("ExitError = %+v %q", ee, ee.Error())
	}
}

func TestPctOf(t *testing.T) {
	if pctOf(0, 0) != 100 || pctOf(1, 4) != 25 || pctOf(4, 4) != 100 {
		t.Errorf("pctOf: %v %v %v", pctOf(0, 0), pctOf(1, 4), pctOf(4, 4))
	}
}

func TestListHelpers(t *testing.T) {
	xs := []string{"a", "b", "a"}
	if !contains(xs, "b") || contains(xs, "c") {
		t.Error("contains")
	}
	if got := remove(xs, "a"); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("remove = %q", got)
	}
	if got := cacheDirFor("/p/", "up"); got != "/p/.pacemaker/cache/repos/up" {
		t.Errorf("cacheDirFor = %q", got)
	}
}
