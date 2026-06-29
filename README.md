# Bucketed object server

A Go HTTP service that stores immutable text objects under
`/objects/{bucket}/{objectID}`. It supports memory and disk storage and rejects
duplicate IDs or duplicate content within a bucket.

## Requirements

- Go 1.25.11 or newer
- `curl` for end-to-end tests
- `oapi-codegen` v2.7.1 when regenerating the API adapter

## Running the server

Run the service with the in-memory backend on the default port:

```sh
go run ./cmd/object-server
```

Configuration is available through command-line flags or environment variables.
Flags take precedence over environment variables.

| Flag | Environment variable | Default | Values |
| --- | --- | --- | --- |
| `--port` | `OBJECT_STORE_PORT` | `8080` | `1`–`65535` |
| `--bind-address` | `OBJECT_STORE_BIND_ADDRESS` | `127.0.0.1` | IPv4 or IPv6 address |
| `--backend` | `OBJECT_STORE_BACKEND` | `memory` | `memory`, `disk` |
| `--data-dir` | `OBJECT_STORE_DATA_DIR` | `./data` | Disk storage directory |
| `--max-object-size` | `OBJECT_STORE_MAX_OBJECT_SIZE` | `1073741824` | Positive byte count |

For example:

```sh
OBJECT_STORE_PORT=9090 OBJECT_STORE_BACKEND=disk \
  OBJECT_STORE_DATA_DIR=./objects go run ./cmd/object-server
```

The server binds to loopback by default. Set `--bind-address` explicitly to
expose it on another interface. PUT bodies must be valid UTF-8 `text/plain`,
must not exceed the configured size limit, and bucket/object identifiers may be
at most 180 UTF-8 bytes.

Common development commands:

```sh
make build      # Build bin/object-server
make test       # Run unit tests
make coverage   # Run unit tests and report overall coverage
make test-e2e   # Test both backends externally with curl
make vulncheck  # Scan reachable code with pinned govulncheck
```

See [`docs/architecture.md`](docs/architecture.md) for the design and module
responsibilities.

## API generation

`openapi.yaml` is the API source of truth. Install the pinned generator and
regenerate the checked-in adapter after changing the specification:

```sh
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.7.1
go generate ./...
```

## AI tool disclosure

OpenAI Codex was used throughout development to:

- translate the assignment into an OpenAPI contract and generate the initial
  `oapi-codegen`-based server structure;
- suggest the package boundaries and implement storage, HTTP lifecycle,
  request logging, CLI configuration, and Make targets;
- generate and iterate on unit and curl-based end-to-end test cases;
- review the implementation for task compliance, security vulnerabilities,
  resource-exhaustion risks, and Go HTTP anti-patterns; and
- draft and refine repository documentation.

AI output was not accepted without verification. Changes were reviewed against
`task.md` and `openapi.yaml`, regenerated code was checked for reproducibility,
and behavior was iterated on after failures or review findings. The resulting
implementation was validated with `go test -race ./...`, `go vet ./...`,
`make coverage`, `make test-e2e`, and `make vulncheck`.

## Suggested future improvements

- Add a Dockerfile and run tests in a container.
- Use a dedicated end-to-end test framework if the suite grows substantially.
- Terminate TLS at the service or a trusted reverse proxy.
- Add performance benchmarks and throughput estimates.
