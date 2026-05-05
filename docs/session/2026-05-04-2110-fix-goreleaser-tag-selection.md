# Session: Fix GoReleaser tag selection on same-commit release tags

- Status: done
- Task: Diagnose why a `v0.1.0` tag push released `v0.1.0-rc1` and fix the workflow.
- TASK.md: not present in repo at session start.

## Progress log

1. Inspected `.github/workflows/release.yml` and `.goreleaser.yaml`.
2. Confirmed both tags `v0.1.0-rc1` and `v0.1.0` point to the same commit locally.
3. Confirmed the release workflow currently lets GoReleaser infer the current tag from git state.
4. Patched `.github/workflows/release.yml` to set `GORELEASER_CURRENT_TAG: ${{ github.ref_name }}` for the GoReleaser step.
5. Validated `.goreleaser.yaml` with `go run github.com/goreleaser/goreleaser/v2@v2.15.4 check`.
6. `TASK.md` is still not present in the repo, so there was nothing to update.
