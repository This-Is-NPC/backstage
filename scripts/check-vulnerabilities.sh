#!/usr/bin/env bash
# Run the pinned scanner with the project's supported Go toolchain.
set -euo pipefail
scan_dir=$(mktemp -d)
trap 'rm -rf "$scan_dir"' EXIT
GOBIN="$scan_dir" go install golang.org/x/vuln/cmd/govulncheck@v1.7.0
"$scan_dir/govulncheck" ./...
