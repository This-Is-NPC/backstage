#!/usr/bin/env bash
# Go 1.25's go/types panics when govulncheck analyzes chromedp's JSON dependency.
# Build the scanner with the fixed runtime while leaving the project's Go version
# and the Go command used to load project packages unchanged.
# https://github.com/golang/go/issues/73871
set -euo pipefail
scan_dir=$(mktemp -d)
trap 'rm -rf "$scan_dir"' EXIT
GOTOOLCHAIN=go1.26.8 GOBIN="$scan_dir" go install golang.org/x/vuln/cmd/govulncheck@v1.7.0
"$scan_dir/govulncheck" ./...
