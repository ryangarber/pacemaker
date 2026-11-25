package clike

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"github.com/ryangarber/pacemaker/internal/diff"
	"github.com/ryangarber/pacemaker/internal/lang"
)

// Decl is a recovered declaration with its token slice (for comparisons).
type Decl struct {
	lang.Symbol
	Tokens []string
	Header []string
}

type parser struct {
	src   []byte
	d     *Dialect
	all   []Token // including comments
	ts    []Token // code tokens only
	idx   []int   // ts[i] -> index into all
	match []int   // bracket matches over ts (-1 when unmatched)
	decls []Decl
	imps  []string
}

func newParser(src []byte, d *Dialect) *parser {
	p := &parser{src: src, d: d, all: Lex(src, d)}
	for i, t := range p.all {
		if t.Kind != Comment {
			p.ts = append(p.ts, t)
			p.idx = append(p.idx, i)
		}
	}
	p.match = make([]int, len(p.ts))
	var stack []int
	pairs := map[string]string{")": "(", "]": "[", "}": "{"}
	for i, t := range p.ts {
		p.match[i] = -1
		if t.Kind != Punct {
			continue
		}
		switch t.Text {
		case "(", "[", "{":
			stack = append(stack, i)
		case ")", "]", "}":
			// Pop to the nearest matching opener (tolerates garbage).
			for k := len(stack) - 1; k >= 0; k-- {
				if p.ts[stack[k]].Text == pairs[t.Text] {
					p.match[stack[k]] = i
					p.match[i] = stack[k]
					stack = stack[:k]
					break
				}
			}
		}
	}
	return p
}

// Parse returns declarations and import statements found in src.
func Parse(src []byte, d *Dialect) ([]Decl, []string) {
	p := newParser(src, d)
	p.scope(0, len(p.ts), "", "", "")
	sort.SliceStable(p.decls, func(i, j int) bool { return p.decls[i].Start < p.decls[j].Start })
	return p.decls, p.imps
}

func (p *parser) skipGroup(j, hi int) int {
	if m := p.match[j]; m > j && m < hi {
		return m + 1
	}
	return hi
}

var canEnd = map[string]bool{")": true, "]": true, "}": true, "++": true, "--": true}
var continues = map[string]bool{
	".": true, ",": true, "?": true, ":": true, "=": true, "=>": true, "+": true, "-": true, "*": true,
	"/": true, "%": true, "&&": true, "||": true, "??": true, "(": true, "[": true, "{": true, "<": true,
	">": true, "|": true, "&": true, "?.": true, "==": true, "===": true, "!=": true, "!==": true,
	"extends": true, "implements": true, "as": true, "satisfies": true, "instanceof": true, "in": true,
}

// asiEnds reports whether, under ASI rules, the statement ends after ts[j].
func (p *parser) asiEnds(start, j, hi int) bool {
	if !p.d.ASI || j+1 >= hi || !p.ts[j+1].NL {
		return false
	}
	t, n := p.ts[j], p.ts[j+1]
	if !(t.Kind != Punct || canEnd[t.Text]) || continues[n.Text] {
		return false
	}
	return !p.decoratorsOnly(start, j+1)
}

// decoratorsOnly reports whether ts[lo:hi] is a run of @decorators.
func (p *parser) decoratorsOnly(lo, hi int) bool {
	if lo >= hi || !p.ts[lo].Is("@") {
		return false
	}
	j := lo
	for j < hi {
		if !p.ts[j].Is("@") {
			return false
		}
		j++
		for j < hi && (p.ts[j].Kind == Ident || p.ts[j].Is(".")) {
			j++
		}
		if j < hi && p.ts[j].Is("(") {
			j = p.skipGroup(j, hi)
		}
	}
	return true
}

// scope parses declarations in ts[lo:hi].
func (p *parser) scope(lo, hi int, prefix, container, containerKind string) {
	i := lo
	for i < hi {
		t := p.ts[i]
		if t.Kind == Punct && (t.Text == ";" || t.Text == "," || t.Text == "}" || t.Text == ")" || t.Text == "]") {
			i++
			continue
		}
		start := i
		j := i
		sawAssign, sawArrow, sawParen := false, false, false
		body := -1 // index of "{" opening a declaration body
		end := hi - 1
	scan:
		for j < hi {
			tk := p.ts[j]
			if tk.Kind == Punct {
				switch tk.Text {
				case "(", "[":
					if tk.Text == "(" {
						sawParen = true
					}
					j = p.skipGroup(j, hi)
					if p.asiEnds(start, j-1, hi) {
						end = j - 1
						break scan
					}
					continue
				case ";":
					end = j
					break scan
				case ",":
					if containerKind == "enum" {
						end = j - 1
						break scan
					}
				case "=":
					if !sawParen {
						sawAssign = true
					}
				case "=>":
					sawArrow = true
				case "{":
					if sawAssign || sawArrow {
						j = p.skipGroup(j, hi)
						if p.asiEnds(start, j-1, hi) {
							end = j - 1
							break scan
						}
						continue
					}
					body = j
					end = p.skipGroup(j, hi) - 1
					break scan
				case "}":
					end = j - 1
					break scan
				}
			}
			if p.asiEnds(start, j, hi) {
				end = j
				break
			}
			j++
		}
		if end < start {
			end = start
		}
		p.classify(start, end, body, prefix, container, containerKind)
		i = end + 1
	}
}

func (p *parser) word(i int) string {
	if i >= 0 && i < len(p.ts) && p.ts[i].Kind == Ident {
		return p.ts[i].Text
	}
	return ""
}

func (p *parser) classify(start, end, body int, prefix, container, containerKind string) {
	headerEnd := end + 1
	if body >= 0 {
		headerEnd = body
	}
	// Skip decorators / annotations.
	k := start
	for k < headerEnd && p.ts[k].Is("@") {
		k++
		for k < headerEnd && (p.ts[k].Kind == Ident || p.ts[k].Is(".")) {
			k++
		}
		if k < headerEnd && p.ts[k].Is("(") {
			k = p.skipGroup(k, headerEnd)
		}
	}
	first := p.word(k)
	topLevel := container == ""
	if topLevel && p.d.Imports[first] && !(first == "export" && p.exportsDecl(k, headerEnd)) {
		var parts []string
		for _, t := range p.ts[k : end+1] {
			parts = append(parts, t.Text)
		}
		p.imps = append(p.imps, strings.Join(parts, " "))
		return
	}
	if topLevel && p.d.Skip[first] { // class bodies only contain declarations
		return
	}

	// Collect top-level header token indexes (bracket groups collapsed).
	var top []int
	for j := k; j < headerEnd; {
		top = append(top, j)
		if p.ts[j].Is("(") || p.ts[j].Is("[") || p.ts[j].Is("{") {
			j = p.skipGroup(j, headerEnd)
			continue
		}
		j++
	}
	if len(top) == 0 {
		return
	}
	if container == "" && p.d.TopLevelNeedsKeyword {
		ok := false
		for _, j := range top {
			if p.d.DeclKeywords[p.word(j)] || p.d.Containers[p.word(j)] != "" {
				ok = true
				break
			}
		}
		if !ok {
			return
		}
	}

	var (
		name, kind string
		isCont     bool
		assignAt   = -1
		parenAt    = -1
		arrowAt    = -1
	)
	for n, j := range top {
		t := p.ts[j]
		switch {
		case t.Is("=") && assignAt < 0 && parenAt < 0:
			assignAt = n
		case t.Is("(") && parenAt < 0 && assignAt < 0 && arrowAt < 0:
			parenAt = n
		case t.Is("=>") && arrowAt < 0:
			arrowAt = n
		}
	}

	// Containers (class, interface, enum, mixin, extension, namespace...).
	for n, j := range top {
		if assignAt >= 0 && n > assignAt {
			break
		}
		if ck := p.d.Containers[p.word(j)]; ck != "" {
			if n+1 < len(top) && p.ts[top[n+1]].Is(".") {
				break // e.g. module.exports
			}
			if p.word(j) == "extension" && n+1 < len(top) && p.word(top[n+1]) == "on" && n+2 < len(top) {
				name = "on " + p.ts[top[n+2]].Text
			} else if n+1 < len(top) && (p.ts[top[n+1]].Kind == Ident || p.ts[top[n+1]].Kind == String) {
				name = strings.Trim(p.ts[top[n+1]].Text, `'"`)
				if p.d.Modifiers[name] || name == "extends" || name == "implements" {
					name = "default"
				}
			} else {
				name = "default"
			}
			kind, isCont = ck, true
			break
		}
	}

	if !isCont {
		w := p.wordAt(top, 0)
		switch {
		case w == "typedef" || w == "type":
			kind = "type"
			if assignAt > 1 || parenAt < 0 {
				name = p.nameAfter(top, 0)
			} else {
				name = p.nameBefore(top, parenAt)
			}
		case assignAt >= 0:
			name = p.varName(top, assignAt)
			kind = "variable"
			if containerKind == "enum" {
				kind = "value"
			} else if container != "" {
				kind = "field"
			}
			rhs := p.ts[top[assignAt]:p.endTok(end)]
			for _, t := range rhs {
				if t.Is("=>") || (t.Kind == Ident && t.Text == "function") {
					kind = "function"
					if container != "" {
						kind = "method"
					}
					break
				}
			}
		case parenAt >= 0:
			name = p.nameBefore(top, parenAt)
			if name == "" && parenAt >= 2 && p.word(top[parenAt-2]) == "operator" {
				name = "operator " + p.ts[top[parenAt-1]].Text
			}
			name = strings.TrimPrefix(name, container+".")
			kind = "function"
			if container != "" {
				kind = "method"
				base := name[strings.LastIndex(name, ".")+1:]
				if name == container || base == "constructor" || p.isNamedCtor(top, parenAt, container) {
					kind = "constructor"
				}
			}
			for n, j := range top[:parenAt] {
				if n+1 == parenAt {
					break // the name itself, e.g. a method called "get"
				}
				switch p.word(j) {
				case "get":
					kind = "getter"
				case "set":
					kind = "setter"
				case "factory":
					kind = "constructor"
				}
			}
		default:
			// get/set accessors without parens (Dart), or plain fields.
			for n, j := range top {
				if (p.word(j) == "get" || p.word(j) == "set") && n+1 < len(top) && p.ts[top[n+1]].Kind == Ident {
					name, kind = p.ts[top[n+1]].Text, "getter"
					break
				}
			}
			if name == "" && (container != "" || p.d.TypeFirst || p.d.DeclKeywords[w]) {
				name = p.varName(top, len(top))
				kind = "variable"
				if containerKind == "enum" {
					kind = "value"
				} else if container != "" {
					kind = "field"
				}
			}
		}
	}
	if name == "" || name == "function" || (container == "" && p.d.Skip[name]) {
		return
	}
	qual := name
	if prefix != "" {
		qual = prefix + "." + name
	}

	// Line span, including directly preceding comments (doc comments).
	firstAll := p.idx[start]
	startLine := p.ts[start].Line
	for a := firstAll - 1; a >= 0 && p.all[a].Kind == Comment && p.all[a].EndLine >= startLine-1; a-- {
		startLine = p.all[a].Line
	}
	endTok := end
	if endTok >= len(p.ts) {
		endTok = len(p.ts) - 1
	}
	var toks, header []string
	for j := start; j <= endTok; j++ {
		toks = append(toks, p.ts[j].Text)
	}
	sigEnd := headerEnd
	if arrowAt >= 0 && !isCont {
		sigEnd = top[arrowAt]
	} else if assignAt >= 0 && kind != "type" {
		sigEnd = top[assignAt]
	}
	for j := start; j < sigEnd && j <= endTok; j++ {
		if j == endTok && (p.ts[j].Is(";") || p.ts[j].Is(",")) {
			break
		}
		header = append(header, p.ts[j].Text)
	}
	h := sha1.Sum([]byte(strings.Join(toks, " ")))
	headerToks := p.ts[start:min(sigEnd, endTok+1)]
	exported := true
	if p.d.Exported != nil {
		exported = p.d.Exported(headerToks, name, container != "")
	}
	p.decls = append(p.decls, Decl{
		Symbol: lang.Symbol{
			Name: qual, Kind: kind, Start: startLine, End: p.ts[endTok].EndLine,
			Signature: p.sourceText(start, start+len(header)-1), BodyHash: hex.EncodeToString(h[:8]),
			Exported: exported, Container: isCont,
		},
		Tokens: toks, Header: header,
	})
	if isCont && body >= 0 {
		closeAt := p.skipGroup(body, len(p.ts)) - 1
		p.scope(body+1, closeAt, qual, name, kind)
	}
}

func (p *parser) endTok(end int) int {
	if end+1 > len(p.ts) {
		return len(p.ts)
	}
	return end + 1
}

func (p *parser) exportsDecl(k, hi int) bool {
	for j := k + 1; j < hi && j < k+4; j++ {
		w := p.word(j)
		if p.d.DeclKeywords[w] || p.d.Containers[w] != "" {
			return true
		}
	}
	return false
}

func (p *parser) wordAt(top []int, n int) string {
	for ; n < len(top); n++ {
		if w := p.word(top[n]); w != "" && !p.d.Modifiers[w] {
			return w
		}
	}
	return ""
}

func (p *parser) nameAfter(top []int, n int) string {
	seen := false
	for ; n < len(top); n++ {
		w := p.word(top[n])
		if w == "" || p.d.Modifiers[w] {
			continue
		}
		if !seen {
			seen = true // the keyword itself
			continue
		}
		return w
	}
	return ""
}

// nameBefore finds the identifier preceding top[at] (an opening paren),
// skipping generic parameter lists and joining dotted names (Dart ctors).
func (p *parser) nameBefore(top []int, at int) string {
	n := at - 1
	// Skip <...> generics.
	if n >= 0 && p.ts[top[n]].Is(">") {
		depth := 0
		for ; n >= 0; n-- {
			if p.ts[top[n]].Is(">") {
				depth++
			} else if p.ts[top[n]].Is("<") {
				depth--
				if depth == 0 {
					n--
					break
				}
			}
		}
	}
	for n >= 0 && (p.ts[top[n]].Is("*") || p.ts[top[n]].Is("?") || p.ts[top[n]].Is("!")) {
		n--
	}
	if n < 0 || p.ts[top[n]].Kind != Ident {
		if n >= 0 && p.ts[top[n]].Kind == String {
			return strings.Trim(p.ts[top[n]].Text, `'"`)
		}
		return ""
	}
	name := p.ts[top[n]].Text
	if n >= 1 && p.ts[top[n-1]].Is("#") {
		name = "#" + name
	}
	if n >= 2 && p.ts[top[n-1]].Is(".") && p.ts[top[n-2]].Kind == Ident {
		name = p.ts[top[n-2]].Text + "." + name
	}
	return name
}

// varName picks the declared name in "<header> = ..." or "<header>;".
func (p *parser) varName(top []int, at int) string {
	if p.d.TypeFirst {
		for n := at - 1; n >= 0; n-- {
			if w := p.word(top[n]); w != "" && !p.d.Modifiers[w] {
				return w
			}
		}
		return ""
	}
	for n := 0; n < at; n++ {
		w := p.word(top[n])
		if w == "" {
			if p.ts[top[n]].Is("[") || p.ts[top[n]].Is("{") {
				return "" // destructuring
			}
			continue
		}
		if p.d.Modifiers[w] || p.d.DeclKeywords[w] {
			continue
		}
		if n >= 1 && p.ts[top[n-1]].Is("#") {
			return "#" + w
		}
		return w
	}
	return ""
}

// ---- Shared capability implementations ----

// Normalize renders code tokens only, canonicalising trivial formatting
// differences (comments, whitespace, trailing commas, quote style, and with
// ASI, semicolons).
func Normalize(src []byte, d *Dialect) []byte {
	toks := Lex(src, d)
	var code []Token
	for _, t := range toks {
		if t.Kind != Comment {
			code = append(code, t)
		}
	}
	var b strings.Builder
	for i, t := range code {
		if t.Is(",") && i+1 < len(code) && (code[i+1].Is(")") || code[i+1].Is("]") || code[i+1].Is("}")) {
			continue
		}
		if d.ASI && t.Is(";") {
			continue
		}
		text := t.Text
		if t.Kind == String {
			text = canonicalString(text)
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func canonicalString(s string) string {
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] && !strings.HasPrefix(s, "'''") && !strings.HasPrefix(s, `"""`) {
		inner := s[1 : len(s)-1]
		if !strings.ContainsAny(inner, `\'"`) {
			return `"` + inner + `"`
		}
	}
	return s
}

// SignificantLines marks lines that contain code tokens.
func SignificantLines(src []byte, d *Dialect) []bool {
	n := strings.Count(strings.TrimSuffix(string(src), "\n"), "\n") + 1
	out := make([]bool, n)
	for _, t := range Lex(src, d) {
		if t.Kind == Comment {
			continue
		}
		for l := t.Line; l <= t.EndLine && l <= n; l++ {
			out[l-1] = true
		}
	}
	return out
}

// Symbols returns declarations as language symbols.
func Symbols(src []byte, d *Dialect) []lang.Symbol {
	decls, _ := Parse(src, d)
	out := make([]lang.Symbol, len(decls))
	for i, dc := range decls {
		out[i] = dc.Symbol
	}
	return out
}

// Compare produces semantic findings between two versions of a block.
func Compare(oldSrc, newSrc []byte, d *Dialect) []lang.Finding {
	oldD, oldImp := Parse(oldSrc, d)
	newD, newImp := Parse(newSrc, d)
	var out []lang.Finding

	group := func(ds []Decl) (map[string]*Decl, []string) {
		m := map[string]*Decl{}
		var order []string
		for i := range ds {
			dc := ds[i]
			if prev, ok := m[dc.Name]; ok { // overloads: merge
				prev.Header = append(append(prev.Header, "|"), dc.Header...)
				prev.Tokens = append(prev.Tokens, dc.Tokens...)
				prev.BodyHash += dc.BodyHash
				continue
			}
			m[dc.Name] = &dc
			order = append(order, dc.Name)
		}
		return m, order
	}
	om, oorder := group(oldD)
	nm, norder := group(newD)

	sev := func(exported bool, hi, lo lang.Severity) lang.Severity {
		if exported {
			return hi
		}
		return lo
	}
	var removed, added []*Decl
	for _, name := range oorder {
		o := om[name]
		n, ok := nm[name]
		if !ok {
			removed = append(removed, o)
			continue
		}
		if o.Exported && !n.Exported {
			out = append(out, lang.Finding{Kind: "visibility", Symbol: name, Severity: lang.SevBreaking,
				Message: "no longer public/exported"})
		} else if !o.Exported && n.Exported {
			out = append(out, lang.Finding{Kind: "visibility", Symbol: name, Severity: lang.SevMinor,
				Message: "became public/exported"})
		}
		if o.Kind != n.Kind {
			out = append(out, lang.Finding{Kind: "kind-changed", Symbol: name, Severity: lang.SevMajor,
				Message: o.Kind + " → " + n.Kind})
		}
		if strings.Join(o.Header, " ") != strings.Join(n.Header, " ") {
			out = append(out, lang.Finding{Kind: "signature-changed", Symbol: name,
				Severity: sev(n.Exported, lang.SevBreaking, lang.SevMajor),
				Message:  "`" + oneLine(o.Signature, 100) + "` → `" + oneLine(n.Signature, 100) + "`"})
		} else if o.BodyHash != n.BodyHash && !n.Container {
			ins, del := diff.Stats(diff.Lines(o.Tokens, n.Tokens))
			out = append(out, lang.Finding{Kind: "body-changed", Symbol: name, Severity: lang.SevMinor,
				Message: "implementation changed (" + strconv.Itoa(ins) + " tokens added, " + strconv.Itoa(del) + " removed)"})
		}
	}
	for _, name := range norder {
		if _, ok := om[name]; !ok {
			added = append(added, nm[name])
		}
	}
	// Rename detection: removed + added with near-identical bodies.
	used := map[*Decl]bool{}
	for _, r := range removed {
		var best *Decl
		bestScore := 0.0
		for _, a := range added {
			if used[a] || a.Kind != r.Kind {
				continue
			}
			if s := diff.Similarity(r.Tokens, a.Tokens); s > bestScore {
				best, bestScore = a, s
			}
		}
		if best != nil && bestScore >= 0.8 {
			used[best] = true
			out = append(out, lang.Finding{Kind: "renamed", Symbol: r.Name,
				Severity: sev(r.Exported, lang.SevBreaking, lang.SevMajor),
				Message:  "→ " + best.Name})
			continue
		}
		out = append(out, lang.Finding{Kind: "removed", Symbol: r.Name,
			Severity: sev(r.Exported, lang.SevBreaking, lang.SevMajor), Message: r.Kind + " removed"})
	}
	for _, a := range added {
		if !used[a] {
			out = append(out, lang.Finding{Kind: "added", Symbol: a.Name, Severity: lang.SevMajor,
				Message: "new " + a.Kind + " may need porting"})
		}
	}
	oi, ni := setOf(oldImp), setOf(newImp)
	for _, s := range newImp {
		if !oi[s] {
			out = append(out, lang.Finding{Kind: "import-added", Severity: lang.SevMinor, Message: oneLine(s, 120)})
		}
	}
	for _, s := range oldImp {
		if !ni[s] {
			out = append(out, lang.Finding{Kind: "import-removed", Severity: lang.SevMinor, Message: oneLine(s, 120)})
		}
	}
	return out
}

func setOf(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max-1] + "…"
	}
	return s
}

// isNamedCtor reports Dart-style "Container.name(" constructors.
func (p *parser) isNamedCtor(top []int, parenAt int, container string) bool {
	return parenAt >= 3 && p.ts[top[parenAt-2]].Is(".") && p.word(top[parenAt-3]) == container
}

// sourceText returns the original text spanning code tokens lo..hi with
// whitespace collapsed (used for human-readable signatures).
func (p *parser) sourceText(lo, hi int) string {
	if hi < lo || lo >= len(p.ts) {
		return ""
	}
	hi = min(hi, len(p.ts)-1)
	return strings.Join(strings.Fields(string(p.src[p.ts[lo].Off:p.ts[hi].EndOff])), " ")
}
