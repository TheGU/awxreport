# Configuration reference

awxreport reads `config.yaml` from the working directory by default. Override with `-c PATH` or set `AWXREPORT_CONFIG=PATH`.

The OAuth2 token is **never** read from the config file — it is read from the `AWX_TOKEN` environment variable only. This keeps tokens out of accidentally-committed config.

## Fields

### `base_url` (string, required)
Full URL of the AWX or AAP controller. No trailing slash.

```yaml
base_url: "https://awx.example.com"
```

### `api_root` (string, default `/api/v2`)
- AWX 24.x: `/api/v2`
- AAP 2.5+: `/api/controller/v2`

### `days_back` (integer, default `30`)
Report window in days. The window is `[now - days_back, now]` and uses the job's `finished` timestamp.

### `page_size` (integer, default `200`)
Page size used for paginated list endpoints. AWX caps this at 200; values outside `1..200` are rejected at startup.

### `request_pacing_ms` (integer, default `200`)
Minimum delay between API requests, enforced across the whole client. Lower values run faster but risk hitting rate limits on busy controllers.

### `http_timeout_sec` (integer, default `60`)
Per-request HTTP timeout.

### `max_retries` (integer, default `5`)
Retries on `429` and `5xx` responses. Backoff is exponential and capped at 30 s; `Retry-After` headers are honoured when present.

### `insecure_skip_verify` (boolean, default `false`)
Disables TLS certificate verification. Only use this for development against self-signed certs. Never set this in production.

### `output_dir` (string, default `./out`)
Directory for the XLSX and detail CSV. Created if missing.

### `debug_dir` (string, default empty)
If set, every API JSON response is dumped to `<debug_dir>/<endpoint>/page-NNNNN.json`, plus a `requests.log` of URL + status + latency. The `Authorization` header is redacted from the log. Off by default.

Override at runtime with `--debug PATH`.

### `exclude_templates` (object)
Filters noisy job templates (health checks, frequent service probes) out of the main `Playbooks`, `Hosts`, and `PlaybookHosts` sheets. Excluded templates still appear on the `Excluded` sheet so the filter is auditable.

```yaml
exclude_templates:
  ids: [99, 142]
  name_contains:
    - "health check"
    - "service probe"
    - "heartbeat"
```

- `ids` — exact template IDs to exclude.
- `name_contains` — case-insensitive substring matches against the template name. Whitespace is trimmed.

A template hits the filter if it matches **either** an id or any substring.

## Environment variables

| Variable | Purpose |
|---|---|
| `AWX_TOKEN` | OAuth2 personal token. Required. |
| `AWXREPORT_CONFIG` | Path to config file (overridden by `-c`). |
| `NO_COLOR` | Disable ANSI colour output. Equivalent to `--no-color`. |

## Token creation

In AWX:
1. Click your username (top right) → **Tokens** → **Add**
2. Set Application: `(none)` (so this is a personal access token)
3. Scope: **Read** is sufficient — awxreport never writes
4. Description: e.g. `awxreport monthly report`
5. Save and copy the token. It is only shown once.
