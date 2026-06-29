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
| `--backend` | `OBJECT_STORE_BACKEND` | `memory` |
| `--data-dir` | `OBJECT_STORE_DATA_DIR` | `./data` |

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
Bucket and object names are URL-safe base64 encoded before becoming filesystem
paths, preventing path traversal. Writes use a temporary file followed by an
atomic link. Duplicate content is detected by comparing files within the target
bucket.

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
- End-to-end request bodies are stored in `test/data`.
