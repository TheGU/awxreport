# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Selective mode: scope the report to specific job template or project IDs
  via the config `include` block or `--template-ids`/`--project-ids`.
- Allow the AWX/AAP token in config.yaml (`token` key); `AWX_TOKEN` still
  overrides it when set.
- `--full` flag on `report`: ignore the `include` block for one run and
  produce a full report without editing config.yaml. Errors if combined with
  `--template-ids`/`--project-ids`.
- Opt-in `summary_strategy: per_host` config key: walk
  `hosts/{id}/job_host_summaries/` for every active host instead of every
  job's summaries, trading missing null-host/deleted-host summary rows for
  far fewer requests on job-heavy controllers. Not supported in selective
  mode; a mandatory pre-flight check aborts the report if the controller
  doesn't support the filtering it needs. Note: with `per_host`, detail CSV
  rows arrive host-major instead of job-id order.

### Changed
- perf: the report's jobs walk now uses keyset pagination (`id__gt`,
  `order_by=id`) instead of following the `next` link, so it is correct
  against controllers whose envelope `Count` shrinks mid-walk and never
  mistakes a server-capped short page for the end of the walk. `probe` gained
  a matching id__gt filter-support check, but only ever fetches one page
  itself; it does not walk with keyset pagination.
- perf: job (and, in `per_host` mode, host) summary fetches now run with
  bounded concurrency, controlled by the new `summary_workers` config key
  (default 4, 1..8); the shared pacing gate still caps total request
  throughput regardless of worker count. Set `summary_workers: 1` for
  deterministic debug-dump and CSV row ordering.
- Bump `github.com/urfave/cli/v3` to 3.9.0 and `github.com/mattn/go-isatty`
  to 0.0.22. Release workflow now uses goreleaser-action v7 (Node 24).

## [0.1.1] - 2026-06-10

### Added
- `--start-date` / `--end-date` flags on `report` to export an explicit window
  (YYYY-MM-DD, UTC, end date inclusive) instead of the relative `days_back`.

### Fixed
- GoReleaser picking the wrong tag when multiple tags point at the same commit.

## [0.1.0] - 2026-05-04

### Added
- Initial public release scaffold: AWX/AAP REST client, streaming aggregator,
  XLSX + CSV writers, `probe` and `report` subcommands.
- Five-sheet XLSX output: Playbooks, Hosts, PlaybookHosts, Excluded, Meta.
- Per-summary detail CSV alongside the XLSX.
- `last_ok` / `last_failed` timestamps on every aggregated row.
- `ansible_host` extracted from host variables (no extra API call).
- Exclude rules by template id or name substring.
- Debug mode dumps every API page to disk.
- CI: golangci-lint, go test, govulncheck, build matrix on linux/amd64,
  linux/arm64, windows/amd64.
- Release pipeline via GoReleaser, triggered on `v*.*.*` tags.
