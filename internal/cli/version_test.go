package cli

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Path: "github.com/ryangarber/pacemaker", Version: v}}, true
		}
	}
	none := func() (*debug.BuildInfo, bool) { return nil, false }
	for _, tc := range []struct {
		name, stamped string
		read          func() (*debug.BuildInfo, bool)
		want          string
	}{
		{"ldflags wins over build info", "0.2.0", info("v0.1.0"), "0.2.0"},
		{"go install of a tag", "dev", info("v0.1.0"), "0.1.0"},
		{"go install of a commit", "dev", info("v0.1.1-0.20261125221700-cb028ad5e2f1"), "0.1.1-0.20261125221700-cb028ad5e2f1"},
		{"build in a dirty checkout", "dev", info("v0.1.1-0.20261125221700-cb028ad5e2f1+dirty"), "0.1.1-0.20261125221700-cb028ad5e2f1+dirty"},
		{"go run or untagged build", "dev", info("(devel)"), "dev"},
		{"empty module version", "dev", info(""), "dev"},
		{"no build info", "dev", none, "dev"},
		{"empty ldflags value", "", info("v0.1.0"), "0.1.0"},
	} {
		if got := resolveVersion(tc.stamped, tc.read); got != tc.want {
			t.Errorf("%s: resolveVersion(%q) = %q, want %q", tc.name, tc.stamped, got, tc.want)
		}
	}
}
