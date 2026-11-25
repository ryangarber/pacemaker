// Package engine ties markers, targets, state and language modules together
// to compute drift and coverage. It contains no language- or
// format-specific logic: capabilities come from lang modules via interfaces.
package engine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ryangarber/pacemaker/internal/config"
	"github.com/ryangarber/pacemaker/internal/diff"
	"github.com/ryangarber/pacemaker/internal/lang"
	"github.com/ryangarber/pacemaker/internal/marker"
	"github.com/ryangarber/pacemaker/internal/model"
	"github.com/ryangarber/pacemaker/internal/ref"
	"github.com/ryangarber/pacemaker/internal/source"
	"github.com/ryangarber/pacemaker/internal/state"
)

// Engine performs analysis for one project.
type Engine struct {
	Root      string
	Cfg       *config.Config
	State     *state.State
	Log       io.Writer
	AutoFetch bool

	sources map[string]source.Source
	srcErrs map[string]error
	scanner *marker.Scanner

	blocks   []marker.Block
	problems []marker.Problem
	scanned  bool
	ctx      []*linkCtx
}

// linkCtx keeps resolution data needed for accept.
type linkCtx struct {
	L         *model.Link
	Block     marker.Block
	Site      marker.RefSite
	Snippet   []byte
	FileHash  string
	Hash      string
	NormHash  string
	LocalHash string
	Resolved  bool
}

// Options controls how much work Analyze does.
type Options struct {
	Diffs    bool
	Commits  bool
	Coverage bool
	Targets  []string // restrict to these targets (empty = all)
}

// Open loads config and state for the project at root.
func Open(root string) (*Engine, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	st, err := state.Load(root)
	if err != nil {
		return nil, err
	}
	return &Engine{
		Root: root, Cfg: cfg, State: st, Log: io.Discard, AutoFetch: true,
		sources: map[string]source.Source{}, srcErrs: map[string]error{},
		scanner: marker.NewScanner(cfg.Marker),
	}, nil
}

// CacheRoot is the gitignored cache directory.
func (e *Engine) CacheRoot() string { return filepath.Join(e.Root, config.Dir, config.CacheDir) }

// Source opens (and memoises) a target.
func (e *Engine) Source(name string) (source.Source, error) {
	if s, ok := e.sources[name]; ok {
		return s, nil
	}
	if err, ok := e.srcErrs[name]; ok {
		return nil, err
	}
	t, ok := e.Cfg.Targets[name]
	if !ok {
		err := fmt.Errorf("unknown target %q", name)
		e.srcErrs[name] = err
		return nil, err
	}
	s, err := source.Open(t.Spec(name), e.Root, e.CacheRoot(), e.AutoFetch, e.Log)
	if err != nil {
		e.srcErrs[name] = err
		return nil, err
	}
	e.sources[name] = s
	return s, nil
}

// Fetch updates remote targets.
func (e *Engine) Fetch(names []string) error {
	if len(names) == 0 {
		names = e.Cfg.TargetNames()
	}
	var errs []error
	for _, n := range names {
		t, ok := e.Cfg.Targets[n]
		if !ok {
			errs = append(errs, fmt.Errorf("unknown target %q", n))
			continue
		}
		if err := source.Fetch(t.Spec(n), e.Root, e.CacheRoot(), e.Log); err != nil {
			errs = append(errs, err)
		}
		delete(e.sources, n)
		delete(e.srcErrs, n)
	}
	return errors.Join(errs...)
}

// Scan finds all markers in the local project.
func (e *Engine) Scan() ([]marker.Block, []marker.Problem, error) {
	if e.scanned {
		return e.blocks, e.problems, nil
	}
	filter := e.Cfg.Local.Set()
	var blocks []marker.Block
	var probs []marker.Problem
	err := filepath.WalkDir(e.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(e.Root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (d.Name() == ".git" || d.Name() == config.Dir || filter.ExcludesDir(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !filter.Match(rel) {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > 4<<20 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || isBinary(b) || !e.scanner.Contains(b) {
			return nil
		}
		bs, ps := e.scanner.Scan(rel, b)
		blocks = append(blocks, bs...)
		probs = append(probs, ps...)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	// Duplicate IDs.
	seen := map[string]marker.Block{}
	var uniq []marker.Block
	for _, b := range blocks {
		if prev, ok := seen[b.ID]; ok {
			probs = append(probs, marker.Problem{File: b.File, Line: b.MarkerLine,
				Message: fmt.Sprintf("duplicate block id %q (first defined at %s:%d)", b.ID, prev.File, prev.MarkerLine)})
			continue
		}
		seen[b.ID] = b
		uniq = append(uniq, b)
	}
	// Unknown targets.
	for _, b := range uniq {
		for _, rs := range b.Refs {
			t := rs.Ref.Target
			if t == "" {
				if _, err := e.Cfg.ResolveTarget(""); err != nil {
					probs = append(probs, marker.Problem{File: b.File, Line: rs.Line, Message: "reference has no target and " + err.Error()})
				}
			} else if _, ok := e.Cfg.Targets[t]; !ok {
				probs = append(probs, marker.Problem{File: b.File, Line: rs.Line, Message: fmt.Sprintf("unknown target %q", t)})
			}
		}
	}
	sort.Slice(uniq, func(i, j int) bool { return uniq[i].ID < uniq[j].ID })
	sort.Slice(probs, func(i, j int) bool {
		if probs[i].File != probs[j].File {
			return probs[i].File < probs[j].File
		}
		return probs[i].Line < probs[j].Line
	})
	e.blocks, e.problems, e.scanned = uniq, probs, true
	return uniq, probs, nil
}

func isBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(b[:n], 0) >= 0
}

// refKeys assigns each ref of a block a stable key: the canonical ref without
// line numbers (so re-pointing after upstream moves keeps identity), with an
// ordinal suffix when a block has several line refs into the same file.
func (e *Engine) refKeys(b marker.Block) []string {
	def, _ := e.Cfg.ResolveTarget("")
	keys := make([]string, len(b.Refs))
	count := map[string]int{}
	for i, rs := range b.Refs {
		r := rs.Ref.WithTarget(def)
		r.Lines = ref.Range{}
		k := r.String()
		count[k]++
		if count[k] > 1 {
			k = fmt.Sprintf("%s@%d", k, count[k])
		}
		keys[i] = k
	}
	return keys
}

// Analyze resolves every link and (optionally) computes coverage.
func (e *Engine) Analyze(opts Options) (*model.Report, error) {
	blocks, probs, err := e.Scan()
	if err != nil {
		return nil, err
	}
	only := map[string]bool{}
	for _, t := range opts.Targets {
		only[t] = true
	}
	def, _ := e.Cfg.ResolveTarget("")
	rep := &model.Report{
		Title:     e.Cfg.Report.Title,
		Project:   filepath.Base(e.Root),
		Generated: time.Now(),
		Problems:  probs,
	}
	if rep.Title == "" {
		rep.Title = "pacemaker report — " + rep.Project
	}
	e.ctx = nil
	for _, b := range blocks {
		keys := e.refKeys(b)
		for i, rs := range b.Refs {
			r := rs.Ref.WithTarget(def)
			if len(only) > 0 && !only[r.Target] {
				continue
			}
			lc := e.resolve(b, rs, r, keys[i], opts)
			e.ctx = append(e.ctx, lc)
			rep.Links = append(rep.Links, lc.L)
		}
	}
	if opts.Coverage {
		for _, name := range e.Cfg.TargetNames() {
			if len(only) > 0 && !only[name] {
				continue
			}
			rep.Targets = append(rep.Targets, e.coverage(name, rep.Links))
		}
	} else {
		for _, name := range e.Cfg.TargetNames() {
			if len(only) > 0 && !only[name] {
				continue
			}
			rep.Targets = append(rep.Targets, e.targetHeader(name))
		}
	}
	rep.Sort()
	rep.Count()
	rep.Failing = e.failing(rep)
	return rep, nil
}

func (e *Engine) failing(rep *model.Report) bool {
	if len(rep.Problems) > 0 {
		return true
	}
	fail := map[string]bool{}
	for _, s := range e.Cfg.Drift.FailOn {
		fail[s] = true
	}
	if e.Cfg.Drift.Cosmetic == "fail" {
		fail[string(model.Cosmetic)] = true
	}
	for _, l := range rep.Links {
		if fail[string(l.Status)] {
			return true
		}
	}
	return false
}

func (e *Engine) targetHeader(name string) model.TargetReport {
	t := e.Cfg.Targets[name]
	tr := model.TargetReport{Name: name, Location: t.Location(), Include: t.Include, Exclude: t.Exclude}
	src, err := e.Source(name)
	if err != nil {
		tr.Error = err.Error()
		return tr
	}
	tr.Revision, tr.Dirty = src.Revision(), src.Dirty()
	return tr
}

func normalize(path string, b []byte) []byte {
	l := lang.ForPath(path)
	if n, ok := l.(lang.Normalizer); ok {
		return n.Normalize(b)
	}
	if n, ok := lang.Fallback().(lang.Normalizer); ok {
		return n.Normalize(b)
	}
	return b
}

func joinLines(lines []string, r ref.Range) []byte {
	if r.Len() == 0 {
		return nil
	}
	return []byte(strings.Join(lines[r.Start-1:r.End], "\n") + "\n")
}

func (e *Engine) localHash(b marker.Block) string {
	if b.Kind == marker.KindSkip {
		return ""
	}
	content, err := os.ReadFile(filepath.Join(e.Root, filepath.FromSlash(b.File)))
	if err != nil {
		return ""
	}
	lines := diff.SplitLines(string(content))
	r := ref.Range{Start: b.Start, End: min(b.End, len(lines))}
	if b.Kind == marker.KindFile {
		// Exclude marker lines so editing the marker itself isn't a change.
		var keep []string
		for _, l := range lines {
			if !e.scanner.Contains([]byte(l)) {
				keep = append(keep, l)
			}
		}
		lines, r = keep, ref.Range{Start: 1, End: len(keep)}
	}
	return state.Hash(normalize(b.File, joinLines(lines, r)))
}

func (e *Engine) resolve(b marker.Block, rs marker.RefSite, r ref.Ref, key string, opts Options) *linkCtx {
	l := &model.Link{
		Block: b.ID, Kind: b.Kind, LocalFile: b.File, LocalStart: b.Start, LocalEnd: b.End,
		MarkerLine: rs.Line, Note: b.Note, Tags: b.Tags,
		Key: key, Ref: r.String(), RefKind: r.Kind(), Target: r.Target, Path: r.Path, Current: r.Path,
		Symbol: r.Symbol, Language: lang.ForPath(r.Path).ID(),
	}
	lc := &linkCtx{L: l, Block: b, Site: rs, LocalHash: e.localHash(b)}
	base := e.State.Get(b.ID, key)
	if base != nil {
		l.Baseline = &model.Baseline{Revision: base.Revision, Path: base.Path, Range: base.Range,
			AcceptedAt: base.AcceptedAt, AcceptedBy: base.AcceptedBy}
		l.LocalChanged = base.LocalHash != "" && lc.LocalHash != "" && base.LocalHash != lc.LocalHash
	}
	fail := func(s model.Status, f string, a ...any) *linkCtx {
		l.Status, l.Detail = s, fmt.Sprintf(f, a...)
		return lc
	}
	src, err := e.Source(r.Target)
	if err != nil {
		return fail(model.Error, "%v", err)
	}
	l.Revision = src.Revision()

	path := r.Path
	cur, err := src.ReadFile(path)
	if err != nil && base != nil {
		if np, ok := src.RenamedTo(base.Revision, base.Path); ok {
			if c2, err2 := src.ReadFile(np); err2 == nil {
				path, cur, err = np, c2, nil
				l.Current = np
			}
		}
	}
	if err != nil {
		return fail(model.Missing, "file %s not found in %s", path, r.Target)
	}
	lines := diff.SplitLines(string(cur))
	lc.FileHash = state.Hash(cur)
	language := lang.ForPath(path)

	var rng ref.Range
	switch r.Kind() {
	case "file":
		rng = ref.Range{Start: 1, End: len(lines)}
	case "symbol":
		sp, ok := language.(lang.SymbolProvider)
		if !ok {
			return fail(model.Error, "language module %q cannot resolve symbols", language.ID())
		}
		syms := sp.Symbols(cur)
		sym, ok := lang.FindSymbol(syms, r.Symbol)
		if !ok {
			if base != nil {
				if guess, ok := e.guessRename(b.ID, base, syms, lines); ok {
					nr := r
					nr.Path, nr.Symbol = path, guess.Name
					l.SuggestedRef = nr.String()
					return fail(model.Missing, "symbol %s not found in %s; probably renamed to %s", r.Symbol, path, guess.Name)
				}
			}
			return fail(model.Missing, "symbol %s not found in %s", r.Symbol, path)
		}
		rng = ref.Range{Start: sym.Start, End: sym.End}
	case "lines":
		rng = r.Lines
		if base != nil && !base.Range.IsZero() {
			if base.Range != r.Lines && base.Path == r.Path {
				// Marker was re-pointed by hand since accept: if it doesn't
				// match where the old content went, it's a new mapping.
				mapped, ok, _ := e.relocate(src, base, b.ID, lines)
				if !ok || mapped != r.Lines {
					l.Status, l.Detail = model.New, "reference changed since last accept"
					lc.Snippet = joinLines(lines, clampRange(r.Lines, len(lines)))
					lc.Hash, lc.NormHash = state.Hash(lc.Snippet), state.Hash(normalize(path, lc.Snippet))
					l.Range, lc.Resolved = clampRange(r.Lines, len(lines)), r.Lines.Start <= len(lines)
					return lc
				}
			}
			mapped, ok, how := e.relocate(src, base, b.ID, lines)
			if !ok {
				return fail(model.Missing, "the referenced lines were removed upstream (%s)", how)
			}
			rng = mapped
		} else if r.Lines.Start > len(lines) {
			return fail(model.Missing, "lines %s are beyond the end of %s (%d lines)", r.Lines, path, len(lines))
		}
		rng = clampRange(rng, len(lines))
	}
	l.Range = rng
	lc.Snippet = joinLines(lines, rng)
	lc.Hash = state.Hash(lc.Snippet)
	lc.NormHash = state.Hash(normalize(path, lc.Snippet))
	lc.Resolved = true

	// Suggest an updated reference when lines moved or the file was renamed.
	if (r.Kind() == "lines" && rng != r.Lines) || path != r.Path {
		nr := r
		nr.Path = path
		if r.Kind() == "lines" {
			nr.Lines = rng
		}
		l.SuggestedRef = nr.String()
	}

	if base == nil {
		return fail(model.New, "not yet accepted")
	}
	switch {
	case lc.Hash == base.Hash:
		l.Status = model.OK
		if l.SuggestedRef != "" {
			l.Status = model.Moved
			l.Detail = "content unchanged; reference is now " + l.SuggestedRef
		}
	case lc.NormHash == base.NormHash:
		l.Status, l.Detail = model.Cosmetic, "formatting/comment-only changes upstream"
	default:
		l.Status, l.Detail = model.Drifted, "upstream content changed since accepted"
	}
	if l.Status == model.Cosmetic || l.Status == model.Drifted {
		e.explain(lc, base, language, opts)
	}
	return lc
}

// guessRename finds the current symbol most similar to the accepted snapshot.
func (e *Engine) guessRename(block string, base *state.Baseline, syms []lang.Symbol, lines []string) (lang.Symbol, bool) {
	snap, err := e.State.Snapshot(block, base.Ref)
	if err != nil {
		return lang.Symbol{}, false
	}
	// Compare word sequences so one-line declarations still score sensibly.
	old := strings.Fields(string(snap))
	var best lang.Symbol
	bestScore := 0.0
	for _, s := range syms {
		if s.Container || s.End > len(lines) || s.Start < 1 {
			continue
		}
		cur := strings.Fields(strings.Join(lines[s.Start-1:s.End], "\n"))
		if sc := diff.Similarity(old, cur); sc > bestScore {
			best, bestScore = s, sc
		}
	}
	return best, bestScore >= 0.6
}

func clampRange(r ref.Range, n int) ref.Range {
	if r.End > n {
		r.End = n
	}
	if r.Start > r.End {
		r.Start = r.End
	}
	if r.Start < 1 {
		r.Start = 1
	}
	return r
}

// relocate maps a baseline range to the current file. It prefers an exact
// diff against the baseline revision of the file; otherwise it searches for
// the snapshot content.
func (e *Engine) relocate(src source.Source, base *state.Baseline, block string, cur []string) (ref.Range, bool, string) {
	if old, err := src.ReadFileAt(base.Revision, base.Path); err == nil && state.Hash(old) == base.FileHash {
		ol := diff.SplitLines(string(old))
		s, en, ok := diff.MapRange(diff.Lines(ol, cur), len(ol), len(cur), base.Range.Start, base.Range.End)
		return ref.Range{Start: s, End: en}, ok, "diff from " + short(base.Revision)
	}
	snap, err := e.State.Snapshot(block, base.Ref)
	if err != nil {
		return clampRange(base.Range, len(cur)), base.Range.Start <= len(cur), "no snapshot; using recorded lines"
	}
	r, ok := locate(diff.SplitLines(string(snap)), cur, base.Range)
	return r, ok, "snapshot search"
}

// locate finds snapshot lines in cur: exact match nearest the hint, else the
// best fuzzy alignment.
func locate(snap, cur []string, hint ref.Range) (ref.Range, bool) {
	n := len(snap)
	if n == 0 {
		return hint, false
	}
	best, bestDist := -1, 1<<30
	for i := 0; i+n <= len(cur); i++ {
		if cur[i] != snap[0] {
			continue
		}
		match := true
		for k := 1; k < n; k++ {
			if cur[i+k] != snap[k] {
				match = false
				break
			}
		}
		if d := abs(i + 1 - hint.Start); match && d < bestDist {
			best, bestDist = i, d
		}
	}
	if best >= 0 {
		return ref.Range{Start: best + 1, End: best + n}, true
	}
	// Fuzzy: align trimmed snapshot lines against the file.
	trim := func(xs []string) []string {
		out := make([]string, len(xs))
		for i, x := range xs {
			out[i] = strings.TrimSpace(x)
		}
		return out
	}
	m := diff.LineMap(diff.Lines(trim(snap), trim(cur)), n)
	first, last, matched := 0, 0, 0
	for i := 1; i <= n; i++ {
		if m[i] > 0 && strings.TrimSpace(snap[i-1]) != "" {
			if first == 0 {
				first = i
			}
			last = i
			matched++
		}
	}
	if matched == 0 || float64(matched) < 0.3*float64(n) {
		return hint, false
	}
	s := m[first] - (first - 1)
	en := m[last] + (n - last)
	return clampRange(ref.Range{Start: max(s, 1), End: en}, len(cur)), true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func short(h string) string {
	if len(h) > 10 {
		return h[:10]
	}
	if h == "" {
		return "working tree"
	}
	return h
}

// explain adds a diff, semantic findings and upstream commits to a link.
func (e *Engine) explain(lc *linkCtx, base *state.Baseline, language lang.Language, opts Options) {
	l := lc.L
	snap, err := e.State.Snapshot(l.Block, base.Ref)
	if err != nil {
		l.Detail += " (baseline snapshot missing)"
		return
	}
	a, b := diff.SplitLines(string(snap)), diff.SplitLines(string(lc.Snippet))
	l.Added, l.Removed = diff.Stats(diff.Lines(a, b))
	if opts.Diffs {
		l.Diff = diff.Unified(a, b,
			fmt.Sprintf("%s:%s@%s (accepted)", l.Target, base.Path, short(base.Revision)),
			fmt.Sprintf("%s:%s@%s (current)", l.Target, l.Current, short(l.Revision)),
			base.Range.Start, max(l.Range.Start, 1), e.Cfg.Drift.Context)
	}
	if sd, ok := language.(lang.SemanticDiffer); ok && l.Status == model.Drifted {
		l.Findings = sd.Compare(snap, lc.Snippet)
		// Snippets are parsed out of context; re-qualify symbol names with
		// the referenced symbol's container (e.g. "login" → "AuthService.login").
		if i := strings.LastIndex(l.Symbol, "."); i > 0 {
			parent := l.Symbol[:i]
			for k := range l.Findings {
				if f := &l.Findings[k]; f.Symbol != "" && !strings.HasPrefix(f.Symbol, parent+".") {
					f.Symbol = parent + "." + f.Symbol
				}
			}
		}
		sort.SliceStable(l.Findings, func(i, j int) bool {
			return l.Findings[i].Severity.Rank() > l.Findings[j].Severity.Rank()
		})
	}
	if opts.Commits {
		if src, err := e.Source(l.Target); err == nil {
			if cs, err := src.Commits(base.Revision, l.Current, e.Cfg.Drift.MaxCommits); err == nil {
				l.Commits = cs
			}
		}
	}
}

// Selector picks links for accept.
type Selector func(*model.Link) bool

// Accept records the current upstream state as the baseline for the selected
// links. It must be called after Analyze. Returns accepted links.
func (e *Engine) Accept(sel Selector, rewrite bool) ([]*model.Link, error) {
	who := ""
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	if name := os.Getenv("PACEMAKER_USER"); name != "" {
		who = name
	}
	var done []*model.Link
	rewrites := map[string][]rewriteOp{}
	for _, lc := range e.ctx {
		if !sel(lc.L) {
			continue
		}
		l := lc.L
		if !lc.Resolved {
			// Unresolvable links can still be re-pointed (e.g. renamed symbol);
			// they get a baseline on the next accept.
			if rewrite && l.SuggestedRef != "" {
				rewrites[l.LocalFile] = append(rewrites[l.LocalFile], rewriteOp{line: lc.Site.Line, old: lc.Site.Raw, new: rewriteRaw(lc.Site, l)})
				done = append(done, l)
			}
			continue
		}
		rng := l.Range
		b := &state.Baseline{
			Ref: l.Key, Target: l.Target, Path: l.Current, Symbol: l.Symbol, Range: rng,
			Revision: l.Revision, FileHash: lc.FileHash, Hash: lc.Hash, NormHash: lc.NormHash,
			LocalHash: lc.LocalHash, AcceptedAt: time.Now().UTC().Truncate(time.Second), AcceptedBy: who,
		}
		if err := e.State.Put(l.Block, b, lc.Snippet); err != nil {
			return done, err
		}
		if rewrite && l.SuggestedRef != "" {
			rewrites[l.LocalFile] = append(rewrites[l.LocalFile], rewriteOp{line: lc.Site.Line, old: lc.Site.Raw, new: rewriteRaw(lc.Site, l)})
		}
		done = append(done, l)
	}
	// Drop baselines of refs the accepted blocks no longer have (renames).
	for _, lc := range e.ctx {
		if !sel(lc.L) {
			continue
		}
		live := map[string]bool{}
		for _, k := range e.refKeys(lc.Block) {
			live[k] = true
		}
		for _, bl := range append([]*state.Baseline(nil), e.State.Links[lc.Block.ID]...) {
			if !live[bl.Ref] {
				e.State.Remove(lc.Block.ID, bl.Ref)
			}
		}
	}
	for file, ops := range rewrites {
		if err := e.rewrite(file, ops); err != nil {
			return done, err
		}
	}
	return done, e.State.Save()
}

type rewriteOp struct {
	line     int
	old, new string
}

// rewriteRaw produces an updated reference preserving how it was written
// (with or without an explicit target prefix).
func rewriteRaw(site marker.RefSite, l *model.Link) string {
	nr, err := ref.Parse(l.SuggestedRef)
	if err != nil {
		return site.Raw
	}
	if site.Ref.Target == "" {
		nr.Target = ""
	}
	return nr.String()
}

func (e *Engine) rewrite(file string, ops []rewriteOp) error {
	p := filepath.Join(e.Root, filepath.FromSlash(file))
	content, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	lines := strings.Split(string(content), "\n")
	for _, op := range ops {
		if op.line-1 < len(lines) && op.old != op.new {
			lines[op.line-1] = strings.Replace(lines[op.line-1], op.old, op.new, 1)
		}
	}
	st, _ := os.Stat(p)
	return os.WriteFile(p, []byte(strings.Join(lines, "\n")), st.Mode().Perm())
}

// Prune removes state for blocks/refs that no longer exist in the source.
func (e *Engine) Prune(dry bool) ([]string, error) {
	blocks, _, err := e.Scan()
	if err != nil {
		return nil, err
	}
	live := map[string]map[string]bool{}
	for _, b := range blocks {
		m := map[string]bool{}
		for _, k := range e.refKeys(b) {
			m[k] = true
		}
		live[b.ID] = m
	}
	var removed []string
	for _, id := range e.State.Blocks() {
		for _, bl := range append([]*state.Baseline(nil), e.State.Links[id]...) {
			if live[id] == nil || !live[id][bl.Ref] {
				removed = append(removed, id+" → "+bl.Ref)
				if !dry {
					e.State.Remove(id, bl.Ref)
				}
			}
		}
	}
	if dry || len(removed) == 0 {
		return removed, nil
	}
	return removed, e.State.Save()
}
