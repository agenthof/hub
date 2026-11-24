module github.com/agenthof/hub

go 1.27.1

// Build on go1.27.4 (patched net/http and crypto/x509 vulnerabilities); the
// `go` directive stays 1.27.1 so golangci-lint, which cannot yet read
// go1.27.3+ export data, can still lint.
toolchain go1.27.4
