// Package glob implements gitignore-flavoured path matching with "**" support.
//
// Rules:
//   - Paths are slash-separated and relative to a repository root.
//   - "**" matches zero or more whole path segments.
//   - Other segments use path.Match semantics ("*", "?", "[...]").
//   - A pattern without a "/" matches a file name in any directory
//     (it is treated as "**/<pattern>").
//   - A pattern ending in "/" matches a directory and everything beneath it.
package glob

import (
	"path"
	"strings"
)

// Match reports whether name matches pattern.
func Match(pattern, name string) bool {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "./")
	name = strings.TrimPrefix(name, "./")
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	if !strings.Contains(strings.TrimSuffix(pattern, "/**"), "/") && !strings.HasPrefix(pattern, "**") {
		pattern = "**/" + pattern
	}
	pattern = strings.TrimPrefix(pattern, "/")
	return matchSegs(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegs(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// Collapse consecutive "**".
			for len(pat) > 1 && pat[1] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchSegs(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// Set is an include/exclude filter. An empty include list includes everything.
type Set struct {
	Include []string
	Exclude []string
}

// Match reports whether p is included and not excluded.
func (s Set) Match(p string) bool {
	if len(s.Include) > 0 && !Any(s.Include, p) {
		return false
	}
	return !Any(s.Exclude, p)
}

// ExcludesDir reports whether a whole directory can be skipped while walking.
func (s Set) ExcludesDir(dir string) bool {
	for _, e := range s.Exclude {
		e = strings.TrimSuffix(strings.TrimSuffix(e, "/**"), "/")
		if Match(e, dir) {
			return true
		}
	}
	return false
}

// Any reports whether any pattern matches p.
func Any(patterns []string, p string) bool {
	for _, pat := range patterns {
		if Match(pat, p) {
			return true
		}
	}
	return false
}
