# AGENTS.md

## Project overview

This repository implements a Go HTTP object-storage service.

Objects are addressed by:

`/objects/{bucket}/{objectID}`

Important behavior:

- Objects are immutable after creation.
- Object IDs must be unique within a bucket.
- Identical content is rejected within the same bucket.
- Identical content is allowed in different buckets.
- Storage backends: `memory` and `disk`.

## Repository structure

- `openapi.yaml` — API source of truth.
- `src/api/` — generated oapi-codegen models and HTTP adapters.
- `src/cmd/` — CLI entrypoint and HTTP server lifecycle.
- `src/service/` — API implementation.
- `src/store/` — storage interfaces and backends.
- `test/e2e.sh` — external curl-based integration tests.
- `test/data/` — end-to-end test payloads.

## Generated code

Do not edit `src/api/openapi.gen.go` manually.

After changing `openapi.yaml`, regenerate it with:

```sh
go generate ./...
```

The project uses `oapi-codegen` v2.7.1. The specification remains on OpenAPI 3.0.3 for generator compatibility.

## Development commands

```sh
make run       # Run with the in-memory backend
make run-disk  # Run with disk storage in a new temporary directory
make test      # Run Go tests
make test-e2e  # Build and run external tests against both backends
make build     # Build bin/object-server
```

Before completing a change, run:

```sh
gofmt -w <changed-go-files>
go vet ./...
make test
make test-e2e
```

## Configuration

Command-line flags override environment variables.

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--port` | `OBJECT_STORE_PORT` | `8080` |
| `--backend` | `OBJECT_STORE_BACKEND` | `memory` |
| `--data-dir` | `OBJECT_STORE_DATA_DIR` | `./data` |

Supported backend values are `memory` and `disk`.

## API expectations

- Successful creation returns `201` and `{"id":"<objectID>"}`.
- An occupied ID returns `409`, referencing the existing ID.
- Duplicate content in the same bucket returns `409`, referencing the original object through both the response body and `Location` header.
- Missing GET and DELETE requests return `404`.
- Successful DELETE requests return `200`.
- Request logs must include the HTTP method, bucket, object ID, response status, and duration.

Keep the OpenAPI contract, generated API, service implementation, and end-to-end tests synchronized when changing behavior.

## Implementation guidelines

- Use standard-library packages unless another dependency materially improves the implementation.
- Preserve concurrency safety in both storage backends.
- Prevent bucket and object names from causing filesystem traversal.
- Do not overwrite existing objects.
- Return copies of mutable byte slices from in-memory storage.
- Propagate unexpected storage failures as server errors.
- Keep graceful shutdown behavior intact.
- Add unit tests for isolated behavior and curl-based end-to-end coverage for externally visible API changes.

## AI disclosure

Update the README AI-tools section when AI assistance materially contributes to implementation, design, debugging, or test generation. Describe the assistance and how the resulting work was validated.
