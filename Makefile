BINARY := dist/arok
GO_SOURCES := $(shell find cmd internal -name '*.go' | sort)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "-X github.com/srbouffard/arok/internal/version.Version=$(VERSION) \
                      -X github.com/srbouffard/arok/internal/version.Commit=$(COMMIT) \
                      -X github.com/srbouffard/arok/internal/version.Date=$(DATE)"

.PHONY: build test lint fmt fmt-check check clean

build:
	mkdir -p dist
	go build $(LDFLAGS) -o $(BINARY) ./cmd/arok

test:
	go test ./...

lint:
	go vet ./...
	bash -n install.sh

fmt:
	gofmt -w $(GO_SOURCES)

fmt-check:
	test -z "$$(gofmt -l $(GO_SOURCES))"

check: fmt-check lint test build

clean:
	rm -rf dist
