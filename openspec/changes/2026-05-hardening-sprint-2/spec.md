# Spec — sprint 2

## obs.Init contract

- Idempotent (sync.Once).
- Reads env once.
- Returns the configured logger; also calls slog.SetDefault.

## obs.WithRequestID contract

- Honors incoming X-Request-Id when length is in [1, 128].
- Else generates 16-byte hex.
- Sets X-Request-Id response header.
- Logs an `http_request` line on completion with `request_id`,
  `method`, `path`, `status`, `duration_ms`.
- Wraps r.Context() with the request-scoped logger so handlers can
  call obs.FromContext(r.Context()) to inherit the tagged logger.

## cloudserver behavior change

- Every cloudserver response now has X-Request-Id.
- Every request emits an `http_request` slog line at the end.
