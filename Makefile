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

.PHONY: all build push run clean fmt vet test help integration-test e2e-tests

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
	@echo "  make e2e-tests IMG=ghcr.io/<user>/my-csi-driver:tag REGISTRY=ghcr.io/<user>   # run e2e tests in kind cluster"
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
	@echo "  e2e-tests           Run end-to-end tests in kind cluster (requires kind, kubectl, helm)"
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

# Run end-to-end tests in a kind cluster
# This target requires:
#   - kind (Kubernetes in Docker)
#   - kubectl
#   - helm
#   - Docker image already built (make build IMG=...)
# Environment variables:
#   IMG                  - Docker image to test (required)
#   REGISTRY             - Registry prefix for the image (required)
#   KIND_CLUSTER_NAME    - Name of the kind cluster (default: csi-e2e)
#   SKIP_CLEANUP         - Set to 'true' to skip cleanup on success (default: false)
#
# Usage:
#   make e2e-tests IMG=ghcr.io/user/my-csi-driver:tag REGISTRY=ghcr.io/user
e2e-tests:
	@if [ -z "$(IMG)" ]; then echo "IMG is required (e.g. make e2e-tests IMG=ghcr.io/<user>/my-csi-driver:tag REGISTRY=ghcr.io/<user>)"; exit 1; fi
	@if [ -z "$(REGISTRY)" ]; then echo "REGISTRY is required (e.g. make e2e-tests IMG=ghcr.io/<user>/my-csi-driver:tag REGISTRY=ghcr.io/<user>)"; exit 1; fi
	@if ! command -v kind &> /dev/null; then echo "kind is required but not found in PATH"; exit 1; fi
	@if ! command -v kubectl &> /dev/null; then echo "kubectl is required but not found in PATH"; exit 1; fi
	@if ! command -v helm &> /dev/null; then echo "helm is required but not found in PATH"; exit 1; fi
	@echo "Running e2e tests with IMG=$(IMG), REGISTRY=$(REGISTRY)"
	IMG=$(IMG) REGISTRY=$(REGISTRY) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) SKIP_CLEANUP=$(SKIP_CLEANUP) ./test/integration/e2e-kind.sh

clean:
	@echo "Nothing to clean for container image; docker system prune -f if needed."
