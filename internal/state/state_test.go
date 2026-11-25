package state

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ryangarber/pacemaker/internal/config"
	"github.com/ryangarber/pacemaker/internal/ref"
)

func statePath(root string) string { return filepath.Join(root, config.Dir, config.StateFile) }

func baseline(r string) *Baseline {
	return &Baseline{
		Ref:        r,
		Target:     "up",
		Path:       "lib/a.dart",
		Symbol:     "Foo.bar",
		Range:      ref.Range{Start: 3, End: 9},
		Revision:   "abc123",
		FileHash:   "f1",
		Hash:       "h1",
		NormHash:   "n1",
		LocalHash:  "l1",
		AcceptedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		AcceptedBy: "ana",
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	root := t.TempDir()
	s, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != 1 || s.Links == nil || len(s.Links) != 0 || len(s.Blocks()) != 0 {
		t.Errorf("empty state = %+v", s)
	}
	if s.Get("x", "y") != nil {
		t.Error("Get on empty state should be nil")
	}
}

func TestLoadErrors(t *testing.T) {
	t.Run("corrupt", func(t *testing.T) {
		root := t.TempDir()
		write(t, statePath(root), "version: 1\nlinks: [unclosed\n")
		if _, err := Load(root); err == nil {
			t.Error("expected error for corrupt state")
		}
	})
	t.Run("wrong shape", func(t *testing.T) {
		root := t.TempDir()
		write(t, statePath(root), "version: 1\nlinks: [a, b]\n")
		if _, err := Load(root); err == nil {
			t.Error("expected error for links that are not a map")
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		root := t.TempDir()
		// state.yaml is a directory: not "not exist", so an error.
		if err := os.MkdirAll(statePath(root), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil {
			t.Error("expected error when state.yaml is a directory")
		}
	})
	t.Run("empty file", func(t *testing.T) {
		root := t.TempDir()
		write(t, statePath(root), "")
		s, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if s.Links == nil || s.Version != 1 {
			t.Errorf("empty file state = %+v", s)
		}
	})
	t.Run("null links", func(t *testing.T) {
		root := t.TempDir()
		write(t, statePath(root), "version: 1\nlinks:\n")
		s, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if s.Links == nil {
			t.Error("Links should be non-nil")
		}
	})
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPutGetSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	s, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	b1, b2 := baseline("up:lib/z.dart"), baseline("up:lib/a.dart")
	b2.Symbol, b2.LocalHash, b2.AcceptedBy, b2.Revision = "", "", "", ""
	if err := s.Put("blk", b1, []byte("z content\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("blk", b2, []byte("a content\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("other", baseline("up:x"), []byte{}); err != nil {
		t.Fatal(err)
	}
	if s.Get("blk", "up:lib/z.dart") != b1 || s.Get("blk", "up:lib/a.dart") != b2 || s.Get("blk", "nope") != nil {
		t.Error("Get did not return stored baselines")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(statePath(root))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "# pacemaker state") {
		t.Errorf("missing header:\n%s", text)
	}
	// Refs are sorted within a block, ranges are written in flow style and
	// empty optional fields are omitted.
	if strings.Index(text, "up:lib/a.dart") > strings.Index(text, "up:lib/z.dart") {
		t.Errorf("refs not sorted:\n%s", text)
	}
	if !strings.Contains(text, "range: {start: 3, end: 9}") {
		t.Errorf("range not in flow style:\n%s", text)
	}

	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Links, s.Links) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got.Links, s.Links)
	}
	if !reflect.DeepEqual(got.Blocks(), []string{"blk", "other"}) {
		t.Errorf("Blocks = %v", got.Blocks())
	}
	snap, err := got.Snapshot("blk", "up:lib/a.dart")
	if err != nil || string(snap) != "a content\n" {
		t.Errorf("Snapshot = %q, %v", snap, err)
	}

	// Saving again with no changes is byte-for-byte identical.
	if err := got.Save(); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(statePath(root))
	if string(again) != text {
		t.Errorf("save not deterministic:\n%s\n---\n%s", text, again)
	}
}

func TestPutReplaces(t *testing.T) {
	s, _ := Load(t.TempDir())
	old := baseline("r")
	if err := s.Put("b", old, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	nb := baseline("r")
	nb.Hash = "h2"
	if err := s.Put("b", nb, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if n := len(s.Links["b"]); n != 1 {
		t.Fatalf("got %d baselines, want 1", n)
	}
	if s.Get("b", "r").Hash != "h2" {
		t.Error("baseline not replaced")
	}
	if snap, _ := s.Snapshot("b", "r"); string(snap) != "v2" {
		t.Errorf("snapshot = %q, want v2", snap)
	}
}

func TestRemove(t *testing.T) {
	root := t.TempDir()
	s, _ := Load(root)
	for _, r := range []string{"r1", "r2"} {
		if err := s.Put("b", baseline(r), []byte(r)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Put("c", baseline("r1"), []byte("c")); err != nil {
		t.Fatal(err)
	}

	s.Remove("b", "r1")
	if s.Get("b", "r1") != nil || s.Get("b", "r2") == nil {
		t.Errorf("single-ref remove: %+v", s.Links["b"])
	}
	if _, err := os.Stat(s.SnapshotPath("b", "r1")); !os.IsNotExist(err) {
		t.Errorf("snapshot r1 not removed: %v", err)
	}
	s.Remove("b", "missing") // no-op
	if s.Get("b", "r2") == nil {
		t.Error("removing an unknown ref dropped others")
	}

	// Removing the last ref drops the block entirely.
	s.Remove("b", "r2")
	if _, ok := s.Links["b"]; ok {
		t.Error("block should be gone after its last ref is removed")
	}

	// Whole-block removal deletes snapshots and the block's snapshot dir.
	snapDir := filepath.Dir(s.SnapshotPath("c", "r1"))
	s.Remove("c", "")
	if _, ok := s.Links["c"]; ok {
		t.Error("block c still present")
	}
	if _, err := os.Stat(snapDir); !os.IsNotExist(err) {
		t.Errorf("snapshot dir not removed: %v", err)
	}
	s.Remove("never", "") // no-op, no panic
	if len(s.Blocks()) != 0 {
		t.Errorf("Blocks = %v", s.Blocks())
	}
}

func TestSnapshotMissing(t *testing.T) {
	s, _ := Load(t.TempDir())
	if _, err := s.Snapshot("b", "r"); !os.IsNotExist(err) {
		t.Errorf("err = %v, want not-exist", err)
	}
}

func TestSnapshotPathIsSafe(t *testing.T) {
	root := t.TempDir()
	s, _ := Load(root)
	base := filepath.Join(root, config.Dir, config.SnapDir)
	name := regexp.MustCompile(`^(.+)-[0-9a-f]{8}\.snap$`)
	cases := []struct {
		block, ref, dir, stem string
	}{
		{"a.one", "up:lib/a.dart#L1-L5", "a.one", "up_lib_a.dart_L1-L5"},
		{"../../etc", "../passwd", "etc", "passwd"},
		{"///", "", "_", "_"},
		{"_x_", ".hidden", "x", "hidden"},
	}
	for _, tc := range cases {
		got := s.SnapshotPath(tc.block, tc.ref)
		dir, file := filepath.Split(got)
		m := name.FindStringSubmatch(file)
		if filepath.Clean(dir) != filepath.Join(base, tc.dir) || m == nil || m[1] != tc.stem {
			t.Errorf("SnapshotPath(%q, %q) = %s, want %s/%s-<hash>.snap", tc.block, tc.ref, got, filepath.Join(base, tc.dir), tc.stem)
		}
		if !strings.HasPrefix(got, base+string(filepath.Separator)) {
			t.Errorf("SnapshotPath(%q, %q) escapes snapshot dir: %s", tc.block, tc.ref, got)
		}
	}
}

// Refs and blocks that sanitise to the same name keep separate snapshots.
func TestSnapshotsDoNotCollide(t *testing.T) {
	s, _ := Load(t.TempDir())
	pairs := [][2]string{
		{"blk", "up:lib/a b.dart#L1-L2"}, {"blk", "up:lib/a_b.dart#L1-L2"},
		{"blk", "x#Foo"}, {"blk", "x?Foo"},
		{"a b", "up:f#L1"}, {"a_b", "up:f#L1"}, // same directory, same ref
	}
	for i, p := range pairs {
		if err := s.Put(p[0], &Baseline{Ref: p[1]}, []byte{byte('0' + i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i, p := range pairs {
		if b, err := s.Snapshot(p[0], p[1]); err != nil || string(b) != string(rune('0'+i)) {
			t.Errorf("snapshot of %q %q = %q, %v; want %q", p[0], p[1], b, err, string(rune('0'+i)))
		}
	}
	s.Remove("a b", "")
	if b, err := s.Snapshot("a_b", "up:f#L1"); err != nil || string(b) != "5" {
		t.Errorf("removing block %q took %q's snapshot with it: %q, %v", "a b", "a_b", b, err)
	}
}

// Snapshots written before names carried a hash are still read, and removed
// with their baseline.
func TestLegacySnapshotPath(t *testing.T) {
	s, _ := Load(t.TempDir())
	legacy := s.legacySnapshotPath("blk", "up:f#L1")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Links["blk"] = []*Baseline{{Ref: "up:f#L1"}}
	if b, err := s.Snapshot("blk", "up:f#L1"); err != nil || string(b) != "old" {
		t.Fatalf("legacy snapshot = %q, %v", b, err)
	}
	// Accepting again writes the new name, which then wins.
	if err := s.Put("blk", &Baseline{Ref: "up:f#L1"}, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.Snapshot("blk", "up:f#L1"); string(b) != "new" {
		t.Errorf("snapshot after re-accept = %q, want new", b)
	}
	s.Remove("blk", "up:f#L1")
	for _, p := range []string{legacy, s.SnapshotPath("blk", "up:f#L1")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s left behind after remove", p)
		}
	}
	if _, err := s.Snapshot("blk", "up:f#L1"); !os.IsNotExist(err) {
		t.Errorf("snapshot of a removed baseline: err = %v, want not-exist", err)
	}
}

func TestSafe(t *testing.T) {
	long := strings.Repeat("a", 200)
	longB := strings.Repeat("a", 199) + "b"
	sl, slB := safe(long), safe(longB)
	if len(sl) != 100+1+12 {
		t.Errorf("long name len = %d (%s)", len(sl), sl)
	}
	if !strings.HasPrefix(sl, strings.Repeat("a", 100)+"-") {
		t.Errorf("long name = %s", sl)
	}
	if sl == slB {
		t.Error("distinct long names must not collide")
	}
	if got := safe(strings.Repeat("x", 120)); got != strings.Repeat("x", 120) {
		t.Errorf("120-char name should be kept as is, got %d chars", len(got))
	}
	for in, want := range map[string]string{
		"plain":       "plain",
		"a b/c":       "a_b_c",
		"Foo.bar-1_2": "Foo.bar-1_2",
		"":            "_",
		"..":          "_",
	} {
		if got := safe(in); got != want {
			t.Errorf("safe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHash(t *testing.T) {
	h := Hash([]byte("hello"))
	if len(h) != 24 {
		t.Errorf("Hash length = %d", len(h))
	}
	if h != Hash([]byte("hello")) || h == Hash([]byte("hello!")) {
		t.Error("Hash not deterministic or not content-sensitive")
	}
	// sha256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e...
	if h != "2cf24dba5fb0a30e26e83b2a" {
		t.Errorf("Hash = %s", h)
	}
}
