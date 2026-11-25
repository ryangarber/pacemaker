// Package textutil provides ANSI styling, width-aware tables and bars for
// human-friendly terminal output.
package textutil

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ryangarber/pacemaker/internal/model"
)

// Style is an ANSI SGR sequence.
type Style string

const (
	Reset   Style = "\x1b[0m"
	Bold    Style = "\x1b[1m"
	Dim     Style = "\x1b[2m"
	Italic  Style = "\x1b[3m"
	Red     Style = "\x1b[31m"
	Green   Style = "\x1b[32m"
	Yellow  Style = "\x1b[33m"
	Blue    Style = "\x1b[34m"
	Magenta Style = "\x1b[35m"
	Cyan    Style = "\x1b[36m"
	Gray    Style = "\x1b[90m"
)

// Painter applies styles when color is enabled.
type Painter struct{ On bool }

// Detect enables color for terminals unless NO_COLOR is set.
func Detect(f *os.File) Painter {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return Painter{}
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return Painter{On: true}
	}
	st, err := f.Stat()
	return Painter{On: err == nil && st.Mode()&os.ModeCharDevice != 0}
}

// S styles text.
func (p Painter) S(text string, styles ...Style) string {
	if !p.On || len(styles) == 0 || text == "" {
		return text
	}
	var b strings.Builder
	for _, s := range styles {
		b.WriteString(string(s))
	}
	b.WriteString(text)
	b.WriteString(string(Reset))
	return b.String()
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Width is the visible width of s.
func Width(s string) int { return utf8.RuneCountInString(ansiRe.ReplaceAllString(s, "")) }

// Pad right-pads s to width w.
func Pad(s string, w int) string {
	if d := w - Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// PadLeft left-pads s to width w.
func PadLeft(s string, w int) string {
	if d := w - Width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

// Truncate shortens s (plain text) to w runes with an ellipsis.
func Truncate(s string, w int) string {
	if w <= 1 || utf8.RuneCountInString(s) <= w {
		return s
	}
	r := []rune(s)
	return string(r[:w-1]) + "…"
}

// TruncateLeft keeps the end of s (useful for paths).
func TruncateLeft(s string, w int) string {
	if w <= 1 || utf8.RuneCountInString(s) <= w {
		return s
	}
	r := []rune(s)
	return "…" + string(r[len(r)-w+1:])
}

// Table renders aligned columns. Cells may contain ANSI styles.
type Table struct {
	Header []string
	Rows   [][]string
	Right  map[int]bool // right-aligned columns
	Indent string
}

// Add appends a row.
func (t *Table) Add(cells ...string) { t.Rows = append(t.Rows, cells) }

// Render writes the table.
func (t *Table) Render(w io.Writer, p Painter) {
	widths := make([]int, len(t.Header))
	all := append([][]string{t.Header}, t.Rows...)
	for _, r := range all {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			if cw := Width(c); cw > widths[i] {
				widths[i] = cw
			}
		}
	}
	line := func(cells []string, style ...Style) {
		var parts []string
		for i, c := range cells {
			if i == len(cells)-1 && !t.Right[i] {
				parts = append(parts, c)
				continue
			}
			if t.Right[i] {
				parts = append(parts, PadLeft(c, widths[i]))
			} else {
				parts = append(parts, Pad(c, widths[i]))
			}
		}
		s := strings.Join(parts, "  ")
		if len(style) > 0 {
			s = p.S(ansiRe.ReplaceAllString(s, ""), style...)
		}
		fmt.Fprintln(w, t.Indent+s)
	}
	if len(t.Header) > 0 {
		line(t.Header, Bold, Gray)
	}
	for _, r := range t.Rows {
		line(r)
	}
}

// Bar renders a coverage bar of width w for pct (0..100), with an optional
// waived portion.
func Bar(p Painter, pct, waivedPct float64, w int) string {
	if w <= 0 {
		w = 20
	}
	full := int(pct/100*float64(w) + 0.5)
	wv := int(waivedPct/100*float64(w) + 0.5)
	if full+wv > w {
		wv = w - full
	}
	rest := w - full - wv
	c := Red
	switch {
	case pct+waivedPct >= 80:
		c = Green
	case pct+waivedPct >= 50:
		c = Yellow
	}
	return p.S(strings.Repeat("█", full), c) + p.S(strings.Repeat("▒", wv), Cyan) + p.S(strings.Repeat("░", rest), Gray)
}

// StatusIcon returns a symbol and color for a status.
func StatusIcon(s model.Status) (string, Style) {
	switch s {
	case model.OK:
		return "✓", Green
	case model.Moved:
		return "↪", Cyan
	case model.Cosmetic:
		return "≈", Blue
	case model.Drifted:
		return "▲", Yellow
	case model.Missing:
		return "✗", Red
	case model.New:
		return "●", Magenta
	case model.Error:
		return "!", Red
	}
	return "?", Gray
}

// StatusLabel renders an icon + status word.
func (p Painter) StatusLabel(s model.Status) string {
	icon, c := StatusIcon(s)
	return p.S(icon+" "+string(s), c)
}

// Percent formats a percentage with color thresholds.
func (p Painter) Percent(pct float64) string {
	c := Red
	switch {
	case pct >= 80:
		c = Green
	case pct >= 50:
		c = Yellow
	}
	return p.S(fmt.Sprintf("%5.1f%%", pct), c)
}

// Diff colors a unified diff.
func (p Painter) Diff(d string) string {
	if !p.On {
		return d
	}
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimSuffix(d, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
			b.WriteString(p.S(l, Bold))
		case strings.HasPrefix(l, "@@"):
			b.WriteString(p.S(l, Cyan))
		case strings.HasPrefix(l, "+"):
			b.WriteString(p.S(l, Green))
		case strings.HasPrefix(l, "-"):
			b.WriteString(p.S(l, Red))
		default:
			b.WriteString(l)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// ShortHash abbreviates a commit hash.
func ShortHash(h string) string {
	if h == "" {
		return "-"
	}
	if len(h) > 10 {
		return h[:10]
	}
	return h
}
