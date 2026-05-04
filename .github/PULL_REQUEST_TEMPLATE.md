<!-- Thanks for the PR. A few quick checks before submitting. -->

## What this changes

<!-- One or two sentences. Link the issue if there is one. -->

## How it was tested

<!--
- `go test ./...` (paste the summary or just confirm it passed)
- For CLI/UX changes: paste a snippet of the new output
- For aggregator/report logic: ideally a new test under `internal/`
-->

## Checklist

- [ ] `go test ./...` passes
- [ ] `gofmt -l .` is empty (or `gofmt -w .` was run)
- [ ] `go vet ./...` is clean
- [ ] User-facing changes have a `CHANGELOG.md` entry under `## [Unreleased]`
- [ ] No `config.yaml`, debug dump, or generated report committed
