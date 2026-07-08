# Contributing to Fathom

Thanks for your interest! Fathom is a repository impact analysis tool for Pull Requests. Here's how to get started.

## Development Setup

**Prerequisites:**
- Go 1.26+
- CGO (requires GCC or Clang)
- Git

```bash
# Clone and build
git clone https://github.com/blak0p/Fathom.git
cd Fathom
CGO_ENABLED=1 go build -o fathom .
```

## Running Tests

```bash
# Unit tests (fast, no git integration)
make test

# Full test suite including integration tests
make test-full

# Verbose output
make test-v

# Lint
make lint
```

## Code Style

- Run `go fmt ./...` before committing
- Run `go vet ./...` to catch common issues
- Follow standard Go conventions (idiomatic Go, no unnecessary abstractions)

## Commit Conventions

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>: <description>

[optional body explaining WHY, not just WHAT]
```

Types:
- `feat` — new feature
- `fix` — bug fix
- `chore` — maintenance, dependencies, tooling
- `docs` — documentation only
- `refactor` — code change that neither fixes nor adds
- `test` — adding or updating tests
- `perf` — performance improvement

**Commit messages explain WHY, not just WHAT.** The body should describe the reasoning behind the change, not just what changed.

## Pull Request Process

1. Create a branch from `main` with a descriptive name
2. Make your changes
3. Run tests and lint
4. Open a PR using the template
5. Link the PR to an issue with `Closes #N` or `Fixes #N`
6. Wait for CI to pass
7. Request review

## PR Requirements

- All PRs must link to an approved issue
- Tests must pass
- No new lint warnings
- Conventional commit format

## Getting Help

- Open an issue for bugs or feature requests
- Use GitHub Discussions for questions
