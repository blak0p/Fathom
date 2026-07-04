package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/Fathom/internal/refs"
	"github.com/Fathom/internal/symbol"
)

// keySeparator separates the file path from the symbol name in the Symbols
// bucket key. '#' is chosen because it is illegal in file paths on every
// mainstream operating system, so a symbol key can never collide with a
// legitimate file path and is always unambiguously parseable.
const keySeparator = "#"

// ErrStoreClosed is returned when an operation is attempted on a closed Store.
var ErrStoreClosed = errors.New("db: store is closed")

// ErrNotFound is returned when no value exists for the requested key.
var ErrNotFound = errors.New("db: not found")

// ErrSchemaVersion is returned when the on-disk schema version is missing or
// incompatible with the current Fathom build. It carries a human-readable
// migration hint so callers can surface actionable guidance to the user.
var ErrSchemaVersion = errors.New("db: incompatible or missing schema version")

// currentSchemaVersion is the schema version written by Fathom after a
// successful index that includes reference extraction. Bumping this value
// triggers the v1→v2 migration message for stale databases.
const currentSchemaVersion = "2"

// Store is the persistence interface used across Fathom. It is intentionally
// narrow: callers can open/close the store, batch-write symbols per file,
// look up individual symbols, prefix-scan symbols by file, and read/write
// simple metadata. Phase 2 extends it with reference persistence and a
// schema-version guard used by `fathom analyze`.
type Store interface {
	Open(path string) error
	Close() error
	PutSymbols(symbols []symbol.Symbol) error
	GetSymbol(file, name string) (symbol.Symbol, error)
	ListSymbols(filePrefix string) ([]symbol.Symbol, error)
	PutMeta(key, value string) error
	GetMeta(key string) (string, error)

	// PutReferences atomically writes all references for a file in a single
	// bbolt transaction. The key format is
	// "{targetSymbolName}#{sourceFile}#{sourceLine}", so a file's references
	// can be listed by prefix scan on "{filePath}#" when the file is the
	// source, and a symbol's references can be listed by prefix scan on
	// "{symbolName}#" when the symbol is the target.
	PutReferences(filePath string, refs []refs.Reference) error
	// GetReferences returns every reference whose target SymbolName matches
	// symbolName, via prefix scan on "{symbolName}#". Results are sorted by
	// (sourceFile, sourceLine) for stable output.
	GetReferences(symbolName string) ([]refs.Reference, error)
	// ListReferencesByFile returns every reference whose source file matches
	// filePath. Because the References bucket key starts with the target
	// symbol name, this requires a full scan filtering by the SourceFile
	// field of each stored Reference.
	ListReferencesByFile(filePath string) ([]refs.Reference, error)
	// CheckSchemaVersion reads the "schema_version" meta key and returns nil
	// only when it equals currentSchemaVersion. Any other value (including
	// missing) returns ErrSchemaVersion with a migration hint.
	CheckSchemaVersion() error
}

// boltStore is the default Store implementation backed by an embedded bbolt
// database. It is safe for concurrent use: bbolt uses a single read/write
// transaction model with MVCC for readers.
type boltStore struct {
	db   *bbolt.DB
	path string
}

// New returns a Store backed by bbolt. The database is not opened until
// Open is called.
func New() Store {
	return &boltStore{}
}

// Open opens (or creates) the bbolt database at the given path and ensures
// all core buckets exist.
func (s *boltStore) Open(path string) error {
	zap.L().Info("opening bolt store", zap.String("path", path))

	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		return fmt.Errorf("db: open %q: %w", path, err)
	}

	if err := db.Update(func(tx *bbolt.Tx) error {
		return EnsureBuckets(tx)
	}); err != nil {
		_ = db.Close()
		return fmt.Errorf("db: ensure buckets: %w", err)
	}

	s.db = db
	s.path = path
	return nil
}

// Close releases the underlying bbolt database. It is safe to call multiple
// times; subsequent calls are no-ops.
func (s *boltStore) Close() error {
	if s.db == nil {
		return nil
	}
	zap.L().Info("closing bolt store", zap.String("path", s.path))
	err := s.db.Close()
	s.db = nil
	return err
}

// symbolKey builds the Symbols-bucket key for a (file, name) pair.
func symbolKey(file, name string) []byte {
	return []byte(file + keySeparator + name)
}

// PutSymbols writes the given symbols to the Symbols bucket atomically. The
// write is performed in a single bbolt transaction, so either all symbols land
// or none do.
//
// Callers should pass all symbols for a given file in one call so that a file
// is either fully indexed or not indexed at all.
func (s *boltStore) PutSymbols(symbols []symbol.Symbol) error {
	if s.db == nil {
		return ErrStoreClosed
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSymbols))
		if bucket == nil {
			return fmt.Errorf("db: bucket %q missing", BucketSymbols)
		}

		for _, sym := range symbols {
			data, err := json.Marshal(sym)
			if err != nil {
				return fmt.Errorf("db: encode symbol %q: %w", sym.Name, err)
			}
			key := symbolKey(sym.File, sym.Name)
			if err := bucket.Put(key, data); err != nil {
				return fmt.Errorf("db: put symbol %q: %w", sym.Name, err)
			}
		}
		return nil
	})
}

// GetSymbol returns the symbol stored under (file, name). It returns
// ErrNotFound when no such symbol exists.
func (s *boltStore) GetSymbol(file, name string) (symbol.Symbol, error) {
	var sym symbol.Symbol

	if s.db == nil {
		return sym, ErrStoreClosed
	}

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSymbols))
		if bucket == nil {
			return fmt.Errorf("db: bucket %q missing", BucketSymbols)
		}

		raw := bucket.Get(symbolKey(file, name))
		if raw == nil {
			return ErrNotFound
		}

		// bbolt reuses the returned byte slice only for the duration of the
		// transaction, so we must copy it out before unmarshalling exits the
		// transaction. json.Unmarshal copies into the struct, but the slice
		// itself is borrowed; copying is the safe, defensive choice.
		cp := make([]byte, len(raw))
		copy(cp, raw)

		if err := json.Unmarshal(cp, &sym); err != nil {
			return fmt.Errorf("db: decode symbol %q: %w", name, err)
		}
		return nil
	})

	return sym, err
}

// ListSymbols returns every symbol whose key starts with filePrefix. The
// prefix is matched against the full key ({file}#{name}), so passing a file
// path yields all symbols for that file, and passing a directory prefix yields
// all symbols under that directory.
//
// A nil/empty prefix returns all symbols.
func (s *boltStore) ListSymbols(filePrefix string) ([]symbol.Symbol, error) {
	if s.db == nil {
		return nil, ErrStoreClosed
	}

	var out []symbol.Symbol

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSymbols))
		if bucket == nil {
			return fmt.Errorf("db: bucket %q missing", BucketSymbols)
		}

		prefix := []byte(filePrefix)
		c := bucket.Cursor()

		// Empty prefix ⇒ scan everything.
		if len(prefix) == 0 {
			for k, v := c.First(); k != nil; k, v = c.Next() {
				var sym symbol.Symbol
				if err := decodeSymbol(v, &sym); err != nil {
					return err
				}
				out = append(out, sym)
			}
			return nil
		}

		for k, v := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, v = c.Next() {
			var sym symbol.Symbol
			if err := decodeSymbol(v, &sym); err != nil {
				return err
			}
			out = append(out, sym)
		}
		return nil
	})

	return out, err
}

// hasPrefix reports whether b starts with prefix. bytes.HasPrefix would be
// cleaner, but inlining avoids an extra import and keeps this file lean.
func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	return string(b[:len(prefix)]) == string(prefix)
}

// decodeSymbol unmarshals raw bytes into sym. The raw slice is copied first to
// avoid holding onto bbolt's mmap-backed buffers beyond the cursor step.
func decodeSymbol(raw []byte, sym *symbol.Symbol) error {
	cp := make([]byte, len(raw))
	copy(cp, raw)
	if err := json.Unmarshal(cp, sym); err != nil {
		return fmt.Errorf("db: decode symbol: %w", err)
	}
	return nil
}

// PutMeta writes a simple key/value pair into the Meta bucket. It is used for
// things like schema version, last-indexed timestamp, etc.
func (s *boltStore) PutMeta(key, value string) error {
	if s.db == nil {
		return ErrStoreClosed
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketMeta))
		if bucket == nil {
			return fmt.Errorf("db: bucket %q missing", BucketMeta)
		}
		return bucket.Put([]byte(key), []byte(value))
	})
}

// GetMeta returns the value stored under key in the Meta bucket. It returns
// ErrNotFound when no such key exists.
func (s *boltStore) GetMeta(key string) (string, error) {
	if s.db == nil {
		return "", ErrStoreClosed
	}

	var value string
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketMeta))
		if bucket == nil {
			return fmt.Errorf("db: bucket %q missing", BucketMeta)
		}

		raw := bucket.Get([]byte(key))
		if raw == nil {
			return ErrNotFound
		}

		// Copy out of the borrowed bbolt slice before the transaction ends.
		value = string(append([]byte(nil), raw...))
		return nil
	})

	return value, err
}

// Compile-time assertion that boltStore satisfies Store.
var _ Store = (*boltStore)(nil)

// referenceKey builds the References-bucket key for a reference. The format
// is "{targetSymbolName}#{sourceFile}#{sourceLine}". The target symbol name
// comes first so GetReferences(symbolName) can prefix-scan "{symbolName}#".
//
// The line number is included so two references to the same symbol from
// different lines of the same file don't overwrite each other; the column is
// NOT included because a single line rarely has two references to the same
// symbol, and even when it does, the second one would be redundant for
// impact analysis. If that assumption ever breaks, the column can be appended
// without breaking existing keys (they'd just be a strict prefix).
func referenceKey(r refs.Reference) []byte {
	return []byte(r.SymbolName + keySeparator + r.SourceFile + keySeparator + itoa(r.SourceLine))
}

// itoa formats a non-negative int as a decimal string. We avoid strconv to
// keep this file's import list lean, matching the style of hasPrefix above.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// PutReferences writes the given references to the References bucket
// atomically, overriding any references previously stored for the same file
// via a delete-then-put within the same transaction. The override semantics
// match PutSymbols' per-file atomicity: a file's references are either fully
// re-indexed or not at all, so stale references from a previous indexing run
// never survive a re-index.
//
// Each reference's SourceFile is set to filePath by this method, so callers
// can pass references with a blank SourceFile and let the store stamp it,
// mirroring how the parser leaves Symbol.File blank for the store to fill.
func (s *boltStore) PutReferences(filePath string, references []refs.Reference) error {
	if s.db == nil {
		return ErrStoreClosed
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketReferences))
		if bucket == nil {
			return fmt.Errorf("db: bucket %q missing", BucketReferences)
		}

		// Delete any references previously stored for filePath so a re-index
		// does not accumulate stale entries. We can't prefix-scan by source
		// file (the key starts with the target symbol name), so we scan the
		// whole bucket and delete keys whose stored Reference.SourceFile
		// matches. This is O(total references) but only runs once per file
		// per indexing pass, which is acceptable for Fathom's workload.
		toDelete := make([][]byte, 0)
		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var existing refs.Reference
			if err := json.Unmarshal(append([]byte(nil), v...), &existing); err != nil {
				// Skip unparseable entries rather than aborting the whole
				// re-index; they'll be overwritten or left alone.
				continue
			}
			if existing.SourceFile == filePath {
				toDelete = append(toDelete, append([]byte(nil), k...))
			}
		}
		for _, k := range toDelete {
			if err := bucket.Delete(k); err != nil {
				return fmt.Errorf("db: delete stale reference for %q: %w", filePath, err)
			}
		}

		// Write the new references, stamping SourceFile on each so callers
		// can pass references with a blank SourceFile.
		for _, r := range references {
			r.SourceFile = filePath
			data, err := json.Marshal(r)
			if err != nil {
				return fmt.Errorf("db: encode reference %q: %w", r.SymbolName, err)
			}
			key := referenceKey(r)
			if err := bucket.Put(key, data); err != nil {
				return fmt.Errorf("db: put reference %q: %w", r.SymbolName, err)
			}
		}
		return nil
	})
}

// GetReferences returns every reference whose target SymbolName matches
// symbolName, via prefix scan on "{symbolName}#". Results are sorted by
// (sourceFile, sourceLine) for stable output, matching the spec's
// requirement that query results be deterministic.
func (s *boltStore) GetReferences(symbolName string) ([]refs.Reference, error) {
	if s.db == nil {
		return nil, ErrStoreClosed
	}

	var out []refs.Reference
	prefix := []byte(symbolName + keySeparator)

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketReferences))
		if bucket == nil {
			return fmt.Errorf("db: bucket %q missing", BucketReferences)
		}

		c := bucket.Cursor()
		for k, v := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, v = c.Next() {
			var r refs.Reference
			cp := make([]byte, len(v))
			copy(cp, v)
			if err := json.Unmarshal(cp, &r); err != nil {
				return fmt.Errorf("db: decode reference: %w", err)
			}
			out = append(out, r)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sortReferences(out)
	return out, nil
}

// ListReferencesByFile returns every reference whose SourceFile matches
// filePath. Because the References bucket key starts with the target symbol
// name (not the source file), this requires a full bucket scan filtering by
// the stored SourceFile field. The result is sorted by (sourceLine, sourceCol)
// for stable, file-order output.
func (s *boltStore) ListReferencesByFile(filePath string) ([]refs.Reference, error) {
	if s.db == nil {
		return nil, ErrStoreClosed
	}

	var out []refs.Reference

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketReferences))
		if bucket == nil {
			return fmt.Errorf("db: bucket %q missing", BucketReferences)
		}

		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var r refs.Reference
			cp := make([]byte, len(v))
			copy(cp, v)
			if err := json.Unmarshal(cp, &r); err != nil {
				return fmt.Errorf("db: decode reference: %w", err)
			}
			if r.SourceFile == filePath {
				out = append(out, r)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort by (line, col) for file-order output.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SourceLine != out[j].SourceLine {
			return out[i].SourceLine < out[j].SourceLine
		}
		return out[i].SourceCol < out[j].SourceCol
	})
	return out, nil
}

// CheckSchemaVersion reads the "schema_version" meta key and returns nil
// only when it equals currentSchemaVersion. Any other value (including a
// missing key) returns ErrSchemaVersion with a migration hint so callers
// like `fathom analyze` can surface actionable guidance.
func (s *boltStore) CheckSchemaVersion() error {
	if s.db == nil {
		return ErrStoreClosed
	}

	version, err := s.GetMeta(schemaVersionKey)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: missing schema_version; run `fathom init` to build the index", ErrSchemaVersion)
		}
		return err
	}
	if version != currentSchemaVersion {
		return fmt.Errorf("%w: index was built with schema v%s; re-run `fathom init` to migrate to v%s", ErrSchemaVersion, version, currentSchemaVersion)
	}
	return nil
}

// schemaVersionKey is the Meta-bucket key under which the schema version is
// stored. Centralized here so PutMeta callers and CheckSchemaVersion agree.
const schemaVersionKey = "schema_version"

// sortReferences orders references by (sourceFile, sourceLine) for stable
// output. It is a helper so GetReferences can keep its result deterministic
// without duplicating the comparator.
func sortReferences(out []refs.Reference) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SourceFile != out[j].SourceFile {
			return out[i].SourceFile < out[j].SourceFile
		}
		return out[i].SourceLine < out[j].SourceLine
	})
}

// splitSymbolKey splits a Symbols-bucket key back into (file, name). This is
// exposed for future tooling/debugging; the store itself never needs to
// parse keys it has just written.
func splitSymbolKey(key []byte) (file, name string, ok bool) {
	idx := strings.IndexByte(string(key), keySeparator[0])
	if idx < 0 {
		return "", "", false
	}
	return string(key[:idx]), string(key[idx+1:]), true
}
