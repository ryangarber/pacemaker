package engine_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryangarber/pacemaker/internal/config"
	"github.com/ryangarber/pacemaker/internal/engine"
	_ "github.com/ryangarber/pacemaker/internal/lang/dart"
	_ "github.com/ryangarber/pacemaker/internal/lang/generic"
	_ "github.com/ryangarber/pacemaker/internal/lang/javascript"
	"github.com/ryangarber/pacemaker/internal/model"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

const upstreamV1 = `class Store {
  int count = 0;

  void inc() {
    count++;
  }

  void dec() {
    count--;
  }
}

int helper(int x) {
  return x;
}

int unused() => 0;
`

// v2: two lines inserted at the top (moves everything), dec() changed,
// helper() reformatted only, inc() untouched.
const upstreamV2 = `// Copyright
// Header

class Store {
  int count = 0;

  void inc() {
    count++;
  }

  void dec({int by = 1}) {
    count -= by;
  }
}

int helper(
  int x,
) {
  return x; // identity
}

int unused() => 0;
`

func setup(t *testing.T) (proj, up string) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	up, proj = filepath.Join(dir, "up"), filepath.Join(dir, "proj")
	write(t, filepath.Join(up, "lib/store.dart"), upstreamV1)
	write(t, filepath.Join(up, "lib/gen.g.dart"), "// generated\nint x = 1;\n")
	git(t, up, "init", "-q", "-b", "main")
	git(t, up, "add", "-A")
	git(t, up, "commit", "-qm", "v1")

	cfg := config.Default()
	cfg.Targets["up"] = config.Target{Path: up, Filter: config.Filter{Include: []string{"lib/**"}, Exclude: []string{"*.g.dart"}}}
	cfg.DefaultTarget = "up"
	if err := cfg.Save(proj); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(proj, "src/store.ts"), `export class Store {
  count = 0
  // @pace:begin store.inc lib/store.dart#L4-L6
  inc() { this.count++ }
  // @pace:end
  // @pace:begin store.dec lib/store.dart#Store.dec
  dec() { this.count-- }
  // @pace:end
}
// @pace:begin helper lib/store.dart#helper
export const helper = (x: number) => x
// @pace:end
// @pace:skip ref=lib/store.dart#unused reason="dead code"
`)
	return proj, up
}

func analyze(t *testing.T, proj string) (*engine.Engine, *model.Report) {
	t.Helper()
	e, err := engine.Open(proj)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Analyze(engine.Options{Diffs: true, Commits: true, Coverage: true})
	if err != nil {
		t.Fatal(err)
	}
	return e, r
}

func statuses(r *model.Report) map[string]model.Status {
	m := map[string]model.Status{}
	for _, l := range r.Links {
		m[l.Block] = l.Status
	}
	return m
}

func TestLifecycle(t *testing.T) {
	proj, up := setup(t)

	// 1. Fresh markers are "new".
	e, r := analyze(t, proj)
	if len(r.Problems) > 0 {
		t.Fatalf("problems: %v", r.Problems)
	}
	for b, s := range statuses(r) {
		if s != model.New {
			t.Errorf("%s: want new, got %s", b, s)
		}
	}
	if !r.Failing {
		t.Error("new links should fail the check by default")
	}

	// 2. Accept everything → ok.
	if _, err := e.Accept(func(*model.Link) bool { return true }, false); err != nil {
		t.Fatal(err)
	}
	_, r = analyze(t, proj)
	for b, s := range statuses(r) {
		if s != model.OK {
			t.Errorf("%s: want ok after accept, got %s", b, s)
		}
	}
	// Coverage: gen.g.dart excluded; unused() waived.
	tr := r.Targets[0]
	if tr.Totals.Files != 1 || tr.Totals.Waived == 0 || tr.Totals.Covered == 0 {
		t.Errorf("unexpected coverage totals: %+v", tr.Totals)
	}
	if _, err := os.Stat(filepath.Join(proj, ".pacemaker/state.yaml")); err != nil {
		t.Fatal("state.yaml not written")
	}

	// 3. Upstream changes.
	write(t, filepath.Join(up, "lib/store.dart"), upstreamV2)
	git(t, up, "commit", "-qam", "v2")
	e, r = analyze(t, proj)
	st := statuses(r)
	want := map[string]model.Status{
		"store.inc": model.Moved, "store.dec": model.Drifted, "helper": model.Cosmetic, "skip:lib/store.dart#unused": model.OK,
	}
	for b, s := range want {
		if st[b] != s {
			t.Errorf("%s: want %s, got %s", b, s, st[b])
		}
	}
	for _, l := range r.Links {
		switch l.Block {
		case "store.inc":
			if l.SuggestedRef != "up:lib/store.dart#L7-L9" {
				t.Errorf("suggested ref = %q", l.SuggestedRef)
			}
		case "store.dec":
			found := false
			for _, f := range l.Findings {
				if f.Kind == "signature-changed" && f.Symbol == "Store.dec" {
					found = true
				}
			}
			if !found {
				t.Errorf("expected signature-changed finding, got %+v", l.Findings)
			}
			if len(l.Commits) != 1 || l.Commits[0].Subject != "v2" {
				t.Errorf("expected v2 commit, got %+v", l.Commits)
			}
			if !strings.Contains(l.Diff, "+  void dec({int by = 1}) {") {
				t.Errorf("diff missing change:\n%s", l.Diff)
			}
		}
	}

	// 4. Accept moved with --rewrite updates the marker in place.
	if _, err := e.Accept(func(l *model.Link) bool { return l.Status == model.Moved }, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(proj, "src/store.ts"))
	if !strings.Contains(string(b), "@pace:begin store.inc lib/store.dart#L7-L9") {
		t.Errorf("marker not rewritten:\n%s", b)
	}
	_, r = analyze(t, proj)
	if s := statuses(r)["store.inc"]; s != model.OK {
		t.Errorf("store.inc after rewrite: %s", s)
	}

	// 5. Local edit is flagged without changing status.
	content := strings.Replace(string(b), "inc() { this.count++ }", "inc() { this.count += 1 }", 1)
	write(t, filepath.Join(proj, "src/store.ts"), content)
	_, r = analyze(t, proj)
	for _, l := range r.Links {
		if l.Block == "store.inc" && !l.LocalChanged {
			t.Error("expected local change to be detected")
		}
	}
}

func TestNonGitSnapshotRelocation(t *testing.T) {
	dir := t.TempDir()
	up, proj := filepath.Join(dir, "plain"), filepath.Join(dir, "proj")
	write(t, filepath.Join(up, "a.txt"), "one\ntwo\nthree\nfour\n")
	cfg := config.Default()
	cfg.Targets["p"] = config.Target{Path: up}
	if err := cfg.Save(proj); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(proj, "x.md"), "<!-- @pace:begin blk p:a.txt#L2-L3 -->\nhi\n<!-- @pace:end -->\n")
	e, _ := analyze(t, proj)
	if _, err := e.Accept(func(*model.Link) bool { return true }, false); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(up, "a.txt"), "zero\none\ntwo\nthree\nfour\n")
	_, r := analyze(t, proj)
	l := r.Links[0]
	if l.Status != model.Moved || l.Range.Start != 3 || l.Range.End != 4 {
		t.Errorf("want moved to L3-L4, got %s %s", l.Status, l.Range)
	}
}
