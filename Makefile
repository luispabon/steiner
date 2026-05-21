BIN_DIR := bin
GO_FILES := $(shell git ls-files '*.go')
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo "dev")
LDFLAGS := -ldflags="-X main.version=$(VERSION)"

GOLANGCI_LINT_VERSION := v2.12.2
GOVULNCHECK_VERSION := v1.3.0
GOIMPORTS_VERSION := v0.45.0

default: build-binaries

.PHONY: install-check-tools
install-check-tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)

.PHONY: build-binaries test test-race vet fmt fmt-check imports imports-check tidy-check lint vuln check

build-binaries:
	mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/steiner ./cmd/steiner

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

check: tidy-check fmt-check imports-check build-binaries test test-race vet lint vuln

format:
	gofmt -w $(GO_FILES)
