// Package lang defines the language-module plug-in interfaces.
//
// The core engine is language agnostic and only relies on plain line diffs.
// A language module may opt into extra capabilities by implementing any of
// the optional interfaces below; the engine discovers them with type
// assertions, so modules implement only what they can do well.
package lang

import (
	"path"
	"sort"
	"strings"
	"sync"
)

// Language is the minimal module contract.
type Language interface {
	// ID is a short stable identifier, e.g. "dart".
	ID() string
	// Extensions lists file extensions handled (with leading dot).
	Extensions() []string
}

// Normalizer canonicalises source so that formatting-only or comment-only
// changes produce identical output. Used to classify drift as "cosmetic".
type Normalizer interface {
	Normalize(src []byte) []byte
}

// LineClassifier marks which lines are meaningful for coverage (not blank,
// not comment-only).
type LineClassifier interface {
	SignificantLines(src []byte) []bool
}

// SymbolProvider extracts declarations so refs like "file#Class.method" can be
// resolved and symbol-level coverage can be reported.
type SymbolProvider interface {
	Symbols(src []byte) []Symbol
}

// SemanticDiffer explains what changed between two versions of a block in
// terms richer than a line diff (signature changes, removed APIs, ...).
type SemanticDiffer interface {
	Compare(oldSrc, newSrc []byte) []Finding
}

// Symbol is a declaration in a source file.
type Symbol struct {
	Name      string `json:"name"` // qualified, e.g. "AuthService.login"
	Kind      string `json:"kind"` // class, function, method, field, ...
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Signature string `json:"signature,omitempty"`
	BodyHash  string `json:"-"`
	Exported  bool   `json:"exported"`
	Container bool   `json:"-"`
}

// Severity of a semantic finding.
type Severity string

const (
	SevInfo     Severity = "info"
	SevMinor    Severity = "minor"
	SevMajor    Severity = "major"
	SevBreaking Severity = "breaking"
)

// Rank orders severities for comparisons.
func (s Severity) Rank() int {
	switch s {
	case SevBreaking:
		return 3
	case SevMajor:
		return 2
	case SevMinor:
		return 1
	}
	return 0
}

// Finding is a single semantic observation about a change.
type Finding struct {
	Kind     string   `json:"kind" yaml:"kind"`
	Symbol   string   `json:"symbol,omitempty" yaml:"symbol,omitempty"`
	Severity Severity `json:"severity" yaml:"severity"`
	Message  string   `json:"message" yaml:"message"`
}

var (
	mu       sync.RWMutex
	registry []Language
	fallback Language
)

// Register adds a module. Later registrations win for shared extensions.
func Register(l Language) {
	mu.Lock()
	defer mu.Unlock()
	registry = append(registry, l)
}

// SetFallback installs the module used when no extension matches.
func SetFallback(l Language) {
	mu.Lock()
	defer mu.Unlock()
	fallback = l
}

// ForPath returns the module for a file path (never nil once a fallback is set).
func ForPath(p string) Language {
	ext := strings.ToLower(path.Ext(p))
	mu.RLock()
	defer mu.RUnlock()
	for i := len(registry) - 1; i >= 0; i-- {
		for _, e := range registry[i].Extensions() {
			if e == ext {
				return registry[i]
			}
		}
	}
	return fallback
}

// All returns registered modules sorted by ID (excluding the fallback).
func All() []Language {
	mu.RLock()
	defer mu.RUnlock()
	out := append([]Language(nil), registry...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Fallback returns the fallback module.
func Fallback() Language {
	mu.RLock()
	defer mu.RUnlock()
	return fallback
}

// Capabilities lists optional interfaces a module implements.
func Capabilities(l Language) []string {
	var caps []string
	if _, ok := l.(Normalizer); ok {
		caps = append(caps, "normalize")
	}
	if _, ok := l.(LineClassifier); ok {
		caps = append(caps, "lines")
	}
	if _, ok := l.(SymbolProvider); ok {
		caps = append(caps, "symbols")
	}
	if _, ok := l.(SemanticDiffer); ok {
		caps = append(caps, "semantic-diff")
	}
	return caps
}

// FindSymbol resolves a (possibly partially) qualified name. An exact match
// wins; otherwise a unique suffix match (".name") is accepted. Overloads with
// the same name are merged into one span.
func FindSymbol(syms []Symbol, name string) (Symbol, bool) {
	var hits []Symbol
	for _, s := range syms {
		if s.Name == name {
			hits = append(hits, s)
		}
	}
	if len(hits) == 0 {
		for _, s := range syms {
			if strings.HasSuffix(s.Name, "."+name) {
				hits = append(hits, s)
			}
		}
		names := map[string]bool{}
		for _, h := range hits {
			names[h.Name] = true
		}
		if len(names) > 1 {
			return Symbol{}, false
		}
	}
	if len(hits) == 0 {
		return Symbol{}, false
	}
	out := hits[0]
	for _, h := range hits[1:] {
		if h.Start < out.Start {
			out.Start = h.Start
		}
		if h.End > out.End {
			out.End = h.End
		}
	}
	return out, true
}
