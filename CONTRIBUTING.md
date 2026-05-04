# Contributing to awxreport

Thanks for your interest. Bug reports, feature requests, and pull requests are all welcome.

## Quick start

```bash
git clone https://github.com/TheGU/awxreport.git
cd awxreport
go test ./...
go build -o awxreport ./cmd/awxreport
```

You'll need Go 1.25 or newer. The full CI matrix runs on the latest stable Go.

## Running against a real controller

1. Copy the example config and edit `base_url`:
   ```bash
   cp config.example.yaml config.yaml
   ```
2. Create an OAuth2 personal token in AWX (User → Tokens → Add).
3. Export the token and run:
   ```bash
   export AWX_TOKEN=<your-token>
   ./awxreport probe -debug ./debug
   ```

`probe` is read-only and cheap — it pulls one page from each endpoint to
verify auth and the API shape. Always run it first when targeting a new
controller.

## Testing

```bash
go test ./...                     # all tests
go test -race ./...               # with race detector (needs cgo + gcc)
go test -cover ./internal/...     # with coverage
go test ./internal/awx -run TestPaginate -v   # one suite, verbose
```

The HTTP client tests use `httptest`; no live AWX is required.

## Linting

CI runs `golangci-lint` v2 with the config in `.golangci.yml`. To run locally:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
golangci-lint run
```

Also useful:

```bash
gofmt -l .                        # files needing formatting
go vet ./...
staticcheck ./...                 # go install honnef.co/go/tools/cmd/staticcheck@latest
```

## Pull requests

1. Open an issue first for non-trivial changes so we can align on direction.
2. Keep PRs focused — one concern per PR is easier to review.
3. Add tests for new logic in `internal/`. The existing tests are the model.
4. Update `CHANGELOG.md` under `## [Unreleased]` when user-facing behaviour changes.
5. Don't commit a populated `config.yaml`, debug dumps, or generated reports.
   The `.gitignore` covers the common paths but double-check `git status`.

## Commit messages

We follow [Conventional Commits](https://www.conventionalcommits.org/) loosely
so the auto-generated changelog groups things sensibly:

```
feat: add --since flag for ad-hoc backfills
fix: skip summaries with null host_name and empty hostname
docs: clarify air-gapped install steps
test: cover 401 fail-fast path
ci: bump golangci-lint to v2.6
chore: tidy go.mod
```

The release tooling exposes `feat:` and `fix:` in release notes; other
prefixes are filtered out so the changelog stays user-focused.

## Releases

Maintainers tag the release — contributors don't need to.

```bash
git tag v0.2.0
git push origin v0.2.0
```

GoReleaser runs in GitHub Actions, builds binaries for the supported
matrix, and publishes a GitHub Release with a checksums file.
