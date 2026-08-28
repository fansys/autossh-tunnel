# Variables
IMAGE_NAME ?= ghcr.io/fansys/autossh-tunnel
IMAGE_TAG ?= latest
VERSION ?= latest
PLATFORMS ?= linux/amd64,linux/arm64

# Registry mirror and proxy support
REGISTRY_MIRROR ?= docker.io
GOPROXY ?= https://goproxy.cn,direct

BUILD_ARGS = --build-arg REGISTRY_MIRROR=$(REGISTRY_MIRROR)
ifneq ($(GOPROXY),)
BUILD_ARGS += --build-arg GOPROXY=$(GOPROXY)
endif

.PHONY: all build test push clean run

# Default target
all: build

# Run Go tests
test:
	@echo "Running tests with race detector..."
	go test -race -v ./...

# Build local binary
build-local:
	@echo "Building local binary..."
	CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o autossh-server .

# Build multi-arch Docker image
build:
	@echo "Building multi-arch Docker image $(IMAGE_NAME):$(IMAGE_TAG)..."
	docker buildx build --platform $(PLATFORMS) \
		$(BUILD_ARGS) \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		.

# Build and Push image to GHCR
push:
	@echo "Building and pushing multi-arch Docker image $(IMAGE_NAME):$(IMAGE_TAG)..."
	docker buildx build --platform $(PLATFORMS) \
		$(BUILD_ARGS) \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		--push .

# Local run
run:
	@echo "Running local server..."
	go run .

# Clean build artifacts
clean:
	@echo "Cleaning artifacts..."
	rm -f autossh-server
