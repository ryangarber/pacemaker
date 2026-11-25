package clike_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/ryangarber/pacemaker/internal/lang"
	"github.com/ryangarber/pacemaker/internal/lang/clike"
	"github.com/ryangarber/pacemaker/internal/lang/javascript"
)

// symbols lexes and parses src as JavaScript, returning name → symbol and how
// many times each name was emitted.
func symbols(t *testing.T, src string) (map[string]lang.Symbol, map[string]int) {
	t.Helper()
	got, count := map[string]lang.Symbol{}, map[string]int{}
	for _, s := range clike.Symbols([]byte(src), javascript.Dialect) {
		got[s.Name] = s
		count[s.Name]++
	}
	return got, count
}

func names(m map[string]lang.Symbol) string {
	var out []string
	for n, s := range m {
		out = append(out, n+"("+s.Kind+")")
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// Members assigned inside any control-flow construct belong to the unit.
func TestUnitBodyControlFlow(t *testing.T) {
	src := `angular.module('m').controller('C', function ($scope) {
  var vm = this;
  do { vm.a = 1; } while (vm.busy);
  outer: for (var i = 0; i < 1; i++) { vm.b = function () {}; }
  for await (const x of xs) { vm.c = 1; }
  while (x) vm.d = 1;
  try { vm.e = 1; } catch { vm.f = 1; }
  with (o) { vm.g = 1; }
  { vm.h = 1; }
  switch (k) {
    case a ? 1 : 2: vm.i = 1; break;
    case 'x': { vm.j = 1; }
    default: vm.k = 1;
  }
  if (a) if (b) vm.l = 1; else vm.m = 1;
  xs.forEach(function () { vm.notMember = 1; });
  var later = () => { vm.notMemberEither = 1; };
});
`
	got, _ := symbols(t, src)
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m"} {
		if _, ok := got["C."+n]; !ok {
			t.Errorf("C.%s missing (all: %s)", n, names(got))
		}
	}
	for _, n := range []string{"C.notMember", "C.notMemberEither", "C.busy", "C.i"} {
		if s, ok := got[n]; ok && n != "C.i" {
			t.Errorf("%s must not be a symbol", n)
		} else if n == "C.i" && ok && s.Kind != "field" {
			t.Errorf("C.i is the vm.i member, not the loop variable: %+v", s)
		}
	}
	if s := got["C.b"]; s.Kind != "method" || s.Start != 4 || s.End != 4 {
		t.Errorf("C.b = %+v, want a method on line 4 without its label and loop header", s)
	}
}

// `var vm = this` declared inside a block still aliases this for the unit.
func TestAliasDeclaredInBlock(t *testing.T) {
	src := `function Ctl() {
  if (true) { var vm = this; }
  vm.x = 1;
  this.y = 2;
}
`
	got, _ := symbols(t, src)
	if _, ok := got["Ctl.x"]; !ok {
		t.Errorf("Ctl.x missing (all: %s)", names(got))
	}
	if _, ok := got["Ctl.y"]; !ok {
		t.Errorf("Ctl.y missing (all: %s)", names(got))
	}
}

func TestRegistrationByReference(t *testing.T) {
	for _, tc := range []struct {
		name, src, unit, kind string
		start, end            int
		members               []string
	}{
		{"bare identifier", "angular.module('m').controller('a', A);\nfunction A() { var vm = this; vm.x = 1; }\n",
			"A", "controller", 2, 2, []string{"A.x"}},
		{"annotated array", "angular.module('m').service('s', ['$http', svc]);\nfunction svc($http) { this.get = function () {}; }\n",
			"svc", "service", 2, 2, []string{"svc.get"}},
		{"array with trailing comma", "angular.module('m').factory('f', ['$q', fac,]);\nfunction fac() { return { run: run }; function run() {} }\n",
			"fac", "factory", 2, 2, []string{"fac.run"}},
		{"array with only the reference", "angular.module('m').directive('d', [dir]);\nfunction dir() { return { link: function () {} }; }\n",
			"dir", "directive", 2, 2, []string{"dir.link"}},
		{"declared before the registration", "function lowerCtrl() { var vm = this; vm.y = 1; }\nangular.module('m').controller('lowerCtrl', ['$scope', lowerCtrl]);\n",
			"lowerCtrl", "controller", 1, 1, []string{"lowerCtrl.y"}},
		{"inside an IIFE, chained", "(function () {\n  angular.module('m')\n    .controller('one', ['$scope', one])\n    .controller('two', two);\n  function one() { var vm = this; vm.a = 1; }\n  function two() { this.b = 1; }\n})();\n",
			"one", "controller", 5, 5, []string{"one.a", "two.b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, count := symbols(t, tc.src)
			s := got[tc.unit]
			if s.Kind != tc.kind || s.Start != tc.start || s.End != tc.end || !s.Exported {
				t.Errorf("%s = %+v, want exported %s spanning %d-%d (all: %s)", tc.unit, s, tc.kind, tc.start, tc.end, names(got))
			}
			for n, c := range count {
				if c > 1 {
					t.Errorf("%s emitted %d times", n, c)
				}
			}
			for _, m := range tc.members {
				if _, ok := got[m]; !ok {
					t.Errorf("%s missing (all: %s)", m, names(got))
				}
			}
		})
	}
}

// Shapes that must keep their existing behaviour.
func TestRegistrationNotByReference(t *testing.T) {
	// Registered here, declared in another file: nothing to attach to.
	got, _ := symbols(t, "angular.module('m').controller('X', ['$scope', X]);\n")
	if len(got) != 0 {
		t.Errorf("a reference to a function declared elsewhere must not produce symbols: %s", names(got))
	}
	// The array's last element is a call, not a reference: the registration is the unit.
	got, _ = symbols(t, "angular.module('m').factory('F', ['$q', make()]);\n")
	if s := got["F"]; s.Kind != "factory" || s.Start != 1 {
		t.Errorf("F = %+v, want the registration itself (all: %s)", s, names(got))
	}
	// A plain lowercase function that is not registered only contributes $scope members.
	got, _ = symbols(t, "function helper($scope) { var vm = this; vm.x = 1; $scope.y = 2; }\n")
	if _, ok := got["helper.x"]; ok {
		t.Errorf("helper.x must not be a symbol for an unregistered lowercase function: %s", names(got))
	}
	if _, ok := got["helper.$scope.y"]; !ok {
		t.Errorf("helper.$scope.y missing: %s", names(got))
	}
}

// A leading byte-order mark must not hide the file's IIFE.
func TestBOMBeforeIIFE(t *testing.T) {
	src := "\xEF\xBB\xBF(function () {\n  angular.module('m').controller('C', function () { this.x = 1; });\n})();\n"
	got, _ := symbols(t, src)
	if got["C"].Kind != "controller" || got["C.x"].Kind != "field" {
		t.Errorf("symbols behind a BOM were lost: %s", names(got))
	}
}

func findings(old, new string) map[string]lang.Finding {
	out := map[string]lang.Finding{}
	for _, f := range clike.Compare([]byte(old), []byte(new), javascript.Dialect) {
		out[f.Kind+" "+f.Symbol] = f
	}
	return out
}

func TestCompare(t *testing.T) {
	old := `import { a } from './a'
export function keep(x) { return x }
export function sig(a) { return a }
export function body() { return 1 }
export function hidden() {}
function shown() {}
export function gone() { cleanUp(); }
function oldName(a, b) { const t = a + b; return t * 2 + a - b }
export const kind = 1
`
	new := `import { b } from './b'
export function keep(x) { return x }
export function sig(a, b) { return a }
export function body() { return 2 }
function hidden() {}
export function shown() {}
function newName(a, b) { const t = a + b; return t * 2 + a - b }
export function kind() {}
export function fresh() { return [1, 2, 3].map(String) }
`
	got := findings(old, new)
	want := map[string]lang.Severity{
		"signature-changed sig": lang.SevBreaking,
		"body-changed body":     lang.SevMinor,
		"visibility hidden":     lang.SevBreaking,
		"visibility shown":      lang.SevMinor,
		"removed gone":          lang.SevBreaking,
		"renamed oldName":       lang.SevMajor,
		"kind-changed kind":     lang.SevBreaking,
		"added fresh":           lang.SevMajor,
		"import-added ":         lang.SevMinor,
		"import-removed ":       lang.SevMinor,
	}
	for k, sev := range want {
		f, ok := got[k]
		if !ok {
			t.Errorf("missing finding %q (got %v)", k, got)
			continue
		}
		if f.Severity != sev {
			t.Errorf("%q severity = %s, want %s", k, f.Severity, sev)
		}
	}
	if f := got["kind-changed kind"]; f.Message != "variable → function: `export const kind` → `export function kind()`" {
		t.Errorf("kind-changed message = %q", f.Message)
	}
	if f := got["renamed oldName"]; f.Message != "→ newName" {
		t.Errorf("rename message = %q", f.Message)
	}
	for k := range got {
		if strings.HasSuffix(k, " keep") {
			t.Errorf("unchanged symbol reported: %q", k)
		}
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected finding %q: %+v", k, got[k])
		}
	}
	if len(findings(old, old)) != 0 {
		t.Errorf("identical sources must produce no findings: %v", findings(old, old))
	}
	// Whitespace and comments alone are not a change.
	if f := findings("function f(a) { return a; }", "function f(a) {\n  return a; // same\n}\n"); len(f) != 0 {
		t.Errorf("reformatting produced findings: %v", f)
	}
}

// A visibility change is one finding, unless the parameters changed too.
func TestCompareVisibilityWithSignature(t *testing.T) {
	got := findings("export function f(a) {}", "function f(a, b) {}")
	if _, ok := got["visibility f"]; !ok {
		t.Errorf("visibility change not reported: %v", got)
	}
	if f, ok := got["signature-changed f"]; !ok || f.Severity != lang.SevMajor {
		t.Errorf("parameter change hidden by the visibility change: %v", got)
	}
	got = findings("function f(a) {}", "export function f(a) {}")
	if len(got) != 1 {
		t.Errorf("exporting alone must be one finding: %v", got)
	}
	// async is not visibility: it changes what callers get back.
	got = findings("function f(a) {}", "export async function f(a) {}")
	if _, ok := got["signature-changed f"]; !ok {
		t.Errorf("async added alongside export not reported: %v", got)
	}
}

func TestCompareIgnoresASISemicolons(t *testing.T) {
	if f := findings("function f(a) { return a; }", "function f(a) { return a }"); len(f) != 0 {
		t.Errorf("semicolon-only change produced findings: %v", f)
	}
}

// AngularJS members compare like any other symbol, including those found
// through a registration by reference.
func TestCompareRegisteredByReference(t *testing.T) {
	old := "angular.module('m').controller('c', ['$scope', c]);\nfunction c() { var vm = this; vm.save = function (a) {}; vm.old = 1; }\n"
	new := "angular.module('m').controller('c', ['$scope', c]);\nfunction c() { var vm = this; if (x) { vm.save = function (a, b) {}; } }\n"
	got := findings(old, new)
	if _, ok := got["signature-changed c.save"]; !ok {
		t.Errorf("signature change inside an if block not reported: %v", got)
	}
	if _, ok := got["removed c.old"]; !ok {
		t.Errorf("removed member not reported: %v", got)
	}
}

func TestParseImports(t *testing.T) {
	_, imps := clike.Parse([]byte("import a from 'a'\nexport { b } from './b'\nexport function c() {}\nconst d = require('d')\n"), javascript.Dialect)
	if len(imps) != 2 || !strings.HasPrefix(imps[0], "import a") || !strings.HasPrefix(imps[1], "export {") {
		t.Errorf("imports = %q, want the import and the re-export only", imps)
	}
}

// The parser is tolerant: unbalanced or truncated input must not panic and
// should still yield what is recognisable.
func TestParseTolerant(t *testing.T) {
	for _, src := range []string{
		"", "}", ")))", "function", "function f(", "class A { m() {", "angular.module('m').controller('x', [",
		"(function () {", "var vm = this; vm.", "switch (x) { case", "\xEF\xBB\xBF", "a\xEF\xBB",
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic on %q: %v", src, r)
				}
			}()
			clike.Symbols([]byte(src), javascript.Dialect)
			clike.Compare([]byte(src), []byte(src+"\n}"), javascript.Dialect)
		}()
	}
	got, _ := symbols(t, "export function ok() {}\nfunction broken( {\n")
	if _, found := got["ok"]; !found {
		t.Errorf("a declaration before broken code must survive: %s", names(got))
	}
}
