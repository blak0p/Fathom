module github.com/Fathom

go 1.26.4

require (
	github.com/spf13/cobra v1.10.2
	github.com/xberg-io/tree-sitter-language-pack/packages/go v1.12.2
	go.etcd.io/bbolt v1.5.0
	go.uber.org/zap v1.28.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/tree-sitter/go-tree-sitter v0.24.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
)

// Local replace: the published module's .lib/ uses Go-arch dir names
// (linux-amd64) but the cgo LDFLAGS in binding.go reference alef arch
// names (linux-x86_64). We vendor a writable copy with symlinks so the
// linker can find libts_pack_core_ffi. Remove this replace once the
// upstream package ships matching directory names.
replace github.com/xberg-io/tree-sitter-language-pack/packages/go v1.12.2 => ./.extmods/tspack
