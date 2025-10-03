# Simple Makefile for building and pushing the my-csi-driver image
# Usage:
#   make build IMG=ghcr.io/<user>/my-csi-driver:tag
#   make push  IMG=ghcr.io/<user>/my-csi-driver:tag
#   make all   IMG=ghcr.io/<user>/my-csi-driver:tag
#
# Defaults can be overridden via environment variables or make variables.

SHELL := /bin/bash

# Image reference (override with IMG=...)
REGISTRY ?=
IMAGE_NAME ?= my-csi-driver
IMAGE_TAG ?= dev

# When REGISTRY is set, prefix image with it; else just name:tag
ifneq ($(strip $(REGISTRY)),)
  IMG ?= $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
else
  IMG ?= $(IMAGE_NAME):$(IMAGE_TAG)
endif

# Build args
GO_BUILD_FLAGS ?=
DOCKER_BUILD_ARGS ?=

.PHONY: all build push run clean fmt vet test help integration-test

all: build

# Show help for common targets and variables
help:
	@echo "my-csi-driver Makefile"
	@echo
	@echo "Usage examples:"
	@echo "  make build IMG=ghcr.io/<user>/my-csi-driver:tag"
	@echo "  make push  IMG=ghcr.io/<user>/my-csi-driver:tag"
	@echo "  make all   IMG=ghcr.io/<user>/my-csi-driver:tag"
	@echo "  make run   IMG=my-csi-driver:dev CSI_ENDPOINT=unix:///csi/csi.sock CSI_BACKING_DIR=/data"
	@echo "  make integration-test   # run integration tests (requires 'csc' in PATH)"
	@echo
	@echo "Variables (overridable):"
	@echo "  REGISTRY           Optional registry prefix (e.g., ghcr.io/<user>)"
	@echo "  IMAGE_NAME         Image name (default: $(IMAGE_NAME))"
	@echo "  IMAGE_TAG          Image tag (default: $(IMAGE_TAG))"
	@echo "  IMG                Full image ref (default: computed from REGISTRY/IMAGE_NAME:IMAGE_TAG)"
	@echo "  DOCKER_BUILD_ARGS  Extra args passed to 'docker build'"
	@echo "  RUN_ARGS           Extra args for 'docker run'"
	@echo "  CSI_ENDPOINT       CSI endpoint (e.g., unix:///csi/csi.sock)"
	@echo "  CSI_BACKING_DIR    Backing dir path inside the container"
	@echo
	@echo "All targets including local go build helpers:"
	@echo "  help                Show this help"
	@echo "  build               Build the container image ($(IMG))"
	@echo "  push                Push the image ($(IMG))"
	@echo "  run                 Run the driver container (privileged; mounts /dev)"
	@echo "  fmt                 Run 'go fmt ./...'"
	@echo "  vet                 Run 'go vet ./...'"
	@echo "  test                Run 'go test ./... -v'"
	@echo "  integration-test    Run 'go test -tags=integration ./test/integration -v' (requires 'csc')"
	@echo "  clean               No-op; use 'docker system prune -f' if needed"
	@echo
	@echo "Current IMG: $(IMG)"

# Build the container image using the Dockerfile at repo root
build:
	docker build $(DOCKER_BUILD_ARGS) -t $(IMG) .
	@echo "Built image: $(IMG)"

# Push the container image
push:
	@if [ -z "$(IMG)" ]; then echo "IMG is required (e.g. make push IMG=ghcr.io/<user>/my-csi-driver:tag)"; exit 1; fi
	docker push $(IMG)
	@echo "Pushed image: $(IMG)"

# Convenience: run the driver locally inside a container
# Example (adjust env/sock/backing dir):
#   make run IMG=my-csi-driver:dev CSI_ENDPOINT=unix:///csi/csi.sock CSI_BACKING_DIR=/data
CSI_ENDPOINT ?=
CSI_BACKING_DIR ?=
RUN_ARGS ?=
run:
	@if [ -z "$(IMG)" ]; then echo "IMG is required (e.g. make run IMG=my-csi-driver:dev)"; exit 1; fi
	docker run --rm \
	  -e CSI_ENDPOINT=$(CSI_ENDPOINT) \
	  -e CSI_BACKING_DIR=$(CSI_BACKING_DIR) \
	  -v $(PWD):/workspace \
	  -v /var/lib:/var/lib \
	  -v /dev:/dev \
	  --privileged \
	  --name my-csi-driver \
	  $(RUN_ARGS) \
	  $(IMG)

# Local go build/test helpers
fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./... -v

# Run integration tests that require csc and a local driver process
integration-test:
	go clean -testcache
	go test -tags=integration ./test/integration -v

clean:
	@echo "Nothing to clean for container image; docker system prune -f if needed."
