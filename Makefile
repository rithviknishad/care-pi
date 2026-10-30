GO          ?= go
MODULE      := github.com/rithviknishad/care-pi
BUILDINFO   := $(MODULE)/agent/internal/buildinfo
PKG         := ./agent/cmd/care-pi
BIN_DIR     := bin

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse HEAD 2>/dev/null || echo dev)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
	-X $(BUILDINFO).Version=$(VERSION) \
	-X $(BUILDINFO).Commit=$(COMMIT) \
	-X $(BUILDINFO).Date=$(DATE)

.DEFAULT_GOAL := build

.PHONY: build build-linux test vet lint api-lint fmt tidy clean

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/care-pi $(PKG)

build-linux:
	for arch in arm64 amd64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch \
			$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/care-pi_linux_$$arch $(PKG) || exit 1; \
	done

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint is not installed: see https://golangci-lint.run/welcome/install/"; \
	fi

api-lint:
	sh docs/api/validate.sh

fmt:
	gofmt -l -w .

tidy:
	$(GO) mod tidy

clean:
	rm -rf $(BIN_DIR) coverage.out
