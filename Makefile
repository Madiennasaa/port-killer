BIN=portkiller
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

.PHONY: build test install clean fmt cover

build:
	go build $(LDFLAGS) -o $(BIN) ./cmd/portkiller

test:
	go vet ./... && go test -race ./...

cover:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

fmt:
	gofmt -w ./cmd

install: build
	sudo install -m 755 $(BIN) /usr/local/bin/$(BIN)

clean:
	rm -f $(BIN) coverage.out
