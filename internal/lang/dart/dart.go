// Package dart is the Dart language module. It adds Dart-aware
// normalisation (comments, formatter trailing commas, quote style), symbol
// resolution (classes, mixins, extensions, enums, constructors, getters...)
// and semantic diffs (signature / visibility changes, renamed members).
package dart

import (
	"strings"

	"github.com/ryangarber/pacemaker/internal/lang"
	"github.com/ryangarber/pacemaker/internal/lang/clike"
)

func init() { lang.Register(Module{}) }

func set(words ...string) map[string]bool {
	m := map[string]bool{}
	for _, w := range words {
		m[w] = true
	}
	return m
}

// Dialect is the clike configuration for Dart.
var Dialect = &clike.Dialect{
	Name:                "dart",
	TripleQuotes:        true,
	RawStrings:          true,
	NestedComments:      true,
	DollarInterpolation: true,
	TypeFirst:           true,
	Containers: map[string]string{
		"class": "class", "mixin": "mixin", "extension": "extension", "enum": "enum",
	},
	DeclKeywords: set("var", "final", "const", "late", "typedef"),
	Modifiers: set("abstract", "base", "interface", "final", "sealed", "static", "const", "late",
		"external", "covariant", "factory", "async", "sync", "required", "var", "macro", "augment"),
	Imports: set("import", "export", "part", "library"),
	Skip:    set("if", "for", "while", "switch", "return", "do", "else", "try", "throw", "assert"),
	Exported: func(_ []clike.Token, name string, _ bool) bool {
		for _, seg := range strings.Split(name, ".") {
			if strings.HasPrefix(seg, "_") {
				return false
			}
		}
		return true
	},
}

// Module implements lang.Language and all optional capabilities.
type Module struct{}

func (Module) ID() string                         { return "dart" }
func (Module) Extensions() []string               { return []string{".dart"} }
func (Module) Normalize(src []byte) []byte        { return clike.Normalize(src, Dialect) }
func (Module) SignificantLines(src []byte) []bool { return clike.SignificantLines(src, Dialect) }
func (Module) Symbols(src []byte) []lang.Symbol   { return clike.Symbols(src, Dialect) }
func (Module) Compare(a, b []byte) []lang.Finding { return clike.Compare(a, b, Dialect) }
