BIN_DIR := bin

.PHONY: build-binaries test check format

build-binaries:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/steiner ./cmd/steiner
	go build -o $(BIN_DIR)/steiner-core-tools ./cmd/steiner-core-tools

test:
	go test ./...

check:
	go vet ./...
	gofmt -d .

format:
	gofmt -w $(shell go list ./...)
