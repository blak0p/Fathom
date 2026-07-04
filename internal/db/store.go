package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"

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

// Store is the persistence interface used across Fathom. It is intentionally
// narrow: callers can open/close the store, batch-write symbols per file,
// look up individual symbols, prefix-scan symbols by file, and read/write
// simple metadata.
type Store interface {
	Open(path string) error
	Close() error
	PutSymbols(symbols []symbol.Symbol) error
	GetSymbol(file, name string) (symbol.Symbol, error)
	ListSymbols(filePrefix string) ([]symbol.Symbol, error)
	PutMeta(key, value string) error
	GetMeta(key string) (string, error)
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