BIN_DIR := bin
GO_FILES := $(shell git ls-files '*.go')
VERSION := $(shell date -u +%y%m%d%H%M)-$(shell git rev-parse --short HEAD 2>/dev/null || echo "nogit")
LDFLAGS := -ldflags="-X main.version=$(VERSION)"

GOLANGCI_LINT_VERSION := latest
GOVULNCHECK_VERSION := latest
GOIMPORTS_VERSION := latest

.PHONY: install-check-tools
install-check-tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)

.PHONY: build test test-race vet fmt fmt-check imports imports-check tidy-check lint vuln check quick-check ci-check build-binaries

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
	test -z "$$(gofmt -l $(GO_FILES))"

imports:
	goimports -w $(GO_FILES)

imports-check:
	test -z "$$(goimports -l $(GO_FILES))"

tidy-check:
	go mod tidy
	git diff --exit-code go.mod go.sum

lint:
	golangci-lint run ./...

vuln:
	govulncheck ./...

quick-check: fmt-check imports-check build test vet

check: quick-check lint vuln

ci-check: tidy-check check test-race build-binaries

format:
	gofmt -w $(GO_FILES)
