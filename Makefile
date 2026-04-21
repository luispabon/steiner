BIN_DIR := bin

.PHONY: build-binaries

build-binaries:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/steiner ./cmd/steiner
	go build -o $(BIN_DIR)/steiner-core-tools ./cmd/steiner-core-tools
