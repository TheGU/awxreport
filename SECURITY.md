# Security policy

## Supported versions

awxreport is pre-1.0. We support the latest released minor version on the
[Releases](https://github.com/TheGU/awxreport/releases) page. Older
versions do not receive security backports — please upgrade.

## Reporting a vulnerability

Please **do not** file a public issue for security problems.

Use GitHub's private vulnerability reporting:
https://github.com/TheGU/awxreport/security/advisories/new

Include:
- A description of the issue and its impact
- Steps to reproduce (if applicable)
- The version of awxreport (`awxreport version`)
- Any proof-of-concept or affected configuration

You can expect an initial response within seven days. We'll work with you
on a coordinated disclosure timeline; for typical severity issues that's
30 days from confirmation to a published fix.

## Scope

In scope:
- The `awxreport` binary and its supporting Go modules under `internal/`
- The release pipeline (workflows under `.github/`)

Out of scope:
- Issues in upstream AWX/AAP themselves — please report those to the
  Ansible community.
- Vulnerabilities in third-party Go dependencies — those are tracked
  via Dependabot and `govulncheck` in CI; please file through the
  upstream project.

## Hardening notes for users

- Treat your AWX OAuth2 token as a secret. awxreport only reads it from
  the `AWX_TOKEN` environment variable; it is never read from a config
  file and never written to logs (the request log redacts the
  Authorization header).
- `insecure_skip_verify: true` disables TLS verification. It exists only
  for development against self-signed certs; never set it in production.
- The debug dump (`-debug ./dir`) writes the full JSON of every API
  response to disk. It can include host names, inventory metadata, and
  job stdout summaries. Delete or restrict the directory after
  troubleshooting; do not commit it.
