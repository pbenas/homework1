GO ?= go
CMD ?= ./src/cmd
BIN ?= bin/object-server
GOVULNCHECK_VERSION ?= v1.5.0

.PHONY: run run-disk test test-e2e coverage vulncheck build

run:
	$(GO) run $(CMD)

run-disk:
	@DATA_DIR="$$(mktemp -d)"; \
		printf 'Disk data directory: %s\n' "$$DATA_DIR"; \
		OBJECT_STORE_BACKEND=disk OBJECT_STORE_DATA_DIR="$$DATA_DIR" $(GO) run $(CMD)

test:
	$(GO) test ./...

test-e2e: build
	bash test/e2e.sh $(BIN)

coverage:
	@COVERAGE_FILE="$$(mktemp)"; \
		trap 'rm -f "$$COVERAGE_FILE"' EXIT; \
		$(GO) test ./... -covermode=atomic -coverprofile="$$COVERAGE_FILE"; \
		$(GO) tool cover -func="$$COVERAGE_FILE" | tail -n 1

vulncheck:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

build:
	mkdir -p $(dir $(BIN))
	$(GO) build -o $(BIN) $(CMD)
