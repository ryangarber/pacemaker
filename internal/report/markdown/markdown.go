// Package markdown renders reports as GitHub-flavoured Markdown (suitable
// for PR comments, wikis and job summaries).
package markdown

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ryangarber/pacemaker/internal/model"
	"github.com/ryangarber/pacemaker/internal/report"
	"github.com/ryangarber/pacemaker/internal/textutil"
)

func init() { report.Register(Renderer{}, "md", "gfm") }

// Renderer implements report.Renderer.
type Renderer struct{}

func (Renderer) Name() string         { return "markdown" }
func (Renderer) Description() string  { return "GitHub-flavoured Markdown" }
func (Renderer) Extensions() []string { return []string{".md", ".markdown"} }

var emoji = map[model.Status]string{
	model.OK: "✅", model.Moved: "↪️", model.Cosmetic: "🎨", model.Drifted: "⚠️",
	model.Missing: "❌", model.New: "🆕", model.Error: "🛑",
}

func esc(s string) string {
	r := strings.NewReplacer("|", `\|`, "\n", " ", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func bar(pct float64) string {
	n := int(pct/10 + 0.5)
	return strings.Repeat("▰", n) + strings.Repeat("▱", 10-n)
}

func (Renderer) Render(w io.Writer, r *model.Report, o report.Options) error {
	p := func(f string, a ...any) { fmt.Fprintf(w, f, a...) }
	p("# %s\n\n", r.Title)
	verdict := "✅ **In sync**"
	if r.Failing {
		verdict = "⚠️ **Needs attention**"
	}
	p("%s · generated %s\n\n", verdict, r.Generated.Format("2006-01-02 15:04 MST"))

	// Status summary.
	p("| Status | Links |\n|---|---:|\n")
	for _, s := range model.Statuses {
		if n := r.Counts[s]; n > 0 {
			p("| %s %s | %d |\n", emoji[s], s, n)
		}
	}
	p("| **total** | **%d** |\n\n", len(r.Links))

	// Targets & coverage.
	p("## Targets\n\n| Target | Location | Revision | Line coverage | Symbols | Files |\n|---|---|---|---|---|---:|\n")
	for _, t := range r.Targets {
		if t.Error != "" {
			p("| `%s` | %s | — | 🛑 %s | | |\n", t.Name, esc(t.Location), esc(t.Error))
			continue
		}
		rev := textutil.ShortHash(t.Revision)
		if t.Dirty {
			rev += " (dirty)"
		}
		cov := t.Totals
		sym := "—"
		if cov.Symbols > 0 {
			sym = fmt.Sprintf("%.1f%% (%d/%d)", cov.SymbolPercent(), cov.SymbolsCovered+cov.SymbolsWaived, cov.Symbols)
		}
		line := "—"
		if cov.Files > 0 {
			line = fmt.Sprintf("%s %.1f%%", bar(cov.Percent()), cov.Percent())
		}
		p("| `%s` | %s | `%s` | %s | %s | %d/%d |\n", t.Name, esc(t.Location), rev, line, sym, cov.FilesTouched, cov.Files)
	}
	p("\n")

	if len(r.Problems) > 0 {
		p("## Marker problems\n\n")
		for _, pr := range r.Problems {
			p("- `%s:%d` — %s\n", pr.File, pr.Line, esc(pr.Message))
		}
		p("\n")
	}

	links := report.Visible(r, o)
	if len(links) > 0 {
		p("## Links\n\n| | Block | Upstream | Local | Detail |\n|---|---|---|---|---|\n")
		for _, l := range links {
			local := fmt.Sprintf("%s:%d", l.LocalFile, l.MarkerLine)
			detail := esc(l.Detail)
			if l.LocalChanged {
				detail += " · _local edited since accept_"
			}
			p("| %s | `%s` | `%s` | `%s` | %s |\n", emoji[l.Status], l.Block, esc(l.Ref), local, detail)
		}
		p("\n")
	}

	// Details for links needing attention.
	var attn []*model.Link
	for _, l := range links {
		if l.Status == model.Drifted || l.Status == model.Cosmetic || l.Status == model.Missing || l.Status == model.Moved {
			attn = append(attn, l)
		}
	}
	if len(attn) > 0 {
		p("## Details\n\n")
		for _, l := range attn {
			p("### %s `%s` → `%s`\n\n", emoji[l.Status], l.Block, l.Ref)
			p("- **Status:** %s — %s\n", l.Status, esc(l.Detail))
			p("- **Local:** `%s` lines %d–%d\n", l.LocalFile, l.LocalStart, l.LocalEnd)
			if l.Baseline != nil {
				p("- **Accepted:** `%s` %s at %s", textutil.ShortHash(l.Baseline.Revision), l.Baseline.Range, l.Baseline.AcceptedAt.Format("2006-01-02"))
				if l.Baseline.AcceptedBy != "" {
					p(" by %s", l.Baseline.AcceptedBy)
				}
				p("\n")
			}
			if !l.Range.IsZero() {
				p("- **Now:** `%s` `%s` %s\n", textutil.ShortHash(l.Revision), l.Current, l.Range)
			}
			if l.Added+l.Removed > 0 {
				p("- **Change size:** +%d / −%d lines\n", l.Added, l.Removed)
			}
			if l.SuggestedRef != "" {
				p("- **Suggested ref:** `%s`\n", l.SuggestedRef)
			}
			if len(l.Findings) > 0 {
				p("\n**Semantic changes (%s):**\n\n", l.Language)
				for _, f := range l.Findings {
					sym := ""
					if f.Symbol != "" {
						sym = " `" + f.Symbol + "`"
					}
					p("- **%s** %s%s — %s\n", f.Severity, f.Kind, sym, f.Message)
				}
			}
			if len(l.Commits) > 0 {
				p("\n**Upstream commits since accept:**\n\n")
				for _, c := range l.Commits {
					p("- `%s` %s — %s (%s)\n", c.Short, c.Date, esc(c.Subject), esc(c.Author))
				}
			}
			if o.Diffs && l.Diff != "" {
				p("\n<details><summary>Upstream diff</summary>\n\n```diff\n%s```\n\n</details>\n", l.Diff)
			}
			p("\n")
		}
	}

	if o.Files {
		for _, t := range r.Targets {
			if len(t.Files) == 0 {
				continue
			}
			p("## Coverage: `%s`\n\n", t.Name)
			p("| File | Coverage | Lines | Links |\n|---|---|---:|---:|\n")
			files := append([]model.FileCoverage(nil), t.Files...)
			sort.SliceStable(files, func(i, j int) bool { return files[i].Percent() < files[j].Percent() })
			for _, f := range files {
				p("| `%s` | %s %.1f%% | %d/%d | %d |\n", f.Path, bar(f.Percent()), f.Percent(), f.Covered+f.Waived, f.Significant, f.Links)
			}
			p("\n")
			if o.Symbols {
				var rows []string
				for _, f := range t.Files {
					for _, s := range f.Symbols {
						if s.State == "uncovered" || s.State == "partial" {
							rows = append(rows, fmt.Sprintf("| `%s#%s` | %s | %s | L%d-L%d |", f.Path, s.Name, s.Kind, s.State, s.Start, s.End))
						}
					}
				}
				if len(rows) > 0 {
					p("<details><summary>Unported symbols (%d)</summary>\n\n| Symbol | Kind | State | Lines |\n|---|---|---|---|\n%s\n\n</details>\n\n", len(rows), strings.Join(rows, "\n"))
				}
			}
		}
	}
	p("---\n_Generated by [pacemaker](https://github.com/ryangarber/pacemaker)._\n")
	return nil
}
