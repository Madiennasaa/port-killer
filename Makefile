BIN=portkiller
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

.PHONY: build test install clean fmt cover cover-gate

build:
	go build $(LDFLAGS) -o $(BIN) ./cmd/portkiller

test:
	go vet ./... && go test -race ./...

cover:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

# cover-gate fails the build when statement coverage drops below 100%.
cover-gate:
	go test -coverprofile=coverage.out ./... > /dev/null
	@total=$$(go tool cover -func=coverage.out | tail -1 | awk '{print $$3}' | tr -d '%'); \
	echo "statement coverage: $$total%"; \
	awk -v c="$$total" 'BEGIN { exit !(c >= 100) }'

fmt:
	gofmt -w ./cmd

install: build
	sudo install -m 755 $(BIN) /usr/local/bin/$(BIN)

clean:
	rm -f $(BIN) coverage.out
