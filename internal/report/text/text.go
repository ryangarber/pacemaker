// Package text renders reports for terminals (optionally colored).
package text

import (
	"fmt"
	"io"
	"strings"

	"github.com/ryangarber/pacemaker/internal/marker"
	"github.com/ryangarber/pacemaker/internal/model"
	"github.com/ryangarber/pacemaker/internal/report"
	tu "github.com/ryangarber/pacemaker/internal/textutil"
)

func init() { report.Register(Renderer{}, "txt", "terminal") }

// Renderer implements report.Renderer.
type Renderer struct{}

func (Renderer) Name() string         { return "text" }
func (Renderer) Description() string  { return "plain/ANSI terminal text" }
func (Renderer) Extensions() []string { return []string{".txt"} }

func (Renderer) Render(w io.Writer, r *model.Report, o report.Options) error {
	p := tu.Painter{On: o.Color}
	fmt.Fprintln(w, p.S(r.Title, tu.Bold))
	fmt.Fprintln(w, p.S(strings.Repeat("─", tu.Width(r.Title)), tu.Gray))
	Summary(w, p, r)
	links := report.Visible(r, o)
	if len(links) > 0 {
		fmt.Fprintln(w)
		LinkTable(w, p, links)
	}
	for _, l := range links {
		if l.Status != model.Drifted && l.Status != model.Cosmetic {
			continue
		}
		fmt.Fprintln(w)
		LinkDetail(w, p, l, o.Diffs)
	}
	if len(r.Todos) > 0 {
		fmt.Fprintln(w)
		TodoTable(w, p, r.Todos)
	}
	if o.Files {
		for _, t := range r.Targets {
			fmt.Fprintln(w)
			FileTable(w, p, t, 0)
		}
	}
	return nil
}

// Summary prints status counts, targets and problems.
func Summary(w io.Writer, p tu.Painter, r *model.Report) {
	var parts []string
	for _, s := range model.Statuses {
		if n := r.Counts[s]; n > 0 {
			icon, c := tu.StatusIcon(s)
			parts = append(parts, p.S(fmt.Sprintf("%s %d %s", icon, n, s), c))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, p.S("no links found", tu.Gray))
	}
	fmt.Fprintf(w, "%s  %s\n", p.S("links", tu.Bold), strings.Join(parts, p.S("  ·  ", tu.Gray)))
	for _, t := range r.Targets {
		if t.Error != "" {
			fmt.Fprintf(w, "%s  %s  %s\n", p.S("target", tu.Bold), p.S(t.Name, tu.Cyan), p.S(t.Error, tu.Red))
			continue
		}
		rev := tu.ShortHash(t.Revision)
		if t.Dirty {
			rev += p.S("+dirty", tu.Yellow)
		}
		line := fmt.Sprintf("%s %s %s %s", p.S("target", tu.Bold), p.S(t.Name, tu.Cyan), p.S(t.Location, tu.Gray), p.S("@"+rev, tu.Gray))
		if t.Totals.Files > 0 {
			c := t.Totals
			pctW := 100 * float64(c.Waived) / float64(max(c.Significant, 1))
			line += fmt.Sprintf("\n        %s %s lines", tu.Bar(p, c.Percent()-pctW, pctW, 24), p.Percent(c.Percent()))
			if c.Symbols > 0 {
				line += fmt.Sprintf("  %s symbols (%d/%d)", p.Percent(c.SymbolPercent()), c.SymbolsCovered+c.SymbolsWaived, c.Symbols)
			}
			line += p.S(fmt.Sprintf("  %d/%d files touched", c.FilesTouched, c.Files), tu.Gray)
		}
		fmt.Fprintln(w, line)
	}
	if line := TodoSummary(p, r.TodoStats); line != "" {
		fmt.Fprintf(w, "%s  %s\n", p.S("todos", tu.Bold), line)
	}
	if len(r.Problems) > 0 {
		fmt.Fprintf(w, "%s\n", p.S(fmt.Sprintf("%d marker problem(s):", len(r.Problems)), tu.Red, tu.Bold))
		for _, pr := range r.Problems {
			fmt.Fprintf(w, "  %s %s\n", p.S(fmt.Sprintf("%s:%d", pr.File, pr.Line), tu.Bold), pr.Message)
		}
	}
}

// LinkTable prints one row per link.
func LinkTable(w io.Writer, p tu.Painter, links []*model.Link) {
	t := tu.Table{Header: []string{"STATUS", "BLOCK", "UPSTREAM", "LOCAL", "DETAIL"}}
	for _, l := range links {
		up := l.Ref
		if l.Current != l.Path {
			up += p.S(" → "+l.Current, tu.Cyan)
		}
		local := fmt.Sprintf("%s:%d", l.LocalFile, l.MarkerLine)
		detail := tu.Truncate(l.Detail, 60)
		if l.LocalChanged {
			detail = p.S("✎ ", tu.Yellow) + detail
		}
		if n := l.OpenTodos(); n > 0 {
			detail = p.S(fmt.Sprintf("☐%d ", n), tu.Magenta) + detail
		}
		if sev := l.MaxSeverity(); sev != "" {
			detail = p.S("["+string(sev)+"] ", sevStyle(sev)) + detail
		}
		t.Add(p.StatusLabel(l.Status), p.S(l.Block, tu.Bold), tu.TruncateLeft(up, 60), p.S(local, tu.Gray), detail)
	}
	t.Render(w, p)
}

func sevStyle(s interface{ Rank() int }) tu.Style {
	switch s.Rank() {
	case 3:
		return tu.Red
	case 2:
		return tu.Yellow
	case 1:
		return tu.Blue
	}
	return tu.Gray
}

// LinkDetail prints everything known about one link.
func LinkDetail(w io.Writer, p tu.Painter, l *model.Link, diffs bool) {
	fmt.Fprintf(w, "%s %s %s %s\n", p.StatusLabel(l.Status), p.S(l.Block, tu.Bold), p.S("→", tu.Gray), l.Ref)
	kv := func(k, v string) { fmt.Fprintf(w, "  %s %s\n", p.S(tu.Pad(k, 10), tu.Gray), v) }
	if l.Detail != "" {
		kv("detail", l.Detail)
	}
	kv("local", fmt.Sprintf("%s:%d-%d (%s)", l.LocalFile, l.LocalStart, l.LocalEnd, l.Kind))
	if l.Note != "" {
		kv("note", l.Note)
	}
	if len(l.Tags) > 0 {
		kv("tags", strings.Join(l.Tags, ", "))
	}
	if l.Baseline != nil {
		by := ""
		if l.Baseline.AcceptedBy != "" {
			by = " by " + l.Baseline.AcceptedBy
		}
		kv("accepted", fmt.Sprintf("%s %s %s at %s%s", tu.ShortHash(l.Baseline.Revision), l.Baseline.Path, l.Baseline.Range, l.Baseline.AcceptedAt.Format("2006-01-02 15:04"), by))
	}
	if !l.Range.IsZero() {
		kv("now", fmt.Sprintf("%s %s %s", tu.ShortHash(l.Revision), l.Current, l.Range))
	}
	if l.Added+l.Removed > 0 {
		kv("change", p.S(fmt.Sprintf("+%d", l.Added), tu.Green)+" "+p.S(fmt.Sprintf("-%d", l.Removed), tu.Red)+" lines")
	}
	if l.LocalChanged {
		kv("local", p.S("edited since last accept", tu.Yellow))
	}
	if l.SuggestedRef != "" {
		kv("suggest", p.S(l.SuggestedRef, tu.Cyan)+p.S("  (accept --rewrite updates the marker)", tu.Gray))
	}
	if len(l.Findings) > 0 {
		fmt.Fprintf(w, "  %s\n", p.S("semantic changes ("+l.Language+"):", tu.Gray))
		for _, f := range l.Findings {
			sym := ""
			if f.Symbol != "" {
				sym = p.S(f.Symbol, tu.Bold) + " "
			}
			fmt.Fprintf(w, "    %s %s%s %s\n", p.S(tu.Pad(string(f.Severity), 8), sevStyle(f.Severity)), sym, p.S(f.Kind, tu.Gray), f.Message)
		}
	}
	if len(l.Commits) > 0 {
		fmt.Fprintf(w, "  %s\n", p.S("upstream commits since accept:", tu.Gray))
		for _, c := range l.Commits {
			fmt.Fprintf(w, "    %s %s %s %s\n", p.S(c.Short, tu.Yellow), p.S(c.Date, tu.Gray), c.Subject, p.S("— "+c.Author, tu.Gray))
		}
	}
	if len(l.Todos) > 0 {
		fmt.Fprintf(w, "  %s\n", p.S("todos & notes:", tu.Gray))
		for _, t := range l.Todos {
			fmt.Fprintf(w, "    %s %s\n", todoIcon(p, t), todoLine(p, t))
		}
	}
	if diffs && l.Diff != "" {
		fmt.Fprintln(w)
		for _, line := range strings.Split(strings.TrimSuffix(p.Diff(l.Diff), "\n"), "\n") {
			fmt.Fprintln(w, "    "+line)
		}
	}
}

// FileTable prints per-file coverage for a target, worst first. limit<=0
// prints all.
func FileTable(w io.Writer, p tu.Painter, t model.TargetReport, limit int) {
	fmt.Fprintf(w, "%s %s\n", p.S("coverage", tu.Bold), p.S(t.Name, tu.Cyan))
	tb := tu.Table{Header: []string{"FILE", "", "COVERAGE", "LINES", "LINKS"}, Right: map[int]bool{2: true, 3: true, 4: true}, Indent: "  "}
	for i, f := range t.Files {
		if limit > 0 && i >= limit {
			tb.Add(p.S(fmt.Sprintf("… %d more", len(t.Files)-limit), tu.Gray), "", "", "", "")
			break
		}
		pw := 100 * float64(f.Waived) / float64(max(f.Significant, 1))
		tb.Add(f.Path, tu.Bar(p, f.Percent()-pw, pw, 16), p.Percent(f.Percent()),
			fmt.Sprintf("%d/%d", f.Covered+f.Waived, f.Significant), fmt.Sprint(f.Links))
	}
	tb.Render(w, p)
}

// TodoSummary renders todo counts on one line ("" when there are none).
func TodoSummary(p tu.Painter, c model.TodoCounts) string {
	if c.Open+c.Notes == 0 {
		return ""
	}
	var parts []string
	if c.Open > 0 {
		s := fmt.Sprintf("☐ %d open", c.Open)
		var prios []string
		for _, pr := range model.Priorities {
			if n := c.ByPriority[pr]; n > 0 && pr != "normal" {
				prios = append(prios, fmt.Sprintf("%d %s", n, pr))
			}
		}
		if len(prios) > 0 {
			s += " (" + strings.Join(prios, ", ") + ")"
		}
		parts = append(parts, p.S(s, tu.Magenta))
	}
	if c.NeedsReview > 0 {
		parts = append(parts, p.S(fmt.Sprintf("⟳ %d need review (upstream changed)", c.NeedsReview), tu.Yellow))
	}
	if c.Stale > 0 {
		parts = append(parts, p.S(fmt.Sprintf("✗ %d stale ref(s)", c.Stale), tu.Red))
	}
	if c.Notes > 0 {
		parts = append(parts, p.S(fmt.Sprintf("ℹ %d note(s)", c.Notes), tu.Gray))
	}
	return strings.Join(parts, p.S("  ·  ", tu.Gray))
}

func todoIcon(p tu.Painter, t *model.Todo) string {
	if t.Kind == marker.NoteNote {
		return p.S("ℹ", tu.Gray)
	}
	return p.S("☐", tu.Magenta)
}

func prioStyle(prio string) tu.Style {
	switch model.PriorityClass(prio) {
	case "blocker":
		return tu.Red
	case "high":
		return tu.Yellow
	case "low":
		return tu.Gray
	}
	return tu.Blue
}

// todoLine renders a todo's text with its flags (without the location).
func todoLine(p tu.Painter, t *model.Todo) string {
	s := t.Text
	if t.Priority != "" {
		s = p.S("["+t.Priority+"] ", prioStyle(t.Priority)) + s
	}
	if t.Owner != "" {
		s += p.S(" @"+t.Owner, tu.Cyan)
	}
	if len(t.Tags) > 0 {
		s += p.S(" #"+strings.Join(t.Tags, " #"), tu.Gray)
	}
	for _, r := range t.Refs {
		switch r.Status {
		case model.OK, "":
			s += p.S(" → "+r.Ref, tu.Gray)
		default:
			s += p.S(" → "+r.Ref+" ("+string(r.Status)+": "+r.Detail+")", tu.Red)
		}
	}
	switch t.BlockStatus {
	case model.Drifted, model.Missing:
		s += p.S(" ⟳ block "+string(t.BlockStatus)+" upstream — re-check", tu.Yellow)
	}
	return s
}

// TodoTable lists todos and notes.
func TodoTable(w io.Writer, p tu.Painter, todos []*model.Todo) {
	t := tu.Table{Header: []string{"", "PRIO", "WHERE", "BLOCK", "TEXT"}}
	for _, td := range todos {
		blk := td.Block
		if blk != "" && td.BlockStatus != "" {
			icon, c := tu.StatusIcon(td.BlockStatus)
			blk = p.S(icon, c) + " " + blk
		}
		prio := ""
		if td.Kind == marker.NoteTodo {
			prio = p.S(model.PriorityClass(td.Priority), prioStyle(td.Priority))
		}
		text := td.Text
		if td.Owner != "" {
			text += p.S(" @"+td.Owner, tu.Cyan)
		}
		if len(td.Tags) > 0 {
			text += p.S(" #"+strings.Join(td.Tags, " #"), tu.Gray)
		}
		for _, r := range td.Refs {
			if r.Status == model.Missing || r.Status == model.Error {
				text += p.S(" ✗ "+r.Ref+" "+string(r.Status), tu.Red)
			} else {
				text += p.S(" → "+r.Ref, tu.Gray)
			}
		}
		if td.Kind == marker.NoteTodo && (td.BlockStatus == model.Drifted || td.BlockStatus == model.Missing) {
			text += p.S(" ⟳ re-check: block "+string(td.BlockStatus), tu.Yellow)
		}
		t.Add(todoIcon(p, td), prio, p.S(fmt.Sprintf("%s:%d", td.File, td.Line), tu.Gray), blk, text)
	}
	t.Render(w, p)
}
