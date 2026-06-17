BINARY := dist/arok
GO_SOURCES := $(shell find cmd internal -name '*.go' | sort)

.PHONY: build test lint fmt fmt-check check clean

build:
	mkdir -p dist
	go build -o $(BINARY) ./cmd/arok

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
