package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ryangarber/pacemaker/internal/config"
	"github.com/ryangarber/pacemaker/internal/engine"
	"github.com/ryangarber/pacemaker/internal/lang"
	"github.com/ryangarber/pacemaker/internal/model"
	"github.com/ryangarber/pacemaker/internal/report"
	"github.com/ryangarber/pacemaker/internal/report/text"
	"github.com/ryangarber/pacemaker/internal/source"
	tu "github.com/ryangarber/pacemaker/internal/textutil"
)

const markerHelp = `
Annotate local code with markers inside any comment syntax:

  // @pace:begin auth.login upstream:lib/src/auth.dart#AuthService.login
  ...ported code...
  // @pace:end auth.login

  # @pace:file lib/models.dart                      (whole local file)
  /* @pace:skip ref=lib/legacy.dart reason="n/a" */ (intentionally not ported)

References: [target:]path[#L10-L42 | #Symbol.name]. Symbols are resolved by
language modules (see ` + "`pacemaker modules`" + `).`

func rootCommand() *Command {
	return &Command{
		Name:  "pacemaker",
		Short: "keep ported code in step with the repository it came from",
		Long:  markerHelp,
		Sub: []*Command{
			initCmd(), fetchCmd(), statusCmd(), lsCmd(), showCmd(), diffCmd(), acceptCmd(),
			coverageCmd(), treeCmd(), reportCmd(), checkCmd(), scanCmd(), pruneCmd(),
			configCmd(), targetCmd(), filterCmd(), modulesCmd(), versionCmd(),
		},
	}
}

// ---------------------------------------------------------------- init

func initCmd() *Command {
	var name, rev string
	var inc, exc multi
	var force bool
	return &Command{
		Name: "init", Args: "[TARGET-URL-OR-PATH]", Short: "create .pacemaker/ in the current directory",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "upstream", "name for the target")
			fs.StringVar(&rev, "rev", "", "branch, tag or commit to track")
			fs.Var(&inc, "include", "target include pattern (repeatable)")
			fs.Var(&exc, "exclude", "target exclude pattern (repeatable)")
			fs.BoolVar(&force, "force", false, "overwrite an existing config")
		},
		Run: func(c *Ctx, fs *flag.FlagSet, args []string) error {
			root := c.Root
			if root == "" {
				root = "."
			}
			root, _ = filepath.Abs(root)
			if _, err := os.Stat(config.Path(root)); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", config.Path(root))
			}
			cfg := config.Default()
			if len(args) > 0 {
				t, err := newTarget(args[0], rev, inc, exc)
				if err != nil {
					return err
				}
				cfg.Targets[name] = t
				cfg.DefaultTarget = name
			}
			if err := cfg.Save(root); err != nil {
				return err
			}
			gi := filepath.Join(root, config.Dir, ".gitignore")
			if err := os.WriteFile(gi, []byte("# local clones of remote targets\ncache/\n"), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(c.Out, "%s initialised %s\n", c.P.S("✓", tu.Green), c.P.S(config.Path(root), tu.Bold))
			if len(args) == 0 {
				fmt.Fprintf(c.Out, "  next: %s\n", c.P.S("pacemaker target add upstream <url-or-path>", tu.Cyan))
			} else {
				fmt.Fprintf(c.Out, "  target %s → %s\n", c.P.S(name, tu.Cyan), args[0])
			}
			fmt.Fprintf(c.Out, "  then annotate code with %s and run %s\n",
				c.P.S("// @pace:begin <id> <path>#L1-L20", tu.Cyan), c.P.S("pacemaker status", tu.Cyan))
			return nil
		},
	}
}

func newTarget(loc, rev string, inc, exc []string) (config.Target, error) {
	t := config.Target{Rev: rev, Filter: config.Filter{Include: inc, Exclude: exc}}
	if st, err := os.Stat(loc); err == nil && st.IsDir() && !source.LooksLikeURL(loc) {
		t.Path = loc
	} else if source.LooksLikeURL(loc) {
		t.URL = loc
	} else {
		return t, fmt.Errorf("%q is neither an existing directory nor a URL", loc)
	}
	return t, nil
}

// ---------------------------------------------------------------- fetch

func fetchCmd() *Command {
	return &Command{
		Name: "fetch", Args: "[TARGET...]", Short: "clone or update remote targets into the cache",
		Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
			e, err := c.Engine()
			if err != nil {
				return err
			}
			e.Log = c.Out
			return e.Fetch(args)
		},
	}
}

// ---------------------------------------------------------------- status / ls

func analyze(c *Ctx, opts engine.Options) (*engine.Engine, *model.Report, error) {
	e, err := c.Engine()
	if err != nil {
		return nil, nil, err
	}
	r, err := e.Analyze(opts)
	return e, r, err
}

func statusCmd() *Command {
	var targets multi
	var all bool
	return &Command{
		Name: "status", Aliases: []string{"st"}, Short: "summarise drift and coverage",
		Flags: func(fs *flag.FlagSet) {
			fs.Var(&targets, "target", "only these targets")
			fs.BoolVar(&all, "all", false, "list ok links too")
		},
		Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
			_, r, err := analyze(c, engine.Options{Coverage: true, Targets: targets})
			if err != nil {
				return err
			}
			text.Summary(c.Out, c.P, r)
			links := report.Visible(r, report.Options{ShowOK: all})
			if len(links) > 0 {
				fmt.Fprintln(c.Out)
				text.LinkTable(c.Out, c.P, links)
			}
			hints(c, r)
			return nil
		},
	}
}

func hints(c *Ctx, r *model.Report) {
	var h []string
	if r.Counts[model.New] > 0 {
		h = append(h, "review new links, then `pacemaker accept <block>` (or --status new)")
	}
	if r.Counts[model.Drifted] > 0 {
		h = append(h, "inspect drift with `pacemaker diff [block]`; accept once aligned")
	}
	if r.Counts[model.Moved] > 0 {
		h = append(h, "`pacemaker accept --status moved --rewrite` updates stale line refs")
	}
	if r.Counts[model.Cosmetic] > 0 {
		h = append(h, "cosmetic changes can be accepted with `pacemaker accept --status cosmetic`")
	}
	if len(h) == 0 && len(r.Problems) == 0 {
		fmt.Fprintf(c.Out, "\n%s\n", c.P.S("✓ everything is in step with upstream", tu.Green))
		return
	}
	fmt.Fprintln(c.Out)
	for _, s := range h {
		fmt.Fprintf(c.Out, "%s %s\n", c.P.S("hint:", tu.Gray), s)
	}
}

type linkFilter struct {
	statuses, targets, blocks, files multi
}

func (f *linkFilter) flags(fs *flag.FlagSet) {
	fs.Var(&f.statuses, "status", "filter by status (ok,moved,cosmetic,drifted,missing,new,error)")
	fs.Var(&f.targets, "target", "filter by target")
	fs.Var(&f.blocks, "block", "filter by block id glob")
	fs.Var(&f.files, "file", "filter by local or upstream file glob")
}

func (f *linkFilter) match(l *model.Link, args []string) bool {
	any := func(pats []string, vals ...string) bool {
		if len(pats) == 0 {
			return true
		}
		for _, p := range pats {
			for _, v := range vals {
				if globMatch(p, v) {
					return true
				}
			}
		}
		return false
	}
	return any(f.statuses, string(l.Status)) && any(f.targets, l.Target) &&
		any(append(append(multi{}, f.blocks...), args...), l.Block, l.LocalFile) &&
		any(f.files, l.LocalFile, l.Current, l.Path)
}

func lsCmd() *Command {
	var f linkFilter
	var asJSON bool
	return &Command{
		Name: "ls", Aliases: []string{"list"}, Args: "[BLOCK-GLOB...]", Short: "list links with filters",
		Flags: func(fs *flag.FlagSet) {
			f.flags(fs)
			fs.BoolVar(&asJSON, "json", false, "output JSON")
		},
		Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
			_, r, err := analyze(c, engine.Options{})
			if err != nil {
				return err
			}
			var links []*model.Link
			for _, l := range r.Links {
				if f.match(l, args) {
					links = append(links, l)
				}
			}
			if asJSON {
				enc := json.NewEncoder(c.Out)
				enc.SetIndent("", "  ")
				return enc.Encode(links)
			}
			if len(links) == 0 {
				fmt.Fprintln(c.Out, c.P.S("no matching links", tu.Gray))
				return nil
			}
			text.LinkTable(c.Out, c.P, links)
			return nil
		},
	}
}

// ---------------------------------------------------------------- show / diff

func selectLinks(r *model.Report, args []string, f *linkFilter) ([]*model.Link, error) {
	if f == nil {
		f = &linkFilter{}
	}
	var out []*model.Link
	for _, l := range r.Links {
		if f.match(l, args) {
			out = append(out, l)
		}
	}
	if len(out) == 0 && len(args) > 0 {
		return nil, fmt.Errorf("no links match %s", strings.Join(args, " "))
	}
	return out, nil
}

func showCmd() *Command {
	var noDiff bool
	return &Command{
		Name: "show", Args: "BLOCK-GLOB...", Short: "show everything known about blocks",
		Flags: func(fs *flag.FlagSet) { fs.BoolVar(&noDiff, "no-diff", false, "omit diffs") },
		Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
			if len(args) == 0 {
				return usagef("show needs at least one block id or glob")
			}
			_, r, err := analyze(c, engine.Options{Diffs: !noDiff, Commits: true})
			if err != nil {
				return err
			}
			links, err := selectLinks(r, args, nil)
			if err != nil {
				return err
			}
			for i, l := range links {
				if i > 0 {
					fmt.Fprintln(c.Out)
				}
				text.LinkDetail(c.Out, c.P, l, !noDiff)
			}
			return nil
		},
	}
}

func diffCmd() *Command {
	var f linkFilter
	var stat bool
	return &Command{
		Name: "diff", Args: "[BLOCK-GLOB...]", Short: "show upstream changes since blocks were accepted",
		Flags: func(fs *flag.FlagSet) {
			f.flags(fs)
			fs.BoolVar(&stat, "stat", false, "only show change sizes and findings")
		},
		Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
			_, r, err := analyze(c, engine.Options{Diffs: !stat, Commits: true})
			if err != nil {
				return err
			}
			links, err := selectLinks(r, args, &f)
			if err != nil {
				return err
			}
			n := 0
			for _, l := range links {
				if l.Status != model.Drifted && l.Status != model.Cosmetic {
					continue
				}
				if n > 0 {
					fmt.Fprintln(c.Out)
				}
				text.LinkDetail(c.Out, c.P, l, !stat)
				n++
			}
			if n == 0 {
				fmt.Fprintln(c.Out, c.P.S("no upstream changes in the selected links", tu.Green))
			}
			return nil
		},
	}
}

// ---------------------------------------------------------------- accept / prune

func acceptCmd() *Command {
	var f linkFilter
	var all, rewrite, dry bool
	return &Command{
		Name: "accept", Args: "[BLOCK-GLOB...]",
		Short: "record current upstream as the aligned baseline",
		Long: `Accept marks the selected links as aligned with upstream as it is now:
the upstream block is snapshotted into .pacemaker/snapshots and its hashes are
written to .pacemaker/state.yaml. Use it after adding a marker, and after you
have ported an upstream change.`,
		Flags: func(fs *flag.FlagSet) {
			f.flags(fs)
			fs.BoolVar(&all, "all", false, "accept every resolvable link")
			fs.BoolVar(&rewrite, "rewrite", false, "update stale line numbers / renamed paths in markers")
			fs.BoolVar(&dry, "dry-run", false, "show what would be accepted")
		},
		Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
			if len(args) == 0 && !all && len(f.statuses)+len(f.blocks)+len(f.files)+len(f.targets) == 0 {
				return usagef("name blocks to accept, use a filter, or pass --all")
			}
			e, r, err := analyze(c, engine.Options{})
			if err != nil {
				return err
			}
			picked, err := selectLinks(r, args, &f)
			if err != nil {
				return err
			}
			set := map[*model.Link]bool{}
			for _, l := range picked {
				set[l] = true
			}
			if dry {
				for _, l := range picked {
					fmt.Fprintf(c.Out, "%s %s → %s\n", c.P.StatusLabel(l.Status), c.P.S(l.Block, tu.Bold), l.Ref)
				}
				return nil
			}
			done, err := e.Accept(func(l *model.Link) bool { return set[l] }, rewrite)
			for _, l := range done {
				if l.Status == model.Missing || l.Status == model.Error {
					fmt.Fprintf(c.Out, "%s %s marker → %s %s\n", c.P.S("↪", tu.Cyan), c.P.S(l.Block, tu.Bold), l.SuggestedRef, c.P.S("(re-pointed; review, then accept again)", tu.Gray))
					continue
				}
				msg := ""
				if rewrite && l.SuggestedRef != "" {
					msg = c.P.S("  marker → "+l.SuggestedRef, tu.Cyan)
				}
				fmt.Fprintf(c.Out, "%s %s → %s %s%s\n", c.P.S("✓", tu.Green), c.P.S(l.Block, tu.Bold), l.Ref, c.P.S("(was "+string(l.Status)+")", tu.Gray), msg)
			}
			skipped := len(picked) - len(done)
			if skipped > 0 {
				fmt.Fprintf(c.Out, "%s %d link(s) could not be resolved and were skipped (missing/error)\n", c.P.S("!", tu.Yellow), skipped)
			}
			if len(done) == 0 && err == nil {
				fmt.Fprintln(c.Out, c.P.S("nothing accepted", tu.Gray))
			}
			return err
		},
	}
}

func pruneCmd() *Command {
	var dry bool
	return &Command{
		Name: "prune", Short: "drop state for blocks/refs no longer present in the source",
		Flags: func(fs *flag.FlagSet) { fs.BoolVar(&dry, "dry-run", false, "only list what would be removed") },
		Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
			e, err := c.Engine()
			if err != nil {
				return err
			}
			removed, err := e.Prune(dry)
			for _, r := range removed {
				fmt.Fprintf(c.Out, "%s %s\n", c.P.S("−", tu.Red), r)
			}
			if len(removed) == 0 {
				fmt.Fprintln(c.Out, c.P.S("state is clean", tu.Green))
			}
			return err
		},
	}
}

// ---------------------------------------------------------------- scan

func scanCmd() *Command {
	return &Command{
		Name: "scan", Short: "list markers found in the local source (no target access)",
		Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
			e, err := c.Engine()
			if err != nil {
				return err
			}
			blocks, probs, err := e.Scan()
			if err != nil {
				return err
			}
			t := tu.Table{Header: []string{"BLOCK", "KIND", "LOCAL", "REFERENCES"}}
			for _, b := range blocks {
				var refs []string
				for _, r := range b.Refs {
					refs = append(refs, r.Ref.String())
				}
				loc := fmt.Sprintf("%s:%d-%d", b.File, b.Start, b.End)
				if b.Kind == "skip" {
					loc = fmt.Sprintf("%s:%d", b.File, b.MarkerLine)
				}
				t.Add(c.P.S(b.ID, tu.Bold), string(b.Kind), c.P.S(loc, tu.Gray), strings.Join(refs, ", "))
			}
			t.Render(c.Out, c.P)
			fmt.Fprintf(c.Out, "\n%d block(s)", len(blocks))
			if len(probs) > 0 {
				fmt.Fprintf(c.Out, ", %s\n", c.P.S(fmt.Sprintf("%d problem(s)", len(probs)), tu.Red))
				for _, p := range probs {
					fmt.Fprintf(c.Out, "  %s %s\n", c.P.S(fmt.Sprintf("%s:%d", p.File, p.Line), tu.Bold), p.Message)
				}
				return ExitError{1}
			}
			fmt.Fprintln(c.Out)
			return nil
		},
	}
}

// ---------------------------------------------------------------- coverage / tree

func coverageCmd() *Command {
	var targets multi
	var files, symbols, uncovered bool
	var limit int
	return &Command{
		Name: "coverage", Aliases: []string{"cov"}, Args: "[FILE-GLOB...]", Short: "show how much of the target is mapped",
		Flags: func(fs *flag.FlagSet) {
			fs.Var(&targets, "target", "only these targets")
			fs.BoolVar(&files, "files", false, "per-file table")
			fs.BoolVar(&symbols, "symbols", false, "list unported symbols (uncovered/partial)")
			fs.BoolVar(&uncovered, "uncovered", false, "list uncovered line ranges")
			fs.IntVar(&limit, "limit", 0, "max files per table (0 = all)")
		},
		Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
			_, r, err := analyze(c, engine.Options{Coverage: true, Targets: targets})
			if err != nil {
				return err
			}
			if len(args) > 0 {
				files = true
			}
			for i, t := range r.Targets {
				if len(args) > 0 {
					var keep []model.FileCoverage
					for _, f := range t.Files {
						for _, a := range args {
							if globMatch(a, f.Path) || strings.HasPrefix(f.Path, strings.TrimSuffix(a, "/")+"/") {
								keep = append(keep, f)
								break
							}
						}
					}
					t.Files = keep
				}
				sort.SliceStable(t.Files, func(a, b int) bool { return t.Files[a].Percent() < t.Files[b].Percent() })
				r.Targets[i] = t
			}
			text.Summary(c.Out, c.P, &model.Report{Targets: r.Targets, Counts: r.Counts})
			for _, t := range r.Targets {
				if files {
					fmt.Fprintln(c.Out)
					text.FileTable(c.Out, c.P, t, limit)
				}
				if symbols || uncovered {
					printGaps(c, t, symbols, uncovered)
				}
			}
			return nil
		},
	}
}

func printGaps(c *Ctx, t model.TargetReport, symbols, ranges bool) {
	fmt.Fprintf(c.Out, "\n%s %s\n", c.P.S("gaps", tu.Bold), c.P.S(t.Name, tu.Cyan))
	for _, f := range t.Files {
		var lines []string
		if symbols {
			for _, s := range f.Symbols {
				if s.State != "uncovered" && s.State != "partial" {
					continue
				}
				st := c.P.S("○", tu.Red)
				if s.State == "partial" {
					st = c.P.S("◐", tu.Yellow)
				}
				exp := ""
				if !s.Exported {
					exp = c.P.S(" private", tu.Gray)
				}
				lines = append(lines, fmt.Sprintf("    %s %s %s%s %s", st, s.Name, c.P.S(s.Kind, tu.Gray), exp, c.P.S(fmt.Sprintf("L%d-L%d", s.Start, s.End), tu.Gray)))
			}
		}
		if ranges && len(f.Uncovered) > 0 {
			var rs []string
			for _, r := range f.Uncovered {
				rs = append(rs, r.String())
			}
			lines = append(lines, "    "+c.P.S("uncovered: ", tu.Gray)+strings.Join(rs, ", "))
		}
		if len(lines) > 0 {
			fmt.Fprintf(c.Out, "  %s %s\n%s\n", f.Path, c.P.Percent(f.Percent()), strings.Join(lines, "\n"))
		}
	}
}

type dirNode struct {
	name     string
	children map[string]*dirNode
	file     *model.FileCoverage
	sig, cov int
}

func treeCmd() *Command {
	var targets multi
	var depth int
	var hideFull bool
	return &Command{
		Name: "tree", Args: "[SUBDIR]", Short: "browse target coverage as a directory tree",
		Flags: func(fs *flag.FlagSet) {
			fs.Var(&targets, "target", "only these targets")
			fs.IntVar(&depth, "depth", 0, "max depth (0 = unlimited)")
			fs.BoolVar(&hideFull, "gaps", false, "hide fully covered entries")
		},
		Run: func(c *Ctx, _ *flag.FlagSet, args []string) error {
			_, r, err := analyze(c, engine.Options{Coverage: true, Targets: targets})
			if err != nil {
				return err
			}
			prefix := ""
			if len(args) > 0 {
				prefix = strings.Trim(args[0], "/") + "/"
			}
			for _, t := range r.Targets {
				if t.Error != "" {
					fmt.Fprintf(c.Out, "%s %s\n", c.P.S(t.Name, tu.Cyan), c.P.S(t.Error, tu.Red))
					continue
				}
				root := &dirNode{name: t.Name, children: map[string]*dirNode{}}
				for i := range t.Files {
					f := &t.Files[i]
					if prefix != "" && !strings.HasPrefix(f.Path, prefix) {
						continue
					}
					n := root
					parts := strings.Split(strings.TrimPrefix(f.Path, prefix), "/")
					n.sig += f.Significant
					n.cov += f.Covered + f.Waived
					for k, part := range parts {
						ch := n.children[part]
						if ch == nil {
							ch = &dirNode{name: part, children: map[string]*dirNode{}}
							n.children[part] = ch
						}
						ch.sig += f.Significant
						ch.cov += f.Covered + f.Waived
						if k == len(parts)-1 {
							ch.file = f
						}
						n = ch
					}
				}
				fmt.Fprintf(c.Out, "%s %s %s\n", c.P.S(t.Name+":"+prefix, tu.Cyan, tu.Bold), c.P.Percent(pctOf(root.cov, root.sig)), tu.Bar(c.P, pctOf(root.cov, root.sig), 0, 16))
				printTree(c, root, "", 1, depth, hideFull)
			}
			return nil
		},
	}
}

func pctOf(cov, sig int) float64 {
	if sig == 0 {
		return 100
	}
	return 100 * float64(cov) / float64(sig)
}

func printTree(c *Ctx, n *dirNode, indent string, level, maxDepth int, hideFull bool) {
	var names []string
	for k, ch := range n.children {
		if hideFull && ch.cov >= ch.sig {
			continue
		}
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := n.children[names[i]], n.children[names[j]]
		if (a.file == nil) != (b.file == nil) {
			return a.file == nil // directories first
		}
		return names[i] < names[j]
	})
	for i, k := range names {
		ch := n.children[k]
		last := i == len(names)-1
		branch, next := "├── ", "│   "
		if last {
			branch, next = "└── ", "    "
		}
		pct := pctOf(ch.cov, ch.sig)
		label := k
		extra := ""
		if ch.file == nil {
			label = c.P.S(k+"/", tu.Blue, tu.Bold)
		} else {
			if ch.file.Links > 0 {
				extra = c.P.S(fmt.Sprintf(" %d link(s)", ch.file.Links), tu.Gray)
			}
			if n := countUnported(ch.file); n > 0 {
				extra += c.P.S(fmt.Sprintf(" %d unported symbol(s)", n), tu.Yellow)
			}
		}
		fmt.Fprintf(c.Out, "%s%s%s %s %s%s\n", c.P.S(indent+branch, tu.Gray), tu.Pad(label, 0), "", c.P.Percent(pct), tu.Bar(c.P, pct, 0, 10), extra)
		if ch.file == nil && (maxDepth <= 0 || level < maxDepth) {
			printTree(c, ch, indent+next, level+1, maxDepth, hideFull)
		}
	}
}

func countUnported(f *model.FileCoverage) int {
	n := 0
	for _, s := range f.Symbols {
		if s.State == "uncovered" || s.State == "partial" {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- report / check

func reportCmd() *Command {
	var format, out string
	var all, noDiff, noFiles bool
	var targets multi
	return &Command{
		Name: "report", Short: "write a report (markdown, html, json, text, ...)",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&format, "format", "", "report format (see `pacemaker modules`); inferred from -o")
			fs.StringVar(&out, "o", "", "output file (default stdout)")
			fs.BoolVar(&all, "all", false, "include ok links")
			fs.BoolVar(&noDiff, "no-diff", false, "omit diffs")
			fs.BoolVar(&noFiles, "no-files", false, "omit per-file coverage")
			fs.Var(&targets, "target", "only these targets")
		},
		Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
			e, err := c.Engine()
			if err != nil {
				return err
			}
			if out == "" {
				out = e.Cfg.Report.Output
			}
			r, err := pickRenderer(format, out, e.Cfg.Report.Format)
			if err != nil {
				return err
			}
			rep, err := e.Analyze(engine.Options{Diffs: !noDiff, Commits: true, Coverage: true, Targets: targets})
			if err != nil {
				return err
			}
			o := report.Options{ShowOK: all, Diffs: !noDiff, Files: !noFiles, Symbols: !noFiles}
			return writeReport(c, r, rep, o, out)
		},
	}
}

func pickRenderer(format, out, def string) (report.Renderer, error) {
	if format != "" {
		return report.Get(format)
	}
	if out != "" {
		if r, ok := report.ForFile(out); ok {
			return r, nil
		}
	}
	if def == "" {
		def = "markdown"
	}
	return report.Get(def)
}

func writeReport(c *Ctx, r report.Renderer, rep *model.Report, o report.Options, out string) error {
	if out == "" || out == "-" {
		o.Color = c.P.On
		return r.Render(c.Out, rep, o)
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, rep, o); err != nil {
		return err
	}
	if dir := filepath.Dir(out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(c.Err, "%s wrote %s report to %s\n", c.P.S("✓", tu.Green), r.Name(), out)
	return nil
}

func checkCmd() *Command {
	var format, out string
	return &Command{
		Name: "check", Short: "CI gate: exit 1 if anything needs attention",
		Long: `Exits 1 when marker problems exist or any link has a status listed in
drift.fail_on (default: drifted, missing, new, error; cosmetic when
drift.cosmetic=fail). Optionally writes a report as well.`,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&format, "format", "", "also render a report in this format")
			fs.StringVar(&out, "o", "", "report output file")
		},
		Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
			e, r, err := analyze(c, engine.Options{Diffs: true, Commits: true, Coverage: true})
			if err != nil {
				return err
			}
			if format != "" || out != "" {
				rn, err := pickRenderer(format, out, e.Cfg.Report.Format)
				if err != nil {
					return err
				}
				if err := writeReport(c, rn, r, report.Options{Diffs: true, Files: true, Symbols: true}, out); err != nil {
					return err
				}
				if out == "" {
					return failIf(r)
				}
			}
			text.Summary(c.Out, c.P, r)
			if links := report.Visible(r, report.Options{}); len(links) > 0 {
				fmt.Fprintln(c.Out)
				text.LinkTable(c.Out, c.P, links)
			}
			if r.Failing {
				fmt.Fprintf(c.Out, "\n%s\n", c.P.S("✗ check failed", tu.Red, tu.Bold))
			} else {
				fmt.Fprintf(c.Out, "\n%s\n", c.P.S("✓ check passed", tu.Green, tu.Bold))
			}
			return failIf(r)
		},
	}
}

func failIf(r *model.Report) error {
	if r.Failing {
		return ExitError{1}
	}
	return nil
}

// ---------------------------------------------------------------- modules / version

func modulesCmd() *Command {
	return &Command{
		Name: "modules", Short: "list language modules and report formats",
		Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
			fmt.Fprintln(c.Out, c.P.S("Language modules", tu.Bold))
			t := tu.Table{Header: []string{"ID", "EXTENSIONS", "CAPABILITIES"}, Indent: "  "}
			for _, l := range append(lang.All(), lang.Fallback()) {
				ext := strings.Join(l.Extensions(), " ")
				if ext == "" {
					ext = c.P.S("(fallback)", tu.Gray)
				}
				t.Add(c.P.S(l.ID(), tu.Cyan), ext, strings.Join(lang.Capabilities(l), ", "))
			}
			t.Render(c.Out, c.P)
			fmt.Fprintln(c.Out)
			fmt.Fprintln(c.Out, c.P.S("Report formats", tu.Bold))
			t = tu.Table{Header: []string{"NAME", "EXTENSIONS", "DESCRIPTION"}, Indent: "  "}
			for _, r := range report.All() {
				t.Add(c.P.S(r.Name(), tu.Cyan), strings.Join(r.Extensions(), " "), r.Description())
			}
			t.Render(c.Out, c.P)
			return nil
		},
	}
}

func versionCmd() *Command {
	return &Command{
		Name: "version", Short: "print version",
		Run: func(c *Ctx, _ *flag.FlagSet, _ []string) error {
			fmt.Fprintln(c.Out, "pacemaker", Version)
			return nil
		},
	}
}
