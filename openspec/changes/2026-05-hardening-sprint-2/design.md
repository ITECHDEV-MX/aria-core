# Design — sprint 2

## obs package

```go
obs.Init()                 // configure slog from env (idempotent)
obs.L()                    // package logger
obs.FromContext(ctx)       // request-scoped logger or fallback
obs.LoggerWith(ctx, l)     // attach a logger to ctx
obs.WithRequestID(handler) // middleware: request_id + http_request line
```

Env:
- `ARIA_LOG_FORMAT`: `json` | `text` (default text)
- `ARIA_LOG_LEVEL`: `debug` | `info` | `warn` | `error` (default info)

## cloudserver Handler() wrap order

```
outer  → obs.WithRequestID
inner  → dashboard.WrapWithBodyLimit
       → s.mux (route handlers)
```

Body limit is innermost so it shares the context with handlers.

## golangci-lint config

Minimal but real linters: errcheck, errorlint, gosimple, govet,
ineffassign, staticcheck, unused, misspell, bodyclose. CI integration
TBD in a follow-up — this PR ships the config so devs can run
`make lint` locally.
