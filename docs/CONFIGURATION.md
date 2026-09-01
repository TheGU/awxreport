# Configuration reference

awxreport reads `config.yaml` from the working directory by default. Override with `-c PATH` or set `AWXREPORT_CONFIG=PATH`.

The OAuth2 token is read from the `AWX_TOKEN` environment variable, or from the `token` key in the config file as a fallback for scheduled runs with no shell to set an env var. `AWX_TOKEN` always wins when it is set (even if `token` is also set in the file). Prefer `AWX_TOKEN` where possible, since it keeps the token out of accidentally-committed config; if you do put it in the file, restrict the file's permissions.

## Fields

### `token` (string, default empty)
OAuth2 personal token, as a fallback to the `AWX_TOKEN` environment variable for scheduled runs with no shell available to set it. `AWX_TOKEN` overrides this when set. Prefer the environment variable; if the token lives in this file, restrict its permissions (for example `chmod 600 config.yaml`) so it is not readable by other users on the host.

```yaml
token: "your-oauth2-token"
```

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

Override per run with the `report` command's `--start-date` / `--end-date` flags
(`YYYY-MM-DD`, UTC, end date inclusive). Either flag works alone: a missing end
defaults to now, a missing start defaults to `days_back` days before the end.

```bash
awxreport report --start-date 2026-05-01 --end-date 2026-05-31
```

### `page_size` (integer, default `200`)
Page size used for paginated list endpoints. AWX caps this at 200; values outside `1..200` are rejected at startup.

### `request_pacing_ms` (integer, default `200`)
Minimum delay between API requests, enforced across the whole client. Lower values run faster but risk hitting rate limits on busy controllers.

### `http_timeout_sec` (integer, default `60`)
Per-request HTTP timeout.

### `max_retries` (integer, default `5`)
Retries on `429` and `5xx` responses. Backoff is exponential and capped at 30 s, with jitter (half to full backoff) so concurrent workers retrying after a shared `429` don't all retry in lockstep; `Retry-After` headers are honoured when present.

### `summary_workers` (integer, default `4`, range `1..8`)
How many job (or host) summary fetches run concurrently. Total request throughput is still capped by the pacing gate at `1000/request_pacing_ms` requests per second regardless of this value -- more workers hides round-trip latency behind concurrency, it does not raise the request rate. Set to `1` for deterministic debug-dump and CSV row ordering; with `summary_workers > 1`, debug dump sequence numbers and the detail CSV's row order are nondeterministic (summaries for different jobs/hosts interleave depending on which fetch finishes first).

### `summary_strategy` (string, default `"per_job"`)
How `job_host_summary` rows are fetched. There is no `"auto"` value; this is opt-in.

- `per_job` (default): walk `jobs/{id}/job_host_summaries/` for every job in the window. Full fidelity -- sees rows with a null host (ad-hoc/localhost plays) and rows for hosts deleted since their jobs ran.
- `per_host`: walk `hosts/{id}/job_host_summaries/` for every active host instead. Far fewer requests on job-heavy controllers (roughly one series per host instead of one per job), but:
  - Cannot see summary rows whose host record is gone: rows with a null host, and rows for hosts deleted since their jobs ran, are silently missing from the report. Use `per_job` when you need full fidelity.
  - Not supported in selective mode (`include.template_ids`/`include.project_ids` or `--template-ids`/`--project-ids`); selective runs always use the per-job path, and the report fails fast if both are set.
  - Requires the controller to support `job__finished__gte`/`job__finished__lt` filtering on `hosts/{id}/job_host_summaries/`. The report runs a mandatory one-request pre-flight before committing to the walk and aborts with a clear error if that filter isn't supported (`awxreport probe` also checks this ahead of time).
  - CSV rows arrive host-major (grouped by host, not by job id) instead of the `per_job` path's job-id order.
  - "Summaries seen" in the Meta sheet is not directly comparable between strategies: `per_host` counts whatever the host walk actually returned, which by design excludes the null-host/deleted-host rows `per_job` would have counted.
  - The Meta sheet's "Hosts walked" and "Host pruning" rows record how many hosts were walked and whether the active-host filter narrowed that set (falls back to every known host, with a warning, if the controller doesn't support it). The active-host filter itself includes a host whose last job is still running (`last_job.finished` is null) as well as one that finished in-window, so a host mid-run isn't wrongly pruned; this also harmlessly pulls in hosts that have never run a job, whose walk just returns zero rows.

### `insecure_skip_verify` (boolean, default `false`)
Disables TLS certificate verification. Only use this for development against self-signed certs. Never set this in production.

### `output_dir` (string, default `./out`)
Directory for the XLSX and detail CSV. Created if missing.

### `debug_dir` (string, default empty)
If set, every API JSON response is dumped to `<debug_dir>/<endpoint>/page-NNNNN.json`, plus a `requests.log` of URL + status + latency. The `Authorization` header is redacted from the log. Off by default.

Override at runtime with `--debug PATH`.

### `exclude_templates` (object)
Filters noisy job templates (health checks, frequent service probes) out of the main `Playbooks`, `Hosts`, and `PlaybookHosts` sheets. In full mode, excluded templates still appear on the `Excluded` sheet so the filter is auditable. In selective mode this does not hold: excluded templates are removed from the selection before any job is fetched, so the `Excluded` sheet is empty (see the selective-mode caveats below).

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

### `include` (object): selective mode

Production controllers can run thousands of jobs every few minutes, so a full walk of `jobs/` never finishes. `include` scopes the report to specific job templates and/or projects: the tool filters jobs server-side with `job_template__in` and only fetches summaries for those jobs.

```yaml
include:
  template_ids: [4, 9, 12]
  project_ids: [2]
```

- `template_ids`: job template IDs to include.
- `project_ids`: project IDs; every job template belonging to the project (as reported by the controller right now) is included.
- Empty lists (the default) mean a full report. Listing either field switches to selective mode.
- The two lists are unioned, then deduplicated.

Override at the command line instead of editing the config, for ad-hoc runs:

```bash
awxreport report --template-ids 4,9,12 --project-ids 2
```

`--template-ids` and `--project-ids` **replace** the corresponding config list; they never merge with it. Passing an empty/omitted flag leaves the config value in place.

Pass `--full` to ignore the `include` block entirely for one run and produce a full report, without editing `config.yaml`:

```bash
awxreport report --full
```

`--full` cannot be combined with `--template-ids`/`--project-ids` -- that's a startup error. When `--full` overrides a non-empty config `include` block, the Meta sheet records a "Full mode forced by --full" row naming the ignored ids.

**Precedence:** `exclude_templates` always wins over `include`. A template that matches both is removed from the selection before any data is fetched; if that empties the selection entirely, the report fails with an error rather than silently falling back to full mode.

**Caveats:**
- Project membership is resolved from current controller state at report run time, not from historical state: a template moved out of the project after the jobs ran will not be included, even for jobs run while it still belonged.
- Requesting a `template_ids`/`project_ids` value that does not exist on the controller fails fast at selection resolution, before any job is fetched. The narrower, real caveat is: a job whose template was later deleted has a null template reference on the controller, and jobs with a null template reference are invisible to the `job_template__in` filter no matter what is selected.
- The `Excluded` sheet is empty in selective mode: excluded templates are removed from the selection before any job is fetched, so there is no data to report on them.
- The `Hosts` sheet in selective mode lists only hosts touched by the selected templates in the report window, not every host known to the controller.

## Environment variables

| Variable | Purpose |
|---|---|
| `AWX_TOKEN` | OAuth2 personal token. Overrides the `token` config key when set. One of the two is required. |
| `AWXREPORT_CONFIG` | Path to config file (overridden by `-c`). |
| `NO_COLOR` | Disable ANSI colour output. Equivalent to `--no-color`. |

## Token creation

In AWX:
1. Click your username (top right) → **Tokens** → **Add**
2. Set Application: `(none)` (so this is a personal access token)
3. Scope: **Read** is sufficient — awxreport never writes
4. Description: e.g. `awxreport monthly report`
5. Save and copy the token. It is only shown once.
