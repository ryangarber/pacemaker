// Package ref parses and formats references into a target repository.
//
// Grammar:
//
//	ref      = [ target ":" ] path [ "#" fragment ]
//	fragment = "L" start [ "-" ["L"] end ]   ; 1-based inclusive line range
//	         | symbol                         ; e.g. AuthService.login
//
// Examples:
//
//	lib/src/auth.dart                      whole file in the default target
//	upstream:lib/src/auth.dart#L10-L42     lines 10..42 of target "upstream"
//	src/client.ts#HttpClient.request       a symbol, resolved by a language module
package ref

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Range is a 1-based inclusive line range. The zero value means "unset".
type Range struct {
	Start int `yaml:"start" json:"start"`
	End   int `yaml:"end" json:"end"`
}

func (r Range) IsZero() bool { return r.Start == 0 && r.End == 0 }
func (r Range) Len() int {
	if r.IsZero() || r.End < r.Start {
		return 0
	}
	return r.End - r.Start + 1
}
func (r Range) String() string {
	if r.IsZero() {
		return ""
	}
	if r.Start == r.End {
		return fmt.Sprintf("L%d", r.Start)
	}
	return fmt.Sprintf("L%d-L%d", r.Start, r.End)
}

// Overlaps reports whether two ranges share at least one line.
func (r Range) Overlaps(o Range) bool {
	return !r.IsZero() && !o.IsZero() && r.Start <= o.End && o.Start <= r.End
}

// Ref points at a whole file, a line range, or a symbol in a target.
type Ref struct {
	Target string
	Path   string
	Lines  Range
	Symbol string
}

var (
	targetRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	linesRe  = regexp.MustCompile(`^L(\d+)(?:-L?(\d+))?$`)
)

// Parse parses a reference string.
func Parse(s string) (Ref, error) {
	var r Ref
	s = strings.TrimSpace(s)
	if s == "" {
		return r, fmt.Errorf("empty reference")
	}
	if i := strings.Index(s, ":"); i > 0 && targetRe.MatchString(s[:i]) && !strings.HasPrefix(s[i:], "://") {
		r.Target, s = s[:i], s[i+1:]
	}
	if i := strings.LastIndex(s, "#"); i >= 0 {
		frag := s[i+1:]
		s = s[:i]
		if m := linesRe.FindStringSubmatch(frag); m != nil {
			r.Lines.Start, _ = strconv.Atoi(m[1])
			r.Lines.End = r.Lines.Start
			if m[2] != "" {
				r.Lines.End, _ = strconv.Atoi(m[2])
			}
			if r.Lines.Start < 1 || r.Lines.End < r.Lines.Start {
				return r, fmt.Errorf("invalid line range %q", frag)
			}
		} else if frag != "" {
			r.Symbol = frag
		}
	}
	r.Path = strings.TrimPrefix(strings.TrimPrefix(s, "./"), "/")
	if r.Path == "" {
		return r, fmt.Errorf("reference has no path")
	}
	return r, nil
}

// String formats the reference in canonical form.
func (r Ref) String() string {
	var b strings.Builder
	if r.Target != "" {
		b.WriteString(r.Target)
		b.WriteByte(':')
	}
	b.WriteString(r.Path)
	switch {
	case r.Symbol != "":
		b.WriteString("#" + r.Symbol)
	case !r.Lines.IsZero():
		b.WriteString("#" + r.Lines.String())
	}
	return b.String()
}

// Kind describes what the reference addresses.
func (r Ref) Kind() string {
	switch {
	case r.Symbol != "":
		return "symbol"
	case !r.Lines.IsZero():
		return "lines"
	default:
		return "file"
	}
}

// WithTarget returns a copy with the target filled in when empty.
func (r Ref) WithTarget(def string) Ref {
	if r.Target == "" {
		r.Target = def
	}
	return r
}
