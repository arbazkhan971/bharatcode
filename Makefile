# Makefile for BharatCode.
#
# All binaries are built CGO-free for static, portable output. CGO_ENABLED=0 is
# exported for every target so `go build`, `go test`, `go vet`, and `go install`
# all run without a C toolchain. The lone exception is `test-race`: the race
# detector requires cgo, so that recipe overrides the flag inline.

# Force a static, CGO-free build everywhere.
export CGO_ENABLED := 0

# Binary metadata.
BINARY := bharatcode
BIN_DIR := bin
MAIN := .

# Tools.
GO ?= go
GOFUMPT ?= gofumpt

.DEFAULT_GOAL := all

.PHONY: all build test test-race lint fmt install validate-config

# all runs the default developer loop: format check, vet, build, and test.
all: lint build test

# build compiles a static binary into bin/.
build:
	$(GO) build -o $(BIN_DIR)/$(BINARY) $(MAIN)

# test runs the full test suite.
test:
	$(GO) test ./...

# test-race runs the suite with the race detector. The race detector needs cgo,
# so this target overrides CGO_ENABLED for the test command only.
test-race:
	CGO_ENABLED=1 $(GO) test -race ./...

# lint checks formatting with gofmt and runs go vet.
lint:
	@echo "==> gofmt"
	@gofmt_out="$$(gofmt -l .)"; \
	if [ -n "$$gofmt_out" ]; then \
		echo "The following files are not gofmt-compliant:"; \
		echo "$$gofmt_out"; \
		exit 1; \
	fi
	@echo "==> go vet"
	$(GO) vet ./...

# fmt rewrites all Go files with gofumpt.
fmt:
	$(GOFUMPT) -w .

# install installs the binary into the Go bin directory.
install:
	$(GO) install $(MAIN)

# validate-config verifies the embedded default config is valid.
validate-config:
	$(GO) run ./scripts/validate-defaults.go
