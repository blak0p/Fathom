# Architecture

High-level overview of Fathom's codebase for contributors.

## Tech Stack

- **Language**: Go 1.26+
- **CLI Framework**: [Cobra](https://github.com/spf13/cobra) for command dispatch, flags, and help
- **Parsing**: [tree-sitter](https://tree-sitter.github.io/tree-sitter/) via the `xberg-io/tree-sitter-language-pack` Go bindings, vendored and patched in `ffi/` (CGO FFI)
- **Storage**: [bbolt](https://github.com/etcd-io/bbolt) for the local symbol index (`.fathom/index.bolt`)
- **TUI**: [Bubbletea](https://github.com/charmbracelet/bubbletea) + [Lipgloss](https://github.com/charmbracelet/lipgloss) for the interactive analyze wizard
- **Logging**: [zap](https://go.uber.org/zap)
- **Build**: CGO_ENABLED=1 (the FFI layer requires a C toolchain)

## Directory Structure

```
Fathom/
├── cmd/                    # CLI commands (init, analyze, report, update, uninstall, interactive)
│   ├── root.go             # Root command + Execute() entry point
│   ├── init.go             # fathom init — build the symbol index
│   ├── analyze.go          # fathom analyze — blast radius + mismatch + deadcode
│   ├── report.go           # fathom report — alias for analyze --html
│   ├── update.go           # fathom update — self-update from GitHub releases
│   ├── uninstall.go        # fathom uninstall — remove binary + clean PATH
│   └── interactive/        # Bubbletea TUI wizard for analyze (analyzer.go, survey.go)
├── ffi/                    # Pre-built tree-sitter FFI shared libraries per platform
│   ├── .lib/
│   │   ├── linux-amd64/    # Linux x86_64 shared library
│   │   ├── macos-amd64/    # macOS Intel shared library
│   │   ├── macos-arm64/    # macOS Apple Silicon shared library
│   │   └── windows-amd64/  # Windows x86_64 DLL
│   ├── include/            # C headers (ts_pack.h)
│   ├── binding.go          # CGO bindings to the tree-sitter language pack
│   ├── embed_ffi.go        # Build-time FFI embedding
│   └── generate.go        # Code generation helpers
├── internal/
│   ├── db/                 # bbolt database layer (buckets, store, schema versioning)
│   ├── deadcode/           # Dead code detection engine
│   ├── diff/               # Diff analysis (align changed files to symbols)
│   ├── git/                # Git operations (repository, merge base, diff, resolve commit)
│   ├── impact/             # Blast radius engine (transitive reference closure)
│   ├── mismatch/           # Signature mismatch detection (call sites, overrides)
│   ├── parser/             # Tree-sitter parser wrapper + symbol extraction
│   ├── refs/               # Reference extraction (imports, exports, query-based)
│   ├── report/             # HTML report generation (compile + render)
│   └── symbol/             # Symbol model (kinds, metadata)
├── docs/                   # Documentation
├── scripts/                # Installer scripts (install.sh)
└── main.go                 # Entry point — calls cmd.Execute(Version)
```

## Architecture

```
┌─────────────────────────────────────────────┐
│              User (CLI / CI)                │
└──────────────────┬──────────────────────────┘
                   │ Cobra command dispatch
┌──────────────────▼──────────────────────────┐
│              cmd/ (Commands)                │
│  init · analyze · report · update · uninstall│
└──────────────────┬──────────────────────────┘
                   │
         ┌─────────┼─────────┐
         │         │         │
┌────────▼──┐ ┌────▼────┐ ┌─▼──────────┐
│ internal/  │ │internal/ │ │ internal/  │
│  parser    │ │  impact  │ │  report    │
│ (symbols)  │ │ (blast)  │ │  (HTML)    │
└────┬──────┘ └────┬─────┘ └────────────┘
     │              │
     │              │
┌────▼──────────────▼──────────────────────────┐
│              internal/db (bbolt)              │
│        .fathom/index.bolt                     │
└──────────────────┬──────────────────────────┘
                   │
┌──────────────────▼──────────────────────────┐
│              ffi/ (tree-sitter CGO)          │
│    Vendored shared libraries per platform    │
└─────────────────────────────────────────────┘
```

## How it works

### `fathom init`

1. Ensure `.fathom/` exists.
2. Walk the working tree once to collect unique file extensions.
3. Map extensions → languages via the parser.
4. Download the tree-sitter parsers for those languages into `.fathom/parsers`.
5. Open the bbolt store at `.fathom/index.bolt`.
6. Best-effort: read the current commit hash from git.
7. Walk the tree again, parsing and indexing each supported file atomically.
8. Persist metadata (schema version, commit, timestamp, indexed file list).
9. Print a one-line summary.

Re-running `init` is safe — it rewrites the index from scratch and does not duplicate symbols.

### `fathom analyze`

Two modes:

- **Explicit files**: `fathom analyze file1.go file2.go` — looks up which symbols are defined in each file, then computes the blast radius.
- **Differential** (`--base <branch>`): finds the merge base with the base branch, diffs against it, resolves the changed symbols, re-syncs the index from the merge base, then computes impact.

In both modes, after the changed symbols are resolved:

1. **Blast radius** (`internal/impact`): transitive closure of everything that references the changed symbols — directly or indirectly.
2. **Signature mismatch** (`internal/mismatch`): call sites whose argument count or literal types no longer match the changed declaration, and overriding methods whose signature diverges from the parent. When run in `--base` mode, the engine compares the NEW (workspace) definitions against the STORED (base branch) references.
3. **Dead code** (`internal/deadcode`): symbols no longer reachable from any entry point, scanned over the changed symbols.

Output: human-readable terminal report (default), JSON (`--json`), or HTML (`--html <path>`).

### `fathom report`

Convenience alias for `fathom analyze --html`. When no `--output` path is given, the report is written to a temp file and opened in the default browser.

### Interactive wizard

When `fathom analyze` is run with no flags in a TTY, the Bubbletea wizard (`cmd/interactive/`) launches to collect the parameters: analysis mode (files vs base branch), file selection, and output format (terminal / JSON / HTML). In a non-TTY (CI, pipe), it prints help instead of blocking. Any explicit flag bypasses the wizard and runs the legacy flag-based path.

## FFI layer (`ffi/`)

The tree-sitter language pack is vendored as pre-built shared libraries (one per platform) under `ffi/.lib/`. The `go.mod` has a `replace` directive pointing `xberg-io/tree-sitter-language-pack/packages/go` at the local `./ffi` directory because the upstream package's `.lib/` uses Go-arch directory names (`linux-amd64`) while the cgo LDFLAGS reference alef arch names (`linux-x86_64`). The vendored copy ships symlinks so the linker finds `libts_pack_core_ffi`.

This is why Fathom requires `CGO_ENABLED=1` and a C toolchain to build, and why goreleaser only cross-compiles `linux/amd64` natively — macOS and Windows binaries are built on native runners.

## Testing

To run the test suite, use the targets defined in the [Makefile](../Makefile):

```bash
# Unit tests + go vet (short mode, skips git-dependent integration tests)
make test

# All tests including integration (requires git + CGO)
make test-full

# Verbose output
make test-v

# Run vet and format checks
make lint

# Clean build artifacts and .fathom/ index
make clean
```

## Common Paths

| Feature | File / Directory |
|---------|------------------|
| CLI Entry Point | [main.go](../main.go) |
| Root Command | [cmd/root.go](../cmd/root.go) |
| Init Flow | [cmd/init.go](../cmd/init.go) |
| Analyze Flow | [cmd/analyze.go](../cmd/analyze.go) |
| Interactive Wizard | [cmd/interactive/analyzer.go](../cmd/interactive/analyzer.go) |
| Blast Radius Engine | [internal/impact/](../internal/impact/) |
| Mismatch Engine | [internal/mismatch/](../internal/mismatch/) |
| Dead Code Scanner | [internal/deadcode/](../internal/deadcode/) |
| bbolt Store | [internal/db/store.go](../internal/db/store.go) |
| Tree-sitter Parser | [internal/parser/](../internal/parser/) |
| Reference Extraction | [internal/refs/](../internal/refs/) |
| HTML Report | [internal/report/](../internal/report/) |
| FFI Bindings | [ffi/binding.go](../ffi/binding.go) |
| Git Operations | [internal/git/](../internal/git/) |