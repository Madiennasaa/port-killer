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

# cover-gate fails the build when any statement block is uncovered and prints
# those blocks so a host-dependent gap is visible in CI logs. It checks the
# blocks, not the rounded total: one missed statement still reads "100.0%".
cover-gate:
	go test -coverprofile=coverage.out ./... > /dev/null
	@total=$$(go tool cover -func=coverage.out | tail -1 | awk '{print $$3}'); \
	echo "statement coverage: $$total"; \
	if ! grep -qE ' 0$$' coverage.out; then exit 0; fi; \
	echo "uncovered blocks:"; \
	grep -E ' 0$$' coverage.out; \
	exit 1

fmt:
	gofmt -w ./cmd

install: build
	sudo install -Dm 755 $(BIN) /usr/local/bin/$(BIN)
	sudo install -Dm 644 docs/portkiller.1 /usr/local/share/man/man1/$(BIN).1

clean:
	rm -f $(BIN) coverage.out
