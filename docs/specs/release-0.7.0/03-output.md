# 0.7.0 Feature 3: consistent output and color (P2 package, P6 migration)

Source: `cbctl-output-color-design.md`. This file maps that design onto every command.

## 1. Inventory of output paths today

| Sink | Where | Stream | Colored today |
|---|---|---|---|
| `logging.Info/Warning/Error/Debug` (`[lvl] - msg`, redacted) | `internal/logging` used by `cli/*` and by `rest/provision.go`, `capellaapi/{client,retry}.go`, `capella/*.go`, `secrets/files.go` | Info → stdout (stderr in `-o json`, through `restoreLogging`). Others → stderr. | Yes, via `fatih/color`. The decision uses **stdout** only (`color.NoColor` is set once from `isatty(os.Stdout)` + `NO_COLOR` + `TERM=dumb`; verified in v1.19.0 `color.go:22`). |
| `logging.Println` (data: `true`/`false`, `cluster map`) | `cluster.go`, `keyspace.go` | stdout | No |
| `printError` / `printAction` (`[error] - …`, `  → …`) | `cli/errors.go` `HandleError`, `run.go` panic | stderr | **No** (literal) |
| `printNotice` (`[warning] - …` + `  → …`), the warning-count line | `capella_env.go`, managed ops, sync, configure followup | stderr | No |
| Wizard lines (`[error] - answer y or n`, …) and prompts | `capella_init.go`, `capella_cluster_wizard*.go`, `capella_prompts.go` | stderr (`w.out`) | No |
| `Renderer` (`Table`, `Object`, `List`, `Line`, `Progress`, `secret(s)`) | `capella_output.go`, every new Capella command | stdout (JSON: one document) | No (data) |
| `progressWriter` (engine progress lines + inline dots) | `capella_env.go` `newRunContext` | stderr; dots only on TTY, not `CI`, not `TERM=dumb` | No |
| `rt.note` | `capella_env.go` | stderr unless `--quiet` | No |
| Plan text (`plan.RenderPlanText`, `RenderSummaryTable`) with symbols `+ ~ ± - = ! …` and `→` | `internal/plan/render.go` | stdout | No |
| Guide topics (`capella_guide.go`) | stdout | — | asserted ANSI-free off TTY |
| `--version`, help, completion | cobra | stdout | No |
| Raw `fmt.Fprint*` in `cli` (≈ 150 call sites) | configure, migrate, doctor, init, wizard, sync, janitor, backup | mixed | No |

Per command group (P6 migrates each row; ✓ = already uses `Renderer` for its data):

| Group | Success lines → `[ok]` | Stays `[info]` | Data on stdout (never colored) |
|---|---|---|---|
| `cluster create/join/add/rebalance/test` | `Cluster created on …`, `Node … added …`, `Arbiter node … added …`, `Cluster rebalanced`, `Cluster test passed` | `Cluster already configured`, `Node already configured`, `Connected to …` | — |
| `cluster exists/map` | — | — | `true`/`false`, map text |
| `bucket/scope/collection create`, `keyspace create`, `import` | `Bucket "…" created`, `Scope …`, `Collection …`, **`Keyspace "…" created`** (missing from the design list; added here), `Imported N documents` | — | `exists` → `true`/`false` |
| `import` non-empty | — | `[warning] - Collection not empty` (exit 0) stays | — |
| `capella` legacy (`cluster create`, `bucket create`, `scope/collection create`, `import`, `cluster test`) | same texts as Server | same | — |
| `capella` resources (✓) | `… created`/`… deleted`/`… updated` notes where printed as log lines | `… accepted for cluster …`, `waiting …`, `already exists` | tables, `key: value`, JSON |
| `capella plan/apply/drift/sync/validate/destroy/init` (✓) | `Apply complete in …`, `capella.yaml is valid: …`, `Wrote capella.yaml` | `No changes. Capella matches capella.yaml.` | plan text, JSON |
| `configure` / `configure migrate` / `doctor` | `Saved profile …`, `Removed profile …`, `Migrated …` | `nothing to remove`, `Next: …` | `show`/`list` tables, JSON |
| `apply -f` (new) | per 02b §2 | per 02b §2 | JSON report |
| errors (all) | — | — | stderr `[error]` + action |

## 2. Decisions (design §10 open decisions, recommended)

1. **Stream: Option B.** `[ok]` and `[info]` stay on stdout in text mode (and on stderr in `-o json`, as today). The README already documents these strings, and the cbctl rule tells agents to read them. Option A would be a second breaking change in the same release. Revisit in 1.0.
2. `FORCE_COLOR` is accepted as an alias of `CLICOLOR_FORCE`, at the same precedence. `FORCE_COLOR=0` means off.
3. **Replace `fatih/color`** with the internal SGR implementation. Its decision is global and keyed on stdout, so it cannot do per-stream checks or `--color`. `mattn/go-colorable` stays an indirect dependency only if Windows legacy consoles need it. The plan: enable VT processing via `golang.org/x/sys/windows` (`SetConsoleMode` + `ENABLE_VIRTUAL_TERMINAL_PROCESSING`) in `output/term_windows.go`. If that fails, no color.
4. `[debug]` is dim (`2`).
5. **ASCII only** (design §1: "no Unicode symbols"). Today the code emits `→` (action lines, plan changes), `·` (plan header, `--version` licence line), `±` and `…` (plan symbols) and `••••` (`redact.MaskTail` on a TTY). Recommended: keep the Unicode glyphs on a color-capable TTY and switch to ASCII (`->`, `-`, `+/-`, `...`, `****`) whenever color is off, i.e. pipes and logs. This keeps the TTY look and makes captured output plain ASCII. Q3 asks whether the user wants ASCII everywhere instead. The `--version` licence line stays as is (a contract tested by `cmd/cbctl/main_test.go`) unless the user picks ASCII-everywhere.

## 3. File and module structure

```
internal/output/                 (new; stdlib + golang.org/x/term (+x/sys on windows); imports redact)
  doc.go
  color.go        ColorMode, ParseColorMode, Decider (per-writer decision, env snapshot)
  label.go        Level, Label(w, lvl), SGR table
  printer.go      Printer: OK/Info/Warn/Error/Debug/Action/Notice/Data, level filtering
  render.go       Renderer (moved from cli/capella_output.go: Table, Object, List, Line, Secret(s))
  progress.go     Progress (moved from capella_env.go progressWriter)
  glyph.go        Glyphs{Arrow, Dot, ...} chosen by Decider (decision 5)
  term_windows.go / term_other.go
  *_test.go, testdata/golden/{level}_{color|plain}.txt, render/*.golden
internal/logging/logging.go      (changed) thin shim: package funcs delegate to a process Printer;
                                 kept so rest/capellaapi/capella/secrets need no import change in P2
internal/cli/
  root.go         --color persistent flag; PersistentPreRunE builds the Printer from Deps
  deps.go         Deps.IsTerminal func(any) bool (readers and writers); Deps.Printer (built)
  errors.go       printError/printAction -> Printer.Error/Action
  capella_env.go  printNotice -> Printer.Notice; progressWriter -> output.Progress
  capella_output.go  Renderer moved; file keeps confirm/prompter helpers
  capella_init.go, capella_*wizard*.go, capella_prompts.go   labeled lines via Printer
  (P6) cluster.go, keyspace*.go, capella.go, capella_legacy_sdk.go, configure*.go, ... Info->OK
internal/plan/render.go          glyphs injected (RenderOptions{Glyphs})
internal/redact/redact.go        MaskTail takes the decision instead of reading NO_COLOR itself
.cursor/rules/cli-logging.mdc    (P6) rewritten: [ok] level, plain info, internal SGR, per-stream
```

## 4. Interfaces and signatures

```go
package output

type ColorMode int
const (ColorAuto ColorMode = iota; ColorAlways; ColorNever)
func ParseColorMode(s string) (ColorMode, error) // "auto"|"always"|"never"; else error -> exit 3

// Decider decides color per writer. Env is snapshotted once per invocation (hermetic tests).
type Decider struct {
	Mode   ColorMode
	Env    func(string) string
	IsTerm func(w any) bool
}
func (d Decider) Color(w io.Writer) bool // precedence exactly as design §4, FORCE_COLOR alias

type Level int
const (LevelError Level = iota; LevelWarn; LevelInfo; LevelDebug) // same order as logging today
func Label(d Decider, w io.Writer, kind string) string           // "[ok]" or "\x1b[1;32m[ok]\x1b[0m"

// Printer writes labeled lines. All text is redacted. Safe for concurrent use (one mutex).
type Printer struct{ /* out, err io.Writer; d Decider; level Level; json bool; quiet bool */ }
func New(out, err io.Writer, d Decider, opts ...Option) *Printer
func WithLevel(Level) Option; func WithJSON(bool) Option; func WithQuiet(bool) Option
func (p *Printer) OK(format string, a ...any)    // stdout (stderr when json); suppressed below Info
func (p *Printer) Info(format string, a ...any)  // same stream rules as OK
func (p *Printer) Warn(format string, a ...any)  // stderr
func (p *Printer) Error(format string, a ...any) // stderr, never suppressed
func (p *Printer) Debug(format string, a ...any) // stderr
func (p *Printer) Action(text string)            // "  -> text" / "  → text", stderr
func (p *Printer) Notice(what, action string)    // Warn + Action
func (p *Printer) Data(format string, a ...any)  // stdout, plain, never suppressed (true/false)
func (p *Printer) Renderer() Renderer
func (p *Printer) Progress() *Progress
func (p *Printer) Glyphs() Glyphs

package logging // shim
func SetPrinter(p *output.Printer) // Run installs the invocation's printer; restored on exit
// Info, Warning, Error, Debug, Println keep their signatures; add Success(format, a...) -> p.OK
```

Wiring: `cli.Run` and `Execute` build the `Printer` in the root `PersistentPreRunE`, after flag parse (`--color`, plus Capella's `--log-level`/`-q`/`-o`), from `Deps.Stdout/Stderr/Env/IsTerminal`. It is stored in the command context (`printerFrom(cmd)`). `logging.SetPrinter` keeps non-cli packages working. Errors raised before `PersistentPreRunE` (flag errors) use a default `Printer` with `ColorAuto`. `runMu` already serialises the package-level shim.

## 5. Data model

- Line grammar (stable, tested): `LABEL " - " MESSAGE "\n"`, where `LABEL ∈ {[ok],[info],[warning],[error],[debug]}`, optionally wrapped as `ESC[<sgr>m … ESC[0m`. Action line: two spaces, glyph, space, text.
- SGR: ok `1;32`, info none, warning `1;33`, error `1;31`, debug `2`. Only the label is colored. Every colored label ends with `\x1b[0m`.
- JSON mode: `Decider.Color` returns false for stdout. Labeled lines go to stderr (as today) and may be colored per stderr's own decision. The JSON document itself is never colored.

## 6. Stable contracts (must not change; each has a test in P2/P6)

| Contract | Test |
|---|---|
| `cluster/bucket/scope/collection exists` prints exactly `true\n` / `false\n`, even with `--color=always` and `CLICOLOR_FORCE=1` | `TestExistsOutputIsPlainData` (table across envs) |
| `-o json` stdout is one parseable document with no `\x1b[` under any env or flag | extend existing JSON tests + `TestJSONNeverColored` |
| Generated password printed exactly once, unredacted, on stdout (`password: …` / `"password"`) after a `[warning]` | existing `printSecretOnce` tests + new color variants |
| `Cluster already configured`, `Node already configured`: `[info]`, exit 0. `Collection not empty`: `[warning]`, exit 0 | goldens |
| Exit codes per command unchanged | existing suites |
| `plan`/`drift` text with color off is unchanged apart from decision 5 glyphs | plan goldens regenerated once, reviewed |

## 7. Error handling

- `--color=bogus` → `CLIError{Code: 3, Message: "--color must be auto, always or never (got \"bogus\")", Action: "run 'cbctl --help'"}`.
- A write error to stdout or stderr is ignored (matches the existing `errcheck` exclusion). EPIPE on stdout (e.g. `| head`): the process exits with the command's own code. A test makes sure there is no panic.
- Windows VT enable failure → color off, with no message (a debug line).
- An invalid `CBCTL_QUIET` warns, as today.

## 8. Edge cases

- stdout piped and stderr a TTY (and the reverse): decided independently (test with fake `IsTerm`).
- `TERM=dumb` with `--color=always`: the flag wins (precedence 1).
- `NO_COLOR=""` (set but empty) does not disable color, per no-color.org.
- `CI=true`: color still follows the TTY rule. Progress dots stay off under CI (existing rule).
- Concurrent writes (engine progress while the waiter ticks): the `Printer` mutex keeps lines whole. An open inline progress line is closed before any labeled line (existing `progressWriter.close` behaviour, kept).
- Redaction runs **before** wrapping the label, so a secret can never be split by SGR codes.
- The `-q` + `[ok]` interaction: `[ok]` follows the `[info]` level (`-q` hides both). Generated passwords and data are never hidden.

## 9. Breaking changes (CHANGELOG `### Changed` with a **Breaking** marker)

1. Success lines use `[ok]` instead of `[info]` (list in §1). Mitigation: scripts should use exit codes or `-o json`.
2. Info is no longer blue. Errors and warnings from `HandleError`, notices and the wizard are now colored on a TTY.
3. If decision 5 is ASCII-off-TTY: piped plan output uses `+/-`, `...` and `->` instead of `±`, `…` and `→`.
4. New `CLICOLOR`/`CLICOLOR_FORCE`/`FORCE_COLOR` handling can turn color **on** in CI logs that set `FORCE_COLOR` (GitHub Actions does not set it by default).

## 10. Acceptance criteria

P2:
1. Table test for `Decider.Color` covers every precedence row (flag × `NO_COLOR` × `TERM` × `CLICOLOR_FORCE`/`FORCE_COLOR` × `CLICOLOR` × TTY), at least 20 rows.
2. Per-level goldens: exact bytes for color on and off.
3. Independent stream test (both directions).
4. `--color=always` colors piped output. `--color=never` strips it on a fake TTY. `--color=x` exits 3.
5. `printError`, `printNotice` and wizard lines carry colored labels on a fake TTY and plain labels otherwise.
6. No visible text change except color (golden diff of the whole hermetic suite with color off is empty).

P6:
7. `rg 'logging\.Info\(' internal/cli` shows no success message from the §1 list. `rg '\[(error|warning|info)\] -' internal --type go -g '!*_test.go'` matches only `internal/output` and the docgen port constants. The docgen constants stay plain: they rewrite documentation text, not terminal output.
8. `fatih/color` is gone from `go.mod`. `THIRD_PARTY_NOTICES.md` is updated. `make tidy-check` passes.
9. README documents the labels, `--color` and the env vars. `cli-logging.mdc` is rewritten. `cbctl.mdc` "Agent usage rules" mention `[ok]` lines.
10. Fixtures and the guide topics `ci.md` and `migrate.md` are updated for `[ok]`. `make docs-check` passes.
