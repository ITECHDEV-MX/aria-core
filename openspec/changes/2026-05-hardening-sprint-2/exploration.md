# Exploration — observability sprint (sprint 2)

**Date**: 2026-05-04

## Findings

- `internal/cloud/cloudserver/` had 7 `log.Printf` calls; no slog
  integration; no request-id correlation.
- Audit finding #7 said "58 log.Printf, 0 slog repo-wide". Migrating
  all 58 in one PR is too risky; we start with the request boundary
  (cloudserver) where the value is highest, and grow incrementally.
- Audit finding #6 said 245 fmt.Errorf without %w. Same incremental
  approach: enable errorlint repo-wide via golangci-lint, fix the
  flagged sites in subsequent PRs.

## Decisions

- `internal/obs/` is the home for cross-cutting observability
  primitives. Keep small: just slog wiring + request-id middleware
  in this sprint. Tracing / metrics stay deferred.
- ARIA_LOG_FORMAT and ARIA_LOG_LEVEL env vars at startup. text by
  default for human ops; switch to json for journald/CF integration.
- request-id is generated when missing, propagated when present.
  Header `X-Request-Id`. Logged on every http_request line.
