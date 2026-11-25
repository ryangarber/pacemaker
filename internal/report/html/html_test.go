package html

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

func TestRegistered(t *testing.T) {
	for _, n := range []string{"html", "htm", "HTML"} {
		if r, err := report.Get(n); err != nil || r.Name() != "html" {
			t.Errorf("Get(%q) = %v, %v", n, r, err)
		}
	}
	for _, f := range []string{"a.html", "b.HTM"} {
		if r, ok := report.ForFile(f); !ok || r.Name() != "html" {
			t.Errorf("ForFile(%q) = %v, %v", f, r, ok)
		}
	}
}

func TestEscaping(t *testing.T) {
	out := render(t, fixture(), all)
	for _, raw := range []string{
		"<script>alert(1)</script>", "<status>", "<repo>", "<denied>", "<block>",
		"<b>bold</b>", "<tag>", "<fast>", "<ported>", "<x>", "<proj>", "-old <i>", "<bob@x>",
	} {
		if strings.Contains(out, raw) {
			t.Errorf("unescaped %q in HTML output", raw)
		}
	}
	for _, esc := range []string{
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		"<title>Port &lt;status&gt; &amp; sync</title>",
		"<h1>Port &lt;status&gt; &amp; sync</h1>",
		"clone failed: &lt;denied&gt;",
		"body changed | &lt;b&gt;bold&lt;/b&gt;",
		`<span class="del">-old &lt;i&gt;</span>`,
	} {
		if !strings.Contains(out, esc) {
			t.Errorf("missing escaped %q", esc)
		}
	}
	// The only <script> element is the page's own filter script.
	if n := strings.Count(out, "<script>"); n != 1 {
		t.Errorf("%d <script> elements, want 1", n)
	}
	if !strings.Contains(out, "ünïcode-块") || !strings.Contains(out, "fresh`tick") {
		t.Error("unicode / backtick block names not rendered verbatim")
	}
	// Backticks are stripped from finding messages (they are Markdown-ish).
	if !strings.Contains(out, "param x removed") {
		t.Error("finding message backticks not stripped")
	}
}

func TestBalancedMarkup(t *testing.T) {
	for name, out := range map[string]string{
		"full":  render(t, fixture(), all),
		"plain": render(t, fixture(), report.Options{}),
		"empty": render(t, empty(), all),
	} {
		for _, tag := range []string{"details", "summary", "table", "tbody", "thead", "tr", "td", "ul", "li", "dl", "pre", "main", "html", "body", "h2"} {
			open := strings.Count(out, "<"+tag+">") + strings.Count(out, "<"+tag+" ")
			closed := strings.Count(out, "</"+tag+">")
			if open != closed {
				t.Errorf("%s: <%s> opened %d times, closed %d", name, tag, open, closed)
			}
		}
		if !strings.HasPrefix(out, "<!doctype html>") || !strings.Contains(out, "</html>") {
			t.Errorf("%s: not a complete document", name)
		}
	}
}

func TestSectionsAndOrder(t *testing.T) {
	out := render(t, fixture(), all)
	order := []string{"<h1>", "<h2>Targets</h2>", "Marker problems</h2>", "<h2>Links</h2>", `<h2 id="todos">`, "<h2>Coverage · <code>up</code></h2>", "<footer>"}
	last := -1
	for _, s := range order {
		i := strings.Index(out, s)
		if i < 0 {
			t.Fatalf("section %q missing", s)
		}
		if i < last {
			t.Errorf("section %q out of order", s)
		}
		last = i
	}
	if !strings.Contains(out, `<span class="verdict bad">Needs attention</span>`) {
		t.Error("failing verdict missing")
	}
	if !strings.Contains(out, "Failing because of: 1 drifted; 1 missing") {
		t.Error("fail reasons missing")
	}
	if !strings.Contains(out, "Generated 2026-11-01 12:30") {
		t.Error("generated date missing")
	}
	// The failed target spans the remaining columns with its error.
	if !strings.Contains(out, `<td colspan="4" class="s-error">clone failed`) {
		t.Error("target error cell missing")
	}
	if !strings.Contains(out, `<span class="tag">dirty</span>`) || !strings.Contains(out, "<code>0123456789</code>") {
		t.Error("short revision / dirty tag missing")
	}
}

func TestStatusCards(t *testing.T) {
	out := render(t, fixture(), report.Options{})
	// One card per non-zero status, in urgency order, with its count.
	var idx []int
	for _, s := range []string{"missing", "drifted", "new", "cosmetic", "ok"} {
		i := strings.Index(out, `<div class="card" data-status="`+s+`"><div class="n s-`+s+`">`)
		if i < 0 {
			t.Fatalf("card for %s missing", s)
		}
		idx = append(idx, i)
	}
	for i := 1; i < len(idx); i++ {
		if idx[i] < idx[i-1] {
			t.Error("status cards out of urgency order")
		}
	}
	for _, s := range []string{"error", "moved"} {
		if strings.Contains(out, `data-status="`+s+`"><div`) {
			t.Errorf("card for zero-count status %s", s)
		}
	}
	if !strings.Contains(out, "50.0%</div><div class=\"l\">line coverage") {
		t.Error("total line coverage card missing")
	}
	if !strings.Contains(out, "75.0%</div><div class=\"l\">symbol coverage") {
		t.Error("total symbol coverage card missing")
	}
	if !strings.Contains(out, "☐ 1</div><div class=\"l\">open todos · 1 to re-check") {
		t.Error("todo card missing")
	}
}

func TestLinks(t *testing.T) {
	hidden := render(t, fixture(), report.Options{})
	shown := render(t, fixture(), report.Options{ShowOK: true})
	const ok = `<details class="link" data-status="ok"`
	if strings.Contains(hidden, ok) {
		t.Error("ok link rendered without ShowOK")
	}
	if !strings.Contains(shown, ok) {
		t.Error("ok link missing with ShowOK")
	}
	if n := strings.Count(shown, `<details class="link"`); n != 5 {
		t.Errorf("%d link entries, want 5", n)
	}
	// Drifted and missing links start expanded; others collapsed.
	for s, open := range map[string]bool{"drifted": true, "missing": true, "new": false, "cosmetic": false, "ok": false} {
		tag := `<details class="link" data-status="` + s + `" open>`
		if got := strings.Contains(shown, tag); got != open {
			t.Errorf("%s open = %v, want %v", s, got, open)
		}
	}
	// Order follows the report (urgency first).
	if strings.Index(shown, `data-status="missing" open`) > strings.Index(shown, `data-status="drifted" open`) {
		t.Error("links not in report order")
	}
	for _, want := range []string{
		"<dt>Note</dt><dd>hand &lt;ported&gt;</dd>",
		`<span class="tag">local edited</span>`,
		`<span class="tag s-todo">☐ 1 todo</span>`,
		`<span class="tag">core</span>`,
		"lines 10–20",
		"<code>fedcba9876</code>",
		"by ana",
		"<code>up:src/c.go#Foo</code>", // suggested ref
		`<span class="sev sev-breaking">breaking</span>`,
		`<span class="sev sev-minor">minor</span>`,
		"Semantic changes (go)",
		"<code>abc1234</code>",
		"fix &lt;tag&gt; | pipe",
		"+1</span>",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("link body missing %q", want)
		}
	}
}

func TestDiffs(t *testing.T) {
	without := render(t, fixture(), report.Options{})
	with := render(t, fixture(), report.Options{Diffs: true})
	if strings.Contains(without, `<pre class="diff`) {
		t.Error("diff rendered without Diffs option")
	}
	// html/template escapes '+' as &#43; in text.
	for _, want := range []string{
		`<span class="hdr">--- a/src/a|b.go</span>`,
		`<span class="hdr">&#43;&#43;&#43; b/src/c.go</span>`,
		`<span class="hunk">@@ -1,1 &#43;1,1 @@</span>`,
		`<span class="del">-old &lt;i&gt;</span>`,
		`<span class="add">&#43;new &lt;i&gt;</span>`,
		`<span class="ctx"> ctx</span>`,
	} {
		if !strings.Contains(with, want) {
			t.Errorf("diff missing %q", want)
		}
	}
	if n := strings.Count(with, `<pre class="diff`); n != 1 {
		t.Errorf("%d diffs, want 1", n)
	}
}

func TestTodos(t *testing.T) {
	out := render(t, fixture(), report.Options{})
	i := strings.Index(out, `<h2 id="todos">`)
	if i < 0 {
		t.Fatal("todo section missing")
	}
	sec := out[i:]
	for _, want := range []string{
		`<span class="p-blocker">blocker</span>`, // p0 maps to blocker
		"port the &lt;fast&gt; path | soon",
		`<span class="tag">@ana</span>`,
		`<span class="tag">#perf</span>`,
		"✗ <code>up:gone.go#X</code> missing: not found",
		"⟳ block drifted upstream — re-check",
		"local/z.go:1",
		"kept v1 codes",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("todo section missing %q", want)
		}
	}
	// The todo sorts before the note.
	if strings.Index(sec, "local/x.go:12") > strings.Index(sec, "local/z.go:1") {
		t.Error("todos not before notes")
	}
}

func TestCoverage(t *testing.T) {
	if out := render(t, fixture(), report.Options{}); strings.Contains(out, "<h2>Coverage") {
		t.Error("coverage rendered without Files option")
	}
	noSym := render(t, fixture(), report.Options{Files: true})
	if strings.Contains(noSym, "unported symbol(s)") {
		t.Error("symbols rendered without Symbols option")
	}
	out := render(t, fixture(), report.Options{Files: true, Symbols: true})
	bad, good := strings.Index(out, "<code>src/bad&lt;x&gt;.go</code>"), strings.Index(out, "<code>src/good.go</code>")
	if bad < 0 || good < 0 || bad > good {
		t.Errorf("files not worst first (bad=%d good=%d)", bad, good)
	}
	for _, want := range []string{
		"2 unported symbol(s)",
		"<code>Bar</code>", "<code>Qux</code>",
		`uncovered <span class="tag s-todo">☐ planned</span>`,
		"L1-L5",
		"25.0%",  // (2+3)/20
		"2+3/20", // covered+waived/significant
		"100.0%", // good.go
	} {
		if !strings.Contains(out, want) {
			t.Errorf("coverage missing %q", want)
		}
	}
	if strings.Contains(out, "<code>Baz</code>") {
		t.Error("covered symbol listed as unported")
	}
	// Bars: covered portion excludes the waived portion, both are emitted.
	if !strings.Contains(out, `<i class="bad" style="width:10%"></i><i class="waive" style="width:15%"></i>`) {
		t.Error("coverage bar widths wrong")
	}
}

func TestEmptyReport(t *testing.T) {
	out := render(t, empty(), all)
	if !strings.Contains(out, `<span class="verdict ok">In sync</span>`) {
		t.Error("in-sync verdict missing")
	}
	for _, absent := range []string{"<h2>Links</h2>", `<h2 id="todos">`, "Marker problems", "<h2>Coverage", "Failing because", `class="card" data-status`} {
		if strings.Contains(out, absent) {
			t.Errorf("empty report contains %q", absent)
		}
	}
	if !strings.Contains(out, "<h2>Targets</h2>") {
		t.Error("targets header missing")
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

func TestFuncs(t *testing.T) {
	tone := funcs["tone"].(func(float64) string)
	for f, want := range map[float64]string{100: "good", 80: "good", 79.9: "warn", 50: "warn", 49.9: "bad", 0: "bad"} {
		if got := tone(f); got != want {
			t.Errorf("tone(%v) = %s, want %s", f, got, want)
		}
	}
	wp := funcs["waivedPct"].(func(int, int) float64)
	if wp(1, 0) != 0 || wp(1, 4) != 25 {
		t.Error("waivedPct wrong")
	}
	dl := funcs["diffLines"].(func(string) []diffLine)
	if got := dl("x\n"); len(got) != 1 || got[0] != (diffLine{"ctx", "x"}) {
		t.Errorf("diffLines trailing newline: %v", got)
	}
	wf := funcs["worstFirst"].(func([]model.FileCoverage) []model.FileCoverage)
	in := []model.FileCoverage{{Path: "a", Significant: 1, Covered: 1}, {Path: "b", Significant: 2}}
	if got := wf(in); got[0].Path != "b" || in[0].Path != "a" {
		t.Error("worstFirst should sort a copy")
	}
}
