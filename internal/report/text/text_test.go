package text

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ryangarber/pacemaker/internal/lang"
	"github.com/ryangarber/pacemaker/internal/marker"
	"github.com/ryangarber/pacemaker/internal/model"
	"github.com/ryangarber/pacemaker/internal/ref"
	"github.com/ryangarber/pacemaker/internal/report"
	"github.com/ryangarber/pacemaker/internal/source"
	tu "github.com/ryangarber/pacemaker/internal/textutil"
)

// fixture builds a report exercising every section a renderer can emit:
// all statuses but moved, findings of several severities, commits, a diff,
// a failed target, marker problems, todos and notes, and names that need
// escaping in HTML (<script>), Markdown tables (|), code spans (`) plus
// non-ASCII text.
func fixture() *model.Report {
	at := time.Date(2026, 11, 1, 12, 30, 0, 0, time.UTC)
	todo := &model.Todo{
		Kind: marker.NoteTodo, File: "local/x.go", Line: 12,
		Text: "port the <fast> path | soon", Owner: "ana", Priority: "p0",
		Tags: []string{"perf"}, Block: "<script>alert(1)</script>", BlockStatus: model.Drifted,
		Refs: []model.TodoRef{{Ref: "up:gone.go#X", Status: model.Missing, Detail: "not found"}},
	}
	note := &model.Todo{Kind: marker.NoteNote, File: "local/z.go", Line: 1, Text: "kept v1 codes"}
	r := &model.Report{
		Title:     "Port <status> & sync",
		Project:   "demo<proj>",
		Generated: at,
		Targets: []model.TargetReport{
			{
				Name: "up", Location: "https://example.com/<repo>", Revision: "0123456789abcdef", Dirty: true,
				Include: []string{"src/**"},
				Totals: model.Coverage{Files: 2, FilesTouched: 2, Significant: 30, Covered: 12, Waived: 3,
					Symbols: 4, SymbolsCovered: 2, SymbolsWaived: 1},
				Files: []model.FileCoverage{
					{Path: "src/good.go", Language: "go", Significant: 10, Covered: 10, Links: 1},
					{Path: "src/bad<x>.go", Language: "go", Significant: 20, Covered: 2, Waived: 3, Links: 2,
						Symbols: []model.SymbolCoverage{
							{Name: "Bar", Kind: "func", Start: 1, End: 5, State: "uncovered", Planned: true},
							{Name: "Qux", Kind: "type", Start: 7, End: 9, State: "partial"},
							{Name: "Baz", Kind: "func", Start: 10, End: 12, State: "covered"},
						}},
				},
			},
			{Name: "broken", Location: "git@example.com:b.git", Error: "clone failed: <denied>"},
		},
		Links: []*model.Link{
			{
				Block: "steady", Kind: marker.KindBlock, LocalFile: "local/s.go", MarkerLine: 3,
				Ref: "up:ok.go", Target: "up", Path: "ok.go", Current: "ok.go", Status: model.OK,
			},
			{
				Block: "<script>alert(1)</script>", Kind: marker.KindBlock, LocalFile: "local/x.go",
				LocalStart: 10, LocalEnd: 20, MarkerLine: 10, Note: "hand <ported>",
				Tags: []string{"core"}, Key: "k1", Ref: "up:src/a|b.go#Foo", RefKind: "symbol",
				Target: "up", Path: "src/a|b.go", Current: "src/c.go", Symbol: "Foo", Language: "go",
				Status: model.Drifted, Detail: "body changed | <b>bold</b>",
				Range: ref.Range{Start: 5, End: 9}, Revision: "0123456789abcdef",
				Baseline: &model.Baseline{Revision: "fedcba9876543210", Path: "src/a|b.go",
					Range: ref.Range{Start: 5, End: 8}, AcceptedAt: at.AddDate(0, -1, 0), AcceptedBy: "ana"},
				LocalChanged: true, SuggestedRef: "up:src/c.go#Foo",
				Findings: []lang.Finding{
					{Kind: "signature", Symbol: "Foo", Severity: lang.SevBreaking, Message: "param `x` removed"},
					{Kind: "body", Severity: lang.SevMinor, Message: "loop changed"},
				},
				Diff:  "--- a/src/a|b.go\n+++ b/src/c.go\n@@ -1,1 +1,1 @@\n-old <i>\n+new <i>\n ctx\n",
				Added: 1, Removed: 1,
				Commits: []source.Commit{{Hash: "abc1234deadbeef", Short: "abc1234", Author: "Bob <bob@x>",
					Date: "2026-10-30", Subject: "fix <tag> | pipe"}},
				Todos: []*model.Todo{todo},
			},
			{
				Block: "ünïcode-块", Kind: marker.KindFile, LocalFile: "local/u.go", MarkerLine: 1,
				Ref: "up:gone.go", Target: "up", Path: "gone.go", Current: "gone.go",
				Status: model.Missing, Detail: "file deleted",
			},
			{
				Block: "fresh`tick", Kind: marker.KindBlock, LocalFile: "local/n.go", MarkerLine: 7,
				Ref: "up:new.go", Target: "up", Path: "new.go", Current: "new.go", Status: model.New,
			},
			{
				Block: "pretty", Kind: marker.KindBlock, LocalFile: "local/p.go", MarkerLine: 2,
				Ref: "up:fmt.go", Target: "up", Path: "fmt.go", Current: "fmt.go",
				Status: model.Cosmetic, Detail: "whitespace only", Range: ref.Range{Start: 1, End: 2},
			},
		},
		Problems:    []marker.Problem{{File: "local/y.go", Line: 3, Message: "unterminated <block>"}},
		Todos:       []*model.Todo{note, todo},
		Failing:     true,
		FailReasons: []string{"1 drifted", "1 missing"},
	}
	r.Sort()
	r.Count()
	r.SortTodos()
	r.CountTodos()
	return r
}

func empty() *model.Report {
	r := &model.Report{Title: "Empty", Project: "p", Generated: time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)}
	r.Count()
	r.CountTodos()
	return r
}

func render(t *testing.T, r *model.Report, o report.Options) string {
	t.Helper()
	var b bytes.Buffer
	if err := (Renderer{}).Render(&b, r, o); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return b.String()
}

var all = report.Options{ShowOK: true, Diffs: true, Files: true, Symbols: true}

func lineWith(t *testing.T, out, needle string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	t.Fatalf("no line containing %q in:\n%s", needle, out)
	return ""
}

func TestRegistered(t *testing.T) {
	for _, n := range []string{"text", "txt", "terminal"} {
		if r, err := report.Get(n); err != nil || r.Name() != "text" {
			t.Errorf("Get(%q) = %v, %v", n, r, err)
		}
	}
	if r, ok := report.ForFile("out.txt"); !ok || r.Name() != "text" {
		t.Errorf("ForFile(.txt) = %v, %v", r, ok)
	}
}

func TestColor(t *testing.T) {
	plain := render(t, fixture(), all)
	if strings.Contains(plain, "\x1b[") {
		t.Error("ANSI escapes without Color option")
	}
	o := all
	o.Color = true
	colored := render(t, fixture(), o)
	if !strings.Contains(colored, "\x1b[") {
		t.Fatal("no ANSI escapes with Color option")
	}
	// Stripping the escapes leaves the same text.
	stripped := stripANSI(colored)
	if stripped != plain {
		t.Error("colored output differs from plain once escapes are stripped")
	}
	for _, s := range []string{"Port <status> & sync", "▲ drifted", "<script>alert(1)</script>", "clone failed: <denied>"} {
		if !strings.Contains(stripped, s) {
			t.Errorf("colored output lost %q", s)
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestTitleUnderline(t *testing.T) {
	r := fixture()
	r.Title = "Ünïcode 块 title"
	lines := strings.SplitN(render(t, r, report.Options{}), "\n", 3)
	if lines[0] != r.Title {
		t.Errorf("title line %q", lines[0])
	}
	if lines[1] != strings.Repeat("─", utf8.RuneCountInString(r.Title)) {
		t.Errorf("underline %q does not match title width", lines[1])
	}
}

func TestSummary(t *testing.T) {
	out := render(t, fixture(), report.Options{})
	if l := lineWith(t, out, "links  "); l != "links  ✗ 1 missing  ·  ▲ 1 drifted  ·  ● 1 new  ·  ≈ 1 cosmetic  ·  ✓ 1 ok" {
		t.Errorf("links summary %q", l)
	}
	if l := lineWith(t, out, "target up"); !strings.Contains(l, "https://example.com/<repo> @0123456789+dirty") {
		t.Errorf("target line %q", l)
	}
	if l := lineWith(t, out, "lines"); !strings.Contains(l, "50.0% lines") || !strings.Contains(l, "75.0% symbols (3/4)") || !strings.Contains(l, "2/2 files touched") {
		t.Errorf("coverage line %q", l)
	}
	if l := lineWith(t, out, "target  broken"); !strings.HasSuffix(l, "clone failed: <denied>") {
		t.Errorf("target error line %q", l)
	}
	if l := lineWith(t, out, "todos  "); l != "todos  ☐ 1 open (1 blocker)  ·  ⟳ 1 need review (upstream changed)  ·  ✗ 1 stale ref(s)  ·  ℹ 1 note(s)" {
		t.Errorf("todo summary %q", l)
	}
	if !strings.Contains(out, "1 marker problem(s):\n  local/y.go:3 unterminated <block>\n") {
		t.Error("marker problems missing")
	}
}

func TestLinkTable(t *testing.T) {
	hidden := render(t, fixture(), report.Options{})
	shown := render(t, fixture(), report.Options{ShowOK: true})
	if strings.Contains(hidden, "steady") {
		t.Error("ok link listed without ShowOK")
	}
	header := lineWith(t, shown, "STATUS")
	col := runeIndex(header, "BLOCK")
	var rows []string
	prev := -1
	for _, c := range []struct{ status, block string }{
		{"✗ missing", "ünïcode-块"}, {"▲ drifted", "<script>"}, {"● new", "fresh`tick"},
		{"≈ cosmetic", "pretty"}, {"✓ ok", "steady"},
	} {
		r := lineWith(t, shown, c.status+" ")
		rows = append(rows, r)
		// Rows in report order, block column aligned with the header.
		if i := strings.Index(shown, r); i < prev {
			t.Errorf("row out of order: %s", r)
		} else {
			prev = i
		}
		if got := runeIndex(r, c.block); got != col {
			t.Errorf("block column at %d, header at %d: %q", got, col, r)
		}
	}
	drifted := rows[1]
	for _, want := range []string{"<script>alert(1)</script>", "up:src/a|b.go#Foo → src/c.go", "local/x.go:10", "[breaking] ☐1 ✎ body changed | <b>bold</b>"} {
		if !strings.Contains(drifted, want) {
			t.Errorf("drifted row missing %q: %s", want, drifted)
		}
	}
}

func TestLinkDetail(t *testing.T) {
	out := render(t, fixture(), report.Options{})
	// Details are printed for drifted and cosmetic links only.
	if !strings.Contains(out, "\n▲ drifted <script>alert(1)</script> → up:src/a|b.go#Foo\n") {
		t.Error("drifted detail header missing")
	}
	if !strings.Contains(out, "\n≈ cosmetic pretty → up:fmt.go\n") {
		t.Error("cosmetic detail header missing")
	}
	for _, s := range []string{"\n✗ missing ünïcode-块 →", "\n● new fresh`tick →"} {
		if strings.Contains(out, s) {
			t.Errorf("unexpected detail %q", s)
		}
	}
	for _, want := range []string{
		"  detail     body changed | <b>bold</b>\n",
		"  local      local/x.go:10-20 (block)\n",
		"  note       hand <ported>\n",
		"  tags       core\n",
		"  accepted   fedcba9876 src/a|b.go L5-L8 at 2026-10-01 12:30 by ana\n",
		"  now        0123456789 src/c.go L5-L9\n",
		"  change     +1 -1 lines\n",
		"  local      edited since last accept\n",
		"  suggest    up:src/c.go#Foo  (accept --rewrite updates the marker)\n",
		"  semantic changes (go):\n    breaking Foo signature param `x` removed\n    minor    body loop changed\n",
		"  upstream commits since accept:\n    abc1234 2026-10-30 fix <tag> | pipe — Bob <bob@x>\n",
		"  todos & notes:\n    ☐ [p0] port the <fast> path | soon @ana #perf → up:gone.go#X (missing: not found) ⟳ block drifted upstream — re-check\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing %q", want)
		}
	}
	if strings.Contains(out, "-old <i>") {
		t.Error("diff printed without Diffs option")
	}
	with := render(t, fixture(), report.Options{Diffs: true})
	if !strings.Contains(with, "\n    --- a/src/a|b.go\n    +++ b/src/c.go\n    @@ -1,1 +1,1 @@\n    -old <i>\n    +new <i>\n     ctx\n") {
		t.Errorf("diff not indented as expected:\n%s", with)
	}
}

func TestTodoTable(t *testing.T) {
	out := render(t, fixture(), report.Options{})
	header := lineWith(t, out, "PRIO")
	todo := lineWith(t, out, "local/x.go:12")
	note := lineWith(t, out, "local/z.go:1")
	if strings.Index(out, todo) > strings.Index(out, note) {
		t.Error("todo not before note")
	}
	if !strings.HasPrefix(todo, "☐  blocker") || !strings.Contains(todo, "▲ <script>alert(1)</script>") ||
		!strings.HasSuffix(todo, "port the <fast> path | soon @ana #perf ✗ up:gone.go#X missing ⟳ re-check: block drifted") {
		t.Errorf("todo row %q", todo)
	}
	if !strings.HasPrefix(note, "ℹ ") || !strings.HasSuffix(note, "kept v1 codes") {
		t.Errorf("note row %q", note)
	}
	if runeIndex(header, "WHERE") != runeIndex(todo, "local/x.go") {
		t.Errorf("WHERE column misaligned:\n%s\n%s", header, todo)
	}
}

func runeIndex(s, sub string) int {
	return utf8.RuneCountInString(s[:strings.Index(s, sub)])
}

func TestFiles(t *testing.T) {
	if strings.Contains(render(t, fixture(), report.Options{}), "coverage up") {
		t.Error("file table without Files option")
	}
	out := render(t, fixture(), report.Options{Files: true})
	for _, want := range []string{"coverage up\n", "coverage broken\n", "src/good.go", "src/bad<x>.go", "5/20", "10/10"} {
		if !strings.Contains(out, want) {
			t.Errorf("file table missing %q", want)
		}
	}
	if l := lineWith(t, out, "src/bad<x>.go"); !strings.Contains(l, "25.0%") || !strings.HasSuffix(l, " 2") {
		t.Errorf("bad file row %q", l)
	}
}

func TestFileTableLimit(t *testing.T) {
	var b bytes.Buffer
	FileTable(&b, tu.Painter{}, fixture().Targets[0], 1)
	out := b.String()
	// Worst first, so a limit keeps the files most in need of porting.
	if !strings.Contains(out, "src/bad<x>.go") || strings.Contains(out, "src/good.go") || !strings.Contains(out, "… 1 more") {
		t.Errorf("limit not applied to the worst-first order:\n%s", out)
	}
	b.Reset()
	FileTable(&b, tu.Painter{}, fixture().Targets[0], 0)
	if strings.Contains(b.String(), "more") {
		t.Error("limit 0 should print all")
	}
}

func TestFileTableWorstFirst(t *testing.T) {
	out := render(t, fixture(), report.Options{Files: true})
	if strings.Index(out, "src/bad<x>.go") > strings.Index(out, "src/good.go") {
		t.Error("files not worst first")
	}
}

func TestTodoSummary(t *testing.T) {
	p := tu.Painter{}
	if s := TodoSummary(p, model.TodoCounts{}); s != "" {
		t.Errorf("empty summary %q", s)
	}
	s := TodoSummary(p, model.TodoCounts{Open: 4, ByPriority: map[string]int{"normal": 2, "high": 1, "low": 1}})
	if s != "☐ 4 open (1 high, 1 low)" {
		t.Errorf("summary %q", s)
	}
	if s := TodoSummary(p, model.TodoCounts{Notes: 2}); s != "ℹ 2 note(s)" {
		t.Errorf("notes-only summary %q", s)
	}
}

func TestEmptyReport(t *testing.T) {
	out := render(t, empty(), all)
	want := "Empty\n─────\nlinks  no links found\n"
	if out != want {
		t.Errorf("empty report:\n%q\nwant\n%q", out, want)
	}
}

func TestDeterministic(t *testing.T) {
	o := all
	o.Color = true
	first := render(t, fixture(), o)
	for range 10 {
		if render(t, fixture(), o) != first {
			t.Fatal("output differs between runs")
		}
	}
}
