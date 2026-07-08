<!-- mcp-name: io.github.blak0p/Fathom -->
<!-- markdownlint-disable MD041 -->

<p align="center">
  <a href="https://github.com/blak0p/Fathom/releases/latest">
    <img src="https://img.shields.io/github/v/release/blak0p/Fathom?color=%2300BFFF&label=latest" alt="Release">
  </a>
  <a href="https://github.com/blak0p/Fathom/actions">
    <img src="https://img.shields.io/github/actions/workflow/status/blak0p/Fathom/release.yml?branch=main" alt="Build">
  </a>
  <a href="https://github.com/blak0p/Fathom/blob/main/LICENSE">
    <img src="https://img.shields.io/github/license/blak0p/Fathom" alt="MIT License">
  </a>
</p>

> **Issues & Bugs**: [@blak0p/Fathom/issues](https://github.com/blak0p/Fathom/issues) · **Discussions**: [@blak0p/Fathom/discussions](https://github.com/blak0p/Fathom/discussions)

| Doc | Description |
| --- | --- |
| [Architecture](docs/architecture.md) | Codebase structure, packages, and how to add features |
| [Commands](docs/commands.md) | Complete reference for every `fathom` command and flag |

---

# Fathom

**Know what a PR actually touches. Not just the files in the diff — the real impact across your whole codebase.**

Fathom is a repository impact analysis CLI for Pull Requests. It builds a local, tree-sitter-backed symbol index of a repository and uses it to answer "what does this PR actually touch?" — the transitive closure of everything that references the changed symbols, not just the files that show up in the diff.

Dead code, signature mismatches, blast radius. All computed locally, all in one pass.

---

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/blak0p/Fathom/main/scripts/install.sh | sh
```

```go
go install github.com/blak0p/Fathom@latest
```

**Homebrew:**
```bash
brew install blak0p/tap/fathom
```

### Quick start

```bash
fathom init               # build the .fathom/ symbol index for this repo
fathom analyze --base main   # compute the blast radius of your branch vs main
fathom report                # generate an HTML report and open it in your browser
```

That's it. `fathom init` walks the working tree, detects the languages present from file extensions, parses every supported source file with tree-sitter, and stores the extracted symbols in `.fathom/index.bolt`. `fathom analyze` compares your branch against a base and reports the real impact.

---

## What Fathom does

Most "PR impact" tools stop at the diff: which files changed, how many lines. That tells you what was *edited*, not what was *affected*.

Fathom goes further:

- **Blast radius**: for every changed symbol, it computes the transitive closure of everything that references it — directly or indirectly. A one-line change in a shared helper can surface 47 downstream call sites across 12 files, even though the diff only touches one.
- **Signature mismatches**: call sites whose argument count or literal types no longer match the changed declaration, and overriding methods whose signature diverges from the parent. Advisory by default; `--fail-on-mismatch` exits 1 for CI gating.
- **Dead code**: symbols that are no longer reachable from any entry point, detected in the same pass as the blast radius.
- **Whole-codebase, not just the diff**: the analysis runs against the symbol index of the entire repository, so impact is reported even when the affected files never appear in the diff.

All of this runs locally. No GPU, no cloud, no tokens.

---

## How it works

```
fathom init
    ↓
Walk working tree → detect languages → download tree-sitter parsers
    ↓
Parse every supported file → extract symbols → store in .fathom/index.bolt (bbolt)

fathom analyze --base main
    ↓
Find merge base → diff against it → resolve changed symbols
    ↓
Blast radius engine: transitive closure of references to changed symbols
    ↓
Mismatch engine: call sites + overrides vs changed declarations
    ↓
Deadcode scanner: unreachable symbols in the same pass
    ↓
Human report (terminal) / JSON / HTML report

fathom report
    ↓
Alias for `analyze --html` → temp file + open browser
```

---

## Commands

| Command | What it does |
| --- | --- |
| `fathom init` | Build the `.fathom/` symbol index for the current repo |
| `fathom analyze [files...]` | Analyze impact of changes. Flags: `--base`, `--html`, `--json`, `--fail-on-mismatch` |
| `fathom report [files...]` | Alias for `analyze --html` with a temp file + browser |
| `fathom interactive` | Launch the interactive TUI wizard (auto-triggered by `analyze` with no flags in a TTY) |
| `fathom update` | Self-update to the latest GitHub release |
| `fathom uninstall` | Remove the binary and clean up PATH entries |
| `fathom --version` | Show the current version |
| `fathom --help` | Show help |

Full reference with every flag: [docs/commands.md](docs/commands.md).

---

## Architecture

Fathom is a Go CLI built on [Cobra](https://github.com/spf13/cobra). It uses tree-sitter (via the `xberg-io/tree-sitter-language-pack` CGO FFI bindings, vendored in `ffi/`) to parse source files into symbols, stores them in a [bbolt](https://github.com/etcd-io/bbolt) database, and runs impact/mismatch/deadcode analysis over that index. The interactive wizard uses [Bubbletea](https://github.com/charmbracelet/bubbletea).

```
Fathom/
├── cmd/                    # CLI commands (init, analyze, report, update, uninstall, interactive)
├── ffi/                    # Pre-built tree-sitter FFI shared libraries per platform
├── internal/
│   ├── db/                 # bbolt database layer (buckets, store)
│   ├── deadcode/           # Dead code detection engine
│   ├── diff/               # Diff analysis
│   ├── git/                # Git operations
│   ├── impact/             # Blast radius engine
│   ├── mismatch/           # Signature mismatch detection
│   ├── parser/             # Tree-sitter parser + symbol extraction
│   ├── refs/               # Reference extraction (imports, exports)
│   ├── report/             # HTML report generation
│   └── symbol/             # Symbol model
├── docs/                   # Documentation
└── scripts/                # Installer scripts
```

Detailed structure and patterns: [docs/architecture.md](docs/architecture.md).

---

## FAQ

**What is Fathom?**
A repository impact analysis CLI. It builds a local symbol index and uses it to tell you what a PR actually touches across the whole codebase — blast radius, signature mismatches, and dead code — not just the files in the diff.

**Does it need a GPU?**
No. Fathom runs entirely on the CPU. There is no LLM, no model inference, no network call during analysis.

**Does my code leave my machine?**
No. Everything runs locally. The only network traffic is `fathom init` downloading tree-sitter parsers (cached in `.fathom/parsers`) and `fathom update` fetching a new binary from GitHub releases.

**Which languages are supported?**
Fathom parses any language the tree-sitter language pack ships a grammar for — over 300, including Go, Rust, Python, TypeScript, JavaScript, Java, C/C++, Ruby, and more. Languages are detected automatically from file extensions during `fathom init`.

**Do I need to re-run init after every change?**
Re-run `fathom init` when the *structure* of the repo changes (new files, renamed symbols, deleted modules). For day-to-day analysis, `fathom analyze --base main` re-syncs the index from the merge base before computing impact, so you don't need a full `init` on every branch.

**How do I gate a PR on mismatches?**
Run `fathom analyze --base main --fail-on-mismatch` in CI. It exits with code 1 when any signature mismatch is detected.

---

## License

[MIT](LICENSE)