GO          ?= go
MODULE      := github.com/rithviknishad/care-pi
BUILDINFO   := $(MODULE)/agent/internal/buildinfo
PKG         := ./agent/cmd/care-pi
BIN_DIR     := bin

# Only the release tags vX.Y.Z count. Other tags (for example the fe-bundle tags) are not versions.
# A version that is not vX.Y.Z is a dev build (docs/api/README.md, section 5).
VERSION     ?= $(shell git describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse HEAD 2>/dev/null || echo dev)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
	-X $(BUILDINFO).Version=$(VERSION) \
	-X $(BUILDINFO).Commit=$(COMMIT) \
	-X $(BUILDINFO).Date=$(DATE)

.DEFAULT_GOAL := build

.PHONY: build build-linux console-build test vet lint api-lint fmt tidy clean

# console-build builds the console SPA into console/dist, which the agent embeds.
# It does nothing when console/package.json or npm is missing: the agent then
# serves a "console not built" page.
console-build:
	@if [ -f console/package.json ] && command -v npm >/dev/null 2>&1; then \
		cd console && npm ci && npm run build; \
	elif [ -f console/package.json ]; then \
		echo "npm is not installed: the agent embeds no console build"; \
	fi

build: console-build
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/care-pi $(PKG)

build-linux: console-build
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
