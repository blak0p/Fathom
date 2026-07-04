package main

import "fmt"

// Config holds runtime configuration.
type Config struct {
	Port int
}

// Store abstracts persistence.
type Store interface {
	Get(key string) (string, error)
}

// HandleRequest is the HTTP entry point.
func HandleRequest(w int) {
	fmt.Println(w)
}
