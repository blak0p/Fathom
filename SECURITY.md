# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.x     | :white_check_mark: |

## Reporting a Vulnerability

Fathom is a local-only CLI tool — it never sends your code anywhere. However, if you discover a security vulnerability, please report it privately.

**How to report:**

1. **GitHub Private Vulnerability Reporting**: Use the "Report a vulnerability" button under the Security tab of the repository.
2. **Email**: Open an issue requesting contact details.

You can expect an acknowledgment within 48 hours and a fix timeline within 7 days depending on severity.

## What to include

- Description of the vulnerability
- Steps to reproduce
- Affected versions
- Any potential impact

## Scope

- The `fathom` binary and its source code
- CI/CD pipelines and release infrastructure
- GitHub Actions workflows

Out of scope:
- Third-party dependencies (report them to their respective maintainers)
- The tree-sitter language pack FFI (report to [xberg-io](https://github.com/xberg-io/tree-sitter-language-pack))
