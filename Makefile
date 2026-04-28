BIN_DIR := bin
GO_FILES := $(shell git ls-files '*.go')

.PHONY: build-binaries test check format

build-binaries:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/steiner ./cmd/steiner

test:
	go test ./...

check:
	go vet ./...
	gofmt -d $(GO_FILES)

format:
	gofmt -w $(GO_FILES)
