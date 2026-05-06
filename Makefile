BIN_DIR := bin
GO_FILES := $(shell git ls-files '*.go')
VERSION := $(shell date -u +%y%m%d%H%M)-$(shell git rev-parse --short HEAD 2>/dev/null || echo "nogit")
LDFLAGS := -ldflags="-X main.version=$(VERSION)"

.PHONY: build-binaries test check format

build-binaries:
	mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/steiner ./cmd/steiner

test:
	go test ./...

check:
	go vet ./...
	gofmt -d $(GO_FILES)

format:
	gofmt -w $(GO_FILES)
