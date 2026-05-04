# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
