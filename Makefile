BIN=portkiller

.PHONY: build test install clean

build:
	go build -o $(BIN) .

test:
	go vet ./... && go test -race ./...

install: build
	sudo install -m 755 $(BIN) /usr/local/bin/$(BIN)

clean:
	rm -f $(BIN)
