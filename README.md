# pacemaker

Keep ported code in step with the repository it came from.

pacemaker maps blocks of your code to blocks of code in a **target** (upstream) repository
using source-map-like comments. It tracks how much of the target you've covered, and tells you
when the target changes in ways that make your port stale.

```ts
// @pace:begin auth.login upstream:lib/src/auth.dart#AuthService.login
export async function login(user: string, pass: string) { ... }
// @pace:end
```

```
$ pacemaker status
links  ▲ 2 drifted  ·  ≈ 1 cosmetic  ·  ↪ 1 moved  ·  ✓ 1 ok
target upstream ../upstream @a2e0b9087c
        ██████████████▒░░░░░░░░░  64.0% lines   44.4% symbols (4/9)  2/2 files touched

STATUS      BLOCK        UPSTREAM                                      LOCAL            DETAIL
▲ drifted   auth.login   upstream:lib/src/auth.dart#AuthService.login  src/auth.ts:2    [breaking] upstream content changed…
▲ drifted   http.client  web:web/client.ts#HttpClient                  src/http.dart:1  [breaking] upstream content changed…
≈ cosmetic  util.clamp   upstream:lib/src/util.dart#clamp              src/util.py:1    formatting/comment-only changes upstream
↪ moved     auth.logout  upstream:lib/src/auth.dart#L16-L18            src/auth.ts:10   content unchanged; reference is now …
```

## Install

```sh
go install github.com/ryangarber/pacemaker@latest
# or: go build -o pacemaker .
```

Requires `git` on `PATH` for git-backed targets (plain directories work without it).

## Quick start

```sh
pacemaker init https://github.com/acme/sdk-dart.git --include 'lib/**' --exclude '*.g.dart'
# ...add markers to your code...
pacemaker status            # what's new / drifted / covered
pacemaker accept --all      # record the current upstream as the baseline
git add .pacemaker && git commit -m "pacemaker baseline"

# later
pacemaker fetch             # update remote targets
pacemaker diff              # upstream changes since you last aligned, with semantic analysis
pacemaker accept auth.login # after porting the change
pacemaker check             # CI gate (exit 1 if anything needs attention)
```

## Markers

Markers are recognised anywhere on a line, so they work in any comment syntax
(`//`, `#`, `--`, `/* */`, `<!-- -->`, …). The token is configurable (`marker:` in config).

| Marker | Meaning |
|---|---|
| `@pace:begin <id> <ref>...` … `@pace:end [id]` | The lines between map to the reference(s). Nestable. |
| `@pace:file <ref>` or `@pace:file <id> <ref>...` | The whole local file maps to the reference(s). With a single argument it is the ref and the id defaults to the file path; with two or more, the first is the id (same order as `@pace:begin`). |
| `@pace:skip <ref>... reason="..."` | Upstream code intentionally **not** ported. Counts as *waived* coverage, and is still drift-tracked so you notice if it changes. Takes no positional id (use `id=`). |
| `@pace:todo <text> [ref=…] [prio=…] [owner=…] [tags=…]` | Open work: a known gap, a follow-up, a deviation to fix. Shown in every report. |
| `@pace:note <text> [ref=…]` | Information for whoever ports the next upstream change: intentional deviations, rationale. Shown in every report; never counts as open work. |

Key/value form is also accepted: `id=`, `ref=` (repeatable for many-to-one mappings),
`note="..."`, `tags=a,b`, `reason="..."`. Positional arguments are always `<id>` first, then `<ref>`s.

### Todos and notes

Porting from a moving target means tracking more than mappings: what's still missing, what you did differently on
purpose, and which of those notes upstream has since invalidated.

```ts
// @pace:begin auth.login upstream:lib/src/auth.dart#AuthService.login
export async function login(user: string, pass: string) {
  // @pace:todo surface server error messages prio=high owner=ana
  // @pace:note no offline cache here; the web build always has a network
}
// @pace:end

// @pace:todo port token refresh ref=lib/src/auth.dart#AuthService.refresh tags=auth
```

* Text is free-form; `key=value` attributes may appear anywhere: `ref=` (repeatable), `owner=`, `prio=`
  (`blocker`, `high`, `normal`, `low`; also `p0`–`p3`, `critical`, …), `tags=a,b`, and `block=<id>` to attach the
  entry to a block defined elsewhere.
* An entry inside `@pace:begin`/`@pace:end` (or in a file with `@pace:file`) belongs to that block. When the block
  **drifts or goes missing upstream**, its todos are flagged *needs review*: the plan may no longer match upstream.
* `ref=` points at upstream code and is resolved on every run. An entry whose ref no longer resolves is **stale**
  (the gap may have been closed, renamed or removed upstream). Uncovered upstream symbols referenced by a todo are
  shown as **planned** in coverage reports, which separates known gaps from ones nobody has noticed.
* Todos appear in `status`, `show`, `scan`, `coverage --symbols`, `tree`, and every report format (text, Markdown,
  HTML, JSON). `pacemaker todo` lists and filters them.
* By default todos never fail `check`. Opt in with `todo.fail_on`, for example
  `pacemaker config set todo.fail_on blocker,stale`. Accepted values are a priority, `any`, `stale`, and `review`.

### References

```
[target:]path[#L10-L42 | #Symbol.name]
```

* `lib/a.dart` — whole file in the default target
* `upstream:lib/a.dart#L10-L42` — a line range; relocated automatically when upstream inserts/removes lines above it
* `web:src/client.ts#HttpClient.request` — a symbol, resolved by a language module; immune to line shifts

## Statuses

| Status | Meaning |
|---|---|
| `ok` | Upstream block is byte-identical to what you accepted |
| `moved` | Same content, new location — `accept --rewrite` updates the marker's line numbers / path |
| `cosmetic` | Only formatting/comments changed (per the language module's normaliser) |
| `drifted` | Upstream content changed — see `pacemaker diff` |
| `missing` | File, symbol or lines are gone (renamed symbols are detected and suggested) |
| `new` | Never accepted |
| `error` | Target unavailable, or symbol refs into a language without a symbol module |

Each link also reports whether the **local** block was edited since the last accept (`✎`).

## Commands

| Command | |
|---|---|
| `init [URL\|PATH]` | Create `.pacemaker/` (`--name`, `--rev`, `--include`, `--exclude`) |
| `status` | Summary, coverage bars, and links needing attention (`--all` for everything) |
| `ls [GLOB]` | Filterable list (`--status`, `--target`, `--block`, `--file`, `--json`) |
| `show GLOB` | Everything about a block: baselines, ranges, findings, commits, diff |
| `diff [GLOB]` | Upstream changes since accepted, with semantic findings (`--stat`) |
| `accept [GLOB]` | Record current upstream as baseline (`--all`, `--status`, `--rewrite`, `--dry-run`) |
| `todo [TEXT\|BLOCK-GLOB]` | List todos (`--notes`, `--only-notes`, `--owner`, `--tag`, `--prio`, `--block`, `--file`, `--review`, `--stale`, `--json`) |
| `coverage [FILE-GLOB]` | Line + symbol coverage (`--files`, `--symbols`, `--uncovered`) |
| `tree [SUBDIR]` | Browse target coverage as a directory tree (`--depth`, `--gaps`) |
| `report` | Render a report: `-o report.html`, `--format md\|html\|json\|text` |
| `check` | CI gate; exits 1 on marker problems, statuses in `drift.fail_on`, or todos matching `todo.fail_on` |
| `scan` | List markers without touching targets |
| `prune` | Drop state for blocks/refs that no longer exist |
| `fetch [TARGET]` | Clone/update remote targets (also happens automatically on first use; `--offline` disables) |
| `config show\|get\|set\|unset\|keys\|path` | Dotted-key config editing with validation |
| `target list\|add\|set\|rm\|default` | Manage targets (URL or local path, optional `--rev`) |
| `filter add\|rm\|list include\|exclude PATTERN` | Manage include/exclude globs for a target (or `--local` for marker scanning) |
| `modules` | List language modules (with capabilities) and report formats |

Global flags: `-C DIR`, `--offline`, `--no-color` / `--color` (honours `NO_COLOR`).

## Files

```
.pacemaker/
  config.yaml          # targets, filters, drift policy       (commit)
  state.yaml           # accepted baselines: hashes, ranges, revisions (commit)
  snapshots/<block>/…  # upstream content as accepted, plaintext   (commit)
  cache/               # clones of remote targets               (gitignored)
```

Everything committed is plaintext, deterministic and sorted, so it diffs and merges cleanly. Snapshots make drift
diffs self-contained: they work for non-git targets and survive upstream history rewrites.

### Configuration

```yaml
version: 1
marker: '@pace'
default_target: upstream
local:                      # where markers are scanned for
  exclude: [.git/, node_modules/, build/, dist/, .dart_tool/, vendor/]
targets:
  upstream:
    url: https://github.com/acme/sdk-dart.git    # or path: ../sdk-dart
    rev: main                                     # branch / tag / commit (optional)
    include: ['lib/**']
    exclude: ['*.g.dart']
drift:
  cosmetic: warn            # ok | warn | fail
  fail_on: [drifted, missing, new, error]
  context: 3
  max_commits: 10
todo:
  fail_on: []               # any | blocker | high | normal | low | stale | review
report:
  format: markdown
```

Patterns are gitignore-like: `**` spans directories, a pattern without `/` matches in any directory, a trailing `/`
matches a directory. Only included, non-excluded target files count toward coverage.

## How drift detection works

1. **Locate** the block in the current upstream:
   * symbol refs → the language module's symbol table;
   * line refs → a Myers line diff between the file at the baseline revision and now maps the range
     (falls back to searching for the snapshot — exact, then fuzzy — for non-git targets);
   * renamed files are followed via git rename detection; renamed symbols are guessed by similarity.
2. **Compare** with the baseline: raw hash → `ok`/`moved`; normalised hash → `cosmetic`; otherwise `drifted`.
3. **Explain** drift: unified diff against the snapshot, upstream commits touching the file since the baseline,
   and (if the language module supports it) semantic findings.

## Architecture

The core is language- and format-agnostic; everything specific is a self-registering module.

```
main.go                       wires modules in via blank imports
internal/
  marker/    marker parsing (any comment syntax)
  ref/       reference grammar
  glob/      include/exclude matching
  diff/      Myers diff, range relocation, unified diffs
  source/    targets: remote URL (cached clone), local git, pinned rev, plain directory
  state/     state.yaml + snapshots
  config/    config.yaml + dotted-key get/set
  engine/    resolution, drift classification, coverage, accept/prune
  model/     format-agnostic report model
  lang/      language module interfaces + registry
    generic/       fallback: whitespace normalisation, blank-line-aware coverage
    clike/         shared tolerant lexer/structural parser for C-family languages
    dart/          Dart module
    javascript/    JavaScript/TypeScript module
  report/    renderer interface + registry
    text/ markdown/ html/ jsonfmt/
  cli/       command tree, pretty terminal output
```

### Language modules

A module implements `lang.Language` (`ID`, `Extensions`) and opts into any capability:

| Interface | Enables |
|---|---|
| `Normalizer` | `cosmetic` classification (Dart/JS ignore comments, whitespace, trailing commas, quote style; JS also semicolons) |
| `LineClassifier` | Coverage counts only code lines (not comments) |
| `SymbolProvider` | `#Symbol` refs, symbol coverage, rename guesses (see [JavaScript symbols](#javascript-symbols)) |
| `SemanticDiffer` | Findings: signature changes, removed/renamed/added members, visibility changes, import changes — with severities |

#### JavaScript symbols

Besides `class`/`function`/`const` declarations, the JavaScript/TypeScript module recognises symbols declared by
assignment and registration, so AngularJS 1.x, ES5, and CommonJS code can be referenced by symbol:

```js
(function () {                                            // IIFE bodies are parsed as top level
  angular.module('app')
    .controller('LoginCtrl', ['$scope', function ($scope) { // #LoginCtrl            (controller)
      $scope.login = function (name) { ... };              // #LoginCtrl.$scope.login
    }])
    .factory('Auth', function ($http) {                    // #Auth                 (factory)
      return { check: check, ping: function () {} };       // #Auth.check, #Auth.ping
      function check() { ... }
    });

  angular.module('app').controller('ProfileCtrl', ProfileCtrl);
  function ProfileCtrl() {                                 // #ProfileCtrl          (controller)
    var vm = this;
    vm.reset = function () { ... };                        // #ProfileCtrl.reset
    vm.save = save;                                        //  exposes ↓
    function save() { ... }                                // #ProfileCtrl.save     (exported)
  }
})();
Store.prototype.add = function (x) { ... };               // #Store.add
exports.makeStore = function () { ... };                   // #makeStore
```

Recognised registrations are `controller`, `service`, `factory`, `provider`, `directive`, `component`
(including its object's `controller`), `filter`, `decorator`, `animation`, `constant`, and `value`.
`$scope.x`/`$rootScope.x` members are found in any function body. `this.x` members (and aliases such as
`var vm = this`), inner functions, and returned object literals are found in registered units and in
Capitalized (constructor-style) functions. Suffixes resolve too, so `#$scope.login` works when it is unique.

Adding a language: create a package that calls `lang.Register(YourModule{})` in `init()`, and blank-import it in
`main.go`. C-family languages can reuse `clike` by supplying a `Dialect` (see `lang/dart`).

### Report formats

Implement `report.Renderer` (`Name`, `Description`, `Extensions`, `Render(w, *model.Report, Options)`) and call
`report.Register(r, aliases...)` in `init()`. Formats are picked by `--format` or inferred from the `-o` extension.

## Development

```sh
go test ./...
go vet ./...
```
