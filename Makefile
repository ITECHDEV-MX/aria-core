.PHONY: templ build build-snapshot release-snapshot release-check clean tidy run-mcp run-serve

# Generate templ files (dashboard SSR)
templ:
	go tool templ generate ./internal/cloud/dashboard/...

# Build local binary (current platform)
build: templ
	go build -o bin/aria-core -ldflags="-s -w -X main.version=local-$$(git describe --tags --always)" ./cmd/aria-core
	@echo "Built: bin/aria-core"

# Cross-platform snapshot build (no publish, for testing)
build-snapshot: templ
	goreleaser build --snapshot --clean --single-target

# Full snapshot release simulation (all 6 platforms, no publish)
release-snapshot: templ
	goreleaser release --snapshot --clean --skip=publish

# Validate goreleaser config
release-check:
	goreleaser check

# Cut a real release (requires git tag pushed + HOMEBREW_TAP_TOKEN env)
release: release-check
	@if [ -z "$$(git tag --points-at HEAD)" ]; then \
		echo "ERROR: HEAD has no tag. Run: git tag v0.x.y && git push origin v0.x.y"; \
		exit 1; \
	fi
	goreleaser release --clean

# Clean build artifacts
clean:
	rm -rf bin/ dist/

# Tidy go modules
tidy:
	go mod tidy

# Run local MCP server (stdio) — for testing with Claude Code/Cursor
run-mcp: build
	./bin/aria-core mcp

# Run local HTTP serve (port 7437)
run-serve: build
	./bin/aria-core serve
