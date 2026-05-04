package mcp

import "os"

// envOrEmpty returns os.Getenv(key). Wrapped so tests can stub via
// the package-level getEnv var if needed.
func envOrEmpty(key string) string {
	return os.Getenv(key)
}
