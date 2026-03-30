.PHONY: build install clean test tidy create-key run

BINARY_NAME := cccli
BUILD_DIR := ./build

# Build flags
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')

LDFLAGS := -ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)"

# Default target
all: build

# Build the binary
build: tidy
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/cccli

# Build for Windows
build-windows: tidy
	@echo "Building $(BINARY_NAME) for Windows..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME).exe ./cmd/cccli

# Build for Windows ARM64
build-windows-arm64: tidy
	@echo "Building $(BINARY_NAME) for Windows ARM64..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=windows GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-arm64.exe ./cmd/cccli

# Build for Linux
build-linux: tidy
	@echo "Building $(BINARY_NAME) for Linux..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux ./cmd/cccli

# Build for Linux ARM64
build-linux-arm64: tidy
	@echo "Building $(BINARY_NAME) for Linux ARM64..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/cccli

# Build for macOS
build-darwin: tidy
	@echo "Building $(BINARY_NAME) for macOS..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin ./cmd/cccli

# Build for macOS ARM64
build-darwin-arm64: tidy
	@echo "Building $(BINARY_NAME) for macOS ARM64..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/cccli

# Build all platforms
build-all: build-windows build-windows-arm64 build-linux build-linux-arm64 build-darwin build-darwin-arm64

# Install to GOPATH/bin
install: tidy
	@echo "Installing $(BINARY_NAME)..."
	@go install $(LDFLAGS) ./cmd/cccli

# Run tests
test:
	@echo "Running tests..."
	@go test -v ./...

# Tidy dependencies
tidy:
	@echo "Tidying dependencies..."
	@go mod tidy

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	@go mod download

# Create a new wallet key (usage: make create-key NAME=mykey)
create-key:
	@$(BUILD_DIR)/$(BINARY_NAME) wallet create-key $(NAME) --config ./demo/cccli.yaml

# Run miner (usage: make run NAME=miner1)
run:
	@mkdir -p ../log
	@$(BUILD_DIR)/$(BINARY_NAME) miner run --key $(NAME) --config ./demo/cccli.yaml --log-file ../log/$(NAME).log

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)

# Show help
help:
	@echo "Available targets:"
	@echo "  build         - Build the binary for current platform"
	@echo "  build-windows - Build for Windows"
	@echo "  build-windows-arm64 - Build for Windows ARM64"
	@echo "  build-linux   - Build for Linux"
	@echo "  build-linux-arm64 - Build for Linux ARM64"
	@echo "  build-darwin  - Build for macOS"
	@echo "  build-darwin-arm64 - Build for macOS ARM64"
	@echo "  build-all     - Build for all platforms"
	@echo "  install       - Install to GOPATH/bin"
	@echo "  test          - Run tests"
	@echo "  tidy          - Tidy go modules"
	@echo "  deps          - Download dependencies"
	@echo "  create-key    - Create a new wallet key (NAME=mykey)"
	@echo "  run           - Run miner (NAME=miner1)"
	@echo "  clean         - Remove build artifacts"

