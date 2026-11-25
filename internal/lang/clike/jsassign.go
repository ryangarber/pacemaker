package clike

import "strings"

// This file implements Dialect.MemberAssign: JavaScript symbols that are
// declared by assignment or registration instead of by a keyword, as found in
// AngularJS 1.x, ES5 and CommonJS code:
//
//	(function () {                                  // IIFE: parsed as top level
//	  angular.module('app')
//	    .controller('LoginCtrl', ['$scope', function ($scope) {   // LoginCtrl
//	      var vm = this;
//	      $scope.user = null;                        // LoginCtrl.$scope.user
//	      $scope.login = function () { ... };        // LoginCtrl.$scope.login
//	      vm.logout = logout;                        // (exports logout below)
//	      function logout() { ... }                  // LoginCtrl.logout
//	    }])
//	    .factory('Auth', function ($http) {          // Auth
//	      return { check: check, ping: function () {} };  // Auth.check, Auth.ping
//	      function check() { ... }
//	    });
//	  Store.prototype.save = function () { ... };    // Store.save
//	  exports.helper = function () { ... };          // helper
//	})();

// span is an inclusive token range.
type span struct{ start, end int }

// split splits the contents of the bracket group opened at ts[open] on
// top-level commas.
func (p *parser) split(open int) []span {
	close := p.match[open]
	if close < 0 {
		return nil
	}
	var out []span
	i := open + 1
	for i < close {
		j := i
		for j < close && !p.ts[j].Is(",") {
			if t := p.ts[j]; t.Is("(") || t.Is("[") || t.Is("{") {
				j = p.skipGroup(j, close)
				continue
			}
			j++
		}
		if j > i {
			out = append(out, span{i, j - 1})
		}
		i = j + 1
	}
	return out
}

// funcLit recognises a function expression starting at ts[i] (bounded by
// hi): `function name?(...) {`, `async (...) => {`, `x => expr`. It returns
// the index of the body's "{" (-1 for expression-bodied arrows) and whether
// ts[i] starts a function at all.
func (p *parser) funcLit(i, hi int) (int, bool) {
	if i < hi && p.word(i) == "async" && i+1 < hi && (p.word(i+1) == "function" || p.ts[i+1].Is("(") || p.ts[i+1].Kind == Ident) {
		i++
	}
	if i >= hi {
		return -1, false
	}
	brace := func(j int) (int, bool) {
		if j < hi && p.ts[j].Is("{") {
			return j, true
		}
		return -1, true
	}
	switch {
	case p.word(i) == "function":
		j := i + 1
		for j < hi && !p.ts[j].Is("(") { // *, name, <generics>
			if p.ts[j].Is("{") || p.ts[j].Is(";") {
				return -1, true
			}
			j++
		}
		j = p.skipGroup(j, hi)
		for j < hi && !p.ts[j].Is("{") { // ": ReturnType"
			if p.ts[j].Is(";") || p.ts[j].Is(",") || p.ts[j].Is("=") {
				return -1, true
			}
			j = p.skipGroup0(j, hi)
		}
		return brace(j)
	case p.ts[i].Is("("):
		j := p.skipGroup(i, hi)
		if j < hi && p.ts[j].Is(":") { // (x): T => ...
			for j < hi && !p.ts[j].Is("=>") {
				if t := p.ts[j]; t.Is(";") || t.Is(",") || t.Is("=") || t.Is(")") {
					return -1, false
				}
				j = p.skipGroup0(j, hi)
			}
		}
		if j < hi && p.ts[j].Is("=>") {
			return brace(j + 1)
		}
	case p.ts[i].Kind == Ident && i+1 < hi && p.ts[i+1].Is("=>"):
		return brace(i + 2)
	}
	return -1, false
}

// skipGroup0 advances past ts[j], skipping a whole bracket group.
func (p *parser) skipGroup0(j, hi int) int {
	if t := p.ts[j]; t.Is("(") || t.Is("[") || t.Is("{") {
		return p.skipGroup(j, hi)
	}
	return j + 1
}

// factoryBody finds the body "{" of a function argument, including the
// AngularJS inline-annotation form ['$scope', 'dep', function ($scope, dep) {}].
func (p *parser) factoryBody(a span) int {
	if body, ok := p.funcLit(a.start, a.end+1); ok {
		return body
	}
	if p.ts[a.start].Is("[") && p.match[a.start] == a.end {
		if el := p.split(a.start); len(el) > 0 {
			body, _ := p.funcLit(el[len(el)-1].start, el[len(el)-1].end+1)
			return body
		}
	}
	return -1
}

func unquoteTok(s string) string { return strings.Trim(s, "'\"`") }

// jsStatement handles top-level statements that declare symbols without a
// declaration keyword. It returns true when the statement was consumed.
func (p *parser) jsStatement(start, k, end int, prefix string) bool {
	if body := p.iifeBody(k, end); body >= 0 {
		p.scope(body+1, p.skipGroup(body, len(p.ts))-1, prefix, "", "")
		return true
	}
	if p.registrations(start, k, end, prefix) {
		// `var app = angular.module(...).controller(...)` still declares app.
		return !p.d.DeclKeywords[p.word(k)] && !p.d.Imports[p.word(k)]
	}
	return p.topMemberAssign(start, k, end, prefix)
}

// iifeBody returns the body "{" of an immediately-invoked function
// expression statement: (function () {...})(), (function () {...}()),
// !function () {...}(), (() => {...})().
func (p *parser) iifeBody(k, end int) int {
	i := k
	for i <= end && (p.ts[i].Is("!") || p.ts[i].Is("+") || p.ts[i].Is("-") || p.ts[i].Is("~") || p.word(i) == "void") {
		i++
	}
	if i > end {
		return -1
	}
	if p.ts[i].Is("(") {
		close := p.match[i]
		if close < 0 {
			return -1
		}
		if body, ok := p.funcLit(i+1, close); ok && body >= 0 {
			// The literal must be the whole group, then called: (fn)() / (fn())
			after := p.skipGroup(body, close)
			if after == close && close+1 <= end && p.ts[close+1].Is("(") {
				return body
			}
			if after < close && p.ts[after].Is("(") && p.match[after] == close-1 {
				return body
			}
		}
		return -1
	}
	if i > k {
		if body, ok := p.funcLit(i, end+1); ok && body >= 0 {
			return body
		}
	}
	return -1
}

// registrations emits AngularJS-style units registered in a call chain:
// angular.module('app').controller('A', fn).service('B', ['$http', fn]).
func (p *parser) registrations(start, k, end int, prefix string) bool {
	type reg struct {
		dot, open int
		kind      string
	}
	var regs []reg
	module := false
	for j := k; j <= end; {
		t := p.ts[j]
		if t.Is("(") || t.Is("[") || t.Is("{") {
			if t.Is("(") && j-1 >= k {
				if p.word(j-1) == "module" {
					module = true
				}
				if kind := p.d.Registrations[p.word(j-1)]; kind != "" && j-2 >= k && p.ts[j-2].Is(".") &&
					p.match[j] > j && p.match[j] <= end {
					regs = append(regs, reg{j - 2, j, kind})
				}
			}
			j = p.skipGroup(j, end+1)
			continue
		}
		j++
	}
	found := false
	for n, r := range regs {
		args := p.split(r.open)
		if len(args) < 2 || args[0].start != args[0].end || p.ts[args[0].start].Kind != String {
			continue
		}
		name := unquoteTok(p.ts[args[0].start].Text)
		fn := args[1]
		body := p.factoryBody(fn)
		obj := p.ts[fn.start].Is("{") && p.match[fn.start] == fn.end
		if p.d.WeakRegistrations[p.word(r.dot+1)] && !module && body < 0 {
			continue
		}
		if name == "" || (body < 0 && !obj && fn.start == fn.end && p.ts[fn.start].Kind == Ident) {
			continue // .controller('X', X): the function declaration is the unit
		}
		declStart, declEnd := r.dot, p.match[r.open]
		if n == 0 {
			declStart = start // include `angular.module(...)` and doc comments
		}
		if n == len(regs)-1 {
			declEnd = end
		}
		sigEnd := declEnd + 1
		if body >= 0 {
			sigEnd = body
		} else if obj {
			sigEnd = fn.start
		}
		qual := join(prefix, name)
		idx := p.emit(declStart, declEnd, sigEnd, qual, r.kind, true, false)
		before := len(p.decls)
		if body >= 0 {
			p.unitBody(body+1, p.skipGroup(body, len(p.ts))-1, qual, true)
		} else if obj {
			p.objectProps(fn.start, qual, nil, nil)
		}
		p.decls[idx].Container = len(p.decls) > before
		found = true
	}
	return found
}

// memberPath parses `a.b.c =` at ts[k:], returning the dotted path and the
// index of "=".
func (p *parser) memberPath(k, end int) ([]string, int) {
	if k > end || p.ts[k].Kind != Ident {
		return nil, -1
	}
	path := []string{p.ts[k].Text}
	j := k + 1
	for j+1 <= end && p.ts[j].Is(".") && p.ts[j+1].Kind == Ident {
		path = append(path, p.ts[j+1].Text)
		j += 2
	}
	if j <= end && p.ts[j].Is("=") {
		return path, j
	}
	return nil, -1
}

// topMemberAssign handles Foo.prototype.bar = function, Foo.bar = function,
// exports.x = ..., module.exports.x = ..., and $scope.x / this.x at top level.
func (p *parser) topMemberAssign(start, k, end int, prefix string) bool {
	path, eq := p.memberPath(k, end)
	if len(path) < 2 {
		return false
	}
	body, isFn := p.funcLit(eq+1, end+1)
	var name []string
	switch root := path[0]; {
	case root == "module" && path[1] == "exports":
		name = path[2:]
	case root == "exports" || root == "this":
		name = path[1:]
	case p.d.ScopeRoots[root]:
		name = path
	case isFn:
		for _, s := range path {
			if s != "prototype" {
				name = append(name, s)
			}
		}
	}
	if len(name) == 0 {
		return false
	}
	kind := "variable"
	switch {
	case isFn && len(name) > 1:
		kind = "method"
	case isFn:
		kind = "function"
	case len(name) > 1:
		kind = "field"
	}
	sigEnd := eq
	if body >= 0 {
		sigEnd = body
	}
	last := name[len(name)-1]
	p.emit(start, end, sigEnd, join(prefix, strings.Join(name, ".")), kind, !strings.HasPrefix(last, "_"), false)
	return true
}

// functionBody parses the body of the top-level function declared as
// p.decls[idx]. Registered units (unitKind != "") and Capitalized,
// constructor-style functions get the full treatment (this.x members, inner
// functions, returned object); other functions only contribute members of
// ScopeRoots such as $scope.x (controllers registered in another file).
func (p *parser) functionBody(idx, body int, qual, name, unitKind string) {
	before := len(p.decls)
	full := unitKind != "" || (name != "" && name[0] >= 'A' && name[0] <= 'Z')
	p.unitBody(body+1, p.skipGroup(body, len(p.ts))-1, qual, full)
	if unitKind != "" {
		p.decls[idx].Kind = unitKind
		p.decls[idx].Exported = true // registered with the framework
	}
	p.decls[idx].Container = len(p.decls) > before
}

// unitBody collects symbols in a function body ts[lo:hi]: members assigned
// on ScopeRoots such as $scope and, with full (AngularJS units and
// constructors), on this or an alias like `var vm = this`, plus inner
// function declarations and the properties of a returned object literal.
// Inner functions are exported when exposed via vm.x = x or return { x: x }.
func (p *parser) unitBody(lo, hi int, prefix string, full bool) {
	var stmts []span
	bodies := map[int]int{}
	p.statements(lo, hi, false, func(start, end, body int) {
		stmts = append(stmts, span{start, end})
		bodies[start] = body
	})
	aliases := map[string]bool{"this": full}
	inner := map[string]int{} // inner function name → decl index
	declared := map[string]bool{}
	exposed := map[string]bool{}
	for _, st := range stmts {
		k := st.start
		if w := p.word(k); w == "var" || w == "let" || w == "const" {
			for _, d := range p.declarators(k+1, st.end) {
				if full && d.end == d.start+2 && p.ts[d.start+1].Is("=") && p.word(d.start+2) == "this" {
					aliases[p.ts[d.start].Text] = true
				}
				if _, isFn := p.funcLit(d.start+2, d.end+1); full && isFn && p.ts[d.start+1].Is("=") {
					declared[p.ts[d.start].Text] = true
				}
			}
		}
		if n := p.innerFuncName(k, st.end); full && n != "" {
			declared[n] = true
		}
	}
	for _, st := range stmts {
		k, end := st.start, st.end
		w := p.word(k)
		switch {
		case w == "var" || w == "let" || w == "const":
			if !full {
				continue
			}
			for _, d := range p.declarators(k+1, end) {
				if d.start+1 > d.end || !p.ts[d.start+1].Is("=") {
					continue
				}
				if body, isFn := p.funcLit(d.start+2, d.end+1); isFn {
					from, to := d.start, d.end
					if len(p.declarators(k+1, end)) == 1 {
						from, to = k, end // include the keyword and ";"
					}
					sig := d.start + 1
					if body >= 0 {
						sig = body
					}
					name := p.ts[d.start].Text
					inner[name] = p.emit(from, to, sig, join(prefix, name), "function", false, false)
				}
			}
			continue
		case full && p.innerFuncName(k, end) != "":
			name := p.innerFuncName(k, end)
			sig := bodies[k]
			if sig < 0 {
				sig = end + 1
			}
			inner[name] = p.emit(k, end, sig, join(prefix, name), "function", false, false)
			continue
		case full && w == "return" && k+1 <= end && p.ts[k+1].Is("{"):
			p.objectProps(k+1, prefix, declared, exposed)
			continue
		}
		path, eq := p.memberPath(k, end)
		if len(path) < 2 {
			continue
		}
		var name []string
		switch {
		case aliases[path[0]]:
			name = path[1:]
		case p.d.ScopeRoots[path[0]]:
			name = path
		default:
			continue
		}
		// vm.logout = logout → the inner function logout is the symbol.
		if eq+1 <= end && p.ts[eq+1].Kind == Ident && (eq+1 == end || (eq+2 == end && p.ts[end].Is(";"))) {
			if ref := p.ts[eq+1].Text; declared[ref] {
				exposed[ref] = true
				if aliases[path[0]] && len(name) == 1 && name[0] == ref {
					continue
				}
			}
		}
		body, isFn := p.funcLit(eq+1, end+1)
		kind, sig := "field", eq
		if isFn {
			kind = "method"
			if body >= 0 {
				sig = body
			}
		}
		p.emit(k, end, sig, join(prefix, strings.Join(name, ".")), kind, true, false)
	}
	for name, i := range inner {
		if exposed[name] {
			p.decls[i].Exported = true
		}
	}
}

// innerFuncName returns the name of a `function name(...) {}` (or async)
// statement at ts[k:end], or "".
func (p *parser) innerFuncName(k, end int) string {
	if p.word(k) == "async" {
		k++
	}
	if k+1 <= end && p.word(k) == "function" {
		j := k + 1
		if j <= end && p.ts[j].Is("*") {
			j++
		}
		if n := p.word(j); n != "" {
			return n
		}
	}
	return ""
}

// declarators splits `a = 1, b = function () {}` (after var/let/const) into
// individual declarators, each starting at its name.
func (p *parser) declarators(lo, end int) []span {
	var out []span
	i := lo
	for i <= end {
		j := i
		for j <= end && !p.ts[j].Is(",") && !p.ts[j].Is(";") {
			j = p.skipGroup0(j, end+1)
		}
		if j > i && p.ts[i].Kind == Ident {
			out = append(out, span{i, j - 1})
		}
		if j <= end && p.ts[j].Is(";") {
			break
		}
		i = j + 1
	}
	return out
}

// objectProps emits the properties of the object literal opened at ts[open]
// as members of prefix: { a: 1, b: function () {}, c() {}, d }. Properties
// that just expose a declared inner function (`login: login`, `login`) mark
// it exposed instead. A `controller` function property is parsed as a unit.
func (p *parser) objectProps(open int, prefix string, declared, exposed map[string]bool) {
	for _, pr := range p.split(open) {
		s, e := pr.start, pr.end
		for s < e && (p.word(s) == "async" || p.word(s) == "get" || p.word(s) == "set") && p.ts[s+1].Kind == Ident {
			s++
		}
		if t := p.ts[s]; t.Kind != Ident && t.Kind != String {
			continue // ...spread, [computed]
		}
		key := unquoteTok(p.ts[s].Text)
		if key == "" {
			continue
		}
		if s == e || (s+2 == e && p.ts[s+1].Is(":") && p.word(e) == key) {
			if declared[key] { // { login } or { login: login }
				exposed[key] = true
				continue
			}
		}
		kind, sig, body := "field", s+1, -1
		switch {
		case s+1 <= e && p.ts[s+1].Is(":"):
			b, isFn := p.funcLit(s+2, e+1)
			if !isFn && p.ts[s+2].Is("[") {
				b = p.factoryBody(span{s + 2, e})
				isFn = b >= 0
			}
			if isFn {
				kind, body = "method", b
			}
		case s+1 <= e && p.ts[s+1].Is("("):
			kind = "method"
			if j := p.skipGroup(s+1, e+1); j <= e && p.ts[j].Is("{") {
				body = j
			}
		}
		if body >= 0 {
			sig = body
		}
		qual := join(prefix, key)
		idx := p.emit(pr.start, e, sig, qual, kind, true, false)
		if key == "controller" && body >= 0 {
			before := len(p.decls)
			p.unitBody(body+1, p.skipGroup(body, len(p.ts))-1, qual, true)
			p.decls[idx].Container = len(p.decls) > before
		}
	}
}
