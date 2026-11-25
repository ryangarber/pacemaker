package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ryangarber/pacemaker/internal/config"
	tu "github.com/ryangarber/pacemaker/internal/textutil"
)

// withConfig loads the project config, runs fn, and saves if fn returns save=true.
func withConfig(c *Ctx, fn func(root string, cfg *config.Config) (save bool, err error)) error {
	start := c.Root
	if start == "" {
		start = "."
	}
	root, err := config.Find(start)
	if err != nil {
		return err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	save, err := fn(root, cfg)
	if err != nil || !save {
		return err
	}
	return cfg.Save(root)
}

func ok(c *Ctx, f string, a ...any) {
	fmt.Fprintf(c.Out, "%s %s\n", c.P.S("✓", tu.Green), fmt.Sprintf(f, a...))
}

// ---------------------------------------------------------------- config

func configCmd() *Command {
	return &Command{
		Name: "config", Short: "view and edit .pacemaker/config.yaml",
		Long: `Keys are dotted paths into the YAML, e.g.:
  drift.cosmetic            ok | warn | fail
  drift.fail_on             list of statuses that fail ` + "`check`" + `
  drift.context             diff context lines
  todo.fail_on              todo conditions that fail ` + "`check`" + `: any, blocker, high,
                            normal, low, stale, review (default: none)
  report.format             default report format
  local.exclude             local paths never scanned for markers
  targets.<name>.rev        branch/tag/commit to track
List values accept comma-separated input: ` + "`pacemaker config set drift.fail_on drifted,missing`",
		Sub: []*Command{
			{Name: "show", Short: "print the effective configuration", Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
				return withConfig(c, func(_ string, cfg *config.Config) (bool, error) {
					return false, printYAML(c, cfg)
				})
			}},
			{Name: "get", Args: "KEY", Short: "print one value", Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
				if len(args) != 1 {
					return usagef("config get KEY")
				}
				return withConfig(c, func(_ string, cfg *config.Config) (bool, error) {
					v, err := cfg.Get(args[0])
					if err != nil {
						return false, err
					}
					switch x := v.(type) {
					case map[string]any, []any:
						return false, printYAML(c, x)
					default:
						fmt.Fprintln(c.Out, x)
					}
					return false, nil
				})
			}},
			{Name: "set", Args: "KEY VALUE", Short: "set a value (validated before saving)", Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
				if len(args) < 2 {
					return usagef("config set KEY VALUE")
				}
				return withConfig(c, func(_ string, cfg *config.Config) (bool, error) {
					if err := cfg.Set(args[0], strings.Join(args[1:], " ")); err != nil {
						return false, err
					}
					ok(c, "%s = %s", c.P.S(args[0], tu.Bold), strings.Join(args[1:], " "))
					return true, nil
				})
			}},
			{Name: "unset", Args: "KEY", Short: "remove a value (reverts to default)", Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
				if len(args) != 1 {
					return usagef("config unset KEY")
				}
				return withConfig(c, func(_ string, cfg *config.Config) (bool, error) {
					if err := cfg.Unset(args[0]); err != nil {
						return false, err
					}
					ok(c, "unset %s", args[0])
					return true, nil
				})
			}},
			{Name: "keys", Short: "list all settable keys with current values", Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
				return withConfig(c, func(_ string, cfg *config.Config) (bool, error) {
					t := tu.Table{}
					for _, k := range cfg.Keys() {
						v, _ := cfg.Get(k)
						vs := fmt.Sprint(v)
						if b, err := json.Marshal(v); err == nil {
							vs = string(b)
						}
						t.Add(c.P.S(k, tu.Cyan), vs)
					}
					t.Render(c.Out, c.P)
					return false, nil
				})
			}},
			{Name: "path", Short: "print the config file path", Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
				return withConfig(c, func(root string, _ *config.Config) (bool, error) {
					fmt.Fprintln(c.Out, config.Path(root))
					return false, nil
				})
			}},
		},
	}
}

func printYAML(c *Ctx, v any) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	if !c.P.On {
		_, err = c.Out.Write(b)
		return err
	}
	keyRe := regexp.MustCompile(`^(\s*-?\s*)([\w.-]+):`)
	for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		fmt.Fprintln(c.Out, keyRe.ReplaceAllString(l, "$1"+string(tu.Cyan)+"$2"+string(tu.Reset)+":"))
	}
	return nil
}

// ---------------------------------------------------------------- target

var targetNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func targetCmd() *Command {
	var rev string
	var inc, exc multi
	var makeDefault bool
	return &Command{
		Name: "target", Short: "manage upstream repositories (URL or local path)",
		Sub: []*Command{
			{Name: "list", Aliases: []string{"ls"}, Short: "list targets", Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
				return withConfig(c, func(_ string, cfg *config.Config) (bool, error) {
					if len(cfg.Targets) == 0 {
						fmt.Fprintln(c.Out, c.P.S("no targets — add one with `pacemaker target add NAME URL|PATH`", tu.Gray))
						return false, nil
					}
					t := tu.Table{Header: []string{"", "NAME", "KIND", "LOCATION", "REV", "INCLUDE", "EXCLUDE"}}
					for _, n := range cfg.TargetNames() {
						tg := cfg.Targets[n]
						mark, kind := " ", "path"
						if n == cfg.DefaultTarget {
							mark = c.P.S("*", tu.Green)
						}
						if tg.URL != "" {
							kind = "url"
						}
						rev := tg.Rev
						if rev == "" {
							rev = c.P.S("default", tu.Gray)
						}
						t.Add(mark, c.P.S(n, tu.Cyan, tu.Bold), kind, tg.Location(), rev,
							strings.Join(tg.Include, ", "), strings.Join(tg.Exclude, ", "))
					}
					t.Render(c.Out, c.P)
					return false, nil
				})
			}},
			{
				Name: "add", Args: "NAME URL|PATH", Short: "add a target",
				Flags: func(fs *flag.FlagSet) {
					fs.StringVar(&rev, "rev", "", "branch, tag or commit to track")
					fs.Var(&inc, "include", "include pattern (repeatable)")
					fs.Var(&exc, "exclude", "exclude pattern (repeatable)")
					fs.BoolVar(&makeDefault, "default", false, "make this the default target")
				},
				Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
					if len(args) != 2 {
						return usagef("target add NAME URL|PATH")
					}
					if !targetNameRe.MatchString(args[0]) {
						return fmt.Errorf("target names may only contain letters, digits, '-' and '_'")
					}
					return withConfig(c, func(_ string, cfg *config.Config) (bool, error) {
						if _, exists := cfg.Targets[args[0]]; exists {
							return false, fmt.Errorf("target %q already exists (use `target set`)", args[0])
						}
						t, err := newTarget(args[1], rev, inc, exc)
						if err != nil {
							return false, err
						}
						cfg.Targets[args[0]] = t
						if makeDefault || cfg.DefaultTarget == "" {
							cfg.DefaultTarget = args[0]
						}
						ok(c, "added target %s → %s", c.P.S(args[0], tu.Cyan), t.Location())
						return true, nil
					})
				},
			},
			{
				Name: "set", Args: "NAME [URL|PATH]", Short: "change a target's location or rev",
				Flags: func(fs *flag.FlagSet) { fs.StringVar(&rev, "rev", "", "branch, tag or commit to track (\"-\" clears)") },
				Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
					if len(args) < 1 || len(args) > 2 {
						return usagef("target set NAME [URL|PATH] [--rev REV]")
					}
					return withConfig(c, func(_ string, cfg *config.Config) (bool, error) {
						t, exists := cfg.Targets[args[0]]
						if !exists {
							return false, fmt.Errorf("unknown target %q", args[0])
						}
						if len(args) == 2 {
							nt, err := newTarget(args[1], t.Rev, nil, nil)
							if err != nil {
								return false, err
							}
							t.URL, t.Path = nt.URL, nt.Path
						}
						if rev == "-" {
							t.Rev = ""
						} else if rev != "" {
							t.Rev = rev
						}
						cfg.Targets[args[0]] = t
						ok(c, "updated target %s", c.P.S(args[0], tu.Cyan))
						return true, nil
					})
				},
			},
			{Name: "rm", Aliases: []string{"remove"}, Args: "NAME", Short: "remove a target", Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
				if len(args) != 1 {
					return usagef("target rm NAME")
				}
				return withConfig(c, func(root string, cfg *config.Config) (bool, error) {
					if _, exists := cfg.Targets[args[0]]; !exists {
						return false, fmt.Errorf("unknown target %q", args[0])
					}
					delete(cfg.Targets, args[0])
					if cfg.DefaultTarget == args[0] {
						cfg.DefaultTarget = ""
					}
					_ = os.RemoveAll(cacheDirFor(root, args[0]))
					ok(c, "removed target %s", args[0])
					return true, nil
				})
			}},
			{Name: "default", Args: "NAME", Short: "set the default target", Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
				if len(args) != 1 {
					return usagef("target default NAME")
				}
				return withConfig(c, func(_ string, cfg *config.Config) (bool, error) {
					if _, exists := cfg.Targets[args[0]]; !exists {
						return false, fmt.Errorf("unknown target %q", args[0])
					}
					cfg.DefaultTarget = args[0]
					ok(c, "default target is now %s", c.P.S(args[0], tu.Cyan))
					return true, nil
				})
			}},
		},
	}
}

// ---------------------------------------------------------------- filter

func filterCmd() *Command {
	var target string
	var local bool
	scope := func(fs *flag.FlagSet) {
		fs.StringVar(&target, "target", "", "target to edit (default: the default target)")
		fs.BoolVar(&local, "local", false, "edit the local-source marker scan filter instead")
	}
	pick := func(cfg *config.Config) (*config.Filter, string, func(), error) {
		if local {
			return &cfg.Local, "local", func() {}, nil
		}
		name, err := cfg.ResolveTarget(target)
		if err != nil {
			return nil, "", nil, err
		}
		t := cfg.Targets[name]
		return &t.Filter, "target " + name, func() { cfg.Targets[name] = t }, nil
	}
	edit := func(add bool) func(c *Ctx, _ *flag.FlagSet, args []string) error {
		return func(c *Ctx, _ *flag.FlagSet, args []string) error {
			if len(args) < 2 || (args[0] != "include" && args[0] != "exclude") {
				return usagef("filter add|rm include|exclude PATTERN...")
			}
			return withConfig(c, func(_ string, cfg *config.Config) (bool, error) {
				f, label, commit, err := pick(cfg)
				if err != nil {
					return false, err
				}
				list := &f.Include
				if args[0] == "exclude" {
					list = &f.Exclude
				}
				for _, p := range args[1:] {
					if add {
						if !contains(*list, p) {
							*list = append(*list, p)
						}
					} else {
						*list = remove(*list, p)
					}
				}
				commit()
				verb := "added to"
				if !add {
					verb = "removed from"
				}
				ok(c, "%s %s %s %s", strings.Join(args[1:], ", "), verb, label, args[0])
				return true, nil
			})
		}
	}
	return &Command{
		Name: "filter", Short: "manage include/exclude patterns (target or local)",
		Long: `Patterns are gitignore-like globs: "**" spans directories, a pattern
without "/" matches in any directory, a trailing "/" matches a whole directory.
Only included, non-excluded target files count toward coverage.`,
		Sub: []*Command{
			{Name: "add", Args: "include|exclude PATTERN...", Short: "add patterns", Flags: scope, Run: edit(true)},
			{Name: "rm", Args: "include|exclude PATTERN...", Short: "remove patterns", Flags: scope, Run: edit(false)},
			{Name: "list", Aliases: []string{"ls"}, Short: "show patterns", Flags: scope, Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
				return withConfig(c, func(_ string, cfg *config.Config) (bool, error) {
					show := func(label string, f config.Filter) {
						fmt.Fprintln(c.Out, c.P.S(label, tu.Bold))
						if len(f.Include) == 0 {
							fmt.Fprintf(c.Out, "  %s %s\n", c.P.S("include", tu.Green), c.P.S("(everything)", tu.Gray))
						}
						for _, p := range f.Include {
							fmt.Fprintf(c.Out, "  %s %s\n", c.P.S("include", tu.Green), p)
						}
						for _, p := range f.Exclude {
							fmt.Fprintf(c.Out, "  %s %s\n", c.P.S("exclude", tu.Red), p)
						}
					}
					if local {
						show("local", cfg.Local)
						return false, nil
					}
					if target != "" {
						t, exists := cfg.Targets[target]
						if !exists {
							return false, fmt.Errorf("unknown target %q", target)
						}
						show("target "+target, t.Filter)
						return false, nil
					}
					show("local", cfg.Local)
					for _, n := range cfg.TargetNames() {
						show("target "+n, cfg.Targets[n].Filter)
					}
					return false, nil
				})
			}},
		},
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func remove(xs []string, x string) []string {
	var out []string
	for _, y := range xs {
		if y != x {
			out = append(out, y)
		}
	}
	return out
}

func cacheDirFor(root, name string) string {
	return strings.TrimSuffix(root, "/") + "/" + config.Dir + "/" + config.CacheDir + "/repos/" + name
}
