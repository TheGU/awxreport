# Architecture

A short tour of how awxreport gets from `AWX_TOKEN` to a finished XLSX.

## Layout

```
cmd/awxreport/         CLI entrypoint (urfave/cli/v3 wiring + UI helpers)
internal/config/       YAML config loader + env-token handling
internal/awx/          REST client, paginator, lookup fetchers, runner
internal/aggregate/    Streaming counters + exclude rules
internal/report/       XLSX writer (5 sheets), detail CSV writer
```

Public API (`internal/`) is intentionally not exported — this is a tool, not a library.

## Data flow

```
                ┌──────────────────────────────────────┐
                │ AWX REST  /api/v2                    │
                └──────────────────────────────────────┘
                              │
                              ▼
        ┌─────────────────────────────────────────────────┐
        │ awx.Client                                      │
        │   - Bearer auth, retry/backoff, pacing,         │
        │     debug dump (per-endpoint JSON files)        │
        └─────────────────────────────────────────────────┘
                              │
            ┌─────────────────┼──────────────────┐
            ▼                 ▼                  ▼
   FetchLookups        IterateJobs         iterJobSummaries
  (templates,        (jobs in window)     (per-job, paged)
   inventories,
   hosts +
   ansible_host)
            │                 │                  │
            └────────┬────────┘                  │
                     ▼                           ▼
        ┌─────────────────────────────────────────────────┐
        │ aggregate.Aggregator                            │
        │   templates[id] -> counters                     │
        │   hosts[id]     -> counters                     │
        │   pairs[t,h]    -> counters                     │
        │   (raw rows are never retained)                 │
        └─────────────────────────────────────────────────┘
                              │
            ┌─────────────────┴──────────────────┐
            ▼                                    ▼
   report.WriteXLSX                    report.DetailCSV
   (5 sheets via streamwriter)         (one row per summary)
```

## Why per-job iteration

AWX 24.6.1 does not expose a top-level `/api/v2/job_host_summaries/` list
endpoint. Summaries live under `/api/v2/jobs/{id}/job_host_summaries/`
and `/api/v2/hosts/{id}/job_host_summaries/`.

Per-job iteration:

- visits **only jobs that ran** in the window — hosts with no recent activity contribute zero requests
- gives us job-level status (`successful`/`failed`/`error`/`canceled`) at the outer loop, separately from per-host outcomes
- maps cleanly to a single `finished__gte=...` filter on the jobs list

Per-host iteration would waste requests on idle hosts and require a separate join to get job statuses.

## Memory model

The aggregator keeps three maps:

| Map | Key | Size estimate |
|---|---|---|
| `Templates` | `template_id` | ~300 entries × ~150 B = trivial |
| `Hosts` | `host_id` | ~10k entries × ~150 B = ~2 MB |
| `Pairs` | `(template_id, host_id)` | ~500k entries × ~120 B = ~60 MB |

Worst case for `Pairs` is `templates × hosts` (3M for our reference environment), but in practice most pairs never occur — health-check templates touch all hosts but they're typically excluded; most templates touch a small subset.

Raw `JobLite` and `SummaryLite` rows are decoded into local variables, fed to the aggregator, then garbage-collected. The XLSX writer uses `excelize.StreamWriter`, so even the `PlaybookHosts` sheet (potentially hundreds of thousands of rows) is written incrementally — no in-memory grid.

## Synthetic host bucket

Some summaries arrive with `host: null` — typically `localhost` plays or ad-hoc commands targeting an unmanaged hostname. We don't drop these:

1. The aggregator hands out negative pseudo-IDs keyed by `host_name`.
2. Each unique synthetic name gets one ID, so subsequent summaries for the same name accumulate into the same `HostAgg`.
3. The `Hosts` sheet renders synthetic IDs as `0` so humans aren't confused; `host_name` carries the real label.

## Rate-limit handling

The `Client.pace()` method enforces a global minimum delay between any two outgoing requests. It's a single mutex-guarded `lastCall` timestamp — concurrent callers (we don't have any today, but the design allows it) all share the same budget, matching AWX's per-token rate limit.

On `429`:
- If the response carries `Retry-After: <seconds>`, sleep for that interval before the next attempt.
- Otherwise, fall through to the standard exponential backoff (`1s, 2s, 4s, ...` capped at 30 s).

`401`/`403` short-circuit immediately — no point retrying a bad token.

## Time semantics

- Window filter on jobs is `finished__gte` (UTC). Jobs still running at run time are excluded; only completed work is counted.
- `Last OK` / `Last Failed` for templates use `job.finished` (job-level resolution).
- `Last OK` / `Last Failed` for hosts and pairs use `summary.modified` (per-host resolution).
- All timestamps in the XLSX/CSV are RFC 3339 in UTC.
