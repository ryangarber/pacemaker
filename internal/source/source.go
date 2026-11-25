// Package source gives uniform read access to a target ("upstream")
// repository, whether it is a remote URL (cloned into the cache), a local git
// checkout, a specific revision of a local repo, or a plain directory.
package source

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Spec describes where a target lives.
type Spec struct {
	Name string
	URL  string // remote git URL (cloned into the cache)
	Path string // local directory (absolute or relative to the project)
	Rev  string // branch, tag or commit; empty = default branch / working tree
}

// Commit is a short commit description.
type Commit struct {
	Hash    string `json:"hash" yaml:"hash"`
	Short   string `json:"short" yaml:"short"`
	Author  string `json:"author" yaml:"author"`
	Date    string `json:"date" yaml:"date"`
	Subject string `json:"subject" yaml:"subject"`
}

// ErrNoHistory is returned when historical content is unavailable.
var ErrNoHistory = errors.New("no version history available")

// Source is a readable snapshot of a target repository.
type Source interface {
	Name() string
	Location() string
	Revision() string // commit hash, or "" when not a git repository
	Dirty() bool      // working tree has uncommitted changes
	ReadFile(p string) ([]byte, error)
	ReadFileAt(rev, p string) ([]byte, error)
	ListFiles() ([]string, error)
	// RenamedTo returns the current path of a file that was at oldPath in rev.
	RenamedTo(rev, oldPath string) (string, bool)
	// Commits lists commits touching p after rev (newest first).
	Commits(rev, p string, limit int) ([]Commit, error)
}

// LooksLikeURL decides whether a location string is a remote URL.
func LooksLikeURL(s string) bool {
	return strings.Contains(s, "://") || regexp.MustCompile(`^[\w.-]+@[\w.-]+:`).MatchString(s)
}

// CacheDir returns where a remote target is cloned.
func CacheDir(cacheRoot, name string) string {
	return filepath.Join(cacheRoot, "repos", sanitize(name))
}

func sanitize(s string) string {
	return regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(s, "_")
}

// Open returns a Source for spec. Remote targets are cloned on first use
// when autoFetch is set.
func Open(spec Spec, projectRoot, cacheRoot string, autoFetch bool, log io.Writer) (Source, error) {
	if spec.URL != "" {
		dir := CacheDir(cacheRoot, spec.Name)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			if !autoFetch {
				return nil, fmt.Errorf("target %q is not fetched yet; run `pacemaker fetch`", spec.Name)
			}
			if err := Fetch(spec, projectRoot, cacheRoot, log); err != nil {
				return nil, err
			}
		}
		return newRepo(spec, dir, "")
	}
	dir := spec.Path
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(projectRoot, dir)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("target %q: %s is not a directory", spec.Name, dir)
	}
	return newRepo(spec, dir, spec.Rev)
}

// Fetch clones or updates a remote target and checks out the configured rev.
func Fetch(spec Spec, projectRoot, cacheRoot string, log io.Writer) error {
	if spec.URL == "" {
		return nil // local targets are always live
	}
	if log == nil {
		log = io.Discard
	}
	dir := CacheDir(cacheRoot, spec.Name)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		fmt.Fprintf(log, "cloning %s → %s\n", spec.URL, dir)
		if _, err := git("", "clone", "--quiet", "--filter=blob:none", "--no-checkout", spec.URL, dir); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(log, "fetching %s\n", spec.URL)
		if _, err := git(dir, "remote", "set-url", "origin", spec.URL); err != nil {
			return err
		}
		if _, err := git(dir, "fetch", "--quiet", "--prune", "--tags", "--force", "origin"); err != nil {
			return err
		}
		_, _ = git(dir, "remote", "set-head", "origin", "--auto")
	}
	rev, err := resolveRemoteRev(dir, spec.Rev)
	if err != nil {
		return err
	}
	if _, err := git(dir, "checkout", "--quiet", "--force", "--detach", rev); err != nil {
		return err
	}
	fmt.Fprintf(log, "%s at %s\n", spec.Name, shortHash(rev))
	return nil
}

func resolveRemoteRev(dir, rev string) (string, error) {
	cands := []string{"origin/HEAD"}
	if rev != "" {
		cands = []string{"origin/" + rev, rev}
	}
	for _, c := range cands {
		if out, err := git(dir, "rev-parse", "--verify", "--quiet", c+"^{commit}"); err == nil {
			return strings.TrimSpace(out), nil
		}
	}
	return "", fmt.Errorf("cannot resolve revision %q in %s", rev, dir)
}

func shortHash(h string) string {
	if len(h) > 10 {
		return h[:10]
	}
	return h
}

func git(dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// repo implements Source for directories, optionally backed by git.
type repo struct {
	spec   Spec
	dir    string
	isGit  bool
	rev    string // pinned revision (rev mode) — empty means working tree
	head   string
	dirty  bool
	mu     sync.Mutex
	batch  *catFile
	cache  map[string][]byte
	listed []string
}

func newRepo(spec Spec, dir, rev string) (*repo, error) {
	r := &repo{spec: spec, dir: dir, cache: map[string][]byte{}}
	if out, err := git(dir, "rev-parse", "--is-inside-work-tree"); err == nil && strings.TrimSpace(out) == "true" {
		r.isGit = true
	}
	if rev != "" {
		if !r.isGit {
			return nil, fmt.Errorf("target %q: rev %q set but %s is not a git repository", spec.Name, rev, dir)
		}
		out, err := git(dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
		if err != nil {
			return nil, fmt.Errorf("target %q: unknown revision %q", spec.Name, rev)
		}
		r.rev = strings.TrimSpace(out)
		r.head = r.rev
		return r, nil
	}
	if r.isGit {
		if out, err := git(dir, "rev-parse", "HEAD"); err == nil {
			r.head = strings.TrimSpace(out)
		}
		if out, err := git(dir, "status", "--porcelain", "--", "."); err == nil && strings.TrimSpace(out) != "" {
			r.dirty = spec.URL == ""
		}
	}
	return r, nil
}

func (r *repo) Name() string { return r.spec.Name }
func (r *repo) Location() string {
	if r.spec.URL != "" {
		return r.spec.URL
	}
	return r.dir
}
func (r *repo) Revision() string { return r.head }
func (r *repo) Dirty() bool      { return r.dirty }

func (r *repo) ReadFile(p string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.cache[p]; ok {
		return b, nil
	}
	var (
		b   []byte
		err error
	)
	if r.rev != "" {
		b, err = r.catFile(r.rev, p)
	} else {
		b, err = os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(p)))
	}
	if err != nil {
		return nil, err
	}
	r.cache[p] = b
	return b, nil
}

func (r *repo) ReadFileAt(rev, p string) ([]byte, error) {
	if !r.isGit || rev == "" {
		return nil, ErrNoHistory
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.catFile(rev, p)
}

func (r *repo) catFile(rev, p string) ([]byte, error) {
	if r.batch == nil {
		cf, err := startCatFile(r.dir)
		if err != nil {
			return nil, err
		}
		r.batch = cf
	}
	return r.batch.get(rev + ":./" + p)
}

func (r *repo) ListFiles() ([]string, error) {
	if r.listed != nil {
		return r.listed, nil
	}
	var files []string
	switch {
	case r.rev != "":
		out, err := git(r.dir, "ls-tree", "-r", "--name-only", "-z", "--full-tree", r.rev)
		if err != nil {
			return nil, err
		}
		prefix, _ := git(r.dir, "rev-parse", "--show-prefix")
		prefix = strings.TrimSpace(prefix)
		for _, f := range strings.Split(out, "\x00") {
			if f != "" && strings.HasPrefix(f, prefix) {
				files = append(files, strings.TrimPrefix(f, prefix))
			}
		}
	case r.isGit:
		out, err := git(r.dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
		if err != nil {
			return nil, err
		}
		for _, f := range strings.Split(out, "\x00") {
			if f == "" {
				continue
			}
			if _, err := os.Lstat(filepath.Join(r.dir, f)); err == nil { // skip deleted
				files = append(files, f)
			}
		}
	default:
		err := filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && (d.Name() == ".git" || d.Name() == ".pacemaker") {
				return filepath.SkipDir
			}
			if d.Type().IsRegular() {
				rel, _ := filepath.Rel(r.dir, p)
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	r.listed = files
	return files, nil
}

func (r *repo) currentRev() string {
	if r.rev != "" {
		return r.rev
	}
	return "HEAD"
}

func (r *repo) RenamedTo(rev, oldPath string) (string, bool) {
	if !r.isGit || rev == "" {
		return "", false
	}
	out, err := git(r.dir, "diff", "--name-status", "-M", "-z", "--relative", rev, r.currentRev())
	if err != nil {
		return "", false
	}
	f := strings.Split(out, "\x00")
	for i := 0; i < len(f); i++ {
		if strings.HasPrefix(f[i], "R") && i+2 < len(f) {
			if f[i+1] == oldPath {
				return f[i+2], true
			}
			i += 2
		} else if f[i] != "" && len(f[i]) <= 4 && i+1 < len(f) {
			i++ // status + one path
		}
	}
	return "", false
}

func (r *repo) Commits(rev, p string, limit int) ([]Commit, error) {
	if !r.isGit || rev == "" {
		return nil, ErrNoHistory
	}
	args := []string{"log", "--format=%H%x1f%h%x1f%an%x1f%ad%x1f%s", "--date=short", "--follow"}
	if limit > 0 {
		args = append(args, "-n", strconv.Itoa(limit))
	}
	args = append(args, rev+".."+r.currentRev(), "--", p)
	out, err := git(r.dir, args...)
	if err != nil {
		return nil, err
	}
	var cs []Commit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) == 5 {
			cs = append(cs, Commit{Hash: f[0], Short: f[1], Author: f[2], Date: f[3], Subject: f[4]})
		}
	}
	return cs, nil
}

// catFile wraps a long-running `git cat-file --batch` process.
type catFile struct {
	in  io.WriteCloser
	out *bufio.Reader
}

func startCatFile(dir string) (*catFile, error) {
	cmd := exec.Command("git", "-C", dir, "cat-file", "--batch")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &catFile{in: in, out: bufio.NewReader(out)}, nil
}

func (c *catFile) get(obj string) ([]byte, error) {
	if _, err := fmt.Fprintln(c.in, obj); err != nil {
		return nil, err
	}
	header, err := c.out.ReadString('\n')
	if err != nil {
		return nil, err
	}
	f := strings.Fields(header)
	if len(f) < 3 || f[1] == "missing" || f[1] == "ambiguous" {
		return nil, fmt.Errorf("%s: %w", obj, fs.ErrNotExist)
	}
	size, err := strconv.Atoi(f[2])
	if err != nil {
		return nil, err
	}
	buf := make([]byte, size+1) // content + trailing LF
	if _, err := io.ReadFull(c.out, buf); err != nil {
		return nil, err
	}
	if f[1] != "blob" {
		return nil, fmt.Errorf("%s is a %s, not a file", obj, f[1])
	}
	return buf[:size], nil
}
