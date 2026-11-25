package markdown

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ryangarber/pacemaker/internal/lang"
	"github.com/ryangarber/pacemaker/internal/marker"
	"github.com/ryangarber/pacemaker/internal/model"
	"github.com/ryangarber/pacemaker/internal/ref"
	"github.com/ryangarber/pacemaker/internal/report"
	"github.com/ryangarber/pacemaker/internal/source"
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

// cells splits a GFM table row on unescaped pipes. GFM treats `\|` as a
// literal pipe even inside code spans, and any other `|` as a delimiter.
func cells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	var out []string
	var cur strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case row[i] == '\\' && i+1 < len(row) && row[i+1] == '|':
			cur.WriteString(`\|`)
			i++
		case row[i] == '|':
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(row[i])
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s) // row without a closing pipe
	}
	return out
}

// tables returns every GFM table (runs of lines starting with '|') keyed by
// the header line, outside fenced code blocks.
func tables(md string) [][]string {
	var out [][]string
	var cur []string
	fence := false
	flush := func() {
		if len(cur) > 0 {
			out = append(out, cur)
			cur = nil
		}
	}
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(l, "```") {
			fence = !fence
		}
		if !fence && strings.HasPrefix(l, "|") {
			cur = append(cur, l)
			continue
		}
		flush()
	}
	flush()
	return out
}

func checkTables(t *testing.T, md string) {
	t.Helper()
	ts := tables(md)
	if len(ts) == 0 {
		t.Fatal("no tables")
	}
	for _, tb := range ts {
		if len(tb) < 2 || !strings.HasPrefix(tb[1], "|---") {
			t.Errorf("table without delimiter row: %q", tb)
			continue
		}
		n := len(cells(tb[0]))
		for _, row := range tb[1:] {
			if got := len(cells(row)); got != n {
				t.Errorf("row has %d cells, header %d:\n  %s\n  %s", got, n, tb[0], row)
			}
		}
	}
}

func section(t *testing.T, md, heading string) string {
	t.Helper()
	i := strings.Index(md, "\n"+heading+"\n")
	if i < 0 {
		t.Fatalf("section %q missing", heading)
	}
	rest := md[i+len(heading)+2:]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func TestRegistered(t *testing.T) {
	for _, n := range []string{"markdown", "md", "gfm", "MD"} {
		if r, err := report.Get(n); err != nil || r.Name() != "markdown" {
			t.Errorf("Get(%q) = %v, %v", n, r, err)
		}
	}
	for _, f := range []string{"a.md", "b.markdown"} {
		if r, ok := report.ForFile(f); !ok || r.Name() != "markdown" {
			t.Errorf("ForFile(%q) = %v, %v", f, r, ok)
		}
	}
}

func TestTablesWellFormed(t *testing.T) {
	for _, o := range []report.Options{{}, all} {
		checkTables(t, render(t, fixture(), o))
	}
	checkTables(t, render(t, empty(), all))
}

func TestHeaderAndSectionOrder(t *testing.T) {
	md := render(t, fixture(), all)
	if !strings.HasPrefix(md, "# Port <status> & sync\n\n⚠️ **Needs attention** · generated 2026-11-01 12:30 UTC\n") {
		t.Errorf("unexpected header:\n%s", md[:120])
	}
	if !strings.Contains(md, "> Failing because of: 1 drifted; 1 missing\n") {
		t.Error("fail reasons missing")
	}
	last := -1
	for _, h := range []string{"## Targets", "## Marker problems", "## Links", "## Details", "## Todos & notes", "## Coverage: `up`", "_Generated by [pacemaker]"} {
		i := strings.Index(md, h)
		if i < 0 {
			t.Fatalf("missing %q", h)
		}
		if i < last {
			t.Errorf("%q out of order", h)
		}
		last = i
	}
	if !strings.HasSuffix(md, "._\n") {
		t.Error("footer not last")
	}
}

func TestTitleIsNotEscaped(t *testing.T) {
	// The title is rendered verbatim (raw HTML in it would reach the page).
	// This documents current behaviour; titles come from the project config.
	md := render(t, fixture(), report.Options{})
	if !strings.HasPrefix(md, "# Port <status> & sync") {
		t.Error("title changed")
	}
}

func TestStatusSummary(t *testing.T) {
	md := render(t, fixture(), report.Options{})
	want := "| Status | Links |\n|---|---:|\n" +
		"| ❌ missing | 1 |\n| ⚠️ drifted | 1 |\n| 🆕 new | 1 |\n| 🎨 cosmetic | 1 |\n| ✅ ok | 1 |\n" +
		"| **total** | **5** |\n"
	if !strings.Contains(md, want) {
		t.Errorf("status summary wrong:\n%s", md)
	}
	if !strings.Contains(md, "**Todos:** ☐ 1 open · 🔁 1 need review (upstream changed) · ✖️ 1 stale ref(s) · ℹ️ 1 note(s) — see [Todos & notes](#todos--notes)") {
		t.Error("todo summary wrong")
	}
}

func TestTargets(t *testing.T) {
	sec := section(t, render(t, fixture(), report.Options{}), "## Targets")
	for _, want := range []string{
		"| `up` | https://example.com/&lt;repo&gt; | `0123456789 (dirty)` | ▰▰▰▰▰▱▱▱▱▱ 50.0% | 75.0% (3/4) | 2/2 |",
		"| `broken` | git@example.com:b.git | — | 🛑 clone failed: &lt;denied&gt; | | |",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("targets missing %q\n%s", want, sec)
		}
	}
}

func TestLinks(t *testing.T) {
	hidden := section(t, render(t, fixture(), report.Options{}), "## Links")
	shown := section(t, render(t, fixture(), report.Options{ShowOK: true}), "## Links")
	if strings.Contains(hidden, "`steady`") {
		t.Error("ok link listed without ShowOK")
	}
	if !strings.Contains(shown, "| ✅ | `steady` |") {
		t.Error("ok link missing with ShowOK")
	}
	rows := tables(shown)[0][2:]
	if len(rows) != 5 {
		t.Fatalf("%d link rows, want 5", len(rows))
	}
	order := []string{"❌", "⚠️", "🆕", "🎨", "✅"}
	for i, r := range rows {
		if !strings.HasPrefix(r, "| "+order[i]+" |") {
			t.Errorf("row %d = %s, want status %s", i, r, order[i])
		}
	}
	want := "| ⚠️ | `<script>alert(1)</script>` | `up:src/a\\|b.go#Foo` | `local/x.go:10` | body changed \\| &lt;b&gt;bold&lt;/b&gt; · _local edited since accept_ · ☐ 1 todo(s) |"
	if !strings.Contains(shown, want) {
		t.Errorf("drifted row wrong; want %s\n%s", want, shown)
	}
	if !strings.Contains(shown, "`ünïcode-块`") {
		t.Error("unicode block missing")
	}
}

func TestDetails(t *testing.T) {
	sec := section(t, render(t, fixture(), report.Options{ShowOK: true}), "## Details")
	// Only links needing attention get a details entry.
	for blk, want := range map[string]bool{"<script>alert(1)</script>": true, "ünïcode-块": true, "pretty": true, "fresh`tick": false, "steady": false} {
		if got := strings.Contains(sec, "`"+blk+"` →"); got != want {
			t.Errorf("details for %q = %v, want %v", blk, got, want)
		}
	}
	for _, want := range []string{
		"- **Status:** drifted — body changed \\| &lt;b&gt;bold&lt;/b&gt;\n",
		"- **Local:** `local/x.go` lines 10–20\n",
		"- **Accepted:** `fedcba9876` L5-L8 at 2026-10-01 by ana\n",
		"- **Now:** `0123456789` `src/c.go` L5-L9\n",
		"- **Change size:** +1 / −1 lines\n",
		"- **Suggested ref:** `up:src/c.go#Foo`\n",
		"**Semantic changes (go):**\n\n- **breaking** signature `Foo` — param `x` removed\n- **minor** body — loop changed\n",
		"- `abc1234` 2026-10-30 — fix &lt;tag&gt; \\| pipe (Bob &lt;bob@x&gt;)\n",
		"- ☐ **p0** port the &lt;fast&gt; path \\| soon · @ana `#perf` · ✖️ `up:gone.go#X` missing: not found · 🔁 _block drifted upstream — re-check_ (`local/x.go:12`)\n",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("details missing %q", want)
		}
	}
	if strings.Contains(sec, "```diff") {
		t.Error("diff shown without Diffs option")
	}
}

func TestDiffFence(t *testing.T) {
	md := render(t, fixture(), report.Options{Diffs: true})
	want := "<details><summary>Upstream diff</summary>\n\n```diff\n" + fixture().Links[1].Diff + "```\n\n</details>\n"
	if !strings.Contains(md, want) {
		t.Errorf("diff block wrong:\n%s", md)
	}
	if strings.Count(md, "```")%2 != 0 {
		t.Error("unbalanced code fences")
	}
}

func TestTodos(t *testing.T) {
	sec := section(t, render(t, fixture(), report.Options{}), "## Todos & notes")
	rows := tables(sec)[0][2:]
	if len(rows) != 2 {
		t.Fatalf("%d todo rows, want 2", len(rows))
	}
	if !strings.HasPrefix(rows[0], "| ☐ | blocker | `local/x.go:12` | ⚠️ `<script>alert(1)</script>` | port the &lt;fast&gt; path \\| soon") {
		t.Errorf("todo row wrong: %s", rows[0])
	}
	if rows[1] != "| ℹ️ |  | `local/z.go:1` |  | kept v1 codes |" {
		t.Errorf("note row wrong: %q", rows[1])
	}
}

func TestCoverage(t *testing.T) {
	if strings.Contains(render(t, fixture(), report.Options{}), "## Coverage") {
		t.Error("coverage without Files option")
	}
	if strings.Contains(render(t, fixture(), report.Options{Files: true}), "Unported symbols") {
		t.Error("symbols without Symbols option")
	}
	md := render(t, fixture(), report.Options{Files: true, Symbols: true})
	sec := section(t, md, "## Coverage: `up`")
	bad := strings.Index(sec, "| `src/bad<x>.go` | ▰▰▰▱▱▱▱▱▱▱ 25.0% | 5/20 | 2 |")
	good := strings.Index(sec, "| `src/good.go` | ▰▰▰▰▰▰▰▰▰▰ 100.0% | 10/10 | 1 |")
	if bad < 0 || good < 0 || bad > good {
		t.Errorf("file rows wrong or not worst first:\n%s", sec)
	}
	for _, want := range []string{
		"<details><summary>Unported symbols (2)</summary>",
		"| `src/bad<x>.go#Bar` | func | uncovered · ☐ planned | L1-L5 |",
		"| `src/bad<x>.go#Qux` | type | partial | L7-L9 |",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("coverage missing %q", want)
		}
	}
	if strings.Contains(sec, "#Baz") {
		t.Error("covered symbol listed")
	}
	if strings.Contains(md, "## Coverage: `broken`") {
		t.Error("coverage section for a target without files")
	}
}

func TestEmptyReport(t *testing.T) {
	md := render(t, empty(), all)
	if !strings.Contains(md, "✅ **In sync** · generated 2026-01-02 03:04 UTC") {
		t.Error("in-sync verdict missing")
	}
	if !strings.Contains(md, "| Status | Links |\n|---|---:|\n| **total** | **0** |\n") {
		t.Error("empty status table wrong")
	}
	for _, absent := range []string{"## Links", "## Details", "## Todos", "## Marker problems", "## Coverage", "**Todos:**", "Failing because"} {
		if strings.Contains(md, absent) {
			t.Errorf("empty report contains %q", absent)
		}
	}
}

func TestDeterministic(t *testing.T) {
	first := render(t, fixture(), all)
	for range 10 {
		if render(t, fixture(), all) != first {
			t.Fatal("output differs between runs")
		}
	}
}

func TestEsc(t *testing.T) {
	if got := esc("a|b\nc<d>"); got != `a\|b c&lt;d&gt;` {
		t.Errorf("esc = %q", got)
	}
	for pct, want := range map[float64]string{0: "▱▱▱▱▱▱▱▱▱▱", 100: "▰▰▰▰▰▰▰▰▰▰", 44: "▰▰▰▰▱▱▱▱▱▱", 45: "▰▰▰▰▰▱▱▱▱▱"} {
		if got := bar(pct); got != want {
			t.Errorf("bar(%v) = %s, want %s", pct, got, want)
		}
	}
}

func TestCellsHelper(t *testing.T) {
	if n := len(cells("| a | `b\\|c` | d |")); n != 3 {
		t.Errorf("escaped pipe split: %d", n)
	}
	if n := len(cells("| a | `b|c` | d |")); n != 4 {
		t.Errorf("unescaped pipe in code span not split: %d", n)
	}
}

func TestPipeInUnescapedFields(t *testing.T) {
	r := fixture()
	r.Targets[0].Name = "up|stream"
	r.Targets[0].Files[0].Path = "src/x|y.go"
	r.Links[1].Block = "a|b"
	r.Links[1].LocalFile = "local/p|q.go"
	md := render(t, r, all)
	checkTables(t, md)
}

func TestHTMLInFindingMessage(t *testing.T) {
	r := fixture()
	r.Links[1].Findings[0].Message = "returns <script>x</script> now"
	md := render(t, r, report.Options{})
	if strings.Contains(md, "<script>x</script>") {
		t.Error("raw HTML from finding message")
	}
}
