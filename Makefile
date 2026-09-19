BINARY_NAME := chatlog
GO := go
ifeq ($(VERSION),)
	VERSION := $(shell git describe --tags --always --dirty="-dev")
endif
LDFLAGS := -ldflags '-X "github.com/sjzar/chatlog/pkg/version.Version=$(VERSION)" -w -s'

PLATFORMS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64 \
	windows/arm64

UPX_PLATFORMS := \
	darwin/amd64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64

.PHONY: all clean lint tidy test test-pytest test-all build crossbuild upx biz2md-demo

all: clean lint tidy test build

clean:
	@echo "🧹 Cleaning..."
	@rm -rf bin/

lint:
	@echo "🕵️‍♂️ Running linters..."
	golangci-lint run ./...

tidy:
	@echo "🧼 Tidying up dependencies..."
	$(GO) mod tidy

test:
	@echo "🧪 Running Go tests..."
	$(GO) test ./... -cover

# Runs Python unit tests for the biz2md clipper-sim integration tool.
# Independent of `make test` because Python isn't a build dependency
# of the Go binary — operators can opt-in via `make test-pytest`.
test-pytest:
	@echo "🐍 Running Python tests for scripts/biz2md-clipper-sim..."
	@cd scripts/biz2md-clipper-sim && \
	  (python3 -c "import zstandard" 2>/dev/null || python3 -m pip install --break-system-packages -q -r requirements.txt) && \
	  (python3 -c "import pytest" 2>/dev/null || python3 -m pip install --break-system-packages -q pytest) && \
	  pytest tests/ -v

# Combined Go + Python test suite. Run this in CI.
test-all: test test-pytest

# One-shot demo: runs the clipper simulator end-to-end against a real
# chatlog workDir and prints status. Use to validate the protocol
# (URL list → md → vault → biz_archived) without installing the
# Obsidian Web Clipper browser extension.
biz2md-demo:
	@echo "🎬 Running biz2md clipper-sim demo..."
	@CHATLOG_TEST_WORK_DIR=$${CHATLOG_WORK_DIR:-/tmp/chatlog-decrypted} \
	  python3 scripts/biz2md-clipper-sim/clipper_sim.py

build:
	@echo "🔨 Building for current platform..."
	CGO_ENABLED=1 $(GO) build -trimpath $(LDFLAGS) -o bin/$(BINARY_NAME) main.go

crossbuild: clean
	@echo "🌍 Building for multiple platforms..."
	for platform in $(PLATFORMS); do \
		os=$$(echo $$platform | cut -d/ -f1); \
		arch=$$(echo $$platform | cut -d/ -f2); \
		float=$$(echo $$platform | cut -d/ -f3); \
		output_name=bin/chatlog_$${os}_$${arch}; \
		[ "$$float" != "" ] && output_name=$$output_name_$$float; \
		echo "🔨 Building for $$os/$$arch..."; \
		echo "🔨 Building for $$output_name..."; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=1 GOARM=$$float $(GO) build -trimpath $(LDFLAGS) -o $$output_name main.go ; \
		if [ "$(ENABLE_UPX)" = "1" ] && echo "$(UPX_PLATFORMS)" | grep -q "$$os/$$arch"; then \
			echo "⚙️ Compressing binary $$output_name..." && upx --best $$output_name; \
		fi; \
	done