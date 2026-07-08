// Fathom is a repository impact analysis CLI for Pull Requests.
//
// It builds a local, tree-sitter-backed symbol index (.fathom/index.bolt) and
// uses it to answer "what does this PR actually touch?" questions across the
// whole codebase, not just the files in the diff.
package main

import "github.com/blak0p/Fathom/cmd"

func main() {
	cmd.Execute()
}