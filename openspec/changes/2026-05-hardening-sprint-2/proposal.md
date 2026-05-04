# Proposal — sprint 2

**Owner**: contacto@itechpymes.com.mx
**Status**: Drafting (branch `feat/hardening-sprint-2`)

## Goal

Migrate cloudserver request boundary to slog with structured fields
and request-id correlation. Land golangci-lint config so future PRs
can iterate on errorlint findings.

## Scope

In:
- `internal/obs/` package: Init, L, FromContext, WithRequestID
  middleware, recorderRW.
- 7 hermetic tests covering parseLevel, ctx propagation, request-id
  header behavior.
- `internal/cloud/cloudserver/`: replace 7 log.Printf calls with
  obs.L().Info(fmt.Sprintf(...)) — minimal-diff variant. Not yet
  structured-fields; that comes in a follow-up.
- cloudserver Handler() wraps with obs.WithRequestID.
- `.golangci.yml` with errorlint + standard linters.

Out:
- Repo-wide migration of every log.Printf (deferred).
- Tracing (otel) — deferred.
- Metrics (prometheus) — deferred.
- Critical-package errorlint fixes (vault/redactor) — deferred to
  the dedicated errorlint PR.
