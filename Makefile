GO ?= go
CMD ?= ./src/cmd
BIN ?= bin/object-server

.PHONY: run run-disk test build

run:
	$(GO) run $(CMD)

run-disk:
	@DATA_DIR="$$(mktemp -d)"; \
		printf 'Disk data directory: %s\n' "$$DATA_DIR"; \
		OBJECT_STORE_BACKEND=disk OBJECT_STORE_DATA_DIR="$$DATA_DIR" $(GO) run $(CMD)

test:
	$(GO) test ./...

build:
	mkdir -p $(dir $(BIN))
	$(GO) build -o $(BIN) $(CMD)
