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
//
// Positional form is "<id> <ref>..."; key/value form accepts id=, ref= (may
// repeat), note=, tags= and reason=. Quoted values may contain spaces.
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
		re:    regexp.MustCompile(regexp.QuoteMeta(token) + `:(begin|end|file|skip)\b(.*)$`),
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
	var (
		blocks   []Block
		problems []Problem
		stack    []open
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
		if b.ID == "" && len(pos) > 0 && b.Kind != KindSkip {
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
			blocks = append(blocks, *b)
		}
	}
	for _, o := range stack {
		prob(o.line, "@pace:begin %q is never closed", o.b.ID)
	}
	return blocks, problems
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
