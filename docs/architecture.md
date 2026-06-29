# Server architecture

## Overview

The service stores immutable text objects addressed by:

```text
/objects/{bucket}/{objectID}
```

It supports `PUT`, `GET`, and `DELETE`. Object IDs and object contents are
unique within a bucket. The same contents may be stored independently in
different buckets.

```text
HTTP request
    |
    v
generated API adapter (src/api)
    |
    v
service rules (src/service)
    |
    v
memory or disk store (src/store)
```

## Packages

### `src/api`

Contains code generated from `openapi.yaml` by `oapi-codegen`.

It provides:

- Request routing and path parameter extraction.
- Request and response types.
- Strict server interfaces implemented by the service layer.
- Serialization of response bodies, status codes, and headers.

`openapi.yaml` is the API source of truth. Do not edit
`src/api/openapi.gen.go` directly; regenerate it with `go generate ./...`.

### `src/cmd`

Contains the executable entrypoint.

It:

- Reads command-line flags and environment variables.
- Selects and initializes the storage backend.
- Connects the service implementation to the generated HTTP adapter.
- Starts the HTTP server.
- Logs every request with its method, bucket, object ID, status, and duration.
- Gracefully stops on Ctrl+C or `SIGTERM`, allowing active requests up to ten
  seconds to finish.

Flags override environment variables:

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--port` | `OBJECT_STORE_PORT` | `8080` |
| `--bind-address` | `OBJECT_STORE_BIND_ADDRESS` | `127.0.0.1` |
| `--backend` | `OBJECT_STORE_BACKEND` | `memory` |
| `--data-dir` | `OBJECT_STORE_DATA_DIR` | `./data` |
| `--max-object-size` | `OBJECT_STORE_MAX_OBJECT_SIZE` | `1073741824` |

The loopback default avoids unintentionally exposing the unauthenticated
service. Request-body size and HTTP timeouts bound resource usage. PUT requests
must use `text/plain`, contain valid UTF-8, and remain within the configured
object-size limit. Bucket and object identifiers are capped at 180 UTF-8 bytes.

### `src/service`

Implements the generated strict server interface and contains HTTP-facing
business rules.

It translates storage results into API responses:

- Successful creation becomes `201 Created`.
- Existing IDs or duplicate content become `409 Conflict`, with the original
  object ID and a `Location` header.
- Missing objects become `404 Not Found`.
- Successful reads and deletes become `200 OK`.
- Unexpected storage errors are returned to the HTTP adapter as server errors.

### `src/store`

Defines the storage interface, shared errors, and both backend implementations.
All backends are safe for concurrent use.

The memory backend stores a map of buckets, each containing object IDs and byte
content. Data is copied when written and read so callers cannot mutate stored
objects.

The disk backend stores one directory per bucket and one file per object.
Bucket and object names are URL-safe base64 encoded, and all data access uses
Go's root-relative filesystem API to prevent symlink and path traversal outside
the configured data directory. A persistent SHA-256 index provides constant-time
duplicate-content lookups; an explicit completion marker lets interrupted
legacy-index migrations resume safely. Per-bucket advisory locks serialize
separate server processes sharing the directory, while exclusive file creation
and stale-index recovery make writes interruption-safe.

The disk backend's cross-process locking uses Unix `flock` and `O_NOFOLLOW`, so
the current implementation is supported on Unix-like operating systems. The
server closes its rooted filesystem handle during graceful shutdown, with an
automatic runtime cleanup as a fallback.

## Request behavior

For `PUT`, the store checks the requested object ID first. If it already
exists, the request conflicts with that object. Otherwise, the backend searches
the bucket for identical content. New content is persisted only when neither
conflict exists.

`GET` returns the exact stored text. `DELETE` removes the addressed object;
deleting or reading an absent object returns `404`.

## Testing

- Package unit tests exercise the generated API, CLI, service, and both stores.
- `make coverage` runs unit tests and prints aggregate coverage.
- `make test-e2e` builds the binary and uses `curl` against both backends.
- `make vulncheck` runs the pinned Go vulnerability scanner against reachable
  code and the standard library used by the configured toolchain.
- End-to-end request bodies are stored in `test/data`.
