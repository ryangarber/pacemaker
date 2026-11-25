// Package html renders reports as a single self-contained HTML page with
// status filtering, coverage bars, semantic findings and colored diffs.
package html

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"

	"github.com/ryangarber/pacemaker/internal/marker"
	"github.com/ryangarber/pacemaker/internal/model"
	"github.com/ryangarber/pacemaker/internal/report"
	"github.com/ryangarber/pacemaker/internal/textutil"
)

func init() { report.Register(Renderer{}, "htm") }

//go:embed template.html
var tmplSrc string

// Renderer implements report.Renderer.
type Renderer struct{}

func (Renderer) Name() string         { return "html" }
func (Renderer) Description() string  { return "self-contained interactive HTML page" }
func (Renderer) Extensions() []string { return []string{".html", ".htm"} }

type diffLine struct {
	Class, Text string
}

type view struct {
	*model.Report
	Opts     report.Options
	Links    []*model.Link
	Statuses []model.Status
	Total    model.Coverage
}

var funcs = template.FuncMap{
	"short": textutil.ShortHash,
	"pct":   func(f float64) string { return fmt.Sprintf("%.1f", f) },
	"waivedPct": func(waived, sig int) float64 {
		if sig == 0 {
			return 0
		}
		return 100 * float64(waived) / float64(sig)
	},
	"sub": func(a, b float64) float64 { return a - b },
	"tone": func(f float64) string {
		switch {
		case f >= 80:
			return "good"
		case f >= 50:
			return "warn"
		}
		return "bad"
	},
	"icon": func(s model.Status) string { i, _ := textutil.StatusIcon(s); return i },
	"diffLines": func(d string) []diffLine {
		var out []diffLine
		for _, l := range strings.Split(strings.TrimSuffix(d, "\n"), "\n") {
			c := "ctx"
			switch {
			case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
				c = "hdr"
			case strings.HasPrefix(l, "@@"):
				c = "hunk"
			case strings.HasPrefix(l, "+"):
				c = "add"
			case strings.HasPrefix(l, "-"):
				c = "del"
			}
			out = append(out, diffLine{c, l})
		}
		return out
	},
	"worstFirst": func(fs []model.FileCoverage) []model.FileCoverage {
		out := append([]model.FileCoverage(nil), fs...)
		sort.SliceStable(out, func(i, j int) bool { return out[i].Percent() < out[j].Percent() })
		return out
	},
	"unported": func(f model.FileCoverage) []model.SymbolCoverage {
		var out []model.SymbolCoverage
		for _, s := range f.Symbols {
			if s.State == "uncovered" || s.State == "partial" {
				out = append(out, s)
			}
		}
		return out
	},
	"date":   func(t interface{ Format(string) string }) string { return t.Format("2006-01-02 15:04") },
	"join":   strings.Join,
	"plain":  func(s string) string { return strings.ReplaceAll(s, "`", "") },
	"isTodo": func(t *model.Todo) bool { return t.Kind == marker.NoteTodo },
	"prio":   model.PriorityClass,
	"review": func(t *model.Todo) bool {
		return t.Kind == marker.NoteTodo && (t.BlockStatus == model.Drifted || t.BlockStatus == model.Missing)
	},
	"bad": func(s model.Status) bool { return s == model.Missing || s == model.Error },
}

var tmpl = template.Must(template.New("report").Funcs(funcs).Parse(tmplSrc))

func (Renderer) Render(w io.Writer, r *model.Report, o report.Options) error {
	return tmpl.Execute(w, view{Report: r, Opts: o, Links: report.Visible(r, o), Statuses: model.Statuses, Total: r.Total()})
}
