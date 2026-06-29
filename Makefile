GO ?= go
CMD ?= ./src/cmd
BIN ?= bin/object-server

.PHONY: run test build

run:
	$(GO) run $(CMD)

test:
	$(GO) test ./...

build:
	mkdir -p $(dir $(BIN))
	$(GO) build -o $(BIN) $(CMD)
