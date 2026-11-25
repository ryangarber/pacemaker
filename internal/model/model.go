// Package model holds the format-agnostic analysis result shared by the
// engine, the CLI and every report renderer.
package model

import (
	"sort"
	"time"

	"github.com/ryangarber/pacemaker/internal/lang"
	"github.com/ryangarber/pacemaker/internal/marker"
	"github.com/ryangarber/pacemaker/internal/ref"
	"github.com/ryangarber/pacemaker/internal/source"
)

// Status of a link between a local block and an upstream reference.
type Status string

const (
	OK       Status = "ok"       // upstream unchanged since accepted
	Moved    Status = "moved"    // unchanged content, but at a new location
	Cosmetic Status = "cosmetic" // only formatting/comments changed
	Drifted  Status = "drifted"  // upstream content changed
	Missing  Status = "missing"  // upstream file/symbol/lines no longer exist
	New      Status = "new"      // never accepted (no baseline)
	Error    Status = "error"    // target unavailable or unresolvable
)

// Statuses in display order (most urgent first).
var Statuses = []Status{Error, Missing, Drifted, New, Cosmetic, Moved, OK}

// Rank orders statuses by urgency (higher is worse).
func (s Status) Rank() int {
	for i, x := range Statuses {
		if x == s {
			return len(Statuses) - i
		}
	}
	return 0
}

// Baseline summarises what a link was last accepted against.
type Baseline struct {
	Revision   string    `json:"revision,omitempty"`
	Path       string    `json:"path"`
	Range      ref.Range `json:"range"`
	AcceptedAt time.Time `json:"accepted_at"`
	AcceptedBy string    `json:"accepted_by,omitempty"`
}

// Link is one (local block, upstream reference) pair.
type Link struct {
	Block      string      `json:"block"`
	Kind       marker.Kind `json:"kind"`
	LocalFile  string      `json:"local_file"`
	LocalStart int         `json:"local_start,omitempty"`
	LocalEnd   int         `json:"local_end,omitempty"`
	MarkerLine int         `json:"marker_line"`
	Note       string      `json:"note,omitempty"`
	Tags       []string    `json:"tags,omitempty"`

	Key      string `json:"key"` // stable identity within the block
	Ref      string `json:"ref"` // canonical reference as written (with target)
	RefKind  string `json:"ref_kind"`
	Target   string `json:"target"`
	Path     string `json:"path"`         // path as referenced
	Current  string `json:"current_path"` // path after rename detection
	Symbol   string `json:"symbol,omitempty"`
	Language string `json:"language"`

	Status       Status          `json:"status"`
	Detail       string          `json:"detail,omitempty"`
	Range        ref.Range       `json:"range"` // current upstream range
	Revision     string          `json:"revision,omitempty"`
	Baseline     *Baseline       `json:"baseline,omitempty"`
	LocalChanged bool            `json:"local_changed"`
	SuggestedRef string          `json:"suggested_ref,omitempty"`
	Findings     []lang.Finding  `json:"findings,omitempty"`
	Diff         string          `json:"diff,omitempty"`
	Added        int             `json:"lines_added,omitempty"`
	Removed      int             `json:"lines_removed,omitempty"`
	Commits      []source.Commit `json:"commits,omitempty"`
	// Todos attached to this link's block (also listed in Report.Todos).
	Todos []*Todo `json:"-"`
}

// OpenTodos counts attached @pace:todo items (excluding notes).
func (l *Link) OpenTodos() int {
	n := 0
	for _, t := range l.Todos {
		if t.Kind == marker.NoteTodo {
			n++
		}
	}
	return n
}

// TodoRef is an upstream reference on a todo/note, resolved against the
// current target (no baseline: it only answers "does this still exist?").
type TodoRef struct {
	Ref     string    `json:"ref"`
	Target  string    `json:"target"`
	Path    string    `json:"path"`
	Current string    `json:"current_path,omitempty"`
	Symbol  string    `json:"symbol,omitempty"`
	Range   ref.Range `json:"range"`
	Status  Status    `json:"status"` // ok | missing | error
	Detail  string    `json:"detail,omitempty"`
}

// Todo is a @pace:todo (open work) or @pace:note (information) annotation.
type Todo struct {
	Kind     marker.NoteKind `json:"kind"`
	File     string          `json:"file"`
	Line     int             `json:"line"`
	Text     string          `json:"text"`
	Owner    string          `json:"owner,omitempty"`
	Priority string          `json:"priority,omitempty"`
	Tags     []string        `json:"tags,omitempty"`
	Block    string          `json:"block,omitempty"`
	// BlockStatus is the most urgent status among the block's links. A todo
	// in a drifted block may be outdated by the upstream change.
	BlockStatus Status    `json:"block_status,omitempty"`
	Refs        []TodoRef `json:"refs,omitempty"`
}

// Stale reports whether any upstream reference no longer resolves.
func (t *Todo) Stale() bool {
	for _, r := range t.Refs {
		if r.Status == Missing {
			return true
		}
	}
	return false
}

// NeedsReview reports whether upstream moved under the todo: its block
// drifted/went missing, or a referenced upstream location disappeared.
func (t *Todo) NeedsReview() bool {
	switch t.BlockStatus {
	case Drifted, Missing:
		return true
	}
	return t.Stale()
}

// Priorities in display order (most urgent first). Unknown priorities rank
// with "normal".
var Priorities = []string{"blocker", "high", "normal", "low"}

var prioAlias = map[string]string{
	"critical": "blocker", "crit": "blocker", "p0": "blocker",
	"p1": "high", "hi": "high", "urgent": "high",
	"": "normal", "medium": "normal", "med": "normal", "p2": "normal",
	"p3": "low", "lo": "low", "minor": "low",
}

// PriorityClass maps a free-form priority onto one of Priorities.
func PriorityClass(p string) string {
	if a, ok := prioAlias[p]; ok {
		return a
	}
	for _, x := range Priorities {
		if x == p {
			return p
		}
	}
	return "normal"
}

// PriorityRank orders priorities (higher is more urgent).
func PriorityRank(p string) int {
	c := PriorityClass(p)
	for i, x := range Priorities {
		if x == c {
			return len(Priorities) - i
		}
	}
	return 0
}

// TodoCounts summarises todo/note annotations.
type TodoCounts struct {
	Open        int            `json:"open"`
	Notes       int            `json:"notes"`
	Stale       int            `json:"stale"`        // todos/notes whose upstream ref is gone
	NeedsReview int            `json:"needs_review"` // open todos whose upstream changed underneath
	ByPriority  map[string]int `json:"by_priority,omitempty"`
}

// ID is a human-facing identifier, unique within a report.
func (l *Link) ID() string {
	if l.Key == "" {
		return l.Block
	}
	return l.Block + " → " + l.Ref
}

// MaxSeverity of findings.
func (l *Link) MaxSeverity() lang.Severity {
	var s lang.Severity = ""
	for _, f := range l.Findings {
		if f.Severity.Rank() > s.Rank() || s == "" {
			s = f.Severity
		}
	}
	return s
}

// SymbolCoverage reports whether an upstream declaration is mapped.
type SymbolCoverage struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	State    string `json:"state"` // covered | partial | waived | uncovered
	Exported bool   `json:"exported"`
	// Planned is true when a @pace:todo references this symbol (a known,
	// tracked gap rather than an unnoticed one).
	Planned bool `json:"planned,omitempty"`
}

// FileCoverage is coverage for one upstream file.
type FileCoverage struct {
	Path        string           `json:"path"`
	Language    string           `json:"language"`
	Lines       int              `json:"lines"`
	Significant int              `json:"significant"`
	Covered     int              `json:"covered"`
	Waived      int              `json:"waived"`
	Links       int              `json:"links"`
	Uncovered   []ref.Range      `json:"uncovered,omitempty"`
	Symbols     []SymbolCoverage `json:"symbols,omitempty"`
	Todos       int              `json:"todos,omitempty"` // open todos referencing this file
}

// Percent of significant lines covered or waived.
func (f FileCoverage) Percent() float64 {
	if f.Significant == 0 {
		return 100
	}
	return 100 * float64(f.Covered+f.Waived) / float64(f.Significant)
}

// Coverage totals.
type Coverage struct {
	Files          int `json:"files"`
	FilesTouched   int `json:"files_touched"`
	Significant    int `json:"significant_lines"`
	Covered        int `json:"covered_lines"`
	Waived         int `json:"waived_lines"`
	Symbols        int `json:"symbols"`
	SymbolsCovered int `json:"symbols_covered"`
	SymbolsPartial int `json:"symbols_partial"`
	SymbolsWaived  int `json:"symbols_waived"`
}

// Percent of significant lines covered or waived.
func (c Coverage) Percent() float64 {
	if c.Significant == 0 {
		return 0
	}
	return 100 * float64(c.Covered+c.Waived) / float64(c.Significant)
}

// SymbolPercent of symbols fully covered or waived.
func (c Coverage) SymbolPercent() float64 {
	if c.Symbols == 0 {
		return 0
	}
	return 100 * float64(c.SymbolsCovered+c.SymbolsWaived) / float64(c.Symbols)
}

// TargetReport aggregates per-target information.
type TargetReport struct {
	Name     string         `json:"name"`
	Location string         `json:"location"`
	Revision string         `json:"revision,omitempty"`
	Dirty    bool           `json:"dirty,omitempty"`
	Include  []string       `json:"include,omitempty"`
	Exclude  []string       `json:"exclude,omitempty"`
	Error    string         `json:"error,omitempty"`
	Totals   Coverage       `json:"totals"`
	Files    []FileCoverage `json:"files,omitempty"`
}

// Report is the complete analysis result.
type Report struct {
	Title     string           `json:"title"`
	Project   string           `json:"project"`
	Generated time.Time        `json:"generated"`
	Targets   []TargetReport   `json:"targets"`
	Links     []*Link          `json:"links"`
	Problems  []marker.Problem `json:"problems,omitempty"`
	Counts    map[Status]int   `json:"counts"`
	Todos     []*Todo          `json:"todos,omitempty"`
	TodoStats TodoCounts       `json:"todo_counts"`
	Failing   bool             `json:"failing"`
	// FailReasons explains why Failing is set (empty when passing).
	FailReasons []string `json:"fail_reasons,omitempty"`
}

// SortTodos orders todos before notes, then by priority, then location.
func (r *Report) SortTodos() {
	sort.SliceStable(r.Todos, func(i, j int) bool {
		a, b := r.Todos[i], r.Todos[j]
		if a.Kind != b.Kind {
			return a.Kind == marker.NoteTodo
		}
		if pa, pb := PriorityRank(a.Priority), PriorityRank(b.Priority); pa != pb {
			return pa > pb
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
}

// CountTodos recomputes TodoStats.
func (r *Report) CountTodos() {
	c := TodoCounts{ByPriority: map[string]int{}}
	for _, t := range r.Todos {
		if t.Stale() {
			c.Stale++
		}
		if t.Kind != marker.NoteTodo {
			c.Notes++
			continue
		}
		c.Open++
		c.ByPriority[PriorityClass(t.Priority)]++
		if t.NeedsReview() {
			c.NeedsReview++
		}
	}
	r.TodoStats = c
}

// Sort orders links by urgency, then block and ref.
func (r *Report) Sort() {
	sort.SliceStable(r.Links, func(i, j int) bool {
		a, b := r.Links[i], r.Links[j]
		if a.Status.Rank() != b.Status.Rank() {
			return a.Status.Rank() > b.Status.Rank()
		}
		if a.Block != b.Block {
			return a.Block < b.Block
		}
		return a.Ref < b.Ref
	})
}

// Count recomputes Counts.
func (r *Report) Count() {
	r.Counts = map[Status]int{}
	for _, l := range r.Links {
		r.Counts[l.Status]++
	}
}

// Total coverage across all targets.
func (r *Report) Total() Coverage {
	var c Coverage
	for _, t := range r.Targets {
		x := t.Totals
		c.Files += x.Files
		c.FilesTouched += x.FilesTouched
		c.Significant += x.Significant
		c.Covered += x.Covered
		c.Waived += x.Waived
		c.Symbols += x.Symbols
		c.SymbolsCovered += x.SymbolsCovered
		c.SymbolsPartial += x.SymbolsPartial
		c.SymbolsWaived += x.SymbolsWaived
	}
	return c
}
