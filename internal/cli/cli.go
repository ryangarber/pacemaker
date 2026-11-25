// Package cli implements the pacemaker command-line interface.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/ryangarber/pacemaker/internal/config"
	"github.com/ryangarber/pacemaker/internal/engine"
	tu "github.com/ryangarber/pacemaker/internal/textutil"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// ExitError carries a specific exit code without extra output.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

// usageError marks errors that should print command usage.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(f string, a ...any) error { return usageError{fmt.Sprintf(f, a...)} }

// Ctx is passed to every command.
type Ctx struct {
	Out, Err io.Writer
	P        tu.Painter
	Root     string // explicit --root, or discovered
	NoFetch  bool
	Verbose  bool
	eng      *engine.Engine
}

// Engine opens the project lazily.
func (c *Ctx) Engine() (*engine.Engine, error) {
	if c.eng != nil {
		return c.eng, nil
	}
	root := c.Root
	if root == "" {
		root = "."
	}
	found, err := config.Find(root)
	if err != nil {
		return nil, err
	}
	e, err := engine.Open(found)
	if err != nil {
		return nil, err
	}
	e.AutoFetch = !c.NoFetch
	e.Log = c.Err
	c.eng = e
	return e, nil
}

// Command is a node in the command tree.
type Command struct {
	Name    string
	Args    string // usage synopsis for positional args
	Short   string
	Long    string
	Flags   func(fs *flag.FlagSet)
	Run     func(c *Ctx, fs *flag.FlagSet, args []string) error
	Sub     []*Command
	Aliases []string
}

func (cmd *Command) find(name string) *Command {
	for _, s := range cmd.Sub {
		if s.Name == name {
			return s
		}
		for _, a := range s.Aliases {
			if a == name {
				return s
			}
		}
	}
	return nil
}

// multi is a repeatable string flag (also splits on commas).
type multi []string

func (m *multi) String() string { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*m = append(*m, p)
		}
	}
	return nil
}

// parseInterspersed lets flags appear before or after positional args.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		if rest[0] == "--" {
			return append(pos, rest[1:]...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// Main runs the CLI and returns an exit code.
func Main(args []string, stdout, stderr *os.File) int {
	c := &Ctx{Out: stdout, Err: stderr, P: tu.Detect(stdout)}
	root := rootCommand()

	// Global flags (must precede the command).
	g := flag.NewFlagSet("pacemaker", flag.ContinueOnError)
	g.SetOutput(io.Discard)
	g.StringVar(&c.Root, "C", "", "")
	g.StringVar(&c.Root, "root", "", "")
	noColor := g.Bool("no-color", false, "")
	color := g.Bool("color", false, "")
	g.BoolVar(&c.NoFetch, "offline", false, "")
	g.BoolVar(&c.Verbose, "v", false, "")
	if err := g.Parse(args); err != nil {
		fmt.Fprintln(stderr, "pacemaker:", err)
		return 2
	}
	if *noColor {
		c.P.On = false
	}
	if *color {
		c.P.On = true
	}
	args = g.Args()

	cmd, chain := root, []string{"pacemaker"}
	for len(args) > 0 {
		if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
			if args[0] == "help" && len(args) > 1 {
				args = args[1:]
				for len(args) > 0 && cmd.find(args[0]) != nil {
					cmd = cmd.find(args[0])
					chain = append(chain, cmd.Name)
					args = args[1:]
				}
			}
			printHelp(c, cmd, chain)
			return 0
		}
		sub := cmd.find(args[0])
		if sub == nil {
			break
		}
		cmd, chain, args = sub, append(chain, sub.Name), args[1:]
	}
	if cmd.Run == nil {
		if len(args) > 0 {
			fmt.Fprintf(stderr, "pacemaker: unknown command %q\n\n", strings.Join(append(chain[1:], args[0]), " "))
		}
		printHelp(c, cmd, chain)
		if len(args) > 0 {
			return 2
		}
		return 0
	}
	fs := flag.NewFlagSet(strings.Join(chain, " "), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if cmd.Flags != nil {
		cmd.Flags(fs)
	}
	pos, err := parseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		printHelp(c, cmd, chain)
		return 0
	}
	if err == nil {
		err = cmd.Run(c, fs, pos)
	}
	if err != nil {
		var ee ExitError
		if errors.As(err, &ee) {
			return ee.Code
		}
		fmt.Fprintf(stderr, "%s %v\n", c.P.S("error:", tu.Red, tu.Bold), err)
		var ue usageError
		if errors.As(err, &ue) || strings.HasPrefix(err.Error(), "flag provided") {
			fmt.Fprintln(stderr)
			printHelp(&Ctx{Out: stderr, P: c.P}, cmd, chain)
		}
		return 2
	}
	return 0
}

func printHelp(c *Ctx, cmd *Command, chain []string) {
	p, w := c.P, c.Out
	name := strings.Join(chain, " ")
	if cmd.Short != "" {
		fmt.Fprintf(w, "%s — %s\n\n", p.S(name, tu.Bold), cmd.Short)
	}
	if cmd.Long != "" {
		fmt.Fprintln(w, strings.TrimSpace(cmd.Long))
		fmt.Fprintln(w)
	}
	syn := name
	if len(cmd.Sub) > 0 && cmd.Run == nil {
		syn += " <command>"
	}
	if cmd.Args != "" {
		syn += " " + cmd.Args
	}
	if cmd.Flags != nil {
		syn += " [flags]"
	}
	fmt.Fprintf(w, "%s\n  %s\n", p.S("Usage:", tu.Bold), syn)
	if len(cmd.Sub) > 0 {
		fmt.Fprintf(w, "\n%s\n", p.S("Commands:", tu.Bold))
		subs := append([]*Command(nil), cmd.Sub...)
		if len(chain) > 1 {
			sort.Slice(subs, func(i, j int) bool { return subs[i].Name < subs[j].Name })
		}
		width := 0
		for _, s := range subs {
			width = max(width, len(s.Name))
		}
		for _, s := range subs {
			fmt.Fprintf(w, "  %s  %s\n", p.S(tu.Pad(s.Name, width), tu.Cyan), s.Short)
		}
	}
	if cmd.Flags != nil {
		fs := flag.NewFlagSet("", flag.ContinueOnError)
		cmd.Flags(fs)
		var lines [][2]string
		fs.VisitAll(func(f *flag.Flag) {
			dash := "--"
			if len(f.Name) == 1 {
				dash = "-"
			}
			n := dash + f.Name
			if _, isBool := f.Value.(interface{ IsBoolFlag() bool }); !isBool {
				n += " " + valueName(f)
			}
			u := f.Usage
			if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "[]" {
				u += p.S(" (default "+f.DefValue+")", tu.Gray)
			}
			lines = append(lines, [2]string{n, u})
		})
		width := 0
		for _, l := range lines {
			width = max(width, len(l[0]))
		}
		fmt.Fprintf(w, "\n%s\n", p.S("Flags:", tu.Bold))
		for _, l := range lines {
			fmt.Fprintf(w, "  %s  %s\n", p.S(tu.Pad(l[0], width), tu.Cyan), l[1])
		}
	}
	if len(chain) == 1 {
		fmt.Fprintf(w, "\n%s\n", p.S("Global flags:", tu.Bold))
		fmt.Fprintln(w, "  -C, --root DIR   project directory (default: search upward from cwd)")
		fmt.Fprintln(w, "  --offline        never clone/fetch remote targets automatically")
		fmt.Fprintln(w, "  --no-color       disable colors (also honours NO_COLOR)")
		fmt.Fprintln(w, "  --color          force colors")
		fmt.Fprintf(w, "\nRun %s for details on a command.\n", p.S("pacemaker help <command>", tu.Bold))
	}
}

func valueName(f *flag.Flag) string {
	n, _ := flag.UnquoteUsage(f)
	if n == "" || n == "value" {
		return "VALUE"
	}
	return strings.ToUpper(n)
}

// globMatch matches s against a shell-style glob (or exact string).
func globMatch(pattern, s string) bool {
	if pattern == s {
		return true
	}
	ok, _ := path.Match(pattern, s)
	return ok
}
