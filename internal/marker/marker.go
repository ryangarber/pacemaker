// Package marker finds pacemaker annotations in source files.
//
// Markers are language agnostic: they are recognised anywhere on a line, so
// they work inside any comment syntax (//, #, --, <!-- -->, /* */, ...).
//
//	// @pace:begin auth.login upstream:lib/auth.dart#AuthService.login
//	... local code ...
//	// @pace:end auth.login
//
//	# @pace:file id=models ref=lib/models.dart          (whole local file)
//	<!-- @pace:skip ref=lib/legacy.dart reason="not needed on web" -->
//	// @pace:todo handle token refresh ref=lib/auth.dart#refresh owner=ana prio=high
//	// @pace:note we keep the v1 error codes on purpose
//
// Positional form is "<id> <ref>..."; key/value form accepts id=, ref= (may
// repeat), note=, tags= and reason=. Quoted values may contain spaces. A
// @pace:file marker with a single positional argument treats it as the ref.
//
// @pace:todo and @pace:note take free text plus optional ref=, owner=,
// prio=, tags= and block= attributes; they attach to the innermost enclosing
// block (or the file's @pace:file block) unless block= names another one.
package marker

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/ryangarber/pacemaker/internal/ref"
)

// Kind of block.
type Kind string

const (
	KindBlock Kind = "block" // begin/end delimited region
	KindFile  Kind = "file"  // whole local file
	KindSkip  Kind = "skip"  // upstream region intentionally not ported
)

// RefSite is a reference together with where it was written.
type RefSite struct {
	Ref  ref.Ref
	Raw  string // reference text exactly as written
	Line int    // line of the marker containing it
}

// Block is a local region mapped to one or more upstream references.
type Block struct {
	ID    string
	Kind  Kind
	File  string
	Start int // first content line (1-based); 0 for skip blocks
	End   int // last content line (inclusive)
	Refs  []RefSite
	Note  string
	Tags  []string
	Attrs map[string]string
	// MarkerLine is the line of the opening marker.
	MarkerLine int
}

// NoteKind distinguishes open work from informational notes.
type NoteKind string

const (
	NoteTodo NoteKind = "todo" // open work: a gap, a follow-up, a known deviation to fix
	NoteNote NoteKind = "note" // informational: intentional deviation, porting rationale
)

// Todo is a free-text annotation from @pace:todo or @pace:note.
type Todo struct {
	Kind     NoteKind
	File     string
	Line     int
	Text     string
	Owner    string
	Priority string
	Tags     []string
	Refs     []RefSite
	// Block is the id of the block the note belongs to: the innermost
	// enclosing @pace:begin, the file's @pace:file block, or block=.
	Block string
	// Explicit is true when Block came from block= (and must exist).
	Explicit bool
}

// Problem is a malformed or inconsistent marker.
type Problem struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

func (p Problem) String() string { return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Message) }

// Scanner parses markers using a configurable token (default "@pace").
type Scanner struct {
	re    *regexp.Regexp
	token []byte
}

// NewScanner builds a scanner for the given marker token.
func NewScanner(token string) *Scanner {
	if token == "" {
		token = "@pace"
	}
	return &Scanner{
		re:    regexp.MustCompile(regexp.QuoteMeta(token) + `:(begin|end|file|skip|todo|note)\b(.*)$`),
		token: []byte(token + ":"),
	}
}

// Contains is a fast pre-check before full scanning.
func (s *Scanner) Contains(content []byte) bool { return bytes.Contains(content, s.token) }

var closers = []string{"*/", "-->", "--}}", "#}", "%>", "*)", "-}", "}}"}

type open struct {
	b    *Block
	line int
}

// Scan extracts blocks from a single file's content.
func (s *Scanner) Scan(file string, content []byte) ([]Block, []Problem) {
	blocks, _, problems := s.ScanAll(file, content)
	return blocks, problems
}

// ScanAll extracts blocks and todo/note annotations from a file's content.
func (s *Scanner) ScanAll(file string, content []byte) ([]Block, []Todo, []Problem) {
	var (
		blocks   []Block
		todos    []Todo
		problems []Problem
		stack    []open
		fileID   string // id of this file's @pace:file block, if any
	)
	lines := strings.Split(string(content), "\n")
	nLines := len(lines)
	if nLines > 0 && lines[nLines-1] == "" {
		nLines--
	}
	prob := func(line int, f string, a ...any) {
		problems = append(problems, Problem{File: file, Line: line, Message: fmt.Sprintf(f, a...)})
	}
	for i, text := range lines {
		ln := i + 1
		m := s.re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		verb, rest := m[1], strings.TrimSpace(m[2])
		for _, c := range closers {
			rest = strings.TrimSpace(strings.TrimSuffix(rest, c))
		}
		if verb == "todo" || verb == "note" {
			t, errs := parseTodo(NoteKind(verb), file, ln, rest)
			for _, e := range errs {
				prob(ln, "%s", e)
			}
			if t == nil {
				continue
			}
			if t.Block == "" && len(stack) > 0 {
				t.Block = stack[len(stack)-1].b.ID
			}
			todos = append(todos, *t)
			continue
		}
		pos, kv, err := tokenize(rest)
		if err != nil {
			prob(ln, "%v", err)
			continue
		}
		if verb == "end" {
			id := kv["id"]
			if id == "" && len(pos) > 0 {
				id = pos[0]
			}
			idx := len(stack) - 1
			if id != "" {
				for idx >= 0 && stack[idx].b.ID != id {
					idx--
				}
			}
			if idx < 0 {
				prob(ln, "@pace:end %q has no matching @pace:begin", id)
				continue
			}
			for j := len(stack) - 1; j > idx; j-- {
				prob(stack[j].line, "@pace:begin %q is never closed", stack[j].b.ID)
			}
			b := stack[idx].b
			b.End = ln - 1
			blocks = append(blocks, *b)
			stack = stack[:idx]
			continue
		}

		b := &Block{File: file, MarkerLine: ln, Attrs: map[string]string{}}
		switch verb {
		case "begin":
			b.Kind, b.Start = KindBlock, ln+1
		case "file":
			b.Kind, b.Start, b.End = KindFile, 1, nLines
		case "skip":
			b.Kind = KindSkip
		}
		b.ID = kv["id"]
		rawRefs := kv["ref"+multiSep]
		// "@pace:file <ref>": a lone positional argument is the reference
		// (the id then defaults to the local path).
		singleFileRef := b.Kind == KindFile && b.ID == "" && rawRefs == "" && len(pos) == 1
		if b.ID == "" && len(pos) > 0 && b.Kind != KindSkip && !singleFileRef {
			b.ID, pos = pos[0], pos[1:]
		}
		refs := append(strings.Split(rawRefs, multiSep), pos...)
		for _, raw := range refs {
			if raw == "" {
				continue
			}
			r, err := ref.Parse(raw)
			if err != nil {
				prob(ln, "bad reference %q: %v", raw, err)
				continue
			}
			b.Refs = append(b.Refs, RefSite{Ref: r, Raw: raw, Line: ln})
		}
		b.Note = kv["note"]
		if b.Kind == KindSkip && b.Note == "" {
			b.Note = kv["reason"]
		}
		if t := kv["tags"]; t != "" {
			for _, tag := range strings.Split(t, ",") {
				if tag = strings.TrimSpace(tag); tag != "" {
					b.Tags = append(b.Tags, tag)
				}
			}
		}
		for k, v := range kv {
			if !strings.HasSuffix(k, multiSep) {
				b.Attrs[k] = v
			}
		}
		if b.ID == "" {
			if b.Kind == KindBlock {
				prob(ln, "@pace:%s requires an id", verb)
				continue
			}
			b.ID = file // whole-file blocks default to the local path
			if b.Kind == KindSkip && len(b.Refs) > 0 {
				b.ID = "skip:" + b.Refs[0].Raw // stable across line shifts
			}
		}
		if len(b.Refs) == 0 {
			prob(ln, "@pace:%s %q has no reference", verb, b.ID)
			continue
		}
		if b.Kind == KindBlock {
			stack = append(stack, open{b: b, line: ln})
		} else {
			if b.Kind == KindFile && fileID == "" {
				fileID = b.ID
			}
			blocks = append(blocks, *b)
		}
	}
	for _, o := range stack {
		prob(o.line, "@pace:begin %q is never closed", o.b.ID)
	}
	for i := range todos {
		if todos[i].Block == "" {
			todos[i].Block = fileID
		}
	}
	return blocks, todos, problems
}

// todoAttr matches the recognised key=value attributes of todo/note markers;
// everything else is free text.
var todoAttr = regexp.MustCompile(`(?:^|\s)(ref|src|owner|prio|priority|tags|block)=("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|\S+)`)

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
		var b strings.Builder
		for i := 0; i < len(v); i++ {
			if v[i] == '\\' && i+1 < len(v) {
				i++
			}
			b.WriteByte(v[i])
		}
		return b.String()
	}
	return v
}

// parseTodo parses the text after "@pace:todo" / "@pace:note".
func parseTodo(kind NoteKind, file string, line int, rest string) (*Todo, []string) {
	t := &Todo{Kind: kind, File: file, Line: line}
	var errs []string
	for _, m := range todoAttr.FindAllStringSubmatch(rest, -1) {
		val := unquote(m[2])
		switch m[1] {
		case "ref", "src":
			r, err := ref.Parse(val)
			if err != nil {
				errs = append(errs, fmt.Sprintf("bad reference %q: %v", val, err))
				continue
			}
			t.Refs = append(t.Refs, RefSite{Ref: r, Raw: val, Line: line})
		case "owner":
			t.Owner = val
		case "prio", "priority":
			t.Priority = strings.ToLower(val)
		case "tags":
			for _, tag := range strings.Split(val, ",") {
				if tag = strings.TrimSpace(tag); tag != "" {
					t.Tags = append(t.Tags, tag)
				}
			}
		case "block":
			t.Block, t.Explicit = val, true
		}
	}
	text := strings.TrimSpace(todoAttr.ReplaceAllString(rest, " "))
	text = strings.TrimSpace(strings.TrimLeft(text, ":-–— "))
	if len(text) >= 2 && strings.Count(text, text[:1]) == 2 {
		text = unquote(text) // "quoted text" → quoted text
	}
	text = strings.Join(strings.Fields(text), " ")
	t.Text = text
	if t.Text == "" && len(t.Refs) == 0 {
		errs = append(errs, fmt.Sprintf("@pace:%s needs a description or a ref=", kind))
		return nil, errs
	}
	return t, errs
}

const multiSep = "\x00"

// tokenize splits "a b key=val key2=\"x y\"" into positional and keyed values.
// Repeated "ref" keys are joined under "ref\x00".
func tokenize(s string) (pos []string, kv map[string]string, err error) {
	kv = map[string]string{}
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		key := ""
		for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != '=' && s[i] != '"' {
			i++
		}
		if i < len(s) && s[i] == '=' {
			key = s[start:i]
			i++
			start = i
		} else {
			i = start
		}
		var val string
		if i < len(s) && (s[i] == '"' || s[i] == '\'') {
			q := s[i]
			i++
			var b strings.Builder
			for i < len(s) && s[i] != q {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				b.WriteByte(s[i])
				i++
			}
			if i >= len(s) {
				return nil, nil, fmt.Errorf("unterminated quote in marker")
			}
			i++
			val = b.String()
		} else {
			for i < len(s) && s[i] != ' ' && s[i] != '\t' {
				i++
			}
			val = s[start:i]
		}
		switch key {
		case "":
			pos = append(pos, val)
		case "ref", "src":
			if prev, ok := kv["ref"+multiSep]; ok {
				val = prev + multiSep + val
			}
			kv["ref"+multiSep] = val
		default:
			kv[key] = val
		}
	}
	return pos, kv, nil
}
