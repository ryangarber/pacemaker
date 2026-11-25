package engine

import (
	"sort"

	"github.com/ryangarber/pacemaker/internal/lang"
	"github.com/ryangarber/pacemaker/internal/marker"
	"github.com/ryangarber/pacemaker/internal/model"
	"github.com/ryangarber/pacemaker/internal/ref"
)

// coverage computes line and symbol coverage for one target.
func (e *Engine) coverage(name string, links []*model.Link) model.TargetReport {
	tr := e.targetHeader(name)
	if tr.Error != "" {
		return tr
	}
	src, _ := e.Source(name)
	files, err := src.ListFiles()
	if err != nil {
		tr.Error = err.Error()
		return tr
	}
	filter := e.Cfg.Targets[name].Set()

	type marks struct{ covered, waived []ref.Range }
	byFile := map[string]*marks{}
	linkCount := map[string]int{}
	for _, l := range links {
		if l.Target != name || l.Range.IsZero() || l.Status == model.Missing || l.Status == model.Error {
			continue
		}
		m := byFile[l.Current]
		if m == nil {
			m = &marks{}
			byFile[l.Current] = m
		}
		if l.Kind == marker.KindSkip {
			m.waived = append(m.waived, l.Range)
		} else {
			m.covered = append(m.covered, l.Range)
			linkCount[l.Current]++
		}
	}

	for _, f := range files {
		if !filter.Match(f) {
			continue
		}
		content, err := src.ReadFile(f)
		if err != nil || isBinary(content) {
			continue
		}
		language := lang.ForPath(f)
		var sig []bool
		if lc, ok := language.(lang.LineClassifier); ok {
			sig = lc.SignificantLines(content)
		} else {
			sig = lang.Fallback().(lang.LineClassifier).SignificantLines(content)
		}
		fc := model.FileCoverage{Path: f, Language: language.ID(), Lines: len(sig), Links: linkCount[f]}
		state := make([]byte, len(sig)+1) // 0 none, 1 covered, 2 waived
		if m := byFile[f]; m != nil {
			for _, r := range m.waived {
				for i := max(r.Start, 1); i <= r.End && i <= len(sig); i++ {
					state[i] = 2
				}
			}
			for _, r := range m.covered {
				for i := max(r.Start, 1); i <= r.End && i <= len(sig); i++ {
					state[i] = 1
				}
			}
		}
		var gap ref.Range
		for i := 1; i <= len(sig); i++ {
			if !sig[i-1] {
				continue
			}
			fc.Significant++
			switch state[i] {
			case 1:
				fc.Covered++
			case 2:
				fc.Waived++
			default:
				if !gap.IsZero() { // only blank/comment lines since gap.End
					gap.End = i
				} else {
					gap = ref.Range{Start: i, End: i}
				}
				continue
			}
			if !gap.IsZero() {
				fc.Uncovered = append(fc.Uncovered, gap)
				gap = ref.Range{}
			}
		}
		if !gap.IsZero() {
			fc.Uncovered = append(fc.Uncovered, gap)
		}
		if sp, ok := language.(lang.SymbolProvider); ok {
			for _, s := range sp.Symbols(content) {
				if s.Container {
					continue
				}
				var tot, cov, wav int
				for i := s.Start; i <= s.End && i <= len(sig); i++ {
					if !sig[i-1] {
						continue
					}
					tot++
					switch state[i] {
					case 1:
						cov++
					case 2:
						wav++
					}
				}
				sc := model.SymbolCoverage{Name: s.Name, Kind: s.Kind, Start: s.Start, End: s.End, Exported: s.Exported}
				switch {
				case tot > 0 && cov == tot:
					sc.State = "covered"
				case tot > 0 && wav == tot, tot > 0 && cov+wav == tot && wav > 0:
					sc.State = "waived"
				case cov > 0 || wav > 0:
					sc.State = "partial"
				default:
					sc.State = "uncovered"
				}
				fc.Symbols = append(fc.Symbols, sc)
				tr.Totals.Symbols++
				switch sc.State {
				case "covered":
					tr.Totals.SymbolsCovered++
				case "waived":
					tr.Totals.SymbolsWaived++
				case "partial":
					tr.Totals.SymbolsPartial++
				}
			}
		}
		tr.Totals.Files++
		if fc.Covered+fc.Waived > 0 {
			tr.Totals.FilesTouched++
		}
		tr.Totals.Significant += fc.Significant
		tr.Totals.Covered += fc.Covered
		tr.Totals.Waived += fc.Waived
		tr.Files = append(tr.Files, fc)
	}
	sort.Slice(tr.Files, func(i, j int) bool { return tr.Files[i].Path < tr.Files[j].Path })
	return tr
}
