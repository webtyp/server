---
PLAN: "fix: queued plans — httpd RemoteAddr producer, then the dotenv visibility fixes"
EXECUTOR: jules
REVIEWER: none
STATUS: review
SESSION: 15233554538843316298
PR: https://github.com/webtyp/server/pull/19
---

> This plan is dispatched via the CodeJob workflow. See skill: agents-workflow.

# PLAN — execution queue for `webtyp.com/server`

> If you were told to "execute the plan described in docs/PLAN.md", execute
> **ALL the plans below, in order (top to bottom)**. Each plan is
> self-contained; finish one (its acceptance criteria green) before starting
> the next. Never mix changes from one plan into another — separate
> commits/PRs per plan.

| Order | Plan | Subject | Gate |
|-------|------|---------|------|
| 1 | [PLAN_REMOTE_ADDR.md](PLAN_REMOTE_ADDR.md) | httpd exposes `router.ContextKeyRemoteAddr` (phase R2 of `LAN_RUT_AUTH_MASTER_PLAN.md` in `tinywasm/app` docs) | requires `webtyp.com/router` tag with the constant — `go get webtyp.com/router@latest` first |
| 2 | [PLAN_DOTENV_VISIBILITY.md](PLAN_DOTENV_VISIBILITY.md) | unify the unsupported-file-event sentinel with devwatch (Stage A) + make the external server's `.env` visible (Stage B) | Stage A: none. **Stage B: do not start until `webtyp.com/gorun` publishes `Config.EnvFile`** (see its own text) |

After completing all plans, run `gotest ./...` one final time: everything green.
