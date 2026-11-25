// Package report defines the format-agnostic renderer plug-in interface.
// Renderers live in sub-packages and self-register on import.
package report

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ryangarber/pacemaker/internal/model"
)

// Options affect how much detail renderers include.
type Options struct {
	Color   bool // terminal renderers only
	ShowOK  bool // include links with status ok
	Diffs   bool // include diffs
	Files   bool // include per-file coverage tables
	Symbols bool // include uncovered symbol lists
}

// Renderer turns a report into a document.
type Renderer interface {
	Name() string
	Description() string
	Extensions() []string
	Render(w io.Writer, r *model.Report, o Options) error
}

var (
	mu        sync.RWMutex
	renderers = map[string]Renderer{}
	aliases   = map[string]string{}
)

// Register adds a renderer, with optional aliases ("md" for "markdown").
func Register(r Renderer, alias ...string) {
	mu.Lock()
	defer mu.Unlock()
	renderers[r.Name()] = r
	for _, a := range alias {
		aliases[a] = r.Name()
	}
}

// Get finds a renderer by name or alias.
func Get(name string) (Renderer, error) {
	mu.RLock()
	defer mu.RUnlock()
	name = strings.ToLower(name)
	if a, ok := aliases[name]; ok {
		name = a
	}
	if r, ok := renderers[name]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("unknown report format %q (available: %s)", name, strings.Join(names(), ", "))
}

// ForFile picks a renderer from an output file's extension.
func ForFile(path string) (Renderer, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	mu.RLock()
	defer mu.RUnlock()
	for _, n := range names() {
		for _, e := range renderers[n].Extensions() {
			if e == ext {
				return renderers[n], true
			}
		}
	}
	return nil, false
}

// All returns renderers sorted by name.
func All() []Renderer {
	mu.RLock()
	defer mu.RUnlock()
	var out []Renderer
	for _, n := range names() {
		out = append(out, renderers[n])
	}
	return out
}

func names() []string {
	var ns []string
	for n := range renderers {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	return ns
}

// Visible filters links per options.
func Visible(r *model.Report, o Options) []*model.Link {
	var out []*model.Link
	for _, l := range r.Links {
		if l.Status == model.OK && !o.ShowOK {
			continue
		}
		out = append(out, l)
	}
	return out
}
