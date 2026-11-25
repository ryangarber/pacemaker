// Package jsonfmt renders reports as JSON for tooling and CI integrations.
package jsonfmt

import (
	"encoding/json"
	"io"

	"github.com/ryangarber/pacemaker/internal/model"
	"github.com/ryangarber/pacemaker/internal/report"
)

func init() { report.Register(Renderer{}) }

// Renderer emits the full report model.
type Renderer struct{}

func (Renderer) Name() string         { return "json" }
func (Renderer) Description() string  { return "machine-readable JSON (full model)" }
func (Renderer) Extensions() []string { return []string{".json"} }

func (Renderer) Render(w io.Writer, r *model.Report, o report.Options) error {
	out := *r
	out.Links = report.Visible(r, o)
	if !o.Diffs {
		for i, l := range out.Links {
			c := *l
			c.Diff = ""
			out.Links[i] = &c
		}
	}
	if !o.Files {
		out.Targets = append([]model.TargetReport(nil), r.Targets...)
		for i := range out.Targets {
			out.Targets[i].Files = nil
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
