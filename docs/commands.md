# Commands Reference

Complete reference for all `fathom` CLI commands and flags.

## Overview

Fathom ships as a single `fathom` binary with the following commands:

| Command | Purpose |
|---------|---------|
| `fathom init` | Build the `.fathom/` symbol index for the current repo |
| `fathom analyze` | Analyze the blast radius of changes (files or base branch) |
| `fathom report` | Generate an HTML impact report (alias for `analyze --html`) |
| `fathom interactive` | Launch the interactive TUI wizard (auto-triggered by `analyze` in a TTY) |
| `fathom update` | Self-update to the latest GitHub release |
| `fathom uninstall` | Remove the binary and clean up PATH entries |
| `fathom --version` | Show the current version |
| `fathom --help` | Show help |

---

## fathom init

```bash
fathom init
```

Builds the `.fathom/` symbol index for the current repository.

**Flow:**
1. Ensure `.fathom/` exists.
2. Walk the working tree once to collect unique file extensions.
3. Map extensions → languages via the tree-sitter parser.
4. Download the tree-sitter parsers for those languages into `.fathom/parsers`.
5. Open the bbolt store at `.fathom/index.bolt`.
6. Best-effort: read the current commit hash from git.
7. Walk the tree again, parsing and indexing each supported file atomically.
8. Persist metadata (schema version, commit, timestamp, indexed file list).

Re-running `init` is safe — it rewrites the index from scratch and does not duplicate symbols.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| — | — | — | No flags. Operates on the current working directory. |

---

## fathom analyze

```bash
fathom analyze [files...]
fathom analyze --base <branch>
fathom analyze --base main --json
fathom analyze --base main --html report.html
fathom analyze --base main --fail-on-mismatch
```

Computes the impact of changes in the given files or against a base branch.

It looks up which symbols are defined in each file (or in the diff against the base), then calculates the transitive closure of everything that references those symbols — directly or indirectly. It also runs:

- **Signature mismatch detection**: call sites whose argument count or literal types no longer match the changed declaration, and overriding methods whose signature diverges from the parent. In `--base` mode, the engine compares the NEW (workspace) definitions against the STORED (base branch) references, detecting mismatches introduced by the current changes.
- **Dead code detection**: symbols no longer reachable from any entry point.

By default mismatches are printed as advisory warnings (exit code 0). Pass `--fail-on-mismatch` to exit with code 1 when any signature mismatch is found — for CI gating.

Requires a `.fathom/index.bolt` built with Fathom v3+. Run `fathom init` first.

### Interactive mode

When `fathom analyze` is run with **no flags** in a TTY, the interactive Bubbletea wizard launches to collect the parameters: analysis mode (files vs base branch), file selection, and output format (terminal / JSON / HTML). When stdout is **not** a TTY (CI, pipe), it prints help instead of blocking. Any explicit flag bypasses the wizard and runs the flag-based path with identical behavior.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--base` | string | `""` | Base branch to compare against (differential analysis) |
| `--html` | string | `""` | Output report as HTML to the specified file path |
| `--json` | bool | `false` | Output report as JSON |
| `--fail-on-mismatch` | bool | `false` | Exit with code 1 when signature mismatches are detected |

Either specify files to analyze as positional arguments, or a `--base` branch for differential analysis. At least one is required.

---

## fathom report

```bash
fathom report [files...]
fathom report --base main
fathom report --base main --output impact.html
```

Generates an HTML impact report for the given files or base branch. It is a convenience alias for `fathom analyze --html <path>`.

When no output path is provided via `--output`, the report is written to a temporary file and opened in the default browser automatically.

Requires a `.fathom/index.bolt` built with Fathom v3+. Run `fathom init` first.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--base` | string | `""` | Base branch to compare against (differential analysis) |
| `--output` | string | `""` | Output HTML file path (default: temp file + open browser) |
| `--fail-on-mismatch` | bool | `false` | Exit with code 1 when signature mismatches are detected |

---

## fathom interactive

```bash
fathom analyze   # with no flags, in a TTY
```

The interactive TUI wizard is not a separate command — it is launched automatically by `fathom analyze` when no flags are set and stdout is a terminal. It walks the user through:

1. **Mode selection**: analyze specific files, or compare against a base branch.
2. **File selection** (files mode): pick from the changed files in the working tree.
3. **Base branch** (branch mode): type or confirm the base branch name (default: `main`).
4. **Output format**: terminal report, JSON, or HTML report.

The wizard is built with [Bubbletea](https://github.com/charmbracelet/bubbletea) and [Lipgloss](https://github.com/charmbracelet/lipgloss). Navigation: `j/k` or `↑/↓` to move, `enter` to select, `y/n` for confirmations, `esc` or `Ctrl+C` to abort.

---

## fathom update

```bash
fathom update
fathom update --force
```

Checks GitHub releases for a newer version of Fathom and replaces the running binary in place with an atomic rename.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--force` | bool | `false` | Force update even when already on the latest version |

---

## fathom uninstall

```bash
fathom uninstall
fathom uninstall --force
```

Removes the fathom binary from disk and strips the PATH entries it added to shell RC files (`.bashrc`, `.zshrc`, `.profile`, `config.fish`). Also removes the local `.fathom/` index in the current directory (best-effort).

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--force` | bool | `false` | Skip the confirmation prompt |

---

## fathom --version

```bash
fathom --version
```

Shows the current version of the `fathom` binary. The version is injected at build time via ldflags (`-X main.Version={{.Version}}`); local builds report `dev`.

---

## fathom --help

```bash
fathom --help
fathom <command> --help
```

Shows help for the root command or a specific subcommand, including all available flags.