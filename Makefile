.PHONY: build test lint lint-fix tidy dist clean help

VERSION    ?= dev-$(shell date +%Y-%m-%d-%H-%M)
GO         := go
GOFLAGS    ?= -trimpath
LDFLAGS    := -s -w -X main.Version=$(VERSION)
ENV        := CGO_ENABLED=0

# Default target. The command tree is built at runtime from the bron-api-toolkit
# catalog — there is no generated Go source in this repo anymore.
build:
	$(ENV) $(GO) build $(GOFLAGS) -ldflags='$(LDFLAGS)' -o bin/bron ./cmd/bron

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
dist:
	@mkdir -p bin
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out=bin/bron-$$os-$$arch$$ext; \
		echo "→ $$out"; \
		$(ENV) GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS) -ldflags='$(LDFLAGS)' -o $$out ./cmd/bron; \
	done

clean:
	rm -rf bin/
