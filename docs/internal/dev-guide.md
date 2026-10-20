# Development Guide

## Local Checks

Use Go 1.26.10, matching `go.mod`, mise and release CI. Backstage uses a
local-first merge gate. The source of truth is:

```bash
mise run check
```

That task runs the same gate as `make check`: build, vet, tests, golangci-lint,
govulncheck, dead-code analysis, the tool-agnostic production-code scan and skill
installation checks.

For changes to rendering or recording evidence, enable the real media and
desktop checks after completing the implementation:

```bash
BACKSTAGE_RENDER_TEST=1 BACKSTAGE_DESKTOP_TEST=1 make check
go test -race ./internal/engine ./internal/pane ./internal/guest ./internal/recorder
```

Render checks need Chromium, FFmpeg and ffprobe. Desktop checks need a running
Hyprland session and their capture/input tools; they create temporary outputs
and a button-only test device, clean them up, and do not move the user's cursor.
The libvirt integration test is separate: set `BACKSTAGE_FRAMEBUFFER_DOMAIN`
only to a running test guest owned by the caller. It never boots or changes the
domain. Benchmark recipes and accepted/rejected optimizations are documented
in [Rendering and performance decisions](performance.md).

Enable the tracked pre-push hook once per clone:

```bash
git config core.hooksPath scripts/hooks
gh auth status
```

The hook runs `scripts/local-check.sh --pre-push`. On green, it waits until the
pushed SHA is visible on GitHub and posts a `local-check` commit status. The
`master` branch protection requires that status before a PR can merge.

To intentionally skip the hook for a push:

```bash
BACKSTAGE_SKIP_LOCAL_CHECK=1 git push
```

To rerun or refresh the status manually:

```bash
scripts/local-check.sh
scripts/local-check.sh --sha=<sha>
scripts/local-check.sh --post-only --state=success
scripts/local-check.sh --dry-run
```

## Releases

Releases are automated by release-please and GoReleaser.

When a PR is merged into `master`, `.github/workflows/release.yml` runs
release-please using `release-please-config.json` and
`.release-please-manifest.json`. Release-please opens or updates a release PR
from Conventional Commit messages:

- `feat:` creates a minor bump before `v1.0.0`.
- `fix:` creates a patch bump.
- `feat!:` or `BREAKING CHANGE:` creates a major bump.
- `docs:`, `chore:`, `refactor:`, `test:`, `build:`, `ci:`, and `perf:` do not
  force a version bump unless release-please includes them in changelog context.

The manifest starts at `0.0.0`, and `release-please-config.json` sets
`initial-version` to `0.1.0`, so the first releasable commit opens the initial
`v0.1.0` release PR. After that release PR merges, release-please updates the
manifest to the published version and owns future bumps.

Do not tag releases manually. Merge the release-please PR and the workflow will
create the GitHub release, tag it as `vX.Y.Z`, and attach GoReleaser binaries.
