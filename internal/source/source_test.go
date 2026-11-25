package source

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// hermeticGit isolates git from the user's and system configuration.
func hermeticGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Ana Tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "ana@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Ana Tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "ana@example.invalid")
	t.Setenv("GIT_AUTHOR_DATE", "2026-01-02T03:04:05Z")
	t.Setenv("GIT_COMMITTER_DATE", "2026-01-02T03:04:05Z")
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "--quiet", "-m", msg)
	return run(t, dir, "rev-parse", "HEAD")
}

// fixture is a local repo with three commits:
//
//	c1: lib/a.dart, lib/old.dart, README
//	c2: lib/a.dart changed
//	c3: lib/old.dart renamed to lib/new.dart, lib/a.dart changed again
type fixture struct {
	dir        string
	c1, c2, c3 string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	hermeticGit(t)
	dir := t.TempDir()
	run(t, dir, "init", "--quiet", "--initial-branch=main")
	writeFile(t, dir, "lib/a.dart", "v1\n")
	writeFile(t, dir, "lib/old.dart", "renamed content that is long enough to be detected\nline two\nline three\n")
	writeFile(t, dir, "README", "readme\n")
	c1 := commitAll(t, dir, "first")
	writeFile(t, dir, "lib/a.dart", "v2\n")
	c2 := commitAll(t, dir, "second")
	run(t, dir, "mv", "lib/old.dart", "lib/new.dart")
	writeFile(t, dir, "lib/a.dart", "v3\n")
	c3 := commitAll(t, dir, "third: rename")
	run(t, dir, "tag", "v1", c1)
	return fixture{dir: dir, c1: c1, c2: c2, c3: c3}
}

func TestLooksLikeURL(t *testing.T) {
	cases := map[string]bool{
		"https://github.com/o/r.git": true,
		"file:///tmp/repo":           true,
		"ssh://git@host/r":           true,
		"git@github.com:o/r.git":     true,
		"user.name@my-host.io:path":  true,
		"../upstream":                false,
		"/abs/path":                  false,
		"C:/repo":                    false,
		"lib/a:b":                    false,
		"":                           false,
	}
	for in, want := range cases {
		if got := LooksLikeURL(in); got != want {
			t.Errorf("LooksLikeURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCacheDir(t *testing.T) {
	cases := map[string]string{
		"up":          "up",
		"my target/x": "my_target_x",
		"../escape":   ".._escape",
		"ok_name-1.2": "ok_name-1.2",
		"a::b":        "a_b",
	}
	for in, want := range cases {
		if got := CacheDir("/cache", in); got != filepath.Join("/cache", "repos", want) {
			t.Errorf("CacheDir(%q) = %s, want .../repos/%s", in, got, want)
		}
	}
}

func TestOpenPlainDirectory(t *testing.T) {
	hermeticGit(t)
	project := t.TempDir()
	up := filepath.Join(project, "up")
	writeFile(t, up, "lib/a.dart", "alpha\n")
	writeFile(t, up, "b.txt", "beta\n")
	writeFile(t, up, ".git/config", "ignored\n") // not a real repo, but skipped
	writeFile(t, up, ".pacemaker/state.yaml", "ignored\n")
	writeFile(t, up, "deep/.pacemaker/x", "ignored\n")

	// Relative paths resolve against the project root.
	src, err := Open(Spec{Name: "up", Path: "up"}, project, t.TempDir(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if src.Name() != "up" || src.Location() != up || src.Revision() != "" || src.Dirty() {
		t.Errorf("plain source: name=%q loc=%q rev=%q dirty=%v", src.Name(), src.Location(), src.Revision(), src.Dirty())
	}
	files, err := src.ListFiles()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"b.txt", "lib/a.dart"}; !reflect.DeepEqual(files, want) {
		t.Errorf("ListFiles = %v, want %v", files, want)
	}
	b, err := src.ReadFile("lib/a.dart")
	if err != nil || string(b) != "alpha\n" {
		t.Errorf("ReadFile = %q, %v", b, err)
	}
	if _, err := src.ReadFile("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile missing: %v", err)
	}
	// No history for plain directories.
	if _, err := src.ReadFileAt("HEAD", "b.txt"); !errors.Is(err, ErrNoHistory) {
		t.Errorf("ReadFileAt: %v", err)
	}
	if _, err := src.Commits("HEAD", "b.txt", 5); !errors.Is(err, ErrNoHistory) {
		t.Errorf("Commits: %v", err)
	}
	if _, ok := src.RenamedTo("HEAD", "b.txt"); ok {
		t.Error("RenamedTo should be false without git")
	}
}

func TestReadFileAndListFilesAreCached(t *testing.T) {
	hermeticGit(t)
	up := t.TempDir()
	writeFile(t, up, "a.txt", "one\n")
	src, err := Open(Spec{Name: "up", Path: up}, t.TempDir(), t.TempDir(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := src.ReadFile("a.txt"); string(b) != "one\n" {
		t.Fatalf("ReadFile = %q", b)
	}
	if _, err := src.ListFiles(); err != nil {
		t.Fatal(err)
	}
	// A Source is a snapshot: later disk changes are not observed.
	writeFile(t, up, "a.txt", "two\n")
	writeFile(t, up, "b.txt", "new\n")
	if b, _ := src.ReadFile("a.txt"); string(b) != "one\n" {
		t.Errorf("ReadFile after change = %q, want cached one", b)
	}
	if files, _ := src.ListFiles(); !reflect.DeepEqual(files, []string{"a.txt"}) {
		t.Errorf("ListFiles after change = %v, want cached", files)
	}
}

func TestOpenErrors(t *testing.T) {
	hermeticGit(t)
	project := t.TempDir()
	writeFile(t, project, "file.txt", "x")
	plain := filepath.Join(project, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)

	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{"missing dir", Spec{Name: "up", Path: "nope"}, `target "up": ` + filepath.Join(project, "nope") + " is not a directory"},
		{"file not dir", Spec{Name: "up", Path: "file.txt"}, "is not a directory"},
		{"rev on plain dir", Spec{Name: "up", Path: plain, Rev: "main"}, `target "up": rev "main" set but ` + plain + " is not a git repository"},
		{"unknown rev", Spec{Name: "up", Path: f.dir, Rev: "no-such-branch"}, `target "up": unknown revision "no-such-branch"`},
		{"url not fetched", Spec{Name: "remote", URL: "https://example.invalid/r.git"}, `target "remote" is not fetched yet; run ` + "`pacemaker fetch`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Open(tc.spec, project, t.TempDir(), false, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestGitWorkingTree(t *testing.T) {
	f := newFixture(t)
	src, err := Open(Spec{Name: "up", Path: f.dir}, t.TempDir(), t.TempDir(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if src.Revision() != f.c3 {
		t.Errorf("Revision = %s, want %s", src.Revision(), f.c3)
	}
	if src.Dirty() {
		t.Error("clean tree reported dirty")
	}
	files, err := src.ListFiles()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"README", "lib/a.dart", "lib/new.dart"}; !reflect.DeepEqual(files, want) {
		t.Errorf("ListFiles = %v, want %v", files, want)
	}

	// History.
	for _, tc := range []struct{ rev, want string }{{f.c1, "v1\n"}, {f.c2, "v2\n"}, {"v1", "v1\n"}, {"HEAD", "v3\n"}} {
		b, err := src.ReadFileAt(tc.rev, "lib/a.dart")
		if err != nil || string(b) != tc.want {
			t.Errorf("ReadFileAt(%s) = %q, %v; want %q", tc.rev, b, err, tc.want)
		}
	}
	if _, err := src.ReadFileAt(f.c3, "lib/old.dart"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFileAt deleted path: %v", err)
	}
	if _, err := src.ReadFileAt(f.c1, "lib"); err == nil || !strings.Contains(err.Error(), "is a tree, not a file") {
		t.Errorf("ReadFileAt directory: %v", err)
	}
	if _, err := src.ReadFileAt("", "lib/a.dart"); !errors.Is(err, ErrNoHistory) {
		t.Errorf("ReadFileAt empty rev: %v", err)
	}
	// The cat-file process stays usable after a miss.
	if b, err := src.ReadFileAt(f.c1, "README"); err != nil || string(b) != "readme\n" {
		t.Errorf("ReadFileAt after miss = %q, %v", b, err)
	}

	// Renames.
	if p, ok := src.RenamedTo(f.c1, "lib/old.dart"); !ok || p != "lib/new.dart" {
		t.Errorf("RenamedTo = %q, %v", p, ok)
	}
	if _, ok := src.RenamedTo(f.c1, "lib/a.dart"); ok {
		t.Error("modified file reported as renamed")
	}
	if _, ok := src.RenamedTo(f.c3, "lib/old.dart"); ok {
		t.Error("no rename after c3")
	}
	if _, ok := src.RenamedTo("", "lib/old.dart"); ok {
		t.Error("empty rev should not report a rename")
	}
	if _, ok := src.RenamedTo("bogus-rev", "lib/old.dart"); ok {
		t.Error("bad rev should not report a rename")
	}

	// Commits: newest first, after rev, limited.
	cs, err := src.Commits(f.c1, "lib/a.dart", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Hash != f.c3 || cs[1].Hash != f.c2 {
		t.Fatalf("Commits = %+v", cs)
	}
	c := cs[0]
	if c.Subject != "third: rename" || c.Author != "Ana Tester" || c.Date != "2026-01-02" || !strings.HasPrefix(f.c3, c.Short) || c.Short == "" {
		t.Errorf("commit fields = %+v", c)
	}
	if cs, _ := src.Commits(f.c1, "lib/a.dart", 1); len(cs) != 1 || cs[0].Hash != f.c3 {
		t.Errorf("limited Commits = %+v", cs)
	}
	if cs, err := src.Commits(f.c3, "lib/a.dart", 0); err != nil || len(cs) != 0 {
		t.Errorf("Commits after HEAD = %+v, %v", cs, err)
	}
	// --follow tracks the renamed file back to its old name.
	if cs, _ := src.Commits(f.c1, "lib/new.dart", 0); len(cs) != 1 || cs[0].Hash != f.c3 {
		t.Errorf("Commits on renamed file = %+v", cs)
	}
	if _, err := src.Commits("", "lib/a.dart", 0); !errors.Is(err, ErrNoHistory) {
		t.Errorf("Commits empty rev: %v", err)
	}
	if _, err := src.Commits("bogus-rev", "lib/a.dart", 0); err == nil {
		t.Error("Commits with bad rev should fail")
	}
}

func TestGitWorkingTreeDirtyAndUntracked(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.dir, "lib/a.dart", "edited\n")
	writeFile(t, f.dir, "extra/new.txt", "untracked\n")
	writeFile(t, f.dir, ".gitignore", "*.log\n")
	writeFile(t, f.dir, "debug.log", "ignored\n")
	if err := os.Remove(filepath.Join(f.dir, "README")); err != nil {
		t.Fatal(err)
	}
	src, err := Open(Spec{Name: "up", Path: f.dir}, t.TempDir(), t.TempDir(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !src.Dirty() {
		t.Error("modified tree should be dirty")
	}
	// Working tree reads see the edit; history reads do not.
	if b, _ := src.ReadFile("lib/a.dart"); string(b) != "edited\n" {
		t.Errorf("ReadFile = %q", b)
	}
	if b, _ := src.ReadFileAt("HEAD", "lib/a.dart"); string(b) != "v3\n" {
		t.Errorf("ReadFileAt HEAD = %q", b)
	}
	files, err := src.ListFiles()
	if err != nil {
		t.Fatal(err)
	}
	// Untracked files are listed, ignored and deleted ones are not.
	if want := []string{".gitignore", "extra/new.txt", "lib/a.dart", "lib/new.dart"}; !reflect.DeepEqual(files, want) {
		t.Errorf("ListFiles = %v, want %v", files, want)
	}
}

func TestGitSubdirectory(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.dir, "README", "changed outside the subdirectory\n")
	sub := filepath.Join(f.dir, "lib")

	t.Run("working tree", func(t *testing.T) {
		src, err := Open(Spec{Name: "up", Path: sub}, t.TempDir(), t.TempDir(), false, nil)
		if err != nil {
			t.Fatal(err)
		}
		// Dirtiness is scoped to the target directory.
		if src.Dirty() {
			t.Error("change outside the target directory made it dirty")
		}
		files, _ := src.ListFiles()
		if want := []string{"a.dart", "new.dart"}; !reflect.DeepEqual(files, want) {
			t.Errorf("ListFiles = %v, want %v", files, want)
		}
		if b, err := src.ReadFileAt(f.c2, "a.dart"); err != nil || string(b) != "v2\n" {
			t.Errorf("ReadFileAt = %q, %v", b, err)
		}
		if p, ok := src.RenamedTo(f.c1, "old.dart"); !ok || p != "new.dart" {
			t.Errorf("RenamedTo = %q, %v", p, ok)
		}
	})

	t.Run("pinned rev", func(t *testing.T) {
		src, err := Open(Spec{Name: "up", Path: sub, Rev: f.c1}, t.TempDir(), t.TempDir(), false, nil)
		if err != nil {
			t.Fatal(err)
		}
		files, err := src.ListFiles()
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"a.dart", "old.dart"}; !reflect.DeepEqual(files, want) {
			t.Errorf("ListFiles = %v, want %v", files, want)
		}
		if b, err := src.ReadFile("a.dart"); err != nil || string(b) != "v1\n" {
			t.Errorf("ReadFile = %q, %v", b, err)
		}
	})
}

func TestGitPinnedRev(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.dir, "lib/a.dart", "uncommitted\n") // ignored in rev mode
	for _, rev := range []string{f.c1, "v1", f.c1[:8]} {
		t.Run(rev, func(t *testing.T) {
			src, err := Open(Spec{Name: "up", Path: f.dir, Rev: rev}, t.TempDir(), t.TempDir(), false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if src.Revision() != f.c1 {
				t.Errorf("Revision = %s, want %s", src.Revision(), f.c1)
			}
			if src.Dirty() {
				t.Error("pinned rev is never dirty")
			}
			if b, err := src.ReadFile("lib/a.dart"); err != nil || string(b) != "v1\n" {
				t.Errorf("ReadFile = %q, %v", b, err)
			}
			if _, err := src.ReadFile("lib/new.dart"); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("ReadFile of later file: %v", err)
			}
			files, err := src.ListFiles()
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"README", "lib/a.dart", "lib/old.dart"}; !reflect.DeepEqual(files, want) {
				t.Errorf("ListFiles = %v, want %v", files, want)
			}
			// History is relative to the pinned rev, not HEAD.
			if cs, err := src.Commits(f.c1, "lib/a.dart", 0); err != nil || len(cs) != 0 {
				t.Errorf("Commits = %+v, %v", cs, err)
			}
			if _, ok := src.RenamedTo(f.c1, "lib/old.dart"); ok {
				t.Error("rename after pinned rev should not be visible")
			}
		})
	}

	src, err := Open(Spec{Name: "up", Path: f.dir, Rev: f.c2}, t.TempDir(), t.TempDir(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cs, _ := src.Commits(f.c1, "lib/a.dart", 0); len(cs) != 1 || cs[0].Hash != f.c2 {
		t.Errorf("Commits up to pinned rev = %+v", cs)
	}
}

func TestFetchLocalURL(t *testing.T) {
	f := newFixture(t)
	run(t, f.dir, "branch", "stable", f.c2)
	url := "file://" + filepath.ToSlash(f.dir)
	cache := t.TempDir()
	project := t.TempDir()

	// Local specs are a no-op.
	if err := Fetch(Spec{Name: "loc", Path: f.dir}, project, cache, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cache, "repos")); !os.IsNotExist(err) {
		t.Errorf("local Fetch touched the cache: %v", err)
	}

	// Open with autoFetch clones on first use.
	var log bytes.Buffer
	spec := Spec{Name: "up", URL: url}
	src, err := Open(spec, project, cache, true, &log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "cloning "+url) || !strings.Contains(log.String(), "up at "+f.c3[:10]) {
		t.Errorf("log = %q", log.String())
	}
	if src.Revision() != f.c3 || src.Location() != url || src.Dirty() {
		t.Errorf("rev=%s loc=%s dirty=%v", src.Revision(), src.Location(), src.Dirty())
	}
	if b, err := src.ReadFile("lib/a.dart"); err != nil || string(b) != "v3\n" {
		t.Errorf("ReadFile = %q, %v", b, err)
	}
	if b, err := src.ReadFileAt(f.c1, "lib/a.dart"); err != nil || string(b) != "v1\n" {
		t.Errorf("ReadFileAt = %q, %v", b, err)
	}

	// Already fetched: Open without autoFetch works.
	if _, err := Open(spec, project, cache, false, nil); err != nil {
		t.Errorf("Open after fetch: %v", err)
	}

	// Re-fetch at a branch rev updates the checkout.
	log.Reset()
	spec.Rev = "stable"
	if err := Fetch(spec, project, cache, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "fetching "+url) {
		t.Errorf("log = %q", log.String())
	}
	src, err = Open(spec, project, cache, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if src.Revision() != f.c2 {
		t.Errorf("Revision after fetch stable = %s, want %s", src.Revision(), f.c2)
	}
	if b, _ := src.ReadFile("lib/a.dart"); string(b) != "v2\n" {
		t.Errorf("ReadFile at stable = %q", b)
	}

	// New upstream commits arrive on re-fetch of the default branch.
	writeFile(t, f.dir, "lib/a.dart", "v4\n")
	c4 := commitAll(t, f.dir, "fourth")
	spec.Rev = ""
	if err := Fetch(spec, project, cache, nil); err != nil {
		t.Fatal(err)
	}
	if src, _ := Open(spec, project, cache, false, nil); src == nil || src.Revision() != c4 {
		t.Errorf("Revision after upstream commit = %v, want %s", src, c4)
	}

	// Tags and raw commits resolve too.
	for rev, want := range map[string]string{"v1": f.c1, f.c2: f.c2} {
		spec.Rev = rev
		if err := Fetch(spec, project, cache, nil); err != nil {
			t.Fatalf("Fetch %s: %v", rev, err)
		}
		if src, _ := Open(spec, project, cache, false, nil); src == nil || src.Revision() != want {
			t.Errorf("Fetch %s: revision = %v, want %s", rev, src, want)
		}
	}

	spec.Rev = "no-such-rev"
	if err := Fetch(spec, project, cache, nil); err == nil || !strings.Contains(err.Error(), `cannot resolve revision "no-such-rev"`) {
		t.Errorf("Fetch bad rev: %v", err)
	}
}

func TestFetchCloneFailure(t *testing.T) {
	hermeticGit(t)
	missing := "file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "does-not-exist"))
	cache := t.TempDir()
	_, err := Open(Spec{Name: "up", URL: missing}, t.TempDir(), cache, true, nil)
	if err == nil || !strings.Contains(err.Error(), "git clone") {
		t.Errorf("err = %v, want a git clone error", err)
	}
}

func TestShortHash(t *testing.T) {
	if got := shortHash("0123456789abcdef"); got != "0123456789" {
		t.Errorf("shortHash = %q", got)
	}
	if got := shortHash("abc"); got != "abc" {
		t.Errorf("shortHash short = %q", got)
	}
}
