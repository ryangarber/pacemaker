// Package generic is the fallback language module: whitespace-insensitive
// normalisation and blank-line-aware coverage, nothing language specific.
package generic

import (
	"bytes"
	"strings"

	"github.com/ryangarber/pacemaker/internal/lang"
)

func init() { lang.SetFallback(Module{}) }

// Module is the fallback language.
type Module struct{}

func (Module) ID() string           { return "generic" }
func (Module) Extensions() []string { return nil }

// Normalize collapses runs of whitespace and drops blank lines.
func (Module) Normalize(src []byte) []byte {
	var out bytes.Buffer
	for _, line := range strings.Split(string(src), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		out.WriteString(strings.Join(f, " "))
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// SignificantLines marks non-blank lines.
func (Module) SignificantLines(src []byte) []bool {
	lines := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
	out := make([]bool, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimSpace(l) != ""
	}
	return out
}
