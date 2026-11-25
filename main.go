// Command pacemaker maps ported code to its origin in another repository via
// source-map-like comments, tracks coverage of the origin, and detects when
// the origin drifts.
package main

import (
	"os"

	"github.com/ryangarber/pacemaker/internal/cli"

	// Language modules (self-register).
	_ "github.com/ryangarber/pacemaker/internal/lang/dart"
	_ "github.com/ryangarber/pacemaker/internal/lang/generic"
	_ "github.com/ryangarber/pacemaker/internal/lang/javascript"

	// Report formats (self-register).
	_ "github.com/ryangarber/pacemaker/internal/report/html"
	_ "github.com/ryangarber/pacemaker/internal/report/jsonfmt"
	_ "github.com/ryangarber/pacemaker/internal/report/markdown"
	_ "github.com/ryangarber/pacemaker/internal/report/text"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
