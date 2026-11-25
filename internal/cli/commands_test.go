package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	upA = "one\ntwo\nthree\nfour\n"
	upB = "alpha\nbeta\n"
	// localX links two blocks into the plain-directory upstream and carries a
	// todo and a note.
	localX = `// @pace:begin blk lib/a.txt#L2-L3
hi
// @pace:end
// @pace:begin other lib/b.txt#L1-L2
ho
// @pace:end
// @pace:todo port four ref=lib/a.txt#L4 prio=high owner=ana tags=gap
// @pace:note kept short on purpose
`
)

// fixture builds a project in a temp dir whose single (default) target
// "upstream" is a plain directory, initialised through the CLI itself.
func fixture(t *testing.T) (proj, up string) {
	t.Helper()
	dir := t.TempDir()
	up, proj = filepath.Join(dir, "up"), filepath.Join(dir, "proj")
	write(t, filepath.Join(up, "lib/a.txt"), upA)
	write(t, filepath.Join(up, "lib/b.txt"), upB)
	write(t, filepath.Join(proj, "src/x.ts"), localX)
	must(t, 0, "-C", proj, "init", up)
	return proj, up
}

// pm runs the CLI against proj, offline.
func pm(t *testing.T, proj string, want int, args ...string) result {
	t.Helper()
	return must(t, want, append([]string{"-C", proj, "--offline"}, args...)...)
}

func lsBlocks(t *testing.T, proj string, args ...string) []string {
	t.Helper()
	r := pm(t, proj, 0, append([]string{"ls", "--json"}, args...)...)
	var links []struct {
		Block  string `json:"block"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(r.out), &links); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, r.out)
	}
	var out []string
	for _, l := range links {
		out = append(out, l.Block+"="+l.Status)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- init

func TestInit(t *testing.T) {
	dir := t.TempDir()
	up := filepath.Join(dir, "up")
	write(t, filepath.Join(up, "a.txt"), "x\n")

	t.Run("without target", func(t *testing.T) {
		proj := filepath.Join(dir, "bare")
		r := pm(t, proj, 0, "init")
		has(t, "init", r.out, "✓ initialised "+filepath.Join(proj, ".pacemaker/config.yaml"), "next: pacemaker target add upstream")
		has(t, ".gitignore", read(t, filepath.Join(proj, ".pacemaker/.gitignore")), "cache/")
		r = pm(t, proj, 0, "target", "list")
		has(t, "target list", r.out, "no targets")
	})

	t.Run("with target and flags", func(t *testing.T) {
		proj := filepath.Join(dir, "named")
		r := pm(t, proj, 0, "init", up, "--name", "legacy", "--rev", "v1", "--include", "lib/**,src/**", "--exclude", "*.g.dart")
		has(t, "init", r.out, "target legacy → "+up)
		for key, want := range map[string]string{
			"default_target":         "legacy\n",
			"targets.legacy.path":    up + "\n",
			"targets.legacy.rev":     "v1\n",
			"targets.legacy.include": "- lib/**\n- src/**\n",
			"targets.legacy.exclude": "- '*.g.dart'\n",
		} {
			if got := pm(t, proj, 0, "config", "get", key).out; got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
	})

	t.Run("refuses to overwrite without --force", func(t *testing.T) {
		proj := filepath.Join(dir, "twice")
		pm(t, proj, 0, "init")
		r := pm(t, proj, 2, "init", up)
		has(t, "init", r.err, "already exists (use --force to overwrite)")
		pm(t, proj, 0, "init", up, "--force")
		if got := pm(t, proj, 0, "config", "get", "default_target").out; got != "upstream\n" {
			t.Errorf("default_target after --force = %q", got)
		}
	})

	t.Run("bad location", func(t *testing.T) {
		proj := filepath.Join(dir, "bad")
		r := pm(t, proj, 2, "init", filepath.Join(dir, "missing"))
		has(t, "init", r.err, "is neither an existing directory nor a URL")
		if _, err := os.Stat(filepath.Join(proj, ".pacemaker")); err == nil {
			t.Error("a failed init should not create .pacemaker/")
		}
	})

	t.Run("url target", func(t *testing.T) {
		proj := filepath.Join(dir, "url")
		pm(t, proj, 0, "init", "https://example.com/repo.git")
		if got := pm(t, proj, 0, "config", "get", "targets.upstream.url").out; got != "https://example.com/repo.git\n" {
			t.Errorf("url = %q", got)
		}
	})
}

// ---------------------------------------------------------------- lifecycle

func TestLifecycle(t *testing.T) {
	proj, up := fixture(t)

	// Fresh markers are new: status hints, check fails.
	r := pm(t, proj, 0, "status")
	has(t, "status", r.out, "links  ● 2 new", "blk", "other", "not yet accepted", "hint: review new links")
	r = pm(t, proj, 1, "check")
	has(t, "check", r.out, "✗ check failed: 2 new link(s)")

	// accept needs a selection.
	r = pm(t, proj, 2, "accept")
	has(t, "accept", r.err, "error: name blocks to accept, use a filter, or pass --all", "pacemaker accept [BLOCK-GLOB...] [flags]")
	r = pm(t, proj, 2, "accept", "nope")
	has(t, "accept nope", r.err, "error: no links match nope")

	// --dry-run lists without writing state.
	r = pm(t, proj, 0, "accept", "--dry-run", "blk")
	if strings.TrimSpace(r.out) != "● new blk → upstream:lib/a.txt#L2-L3" {
		t.Errorf("dry run output = %q", r.out)
	}
	if _, err := os.Stat(filepath.Join(proj, ".pacemaker/state.yaml")); err == nil {
		t.Error("--dry-run wrote state.yaml")
	}

	r = pm(t, proj, 0, "accept", "blk")
	has(t, "accept blk", r.out, "✓ blk → upstream:lib/a.txt#L2-L3 (was new)")
	hasNot(t, "accept blk", r.out, "other")
	if got := strings.Join(lsBlocks(t, proj), " "); got != "blk=ok other=new" {
		t.Errorf("after accept blk: %s", got)
	}
	// Links are listed most urgent first.
	if r = pm(t, proj, 0, "ls"); strings.Index(r.out, "other") > strings.Index(r.out, "blk") {
		t.Errorf("new links should list before ok ones:\n%s", r.out)
	}

	// Accepting through a filter.
	r = pm(t, proj, 0, "accept", "--status", "new")
	has(t, "accept --status new", r.out, "✓ other → upstream:lib/b.txt#L1-L2 (was new)")
	hasNot(t, "accept --status new", r.out, "blk")
	r = pm(t, proj, 0, "accept", "--status", "drifted")
	has(t, "accept nothing", r.out, "nothing accepted")

	// All in step.
	r = pm(t, proj, 0, "status")
	has(t, "status", r.out, "links  ✓ 2 ok", "✓ everything is in step with upstream")
	hasNot(t, "status", r.out, "STATUS  BLOCK")
	r = pm(t, proj, 0, "status", "--all")
	has(t, "status --all", r.out, "STATUS", "blk", "other")
	r = pm(t, proj, 0, "check")
	has(t, "check", r.out, "✓ check passed")
	r = pm(t, proj, 0, "diff")
	has(t, "diff", r.out, "no upstream changes in the selected links")

	// Upstream drifts (and moves down a line).
	write(t, filepath.Join(up, "lib/a.txt"), "zero\none\ntwo\nTHREE\nfour\n")
	r = pm(t, proj, 0, "status")
	has(t, "status drifted", r.out, "▲ 1 drifted", "upstream content changed since accepted", "hint: inspect drift with `pacemaker diff [block]`")
	r = pm(t, proj, 1, "check")
	has(t, "check drifted", r.out, "✗ check failed:", "drifted")

	r = pm(t, proj, 0, "diff")
	has(t, "diff", r.out, "▲ drifted blk → upstream:lib/a.txt#L2-L3", "-three", "+THREE", "suggest    upstream:lib/a.txt#L3-L4")
	hasNot(t, "diff", r.out, "other →")
	r = pm(t, proj, 0, "diff", "--stat")
	has(t, "diff --stat", r.out, "change     +1 -1 lines")
	hasNot(t, "diff --stat", r.out, "+THREE")
	r = pm(t, proj, 0, "diff", "other")
	has(t, "diff other", r.out, "no upstream changes in the selected links")

	r = pm(t, proj, 0, "show", "blk", "other")
	has(t, "show", r.out, "blk →", "other →", "+THREE")
	r = pm(t, proj, 0, "show", "blk", "--no-diff")
	has(t, "show --no-diff", r.out, "blk →")
	hasNot(t, "show --no-diff", r.out, "+THREE")
	r = pm(t, proj, 2, "show", "nope")
	has(t, "show nope", r.err, "error: no links match nope")

	// Accept with --rewrite updates the marker in place.
	r = pm(t, proj, 0, "accept", "--status", "drifted", "--rewrite")
	has(t, "accept --rewrite", r.out, "✓ blk", "(was drifted)", "marker → upstream:lib/a.txt#L3-L4")
	has(t, "marker", read(t, filepath.Join(proj, "src/x.ts")), "@pace:begin blk lib/a.txt#L3-L4")
	pm(t, proj, 0, "check")
}

func TestAcceptAll(t *testing.T) {
	proj, _ := fixture(t)
	r := pm(t, proj, 0, "accept", "--all")
	has(t, "accept --all", r.out, "✓ blk", "✓ other")
	pm(t, proj, 0, "check")
}

func TestMissingUpstreamFile(t *testing.T) {
	proj, up := fixture(t)
	pm(t, proj, 0, "accept", "--all")
	if err := os.Remove(filepath.Join(up, "lib/b.txt")); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lsBlocks(t, proj, "other"), " "); got != "other=missing" {
		t.Fatalf("other after delete: %s", got)
	}
	r := pm(t, proj, 1, "check")
	has(t, "check", r.out, "missing")
	r = pm(t, proj, 0, "accept", "other")
	has(t, "accept missing", r.out, "could not be resolved and were skipped", "nothing accepted")
}

// ---------------------------------------------------------------- ls

func TestLsFilters(t *testing.T) {
	proj, _ := fixture(t)
	pm(t, proj, 0, "accept", "blk")
	tests := []struct {
		args []string
		want string
	}{
		{nil, "blk=ok other=new"},
		{[]string{"b*"}, "blk=ok"},
		{[]string{"blk", "other"}, "blk=ok other=new"},
		{[]string{"src/x.ts"}, "blk=ok other=new"}, // positional args also match the local file
		{[]string{"--status", "new"}, "other=new"},
		{[]string{"--status", "ok,new"}, "blk=ok other=new"},
		{[]string{"--block", "o*"}, "other=new"},
		{[]string{"--file", "lib/a.txt"}, "blk=ok"},
		{[]string{"--file", "src/*"}, "blk=ok other=new"},
		{[]string{"--target", "upstream"}, "blk=ok other=new"},
		{[]string{"--target", "nope"}, ""},
		{[]string{"--status", "new", "blk"}, ""},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			if got := strings.Join(lsBlocks(t, proj, tt.args...), " "); got != tt.want {
				t.Errorf("ls %v = %q, want %q", tt.args, got, tt.want)
			}
		})
	}

	r := pm(t, proj, 0, "ls", "--status", "drifted")
	if strings.TrimSpace(r.out) != "no matching links" {
		t.Errorf("empty ls = %q", r.out)
	}
	// JSON of nothing is null (the links slice is nil).
	r = pm(t, proj, 0, "ls", "--json", "--status", "drifted")
	if strings.TrimSpace(r.out) != "null" {
		t.Errorf("empty ls --json = %q", r.out)
	}
	r = pm(t, proj, 0, "list", "other")
	has(t, "list alias", r.out, "STATUS", "other", "upstream:lib/b.txt#L1-L2")
	hasNot(t, "list alias", r.out, "blk")
}

// ---------------------------------------------------------------- scan

func TestScan(t *testing.T) {
	proj, _ := fixture(t)
	r := pm(t, proj, 0, "scan")
	has(t, "scan", r.out, "BLOCK", "blk", "src/x.ts:2-2", "lib/a.txt#L2-L3", "other", "port four → lib/a.txt#L4",
		"kept short on purpose", "2 block(s), 2 todo/note(s)\n")

	write(t, filepath.Join(proj, "src/bad.ts"), "// @pace:begin broken lib/a.txt#L1\nx\n")
	r = pm(t, proj, 1, "scan")
	has(t, "scan with problem", r.out, "1 problem(s)", "src/bad.ts:1")
	if r.err != "" {
		t.Errorf("ExitError must not print an error: %q", r.err)
	}
	r = pm(t, proj, 1, "check")
	has(t, "check with problem", r.out, "✗ check failed")
}

// ---------------------------------------------------------------- todo

func TestTodo(t *testing.T) {
	proj, _ := fixture(t)
	write(t, filepath.Join(proj, "src/y.ts"), "// @pace:todo low thing prio=low owner=bo ref=lib/gone.txt\n")
	tests := []struct {
		args       []string
		want, not  []string
		emptyMatch bool
	}{
		{args: nil, want: []string{"port four @ana", "low thing"}, not: []string{"kept short"}},
		{args: []string{"--notes"}, want: []string{"port four", "kept short"}},
		{args: []string{"--only-notes"}, want: []string{"kept short"}, not: []string{"port four"}},
		{args: []string{"--prio", "high"}, want: []string{"port four"}, not: []string{"low thing"}},
		{args: []string{"--prio", "HIGH"}, want: []string{"port four"}}, // case-insensitive
		{args: []string{"--owner", "bo"}, want: []string{"low thing"}, not: []string{"port four"}},
		{args: []string{"--tag", "gap"}, want: []string{"port four"}, not: []string{"low thing"}},
		{args: []string{"--file", "src/y.ts"}, want: []string{"low thing"}, not: []string{"port four"}},
		{args: []string{"FOUR"}, want: []string{"port four"}, not: []string{"low thing"}}, // text, case-insensitive
		{args: []string{"--stale"}, want: []string{"low thing"}, not: []string{"port four"}},
		{args: []string{"--review"}, want: []string{"low thing"}, not: []string{"port four"}}, // a stale ref needs review
		{args: []string{"--prio", "blocker"}, emptyMatch: true},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			r := pm(t, proj, 0, append([]string{"todo"}, tt.args...)...)
			if tt.emptyMatch {
				if strings.TrimSpace(r.out) != "no matching todos" {
					t.Errorf("want no matches, got:\n%s", r.out)
				}
				return
			}
			has(t, "todo", r.out, tt.want...)
			has(t, "todo", r.out, "total")
			hasNot(t, "todo", r.out, tt.not...)
		})
	}

	r := pm(t, proj, 0, "todos", "--json", "--prio", "high")
	var todos []map[string]any
	if err := json.Unmarshal([]byte(r.out), &todos); err != nil || len(todos) != 1 || todos[0]["owner"] != "ana" {
		t.Errorf("todo --json: %v %v\n%s", err, todos, r.out)
	}
	r = pm(t, proj, 0, "todo", "--json", "--prio", "blocker")
	if strings.TrimSpace(r.out) != "[]" {
		t.Errorf("empty todo --json = %q, want []", r.out)
	}

	r = pm(t, proj, 0, "status")
	has(t, "status", r.out, "hint: some todo/note refs no longer resolve upstream")
}

// ---------------------------------------------------------------- coverage / tree

func TestCoverage(t *testing.T) {
	proj, _ := fixture(t)
	r := pm(t, proj, 0, "coverage")
	has(t, "coverage", r.out, "target upstream")
	hasNot(t, "coverage", r.out, "FILE", "gaps")

	r = pm(t, proj, 0, "cov", "--files")
	has(t, "--files", r.out, "FILE", "lib/a.txt", "lib/b.txt")
	// Least covered first.
	if strings.Index(r.out, "lib/a.txt") > strings.Index(r.out, "lib/b.txt") {
		t.Errorf("files should sort by ascending coverage:\n%s", r.out)
	}

	r = pm(t, proj, 0, "coverage", "lib/a.txt")
	has(t, "glob", r.out, "FILE", "lib/a.txt")
	hasNot(t, "glob", r.out, "lib/b.txt")
	r = pm(t, proj, 0, "coverage", "lib/")
	has(t, "dir prefix", r.out, "lib/a.txt", "lib/b.txt")

	r = pm(t, proj, 0, "coverage", "--uncovered")
	has(t, "--uncovered", r.out, "gaps upstream", "lib/a.txt", "uncovered: L1, L4")
	hasNot(t, "--uncovered", r.out, "lib/b.txt") // fully covered: nothing to list

	r = pm(t, proj, 0, "coverage", "--target", "nope")
	hasNot(t, "--target nope", r.out, "target upstream")
}

func TestTree(t *testing.T) {
	proj, _ := fixture(t)
	r := pm(t, proj, 0, "tree")
	has(t, "tree", r.out, "upstream:", "└── lib/", "├── a.txt", "└── b.txt", "1 link(s)", "☐1")
	if strings.Index(r.out, "a.txt") > strings.Index(r.out, "b.txt") {
		t.Error("tree entries should be sorted")
	}

	r = pm(t, proj, 0, "tree", "lib")
	has(t, "subdir", r.out, "upstream:lib/", "a.txt")
	hasNot(t, "subdir", r.out, "── lib/")

	r = pm(t, proj, 0, "tree", "--depth", "1")
	has(t, "--depth 1", r.out, "lib/")
	hasNot(t, "--depth 1", r.out, "a.txt")

	r = pm(t, proj, 0, "tree", "--gaps")
	has(t, "--gaps", r.out, "a.txt")
	hasNot(t, "--gaps", r.out, "b.txt")

	r = pm(t, proj, 0, "tree", "nope")
	has(t, "empty subdir", r.out, "upstream:nope/ 100.0%")
}

// ---------------------------------------------------------------- report / check

func TestReport(t *testing.T) {
	proj, _ := fixture(t)

	r := pm(t, proj, 0, "report", "--format", "json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(r.out), &doc); err != nil {
		t.Errorf("report --format json is not JSON: %v\n%s", err, r.out)
	}

	r = pm(t, proj, 0, "report")
	has(t, "default markdown", r.out, "#")

	out := filepath.Join(t.TempDir(), "nested", "dir", "r.html")
	r = pm(t, proj, 0, "report", "-o", out)
	has(t, "report -o", r.err, "✓ wrote html report to "+out)
	if r.out != "" {
		t.Errorf("report -o should leave stdout empty: %q", r.out)
	}
	has(t, "html file", read(t, out), "<html")

	r = pm(t, proj, 2, "report", "--format", "nope")
	has(t, "bad format", r.err, `unknown report format "nope"`)

	// report.format from config is the fallback.
	pm(t, proj, 0, "config", "set", "report.format", "json")
	r = pm(t, proj, 0, "report")
	if err := json.Unmarshal([]byte(r.out), &doc); err != nil {
		t.Errorf("report with report.format=json is not JSON: %v", err)
	}
	// report.output from config is used when -o is absent.
	cfgOut := filepath.Join(t.TempDir(), "cfg.md")
	pm(t, proj, 0, "config", "set", "report.output", cfgOut)
	r = pm(t, proj, 0, "report")
	has(t, "report.output", r.err, "wrote markdown report to "+cfgOut)
	if _, err := os.Stat(cfgOut); err != nil {
		t.Error(err)
	}
}

func TestCheckWithReport(t *testing.T) {
	proj, _ := fixture(t)

	// --format without -o: the report replaces the summary on stdout.
	r := pm(t, proj, 1, "check", "--format", "json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(r.out), &doc); err != nil {
		t.Errorf("check --format json stdout is not JSON: %v\n%s", err, r.out)
	}

	// -o writes the file and still prints the summary.
	out := filepath.Join(t.TempDir(), "check.md")
	r = pm(t, proj, 1, "check", "-o", out)
	has(t, "check -o", r.out, "✗ check failed")
	has(t, "check -o", r.err, "wrote markdown report to "+out)

	r = pm(t, proj, 2, "check", "--format", "nope")
	has(t, "bad format", r.err, `unknown report format "nope"`)

	// drift.fail_on drives the gate.
	pm(t, proj, 0, "config", "set", "drift.fail_on", "drifted")
	pm(t, proj, 0, "check")
}

// ---------------------------------------------------------------- prune

func TestPrune(t *testing.T) {
	proj, _ := fixture(t)
	r := pm(t, proj, 0, "prune")
	has(t, "prune empty", r.out, "state is clean")

	pm(t, proj, 0, "accept", "--all")
	write(t, filepath.Join(proj, "src/x.ts"), strings.Replace(localX, "// @pace:begin other lib/b.txt#L1-L2\nho\n// @pace:end\n", "", 1))

	r = pm(t, proj, 0, "prune", "--dry-run")
	has(t, "prune --dry-run", r.out, "− ", "other")
	r = pm(t, proj, 0, "prune", "--dry-run")
	has(t, "dry run kept state", r.out, "other")

	r = pm(t, proj, 0, "prune")
	has(t, "prune", r.out, "other")
	r = pm(t, proj, 0, "prune")
	has(t, "prune again", r.out, "state is clean")
	pm(t, proj, 0, "check")
}

// ---------------------------------------------------------------- modules / fetch

func TestModules(t *testing.T) {
	r := must(t, 0, "modules")
	has(t, "modules", r.out, "Language modules", "generic", "(fallback)", "Report formats", "markdown", "json", "html", "text")
}

func TestFetchPathTargetsOffline(t *testing.T) {
	proj, _ := fixture(t)
	// A path target needs no fetching; it must not fail or touch the network.
	pm(t, proj, 0, "fetch")
}

func TestMovedUpstream(t *testing.T) {
	proj, up := fixture(t)
	pm(t, proj, 0, "accept", "--all")
	write(t, filepath.Join(up, "lib/b.txt"), "header\n"+upB)
	if got := strings.Join(lsBlocks(t, proj, "other"), " "); got != "other=moved" {
		t.Fatalf("other after insert: %s", got)
	}
	r := pm(t, proj, 0, "status")
	has(t, "status moved", r.out, "hint: `pacemaker accept --status moved --rewrite` updates stale line refs")
	pm(t, proj, 0, "check") // moved does not fail by default
	r = pm(t, proj, 0, "diff")
	has(t, "diff", r.out, "no upstream changes in the selected links")
	r = pm(t, proj, 0, "accept", "--status", "moved", "--rewrite")
	has(t, "accept moved", r.out, "(was moved)", "marker → upstream:lib/b.txt#L2-L3")
	has(t, "marker", read(t, filepath.Join(proj, "src/x.ts")), "@pace:begin other lib/b.txt#L2-L3")
}
