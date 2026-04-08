BINARY := gxodus
PKG := github.com/jason/gxodus
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X $(PKG)/internal/cli.Version=$(VERSION)"

.PHONY: build test lint clean

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/gxodus/

test:
	go test ./...

lint:
	golangci-lint run

clean:
	rm -rf bin/ dist/

install:
	go install $(LDFLAGS) ./cmd/gxodus/
