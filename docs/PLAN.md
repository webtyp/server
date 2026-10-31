---
PLAN: "fix: httpd.FromEnv accepts the DNS token as a file (CLOUDFLARE_DNS_API_TOKEN_FILE)"
EXECUTOR: jules
REVIEWER: none
STATUS: review
SESSION: 13655449576233766936
PR: https://github.com/webtyp/server/pull/28
REVIEWED_COMMIT: f6b74a6f3985e141e959cf561d11461b73190ac7
---

> Read AGENTS.md at the repo root before any change.
> **Finish by yourself:** when the delivery check passes, commit, push and open the pull request. Never ask whether to
> proceed or finalize — nobody answers, and a session that waits ends without your work.

# DNS token as a file

## Context

In a self-hosted app's Docker Compose every secret is a file (`/run/secrets/<name>`) and programs read it through the
`<KEY>_FILE` convention. lego, which builds the Cloudflare DNS provider, already reads `CLOUDFLARE_DNS_API_TOKEN_FILE`
on its own. But `httpd.FromEnv` decides whether the token is present with `os.Getenv(EnvDNSAPIToken)` only
(`httpd/env.go:41`), so with the token given as a file it returns `ErrEnvDomainWithoutToken` and the app does not
start. This plan makes the presence check accept either form; the value itself is still read by lego.

## Tools

```bash
go install webtyp.com/devflow/cmd/gotest@latest
```

## Stage 1 — tests first (`tests/fromenv_test.go`)

`setEnv` also clears `httpd.EnvDNSAPIToken + "_FILE"`. Add:

| Test | Setup | Expected |
|---|---|---|
| `TestFromEnv_TokenFile` | `APP_DOMAIN=app.example.com`, `CLOUDFLARE_DNS_API_TOKEN` empty, `CLOUDFLARE_DNS_API_TOKEN_FILE=<file in t.TempDir() containing test-token-not-real>` | `err == nil`, `TLS.DNS01 != nil`, `TLS.Domain == "app.example.com"` |
| `TestFromEnv_TokenFileWithoutDomain` | only `CLOUDFLARE_DNS_API_TOKEN_FILE` set | `ErrEnvTokenWithoutDomain` |

The seven existing cases keep passing unchanged. Commit (red).

## Stage 2 — `httpd/env.go`

Add the constant `fileSuffix = "_FILE"` and replace `token := os.Getenv(EnvDNSAPIToken)` with:

```go
	// The token is present as a value or as a file (Docker secrets); lego reads either.
	token := os.Getenv(EnvDNSAPIToken)
	if token == "" {
		token = os.Getenv(EnvDNSAPIToken + fileSuffix)
	}
```

Update `FromEnv`'s comment: the token may come as `CLOUDFLARE_DNS_API_TOKEN` or `CLOUDFLARE_DNS_API_TOKEN_FILE`.
Run `gotest`: green. Commit.

## Stage 3 — docs

`README.md` table row for `CLOUDFLARE_DNS_API_TOKEN`: «or `CLOUDFLARE_DNS_API_TOKEN_FILE` with the path of a file
holding it (Docker secrets)». Same note in `docs/diagrams/FROM_ENV_FLOW.md` where the token is named. Commit.

## Delivery check

1. `gotest` passes. A PR that does not compile, or whose tests could not run, is a failed delivery.
2. `git diff --stat main` lists only `httpd/env.go`, `tests/fromenv_test.go`, `README.md`, `docs/diagrams/FROM_ENV_FLOW.md`.

## Delivery rules

- Commit after every stage. Never run `git reset`, `git restore`, `git checkout -- <file>` or `git stash` on your own work.
- Stuck for 30 minutes: open the PR anyway, listing what fails.

## Stages

| # | Files | Result |
|---|---|---|
| 1 | `tests/fromenv_test.go` | Token as a file; red |
| 2 | `httpd/env.go` | Presence accepts `_FILE`; green |
| 3 | `README.md`, `docs/diagrams/FROM_ENV_FLOW.md` | Documented |
