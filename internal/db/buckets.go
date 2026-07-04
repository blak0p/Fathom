// Package db provides the embedded key-value store for Fathom symbols,
// references, dependencies, and metadata using bbolt.
package db

import "go.etcd.io/bbolt"

// Bucket names are stable identifiers persisted on disk. They must never
// change once a database has been written, or existing data becomes
// unreachable.
const (
	BucketSymbols      = "symbols"       // key: {filePath}#{symbolName}
	BucketReferences   = "references"    // key shape TBD by future phases
	BucketDependencies = "dependencies"  // key shape TBD by future phases
	BucketMeta         = "meta"          // simple key/value store
)

// EnsureBuckets creates the four core buckets under the given transaction if
// they do not already exist. It is safe to call repeatedly.
func EnsureBuckets(tx *bbolt.Tx) error {
	for _, name := range []string{
		BucketSymbols,
		BucketReferences,
		BucketDependencies,
		BucketMeta,
	} {
		if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
			return err
		}
	}
	return nil
}