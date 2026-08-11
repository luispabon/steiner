BIN_DIR := bin
GO_FILES := $(shell git ls-files '*.go')
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo "dev")
COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "none")
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "unknown")
GO_VERSION := $(shell go version | cut -d' ' -f3 2>/dev/null || echo "unknown")
LDFLAGS := -ldflags="-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE) -X main.goVersion=$(GO_VERSION)"
RELEASE_LDFLAGS := -ldflags="-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE) -X main.goVersion=$(GO_VERSION)"
UNAME_S := $(shell uname -s)
CGO_BUILD_PREFIX := $(if $(filter Linux,$(UNAME_S)),CGO_ENABLED=0 ,)

GOLANGCI_LINT_VERSION := v2.12.2
GOVULNCHECK_VERSION := v1.3.0
GOIMPORTS_VERSION := v0.45.0

default: build-binaries

.PHONY: install-check-tools
install-check-tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)

.PHONY: build build-binaries build-binaries-slim build-binaries-dev test test-race vet fmt fmt-check imports imports-check tidy-check lint vuln bench check

build: build-binaries

build-binaries:
	mkdir -p $(BIN_DIR)
	$(CGO_BUILD_PREFIX)go build $(LDFLAGS) -o $(BIN_DIR)/steiner ./cmd/steiner

build-binaries-slim:
	mkdir -p $(BIN_DIR)
	$(CGO_BUILD_PREFIX)go build $(RELEASE_LDFLAGS) -trimpath -o $(BIN_DIR)/steiner ./cmd/steiner
	@case "$$(uname -s)" in \
		Darwin) echo "UPX skipped: macOS is not supported (see https://github.com/upx/upx/issues/612)" ;; \
		*) \
			command -v upx >/dev/null 2>&1 || { \
				echo "upx not installed; install with 'apt-get install upx-ucl' or 'brew install upx'"; \
				exit 1; \
			}; \
			upx -7 $(BIN_DIR)/steiner ;; \
	esac

SHA := $(shell git rev-parse --short HEAD)
build-binaries-dev:
	mkdir -p $(BIN_DIR)
	$(CGO_BUILD_PREFIX)go build -ldflags="-X main.version=dev-$(SHA) -X main.commit=$(SHA) -X main.buildDate=$(BUILD_DATE) -X main.goVersion=$(GO_VERSION)" -o $(BIN_DIR)/steiner ./cmd/steiner

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w $(GO_FILES)
	goimports -w $(GO_FILES)

fmt-check:
	@files="$$(gofmt -l $(GO_FILES))"; \
	if [ -n "$$files" ]; then \
		printf 'gofmt needs to run on:\n%s\n' "$$files"; \
		exit 1; \
	fi

imports:
	@command -v goimports >/dev/null 2>&1 || { \
		echo "missing goimports; run 'make install-check-tools'"; \
		exit 1; \
	}
	goimports -w $(GO_FILES)

imports-check:
	@command -v goimports >/dev/null 2>&1 || { \
		echo "missing goimports; run 'make install-check-tools'"; \
		exit 1; \
	}
	@files="$$(goimports -l $(GO_FILES))"; \
	if [ -n "$$files" ]; then \
		printf 'goimports needs to run on:\n%s\n' "$$files"; \
		exit 1; \
	fi

tidy-check:
	go mod tidy
	git diff --exit-code go.mod go.sum

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "missing golangci-lint; run 'make install-check-tools'"; \
		exit 1; \
	}
	golangci-lint run ./...

vuln:
	@command -v govulncheck >/dev/null 2>&1 || { \
		echo "missing govulncheck; run 'make install-check-tools'"; \
		exit 1; \
	}
	govulncheck ./...

check: tidy-check
	$(MAKE) -j6 fmt-check imports-check build-binaries test-race vet lint vuln

# Run TUI benchmarks. Default: all suites, 1s each, single count.
# Run a specific suite: `make bench BENCH=BenchmarkKeystroke`
# Pass extra go test flags: `make bench BENCH_FLAGS="-benchtime=3x -count=3"`
# Combine: `make bench BENCH=BenchmarkView BENCH_FLAGS="-count=5"`
bench:
	go test -run=$$^ -bench=$(or $(BENCH),.) $(BENCH_FLAGS) -benchtime=$(or $(BENCHTIME),1s) -count=$(or $(COUNT),1) ./internal/tui/...

format:
	gofmt -w $(GO_FILES)
