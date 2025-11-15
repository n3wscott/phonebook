# Codex Prompt – Bootstrap A New Phonebook Repo

Use this prompt when you want Codex (or yourself in a future session) to spin up a fresh `phonebook` data directory with sane defaults, sample contacts, and passwords. Paste the template below into your assistant, then replace the placeholder values.

```
You are working with github.com/n3wscott/phonebook. I need a brand new data root at <DATA_ROOT> that will feed the service/binary.

Goals
1. Create the required YAML structure:
   - <DATA_ROOT>/config.yaml describing the PBX (global/user_agent, network, transports, endpoint templates, dialplan context, server.addr/base_path).
   - <DATA_ROOT>/defaults.yaml with sensible defaults (auth.username_equals_ext, endpoint template name, AOR limits).
   - <DATA_ROOT>/contacts/… with one or more YAML files listing users/extensions + passwords.
2. Every contact entry must include:
   - `id`, `first_name`, `last_name`
   - `ext` (3–4 digit string)
   - `password` (plain-text SIP secret; create unique values)
   - Optional `phones` array; if omitted use `ext` for the Grandstream phonebook
   - `account_index` (1–6) and optional `group_id` (0–9)
3. Produce a short README snippet reminding operators how to run:
   ```
   phonebook serve --dir <DATA_ROOT> --addr :8080 --base-path /xml/
   phonebook generate asterisk --dir <DATA_ROOT> --dest ./out
   ```

Detailed requirements
- `config.yaml` must contain at least one UDP transport (`transports[0].name = transport-udp`, `protocol = udp`, `bind = 0.0.0.0:5060`) and an endpoint template named `endpoint-template`.
- Include a `network` stanza with `external_signaling_address`, `external_media_address`, and at least one `local_net`.
- `defaults.yaml` should keep `auth.username_equals_ext: true`, `endpoint.template: endpoint-template`, `aor.max_contacts: 1`, `aor.remove_existing: true`, `aor.qualify_frequency: 30`.
- Contacts live under `contacts/*.yaml`; feel free to create multiple files (e.g., contacts/sales.yaml, contacts/engineering.yaml) to show the recursive loader.
- When generating passwords, mix letters and digits (e.g., `B3taPass!01`).
- After writing the files, run `go run . validate --dir <DATA_ROOT>` to prove everything parses.
- Show the resulting directory tree and the contents of each file in the response.
```

## Quick Reference: Required Files

| Path                        | Purpose                                                                    |
|-----------------------------|----------------------------------------------------------------------------|
| `config.yaml`               | Top-level PBX configuration (transports, endpoint templates, dialplan).   |
| `defaults.yaml`             | Repo-wide defaults for AOR/auth/endpoint template selection.              |
| `contacts/**/*.yaml`        | User + password definitions (`contacts:` blocks or plain YAML lists).     |

## Minimal `config.yaml` Skeleton

```yaml
global:
  user_agent: "Asterisk"

network:
  external_signaling_address: "203.0.113.10"
  external_media_address: "203.0.113.10"
  local_net:
    - "192.168.1.0/24"

transports:
  - name: "transport-udp"
    protocol: "udp"
    bind: "0.0.0.0:5060"

endpoint_templates:
  - name: "endpoint-template"
    context: "internal"
    disallow: ["all"]
    allow: ["ulaw"]
    direct_media: false
    rtp_symmetric: true
    force_rport: true
    rewrite_contact: true
    transport: "transport-udp"

dialplan:
  context: "internal"

server:
  addr: ":8080"
  base_path: "/xml/"
```

## Minimal `defaults.yaml`

```yaml
aor:
  max_contacts: 1
  remove_existing: true
  qualify_frequency: 30

auth:
  username_equals_ext: true

endpoint:
  template: "endpoint-template"
```

## Contact Snippet

```yaml
contacts:
  - id: "alice"
    first_name: "Alice"
    last_name: "Nguyen"
    ext: "201"
    password: "S1pPass201!"
    account_index: 1
    group_id: 0
  - id: "bob"
    first_name: "Bob"
    last_name: "Smith"
    ext: "202"
    password: "S1pPass202!"
    phones:
      - number: "+1 555 0102"
        account_index: 2
```

Drop this file into your repo (or copy the sections you need) whenever you want to spin up a fresh dataset with Codex. It captures the mandatory structure and the commands that prove the configuration works end-to-end. 
