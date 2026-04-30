.PHONY: build build-fast generate test lint lint-fix tidy dist clean help

VERSION    ?= dev-$(shell date +%Y-%m-%d-%H-%M)
GO         := go
GOFLAGS    ?= -trimpath
LDFLAGS    := -s -w -X main.Version=$(VERSION)
ENV        := CGO_ENABLED=0

CLIGEN_SRC := $(wildcard cmd/cligen/*.go)
SPEC       := bron-open-api-public.json
STAMP      := generated/.stamp
GEN_FILES  := generated/commands.go generated/helpdoc.go generated/spec.go generated/spec.json

# Default target.
build: $(STAMP)
	$(ENV) $(GO) build $(GOFLAGS) -ldflags='$(LDFLAGS)' -o bin/bron ./cmd/bron

# Force a regen + build (useful after pulling spec changes the timestamp didn't catch).
build-fast: generate
	$(ENV) $(GO) build $(GOFLAGS) -ldflags='$(LDFLAGS)' -o bin/bron ./cmd/bron

# Stamp-based incremental generation: re-run cligen only if the spec
# or its sources moved.
$(STAMP): $(SPEC) $(CLIGEN_SRC)
	$(GO) run ./cmd/cligen $(SPEC) generated
	@touch $@

generate:
	$(GO) run ./cmd/cligen $(SPEC) generated
	@touch $(STAMP)

test:
	$(GO) test ./...

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

tidy:
	$(GO) mod tidy

# Cross-compiled release binaries: bin/bron-<os>-<arch>[.exe].
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64
dist: $(STAMP)
	@mkdir -p bin
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out=bin/bron-$$os-$$arch$$ext; \
		echo "→ $$out"; \
		$(ENV) GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS) -ldflags='$(LDFLAGS)' -o $$out ./cmd/bron; \
	done

clean:
	rm -rf bin/ generated/

help:
	@echo "Targets:"
	@echo "  build       — incremental: regen if spec/cligen changed, then build bin/bron"
	@echo "  build-fast  — always regen, then build (use after suspicious mtime issues)"
	@echo "  dist        — cross-compile for darwin/linux × amd64/arm64 into bin/"
	@echo "  generate    — force-run cligen against $(SPEC)"
	@echo "  test        — go test ./..."
	@echo "  lint        — golangci-lint run ./..."
	@echo "  lint-fix    — golangci-lint --fix"
	@echo "  tidy        — go mod tidy"
	@echo "  clean       — remove bin/ and generated/"
	@echo ""
	@echo "Vars: VERSION=<tag> (default: dev)"
