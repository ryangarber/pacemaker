// Package javascript is the JavaScript / TypeScript language module. It adds
// JS-aware lexing (template literals, regex literals, ASI), normalisation
// that ignores semicolons, trailing commas and quote style (prettier-proof),
// symbol resolution (classes, interfaces, enums, namespaces, functions, arrow
// consts, methods) and semantic diffs (export / signature changes, renames).
//
// Assignment-declared symbols are recognised too, so AngularJS 1.x / ES5 code
// resolves: `$scope.x = ...` and `this.x = ...` (or `vm.x` with `var vm =
// this`) inside controllers/services/constructors, `.controller('Name', fn)`
// style registrations (also with ['$dep', fn] annotations and inside IIFEs),
// factory `return { ... }` objects, `Foo.prototype.x = function` and
// `exports.x = ...`.
package javascript

import (
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

// Dialect is the clike configuration for JS/TS.
var Dialect = &clike.Dialect{
	Name:                 "javascript",
	Backtick:             true,
	RegexLiterals:        true,
	ASI:                  true,
	TopLevelNeedsKeyword: true,
	Containers: map[string]string{
		"class": "class", "interface": "interface", "enum": "enum", "namespace": "namespace", "module": "namespace",
	},
	DeclKeywords: set("const", "let", "var", "function", "type", "class", "interface", "enum", "namespace", "declare"),
	Modifiers: set("export", "default", "declare", "abstract", "async", "static", "public", "private",
		"protected", "readonly", "override", "accessor", "const", "let", "var", "function", "get", "set"),
	Imports: set("import", "export"),
	Skip: set("if", "for", "while", "switch", "return", "do", "else", "try", "throw", "new",
		"await", "yield", "delete", "typeof", "super", "this"),
	MemberAssign: true,
	ScopeRoots:   set("$scope", "$rootScope"),
	Registrations: map[string]string{
		"controller": "controller", "service": "service", "factory": "factory", "provider": "provider",
		"directive": "directive", "component": "component", "filter": "filter", "decorator": "decorator",
		"animation": "animation", "constant": "constant", "value": "value",
	},
	WeakRegistrations: set("filter", "constant", "value", "decorator", "animation", "provider", "service"),
	Exported: func(header []clike.Token, name string, inContainer bool) bool {
		if len(name) > 0 && name[0] == '#' {
			return false
		}
		for _, t := range header {
			if t.Is("(") || t.Is("=") {
				break
			}
			if t.Kind == clike.Ident && (t.Text == "private" || t.Text == "protected") {
				return false
			}
			if t.Kind == clike.Ident && t.Text == "export" {
				return true
			}
		}
		return inContainer
	},
}

// Module implements lang.Language and all optional capabilities.
type Module struct{}

func (Module) ID() string { return "javascript" }
func (Module) Extensions() []string {
	return []string{".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts"}
}
func (Module) Normalize(src []byte) []byte        { return clike.Normalize(src, Dialect) }
func (Module) SignificantLines(src []byte) []bool { return clike.SignificantLines(src, Dialect) }
func (Module) Symbols(src []byte) []lang.Symbol   { return clike.Symbols(src, Dialect) }
func (Module) Compare(a, b []byte) []lang.Finding { return clike.Compare(a, b, Dialect) }
