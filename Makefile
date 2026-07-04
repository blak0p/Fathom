# Fathom — repository impact analysis for Pull Requests
#
# Targets:
#   build       Compile the fathom binary
#   test        Run all tests (short mode, no git-dependent tests)
#   test-full   Run all tests including integration (requires git + CGO)
#   test-v      Verbose test output
#   clean       Remove build artifacts and .fathom/ index
#   lint        Run go vet
#   fmt         Format all Go source files
#   init        Run fathom init on the current repo
#   run         Build and run fathom with args (e.g., make run ARGS="init")
#   help        Show this help

GO       := go
CC       := gcc-16
CGO_ENABLED := 1
GOFLAGS  :=
BINARY   ?= fathom

# Detect OS for the binary name
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Linux)
	BINARY_EXT :=
endif
ifeq ($(UNAME_S),Darwin)
	BINARY_EXT :=
endif
ifeq ($(UNAME_S),Windows)
	BINARY_EXT := .exe
endif

.PHONY: all build test test-full test-v clean lint fmt init run help

all: build

# Build the fathom binary
build:
	CC=$(CC) CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -o $(BINARY)$(BINARY_EXT) .

# Run all tests in short mode (skips git-dependent integration tests)
test:
	CC=$(CC) CGO_ENABLED=$(CGO_ENABLED) $(GO) test $(GOFLAGS) -short ./...

# Run all tests including integration (requires git)
test-full:
	CC=$(CC) CGO_ENABLED=$(CGO_ENABLED) $(GO) test $(GOFLAGS) ./...

# Verbose test output
test-v:
	CC=$(CC) CGO_ENABLED=$(CGO_ENABLED) $(GO) test $(GOFLAGS) -v ./...

# Remove build artifacts and .fathom/ index
clean:
	rm -f $(BINARY)$(BINARY_EXT)
	rm -rf .fathom/

# Run go vet
lint:
	CC=$(CC) CGO_ENABLED=$(CGO_ENABLED) $(GO) vet ./...

# Format all Go source files
fmt:
	$(GO) fmt ./...

# Run fathom init on the current repo
init: build
	./$(BINARY)$(BINARY_EXT) init

# Build and run fathom with custom args
run: build
	./$(BINARY)$(BINARY_EXT) $(ARGS)

# Show help
help:
	@echo "Fathom — Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  build       Compile the fathom binary"
	@echo "  test        Run all tests (short mode)"
	@echo "  test-full   Run all tests including integration"
	@echo "  test-v      Verbose test output"
	@echo "  clean       Remove build artifacts and .fathom/"
	@echo "  lint        Run go vet"
	@echo "  fmt         Format all Go source files"
	@echo "  init        Run fathom init on the current repo"
	@echo "  run         Build and run with ARGS (e.g., make run ARGS='init')"
	@echo ""
	@echo "Variables:"
	@echo "  GO          Go compiler (default: go)"
	@echo "  CC          C compiler for CGO (default: gcc-16)"
	@echo "  CGO_ENABLED Enable CGO (default: 1)"
	@echo "  GOFLAGS     Extra Go flags"
	@echo "  ARGS        Arguments for 'make run'"
