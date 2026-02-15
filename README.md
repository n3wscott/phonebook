# phonebook

Grandstream phonebook server + Asterisk config generator backed by a single YAML source of truth.

## Layout

```
<dir>/
  config.yaml     # required – transports, templates, dialplan, server hints
  defaults.yaml   # optional – repo-wide contact defaults
  contacts/       # required – one or more YAML files (list or contacts:)
    **/*.yaml
```

`config.yaml` defines `[global]`, transports, endpoint templates, and dialplan behavior used when rendering `pjsip.conf`/`extensions.conf` (including optional `dialplan.includes`, `dialplan.conferences`, and `dialplan.messages`). `defaults.yaml` provides repo-wide fallback values (see [examples](examples/)).

Each contact entry contains PBX credentials + XML fields:

```yaml
contacts:
  - id: "scott"
    first_name: "Scott"
    last_name: "Nichols"
    ext: "101"
    password: "secret101"
    account_index: 1         # default for fallback phonebook entry
    group_id: 2
    phones:                  # optional – defaults to the extension
      - number: "6000"
        account_index: 2
    auth:
      username: "101"
    aor:
      max_contacts: 1
```

Validation highlights:
- `ext`/`password` required; duplicates are allowed but last writer wins (with a warning).
- Phone numbers may only contain digits plus `+ * # ,` (spaces are stripped).
- `account_index` ∈ `[1,6]`, `group_id` ∈ `[0,9]`.
- `auth.username` defaults to `ext` when `defaults.yaml` sets `username_equals_ext: true`.

## Commands

```bash
go build -o phonebook .

# Serve phonebook.xml (plus optional staged Asterisk configs)
./phonebook serve --dir ./examples --addr :8080 --base-path /xml/ --out ./out

# Generate phonebook.xml once
./phonebook generate xml --dir ./examples --out ./phonebook.xml

# Generate pjsip.conf + extensions.conf (optionally apply/reload)
./phonebook generate asterisk --dir ./examples --dest ./out [--apply]

# Validate the tree without writing anything
./phonebook validate --dir ./examples
```

`serve` watches `--dir` recursively (fsnotify + 250 ms debounce), hot-rebuilds the in-memory dataset, updates the HTTP snapshot (with `ETag` / `Last-Modified`), and optionally refreshes staged `pjsip.conf`/`extensions.conf` under `--out`. TLS (`--tls-cert/--tls-key`), structured logging (`--log-level`), and base-path overrides match the previous behavior; unspecified paths fall back to the values in `config.yaml`.

`generate asterisk --apply` writes atomically to `--dest` and then runs `asterisk -rx "pjsip reload"` and `dialplan reload`. `serve` never mutates `/etc/asterisk`.

## HTTP Endpoints

- `${basePath}/phonebook.xml` – Grandstream XML (UTF‑8, multi-`<Phone>` support, caching headers)
- `${basePath}/healthz` – `{"ok":true,"contacts":N,"version":V}`
- `${basePath}/debug` – simple HTML listing (log level = `debug`)

Point Grandstream phones at `http://HOST:PORT/<base-path>/` and they will fetch `<base-path>/phonebook.xml`.

## Development

- Sample repo lives in [`examples/`](examples/) and includes generated `expected_phonebook.xml`.
- Goldens for renderers sit under `testdata/`.
- Run the full suite (unit + integration + goldens) with:

```bash
go test ./...
```
