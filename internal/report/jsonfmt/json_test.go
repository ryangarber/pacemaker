package jsonfmt

import (
	"bytes"
	"encoding/json"
	"reflect"
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

func render(t *testing.T, r *model.Report, o report.Options) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := (Renderer{}).Render(&b, r, o); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return b.Bytes()
}

func decode(t *testing.T, b []byte) *model.Report {
	t.Helper()
	if !json.Valid(b) {
		t.Fatalf("invalid JSON:\n%s", b)
	}
	var got model.Report
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return &got
}

// expected applies the documented option semantics to a fresh fixture, and
// drops what the model never serialises (Link.Todos is json:"-").
func expected(o report.Options) *model.Report {
	r := fixture()
	r.Links = report.Visible(r, o)
	for _, l := range r.Links {
		l.Todos = nil
		if !o.Diffs {
			l.Diff = ""
		}
	}
	if !o.Files {
		for i := range r.Targets {
			r.Targets[i].Files = nil
		}
	}
	return r
}

func TestRegistered(t *testing.T) {
	r, err := report.Get("JSON")
	if err != nil || r.Name() != "json" {
		t.Fatalf("Get(JSON) = %v, %v", r, err)
	}
	if r, ok := report.ForFile("out/report.json"); !ok || r.Name() != "json" {
		t.Fatalf("ForFile(.json) = %v, %v", r, ok)
	}
}

func TestRoundTrip(t *testing.T) {
	for _, o := range []report.Options{
		{},
		{ShowOK: true},
		{Diffs: true},
		{Files: true, Symbols: true},
		{ShowOK: true, Diffs: true, Files: true, Symbols: true, Color: true},
	} {
		got := decode(t, render(t, fixture(), o))
		want := expected(o)
		if !reflect.DeepEqual(got, want) {
			g, _ := json.MarshalIndent(got, "", " ")
			w, _ := json.MarshalIndent(want, "", " ")
			t.Errorf("options %+v: round trip differs\n got: %s\nwant: %s", o, g, w)
		}
	}
}

func TestOptionsShapeOutput(t *testing.T) {
	plain := string(render(t, fixture(), report.Options{}))
	full := string(render(t, fixture(), report.Options{ShowOK: true, Diffs: true, Files: true}))

	for _, c := range []struct {
		needle      string
		plain, full bool
	}{
		{`"block": "steady"`, false, true}, // ok link
		{`"diff":`, false, true},
		{`"files": [`, false, true},
		{`"status": "drifted"`, true, true},
		{`"counts":`, true, true},
	} {
		if got := strings.Contains(plain, c.needle); got != c.plain {
			t.Errorf("plain output contains %s = %v, want %v", c.needle, got, c.plain)
		}
		if got := strings.Contains(full, c.needle); got != c.full {
			t.Errorf("full output contains %s = %v, want %v", c.needle, got, c.full)
		}
	}
	// Counts always describe every link, including hidden ok ones.
	got := decode(t, []byte(plain))
	if got.Counts[model.OK] != 1 || len(got.Links) != 4 {
		t.Errorf("counts %v with %d links", got.Counts, len(got.Links))
	}
}

func TestOrderPreserved(t *testing.T) {
	got := decode(t, render(t, fixture(), report.Options{ShowOK: true}))
	var statuses []model.Status
	for _, l := range got.Links {
		statuses = append(statuses, l.Status)
	}
	want := []model.Status{model.Missing, model.Drifted, model.New, model.Cosmetic, model.OK}
	if !reflect.DeepEqual(statuses, want) {
		t.Errorf("link order %v, want %v", statuses, want)
	}
	if got.Todos[0].Kind != marker.NoteTodo || got.Todos[1].Kind != marker.NoteNote {
		t.Errorf("todo order not preserved: %+v", got.Todos)
	}
}

func TestDoesNotMutateInput(t *testing.T) {
	r := fixture()
	render(t, r, report.Options{})
	if !reflect.DeepEqual(r, fixture()) {
		t.Fatal("Render modified the report it was given")
	}
}

func TestEscapingAndUnicode(t *testing.T) {
	out := render(t, fixture(), report.Options{})
	// encoding/json escapes HTML-significant characters; the decoded values
	// must still be exact.
	if bytes.Contains(out, []byte("<script>")) {
		t.Error("raw <script> in JSON output")
	}
	got := decode(t, out)
	blocks := map[string]bool{}
	for _, l := range got.Links {
		blocks[l.Block] = true
	}
	for _, b := range []string{"<script>alert(1)</script>", "ünïcode-块", "fresh`tick"} {
		if !blocks[b] {
			t.Errorf("block %q lost in round trip", b)
		}
	}
}

func TestEmptyReport(t *testing.T) {
	out := render(t, empty(), report.Options{ShowOK: true, Diffs: true, Files: true})
	got := decode(t, out)
	if got.Title != "Empty" || len(got.Links) != 0 || len(got.Targets) != 0 || got.Failing {
		t.Errorf("unexpected empty report: %+v", got)
	}
	if !bytes.HasSuffix(out, []byte("}\n")) {
		t.Error("output should end with a newline-terminated object")
	}
}

func TestDeterministic(t *testing.T) {
	o := report.Options{ShowOK: true, Diffs: true, Files: true, Symbols: true}
	first := render(t, fixture(), o)
	for range 10 {
		if next := render(t, fixture(), o); !bytes.Equal(first, next) {
			t.Fatal("output differs between runs")
		}
	}
}
