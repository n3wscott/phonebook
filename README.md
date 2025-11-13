# phonebook

Grandstream-compatible XML phonebook service written in Go.

## Features
- Recursively loads one or more YAML files and normalizes them into Grandstream’s `phonebook.xml` format.
- Watches the configured directory with `fsnotify` and hot-reloads on create/modify/delete (250 ms debounce).
- Deterministic de-duplication (keyed on last name, first name, phone, account index) with override preference for files deeper in the tree and newer mtimes.
- Serves `phonebook.xml`, `healthz`, and optional `debug` endpoints with proper caching headers (`ETag`, `Last-Modified`, `If-None-Match`, `If-Modified-Since`).
- Structured logging via `slog`, optional TLS, configurable base path.

## Getting Started

```bash
go build -o phonebookd ./cmd/phonebookd
./phonebookd --dir ./examples --addr :8080 --base-path /xml/
```

Point the Grandstream “Phone Book XML Server Path” at `http://<host>:8080/xml/`. Phones will request `/xml/phonebook.xml`.

### Flags & Env Vars

| Flag | Env | Description |
| --- | --- | --- |
| `--dir`, `-d` | `PHONEBOOK_DIR` | **Required.** Root directory to scan for YAML files (recursive). |
| `--addr` | `PHONEBOOK_ADDR` | HTTP listen address (default `:8080`). |
| `--base-path` | `PHONEBOOK_BASE_PATH` | Base URL path prefix (default `/`). Use `/xml/` for phone-friendly URLs. |
| `--tls-cert`, `--tls-key` | `PHONEBOOK_TLS_CERT`, `PHONEBOOK_TLS_KEY` | Serve HTTPS when both are provided. |
| `--log-level` | `PHONEBOOK_LOG_LEVEL` | `debug`, `info` (default), or `error`. Debug mode enables the `/debug` endpoint. |

### YAML Schema

Each `.yaml`/`.yml` file can contain either a top-level list or a `contacts:` object:

```yaml
- first_name: John
  last_name: Doe
  phone: "8000"
  account_index: 1
  group_id: 0

contacts:
  - first_name: Lily
    last_name: Lee
    phone: "+1 555 0101"
    account_index: 2
    group_id: 2
```

Validation rules:
- At least one of `first_name` / `last_name`.
- `phone` must be non-empty and only contain digits plus `+`, `*`, `#`, `,`. Spaces are stripped before emitting XML.
- `account_index` ∈ `[1,6]`.
- `group_id` optional, ∈ `[0,9]`.

### Examples & Tests

Sample data lives under `examples/` with a generated `expected_phonebook.xml`. Run the full suite (unit + integration) with:

```bash
go test ./...
```
