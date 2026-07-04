package main

// broken.go: missing closing brace to exercise a syntax error.
// tree-sitter still produces a (partial) tree, so extraction must not panic.

import "fmt"

func good() {
	fmt.Println("ok")
}

// The declaration below is intentionally malformed (missing closing brace).
func broken( {