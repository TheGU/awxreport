# awxreport

[![CI](https://github.com/TheGU/awxreport/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/TheGU/awxreport/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/TheGU/awxreport?include_prereleases&sort=semver)](https://github.com/TheGU/awxreport/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/TheGU/awxreport)](go.mod)

A small CLI that pulls an AWX / Ansible Automation Platform controller and writes a monthly XLSX + CSV report on playbook and host activity. Single static binary, no runtime dependencies, friendly to air-gapped servers.

## What it gives you

For every job template in the configured time window:
- total jobs run, broken down by status (successful / failed / error / canceled)
- distinct hosts touched and total per-host runs
- last run, last successful run, last failed run

For every host:
- whether it ever ran successfully
- which templates it ran, with per-template counts
- inventory it belongs to
- `ansible_host` (parsed from host vars when present)
- last run, last successful run, last failed run

The XLSX has four data sheets — `Playbooks`, `Hosts`, `PlaybookHosts` (the cross-product, ready for pivoting), `Excluded` (templates filtered out by config) — plus a `Meta` sheet with run provenance. Every job_host_summary row is also written to a CSV alongside, for downstream processing.

## Why a separate tool

AWX's built-in dashboard is real-time and per-organization; it doesn't roll up a month of activity in a way you can hand to leadership. This tool is read-only against the AWX REST API, never touches the database, and produces files you can email or check into a wiki.

## Install

Pre-built binaries will be published on the [Releases](https://github.com/TheGU/awxreport/releases) page once CI is in place. For now, build from source:

```bash
git clone https://github.com/TheGU/awxreport.git
cd awxreport
go build -o awxreport ./cmd/awxreport
```

## Quickstart

1. Copy the example config and edit `base_url`:
   ```bash
   cp config.example.yaml config.yaml
   ```
2. Create an OAuth2 personal token in AWX (User → Tokens → Add) with read scope.
3. Export it and run:
   ```bash
   export AWX_TOKEN=<your-token>
   ./awxreport probe              # validate connectivity + auth
   ./awxreport report             # generate the monthly report
   ```

Output lands in `./out/awx-rollout-<timestamp>.xlsx` and `./out/awx-rollout-detail-<timestamp>.csv`.

## Configuration

All knobs live in `config.yaml`. The token is read only from the `AWX_TOKEN` env var — never from the file.

Key fields (see `config.example.yaml` for the full set):

| Field | Default | Purpose |
|---|---|---|
| `base_url` | — | AWX/AAP URL, no trailing slash |
| `api_root` | `/api/v2` | `/api/v2` for AWX 24.x, `/api/controller/v2` for AAP 2.5+ |
| `days_back` | `30` | report window |
| `page_size` | `200` | paginated page size (AWX max is 200) |
| `request_pacing_ms` | `200` | delay between API requests |
| `max_retries` | `5` | retries on 429/5xx with exponential backoff |
| `insecure_skip_verify` | `false` | only set for dev clusters with self-signed certs |
| `exclude_templates` | — | filter out noisy templates (health checks, probes) |

## Debug mode

Pass `-debug ./debug` to either subcommand and every API page is dumped to disk under `debug/<endpoint>/page-NNNNN.json`, with a `requests.log` of URL + status + latency. Off by default. Useful for troubleshooting field-name differences across AWX versions.

## How it works

- Fetches three reference tables once: `job_templates`, `inventories`, `hosts` (with `ansible_host` extracted from each host's `variables` blob).
- Streams jobs in the window via `/api/v2/jobs/?finished__gte=...`.
- For each job, fetches `/api/v2/jobs/{id}/job_host_summaries/` to get per-host outcomes.
- Aggregates into in-memory counters keyed by `(template_id, host_id)`. Raw rows are never retained.
- Writes the XLSX via the streaming writer; emits the CSV row-by-row.

Memory usage is dominated by the pair counters; sparse worst case is ~60 MB for ~500k unique pairs.

## Documentation

- [Configuration reference](docs/CONFIGURATION.md) — every config field, every env var, every flag.
- [Deployment](docs/DEPLOYMENT.md) — air-gapped install, scheduled runs, sizing.
- [Architecture](docs/ARCHITECTURE.md) — data flow, memory model, rate-limit behaviour.
- [Contributing](CONTRIBUTING.md) — dev setup, tests, commit style.
- [Changelog](CHANGELOG.md)
- [Security policy](SECURITY.md)

## License

MIT — see [LICENSE](LICENSE).
