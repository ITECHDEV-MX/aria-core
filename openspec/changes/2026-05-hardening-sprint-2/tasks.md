# Tasks — sprint 2

- [ ] internal/obs/log.go (Init, L, ctx helpers)
- [ ] internal/obs/middleware.go (WithRequestID, recorderRW)
- [ ] internal/obs/log_test.go (7 tests)
- [ ] cloudserver: replace log.Printf calls with obs.L().Info(fmt.Sprintf)
- [ ] cloudserver Handler() wraps with obs.WithRequestID
- [ ] .golangci.yml with errorlint + standard set
- [ ] PR + CI green + merge + tag v0.10.0
