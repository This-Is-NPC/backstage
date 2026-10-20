#!/usr/bin/env bash
# Quality gate for the backstage core: build, vet, test, lint, vuln scan, dead code, and
# the tool-agnostic check — no tool-specific names (okt/omakiten) in non-test production code.
# The core must record ANY tool; tool specifics belong in projects/.
set -euo pipefail
cd "$(dirname "$0")/.."

echo ">> go build"; go build ./...
echo ">> go vet";   go vet ./...
echo ">> go test";  go test ./...
echo ">> golangci-lint"; golangci-lint run
echo ">> govulncheck";   ./scripts/check-vulnerabilities.sh

echo ">> deadcode"
# Pin the analysis tool for reproducible reachability checks.
dead="$(go run golang.org/x/tools/cmd/deadcode@v0.50.0 ./...)"
if [ -n "$dead" ]; then
  echo "FAIL: functions unreachable from main (move test-only helpers into _test.go):" >&2
  echo "$dead" >&2
  exit 1
fi
echo "OK: no dead code"

echo ">> agnostic gate"
hits="$(grep -rniE 'okt|omakiten' internal cmd --include='*.go' | grep -v '_test\.go' || true)"
if [ -n "$hits" ]; then
  echo "FAIL: tool-specific names in core production code:" >&2
  echo "$hits" >&2
  exit 1
fi
echo "OK: core is tool-agnostic"

echo ">> agent skill"
./scripts/check-skill.sh
