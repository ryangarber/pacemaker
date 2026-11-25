// Package config loads and saves .pacemaker/config.yaml and supports generic
// dotted-key get/set for the config CLI.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ryangarber/pacemaker/internal/glob"
	"github.com/ryangarber/pacemaker/internal/source"
)

const (
	Dir        = ".pacemaker"
	File       = "config.yaml"
	StateFile  = "state.yaml"
	SnapDir    = "snapshots"
	CacheDir   = "cache"
	CurVersion = 1
)

// Filter is an include/exclude pattern pair.
type Filter struct {
	Include []string `yaml:"include,omitempty" json:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
}

// Set converts to a glob.Set.
func (f Filter) Set() glob.Set { return glob.Set{Include: f.Include, Exclude: f.Exclude} }

// Target is an upstream repository.
type Target struct {
	URL    string `yaml:"url,omitempty" json:"url,omitempty"`
	Path   string `yaml:"path,omitempty" json:"path,omitempty"`
	Rev    string `yaml:"rev,omitempty" json:"rev,omitempty"`
	Filter `yaml:",inline" json:",inline"`
}

// Location returns URL or path.
func (t Target) Location() string {
	if t.URL != "" {
		return t.URL
	}
	return t.Path
}

// Spec converts to a source.Spec.
func (t Target) Spec(name string) source.Spec {
	return source.Spec{Name: name, URL: t.URL, Path: t.Path, Rev: t.Rev}
}

// Drift tunes drift classification.
type Drift struct {
	// Cosmetic is how formatting/comment-only upstream changes are treated:
	// "ok" (ignore), "warn" (report, don't fail), or "fail".
	Cosmetic string `yaml:"cosmetic" json:"cosmetic"`
	// FailOn lists statuses that make `pacemaker check` exit non-zero.
	FailOn []string `yaml:"fail_on" json:"fail_on"`
	// Context lines in rendered diffs.
	Context int `yaml:"context" json:"context"`
	// MaxCommits listed per drifted link.
	MaxCommits int `yaml:"max_commits" json:"max_commits"`
}

// Report sets default report options.
type Report struct {
	Format string `yaml:"format" json:"format"`
	Output string `yaml:"output,omitempty" json:"output,omitempty"`
	Title  string `yaml:"title,omitempty" json:"title,omitempty"`
}

// Config is the project configuration.
type Config struct {
	Version       int               `yaml:"version" json:"version"`
	Marker        string            `yaml:"marker" json:"marker"`
	DefaultTarget string            `yaml:"default_target,omitempty" json:"default_target,omitempty"`
	Local         Filter            `yaml:"local" json:"local"`
	Targets       map[string]Target `yaml:"targets" json:"targets"`
	Drift         Drift             `yaml:"drift" json:"drift"`
	Report        Report            `yaml:"report" json:"report"`
}

// Default returns a config with sensible defaults.
func Default() *Config {
	return &Config{
		Version: CurVersion,
		Marker:  "@pace",
		Local: Filter{
			Exclude: []string{".git/", ".pacemaker/", "node_modules/", "build/", "dist/", ".dart_tool/", "vendor/"},
		},
		Targets: map[string]Target{},
		Drift: Drift{
			Cosmetic:   "warn",
			FailOn:     []string{"drifted", "missing", "new", "error"},
			Context:    3,
			MaxCommits: 10,
		},
		Report: Report{Format: "markdown"},
	}
}

// Find walks up from dir to locate a project root containing .pacemaker/.
func Find(dir string) (string, error) {
	dir, _ = filepath.Abs(dir)
	for {
		if st, err := os.Stat(filepath.Join(dir, Dir, File)); err == nil && !st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no .pacemaker/config.yaml found (run `pacemaker init`)")
		}
		dir = parent
	}
}

// Path returns the config file path for a project root.
func Path(root string) string { return filepath.Join(root, Dir, File) }

// Load reads the config, filling defaults for missing fields.
func Load(root string) (*Config, error) {
	b, err := os.ReadFile(Path(root))
	if err != nil {
		return nil, err
	}
	c := Default()
	c.Local = Filter{}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(root), err)
	}
	if c.Targets == nil {
		c.Targets = map[string]Target{}
	}
	return c, c.Validate()
}

// Validate checks semantic constraints.
func (c *Config) Validate() error {
	for name, t := range c.Targets {
		if (t.URL == "") == (t.Path == "") {
			return fmt.Errorf("target %q: exactly one of url or path must be set", name)
		}
	}
	if c.DefaultTarget != "" {
		if _, ok := c.Targets[c.DefaultTarget]; !ok {
			return fmt.Errorf("default_target %q is not a configured target", c.DefaultTarget)
		}
	}
	switch c.Drift.Cosmetic {
	case "ok", "warn", "fail":
	default:
		return fmt.Errorf("drift.cosmetic must be ok, warn or fail (got %q)", c.Drift.Cosmetic)
	}
	return nil
}

// Save writes the config with a short header comment.
func (c *Config) Save(root string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("# pacemaker configuration — https://github.com/ryangarber/pacemaker\n")
	buf.WriteString("# Edit by hand or with `pacemaker config set <key> <value>`.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, Dir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(Path(root), buf.Bytes(), 0o644)
}

// TargetNames returns sorted target names.
func (c *Config) TargetNames() []string {
	var names []string
	for n := range c.Targets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ResolveTarget picks the named target or the default (or the only one).
func (c *Config) ResolveTarget(name string) (string, error) {
	if name == "" {
		name = c.DefaultTarget
	}
	if name == "" && len(c.Targets) == 1 {
		return c.TargetNames()[0], nil
	}
	if name == "" {
		return "", errors.New("no default target configured")
	}
	if _, ok := c.Targets[name]; !ok {
		return "", fmt.Errorf("unknown target %q", name)
	}
	return name, nil
}

// ---- Generic dotted-key access ----

func (c *Config) toTree() (map[string]any, error) {
	b, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, yaml.Unmarshal(b, &m)
}

func (c *Config) fromTree(m map[string]any) error {
	b, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	n := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(n); err != nil {
		return err
	}
	if n.Targets == nil {
		n.Targets = map[string]Target{}
	}
	if err := n.Validate(); err != nil {
		return err
	}
	*c = *n
	return nil
}

// Get returns the value at a dotted key (e.g. "targets.upstream.rev").
func (c *Config) Get(key string) (any, error) {
	m, err := c.toTree()
	if err != nil {
		return nil, err
	}
	var cur any = m
	for _, k := range splitKey(key) {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: not a map", key)
		}
		if cur, ok = mm[k]; !ok {
			return nil, fmt.Errorf("%s: not set", key)
		}
	}
	return cur, nil
}

// Set assigns a value at a dotted key. Values are parsed as YAML scalars;
// comma-separated values become lists for list-typed keys.
func (c *Config) Set(key, value string) error {
	return c.mutate(key, func(parent map[string]any, last string) error {
		parent[last] = parseValue(parent[last], value)
		return nil
	})
}

// Unset removes a dotted key.
func (c *Config) Unset(key string) error {
	return c.mutate(key, func(parent map[string]any, last string) error {
		delete(parent, last)
		return nil
	})
}

func (c *Config) mutate(key string, fn func(map[string]any, string) error) error {
	m, err := c.toTree()
	if err != nil {
		return err
	}
	parts := splitKey(key)
	cur := m
	for _, k := range parts[:len(parts)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	if err := fn(cur, parts[len(parts)-1]); err != nil {
		return err
	}
	return c.fromTree(m)
}

func splitKey(k string) []string {
	return strings.Split(strings.Trim(k, "."), ".")
}

func parseValue(prev any, v string) any {
	if _, isList := prev.([]any); isList || strings.HasPrefix(v, "[") {
		v = strings.Trim(v, "[]")
		var out []any
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	if b, err := strconv.ParseBool(v); err == nil && (v == "true" || v == "false") {
		return b
	}
	return v
}

// Keys lists all leaf keys in dotted form (for completion / `config list`).
func (c *Config) Keys() []string {
	m, _ := c.toTree()
	var keys []string
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		if mm, ok := v.(map[string]any); ok && len(mm) > 0 {
			for k, vv := range mm {
				p := k
				if prefix != "" {
					p = prefix + "." + k
				}
				walk(p, vv)
			}
			return
		}
		keys = append(keys, prefix)
	}
	walk("", m)
	sort.Strings(keys)
	return keys
}
