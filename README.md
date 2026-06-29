## homework1
Tools used: 
 - OpenAPI Codex (gpt-5.6-sol default)
  - task to swagger spec
 - oapi-codegen

## Running the server

Run the service with the in-memory backend on the default port:

```sh
go run ./src/cmd
```

Configuration is available through command-line flags or environment variables.
Flags take precedence over environment variables.

| Flag | Environment variable | Default | Values |
| --- | --- | --- | --- |
| `--port` | `OBJECT_STORE_PORT` | `8080` | `1`–`65535` |
| `--backend` | `OBJECT_STORE_BACKEND` | `memory` | `memory`, `disk` |
| `--data-dir` | `OBJECT_STORE_DATA_DIR` | `./data` | Disk storage directory |

For example:

```sh
OBJECT_STORE_PORT=9090 OBJECT_STORE_BACKEND=disk \
  OBJECT_STORE_DATA_DIR=./objects go run ./src/cmd
```
