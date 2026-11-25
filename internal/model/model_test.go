package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ryangarber/pacemaker/internal/lang"
	"github.com/ryangarber/pacemaker/internal/marker"
	"github.com/ryangarber/pacemaker/internal/ref"
	"github.com/ryangarber/pacemaker/internal/source"
)

func TestStatusRank(t *testing.T) {
	// Every status ranks above zero and strictly below the one before it.
	prev := len(Statuses) + 1
	for _, s := range Statuses {
		r := s.Rank()
		if r <= 0 || r >= prev {
			t.Errorf("%s.Rank() = %d, want in (0,%d)", s, r, prev)
		}
		prev = r
	}
	if len(Statuses) != 7 {
		t.Errorf("Statuses has %d entries, want 7 (one per constant)", len(Statuses))
	}
	order := []Status{Error, Missing, Drifted, New, Cosmetic, Moved, OK}
	for i := 1; i < len(order); i++ {
		if order[i-1].Rank() <= order[i].Rank() {
			t.Errorf("%s should outrank %s", order[i-1], order[i])
		}
	}
	for _, s := range []Status{"", "bogus", "OK"} {
		if s.Rank() != 0 {
			t.Errorf("unknown status %q ranks %d, want 0", s, s.Rank())
		}
	}
}

func TestStatusJSONValues(t *testing.T) {
	// Status strings are part of the JSON report contract.
	want := map[Status]string{OK: "ok", Moved: "moved", Cosmetic: "cosmetic", Drifted: "drifted",
		Missing: "missing", New: "new", Error: "error"}
	for s, w := range want {
		if string(s) != w {
			t.Errorf("status %q, want %q", s, w)
		}
	}
}

func TestPriorityClass(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "normal"},
		{"blocker", "blocker"}, {"critical", "blocker"}, {"crit", "blocker"}, {"p0", "blocker"},
		{"high", "high"}, {"hi", "high"}, {"p1", "high"}, {"urgent", "high"},
		{"normal", "normal"}, {"medium", "normal"}, {"med", "normal"}, {"p2", "normal"},
		{"low", "low"}, {"lo", "low"}, {"p3", "low"}, {"minor", "low"},
		{"whenever", "normal"}, // unknown ranks with normal
		{"HIGH", "normal"},     // case-sensitive
		{" high", "normal"},
	}
	for _, c := range cases {
		if got := PriorityClass(c.in); got != c.want {
			t.Errorf("PriorityClass(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPriorityRank(t *testing.T) {
	if !(PriorityRank("blocker") > PriorityRank("high") &&
		PriorityRank("high") > PriorityRank("normal") &&
		PriorityRank("normal") > PriorityRank("low") &&
		PriorityRank("low") > 0) {
		t.Error("priority ranks out of order")
	}
	if PriorityRank("p0") != PriorityRank("blocker") || PriorityRank("weird") != PriorityRank("normal") ||
		PriorityRank("") != PriorityRank("normal") {
		t.Error("aliases/unknowns should rank as their class")
	}
	for _, p := range Priorities {
		if PriorityClass(p) != p {
			t.Errorf("canonical priority %q does not map to itself", p)
		}
	}
}

func TestLinkID(t *testing.T) {
	cases := []struct {
		l    Link
		want string
	}{
		{Link{Block: "auth"}, "auth"},
		{Link{Block: "auth", Ref: "lib/a.dart#Auth"}, "auth"}, // no key: block only
		{Link{Block: "auth", Key: "k", Ref: "lib/a.dart#Auth"}, "auth → lib/a.dart#Auth"},
	}
	for _, c := range cases {
		if got := c.l.ID(); got != c.want {
			t.Errorf("ID(%+v) = %q, want %q", c.l, got, c.want)
		}
	}
}

func TestLinkMaxSeverity(t *testing.T) {
	f := func(s ...lang.Severity) []lang.Finding {
		var out []lang.Finding
		for _, x := range s {
			out = append(out, lang.Finding{Severity: x})
		}
		return out
	}
	cases := []struct {
		name string
		in   []lang.Finding
		want lang.Severity
	}{
		{"none", nil, ""},
		{"single info", f(lang.SevInfo), lang.SevInfo},
		{"max last", f(lang.SevInfo, lang.SevMinor, lang.SevBreaking), lang.SevBreaking},
		{"max first", f(lang.SevBreaking, lang.SevInfo, lang.SevMajor), lang.SevBreaking},
		{"middle", f(lang.SevMinor, lang.SevMajor, lang.SevInfo), lang.SevMajor},
	}
	for _, c := range cases {
		l := Link{Findings: c.in}
		if got := l.MaxSeverity(); got != c.want {
			t.Errorf("%s: MaxSeverity = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLinkOpenTodos(t *testing.T) {
	l := Link{Todos: []*Todo{
		{Kind: marker.NoteTodo}, {Kind: marker.NoteNote}, {Kind: marker.NoteTodo}, {Kind: marker.NoteNote},
	}}
	if got := l.OpenTodos(); got != 2 {
		t.Errorf("OpenTodos = %d, want 2 (notes excluded)", got)
	}
	if (&Link{}).OpenTodos() != 0 {
		t.Error("no todos should count zero")
	}
}

func TestTodoStaleAndNeedsReview(t *testing.T) {
	missing := []TodoRef{{Status: OK}, {Status: Missing}}
	fine := []TodoRef{{Status: OK}, {Status: Error}} // error is not "gone"
	cases := []struct {
		name        string
		t           Todo
		stale, need bool
	}{
		{"empty", Todo{}, false, false},
		{"refs ok", Todo{Refs: fine}, false, false},
		{"ref missing", Todo{Refs: missing}, true, true},
		{"block drifted", Todo{BlockStatus: Drifted}, false, true},
		{"block missing", Todo{BlockStatus: Missing}, false, true},
		{"block cosmetic", Todo{BlockStatus: Cosmetic}, false, false},
		{"block moved", Todo{BlockStatus: Moved}, false, false},
		{"block new", Todo{BlockStatus: New}, false, false},
		{"block error", Todo{BlockStatus: Error}, false, false},
		{"block ok ref missing", Todo{BlockStatus: OK, Refs: missing}, true, true},
	}
	for _, c := range cases {
		if got := c.t.Stale(); got != c.stale {
			t.Errorf("%s: Stale = %v, want %v", c.name, got, c.stale)
		}
		if got := c.t.NeedsReview(); got != c.need {
			t.Errorf("%s: NeedsReview = %v, want %v", c.name, got, c.need)
		}
	}
}

func TestPercents(t *testing.T) {
	cases := []struct {
		name string
		got  float64
		want float64
	}{
		{"file empty is complete", FileCoverage{}.Percent(), 100},
		{"file half", FileCoverage{Significant: 10, Covered: 5}.Percent(), 50},
		{"file waived counts", FileCoverage{Significant: 4, Covered: 1, Waived: 3}.Percent(), 100},
		{"totals empty is zero", Coverage{}.Percent(), 0},
		{"totals", Coverage{Significant: 8, Covered: 2, Waived: 4}.Percent(), 75},
		{"symbols empty is zero", Coverage{}.SymbolPercent(), 0},
		{"symbols partial excluded", Coverage{Symbols: 4, SymbolsCovered: 1, SymbolsWaived: 1, SymbolsPartial: 2}.SymbolPercent(), 50},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestReportSort(t *testing.T) {
	r := Report{Links: []*Link{
		{Block: "b", Ref: "2", Status: OK},
		{Block: "a", Ref: "1", Status: OK},
		{Block: "z", Ref: "1", Status: Error},
		{Block: "b", Ref: "1", Status: Drifted},
		{Block: "a", Ref: "2", Status: Drifted},
		{Block: "a", Ref: "1", Status: Drifted},
		{Block: "m", Ref: "x", Status: Missing},
		{Block: "q", Ref: "x", Status: "unknown"},
	}}
	r.Sort()
	var got []string
	for _, l := range r.Links {
		got = append(got, string(l.Status)+":"+l.Block+":"+l.Ref)
	}
	want := []string{
		"error:z:1", "missing:m:x",
		"drifted:a:1", "drifted:a:2", "drifted:b:1",
		"ok:a:1", "ok:b:2",
		"unknown:q:x",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sort order\n got %v\nwant %v", got, want)
	}
	(&Report{}).Sort() // empty must not panic
}

func TestReportSortStable(t *testing.T) {
	a := &Link{Block: "x", Ref: "r", Status: OK, Detail: "first"}
	b := &Link{Block: "x", Ref: "r", Status: OK, Detail: "second"}
	r := Report{Links: []*Link{a, b}}
	r.Sort()
	if r.Links[0] != a || r.Links[1] != b {
		t.Error("Sort is not stable for equal keys")
	}
}

func TestReportCount(t *testing.T) {
	r := Report{Counts: map[Status]int{OK: 99, Error: 3}, Links: []*Link{
		{Status: OK}, {Status: OK}, {Status: Drifted},
	}}
	r.Count()
	want := map[Status]int{OK: 2, Drifted: 1}
	if !reflect.DeepEqual(r.Counts, want) {
		t.Errorf("Counts = %v, want %v (recomputed, not accumulated)", r.Counts, want)
	}
	empty := Report{}
	empty.Count()
	if empty.Counts == nil || len(empty.Counts) != 0 {
		t.Errorf("empty Count = %v, want non-nil empty map", empty.Counts)
	}
}

func TestReportSortTodos(t *testing.T) {
	r := Report{Todos: []*Todo{
		{Kind: marker.NoteNote, Priority: "blocker", File: "a", Line: 1},
		{Kind: marker.NoteTodo, Priority: "low", File: "a", Line: 1},
		{Kind: marker.NoteTodo, Priority: "", File: "b", Line: 9},
		{Kind: marker.NoteTodo, Priority: "", File: "b", Line: 2},
		{Kind: marker.NoteTodo, Priority: "p0", File: "z", Line: 1},
		{Kind: marker.NoteTodo, Priority: "normal", File: "a", Line: 5},
		{Kind: marker.NoteNote, Priority: "", File: "a", Line: 0},
	}}
	r.SortTodos()
	var got []string
	for _, x := range r.Todos {
		got = append(got, strings.Join([]string{string(x.Kind), PriorityClass(x.Priority), x.File, itoa(x.Line)}, ":"))
	}
	want := []string{
		"todo:blocker:z:1",
		"todo:normal:a:5", "todo:normal:b:2", "todo:normal:b:9",
		"todo:low:a:1",
		"note:blocker:a:1",
		"note:normal:a:0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SortTodos\n got %v\nwant %v", got, want)
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func TestReportCountTodos(t *testing.T) {
	gone := []TodoRef{{Status: Missing}}
	r := Report{
		TodoStats: TodoCounts{Open: 42},
		Todos: []*Todo{
			{Kind: marker.NoteTodo, Priority: "p1"},
			{Kind: marker.NoteTodo, Priority: "critical", BlockStatus: Drifted},
			{Kind: marker.NoteTodo, Priority: "", Refs: gone},     // stale + needs review
			{Kind: marker.NoteNote, Priority: "high", Refs: gone}, // stale note: no priority, no review
			{Kind: marker.NoteNote, BlockStatus: Missing},         // notes never "need review"
			{Kind: marker.NoteTodo, Priority: "whatever", BlockStatus: OK},
		},
	}
	r.CountTodos()
	want := TodoCounts{
		Open: 4, Notes: 2, Stale: 2, NeedsReview: 2,
		ByPriority: map[string]int{"high": 1, "blocker": 1, "normal": 2},
	}
	if !reflect.DeepEqual(r.TodoStats, want) {
		t.Errorf("TodoStats = %+v, want %+v", r.TodoStats, want)
	}
	empty := Report{}
	empty.CountTodos()
	if empty.TodoStats.ByPriority == nil || empty.TodoStats.Open != 0 {
		t.Errorf("empty CountTodos = %+v", empty.TodoStats)
	}
}

func TestReportTotal(t *testing.T) {
	r := Report{Targets: []TargetReport{
		{Totals: Coverage{Files: 1, FilesTouched: 1, Significant: 10, Covered: 4, Waived: 1, Symbols: 3, SymbolsCovered: 1, SymbolsPartial: 1, SymbolsWaived: 1}},
		{Totals: Coverage{Files: 2, FilesTouched: 0, Significant: 30, Covered: 6, Waived: 2, Symbols: 7, SymbolsCovered: 2, SymbolsPartial: 3, SymbolsWaived: 0}},
		{Error: "unreachable"},
	}}
	want := Coverage{Files: 3, FilesTouched: 1, Significant: 40, Covered: 10, Waived: 3, Symbols: 10, SymbolsCovered: 3, SymbolsPartial: 4, SymbolsWaived: 1}
	if got := r.Total(); got != want {
		t.Errorf("Total = %+v, want %+v", got, want)
	}
	if (&Report{}).Total() != (Coverage{}) {
		t.Error("empty report total should be zero")
	}
}

func TestReportJSONRoundTrip(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 30, 0, 0, time.UTC)
	todo := &Todo{Kind: marker.NoteTodo, File: "lib/a.dart", Line: 7, Text: "port retry", Priority: "high",
		Tags: []string{"net"}, Block: "auth", BlockStatus: Drifted,
		Refs: []TodoRef{{Ref: "up:src/a.js#retry", Target: "up", Path: "src/a.js", Symbol: "retry",
			Range: ref.Range{Start: 3, End: 9}, Status: Missing, Detail: "gone"}}}
	link := &Link{
		Block: "auth", Kind: marker.KindBlock, LocalFile: "lib/a.dart", LocalStart: 2, LocalEnd: 40, MarkerLine: 1,
		Note: "n", Tags: []string{"t"}, Key: "k", Ref: "up:src/a.js#Auth", RefKind: "symbol", Target: "up",
		Path: "src/a.js", Current: "src/b.js", Symbol: "Auth", Language: "javascript",
		Status: Drifted, Detail: "changed", Range: ref.Range{Start: 10, End: 20}, Revision: "abc",
		Baseline:     &Baseline{Revision: "def", Path: "src/a.js", Range: ref.Range{Start: 9, End: 19}, AcceptedAt: at, AcceptedBy: "me"},
		LocalChanged: true, SuggestedRef: "up:src/b.js#Auth",
		Findings: []lang.Finding{{Kind: "signature", Symbol: "Auth.login", Severity: lang.SevBreaking, Message: "m"}},
		Diff:     "@@\n-a\n+b\n", Added: 1, Removed: 1,
		Commits: []source.Commit{{Hash: "abc", Short: "abc", Author: "x", Date: "d", Subject: "s"}},
		Todos:   []*Todo{todo},
	}
	r := Report{
		Title: "T", Project: "P", Generated: at,
		Targets: []TargetReport{{Name: "up", Location: "../up", Revision: "abc", Dirty: true,
			Include: []string{"src/**"}, Exclude: []string{"*.min.js"},
			Totals: Coverage{Files: 1, Significant: 10, Covered: 5},
			Files: []FileCoverage{{Path: "src/a.js", Language: "javascript", Lines: 12, Significant: 10, Covered: 5,
				Uncovered: []ref.Range{{Start: 1, End: 2}},
				Symbols:   []SymbolCoverage{{Name: "Auth", Kind: "class", Start: 1, End: 9, State: "partial", Exported: true, Planned: true}},
				Todos:     1}}}},
		Links:       []*Link{link},
		Problems:    []marker.Problem{},
		Todos:       []*Todo{todo},
		Failing:     true,
		FailReasons: []string{"1 drifted"},
	}
	r.Count()
	r.CountTodos()

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	// Spot-check wire names that consumers depend on.
	for _, key := range []string{`"current_path":"src/b.js"`, `"lines_added":1`, `"lines_removed":1`,
		`"accepted_at":"2026-11-25T10:30:00Z"`, `"todo_counts"`, `"counts":{"drifted":1}`,
		`"significant_lines":10`, `"covered_lines":5`, `"fail_reasons"`, `"block_status":"drifted"`,
		`"needs_review":1`, `"local_changed":true`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("JSON missing %s", key)
		}
	}
	// Link.Todos is not serialised (todos live in Report.Todos only).
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	l0 := raw["links"].([]any)[0].(map[string]any)
	if _, ok := l0["Todos"]; ok {
		t.Error("Link.Todos should not be serialised")
	}
	if _, ok := l0["todos"]; ok {
		t.Error("Link.Todos should not be serialised")
	}

	var back Report
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	link.Todos = nil // expected loss
	defer func() { link.Todos = []*Todo{todo} }()
	if !reflect.DeepEqual(back.Links[0], link) {
		t.Errorf("link round trip\n got %+v\nwant %+v", back.Links[0], link)
	}
	if !reflect.DeepEqual(back.Todos, r.Todos) || !reflect.DeepEqual(back.Targets, r.Targets) ||
		!reflect.DeepEqual(back.Counts, r.Counts) || !reflect.DeepEqual(back.TodoStats, r.TodoStats) ||
		!back.Generated.Equal(r.Generated) || back.Failing != r.Failing || !reflect.DeepEqual(back.FailReasons, r.FailReasons) {
		t.Errorf("report round trip mismatch\n got %+v\nwant %+v", back, r)
	}
}

func TestLinkJSONOmitEmpty(t *testing.T) {
	data, err := json.Marshal(Link{Block: "b", Status: OK})
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, k := range []string{"local_start", "local_end", "note", "tags", "symbol", "detail", "revision",
		"baseline", "suggested_ref", "findings", "diff", "lines_added", "lines_removed", "commits"} {
		if strings.Contains(s, `"`+k+`"`) {
			t.Errorf("empty %s should be omitted: %s", k, s)
		}
	}
	for _, k := range []string{"block", "kind", "local_file", "marker_line", "key", "ref", "ref_kind",
		"target", "path", "current_path", "language", "status", "range", "local_changed"} {
		if !strings.Contains(s, `"`+k+`"`) {
			t.Errorf("required %s missing: %s", k, s)
		}
	}
}
