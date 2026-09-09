#!/usr/bin/env bash
# The agent skill's contract: the pages exist, SKILL.md declares itself and
# points at every one of them, and the installer refuses the two cases where
# doing the obvious thing would destroy somebody's work.
#
# The refusals are the half worth testing. An installer that links is easy; an
# installer that overwrites a skill somebody wrote by hand, or that deletes a
# directory because the name matched, is a tool that costs more than it gives.
set -euo pipefail
cd "$(dirname "$0")/.."

skill="agents/skills/backstage"
pages=(SKILL.md scenes.md vm-stage.md producing.md presentations.md)

for page in "${pages[@]}"; do
  [ -f "$skill/$page" ] || { echo "FAIL: $skill/$page is missing" >&2; exit 1; }
done

head -1 "$skill/SKILL.md" | grep -qx -- '---' \
  || { echo "FAIL: SKILL.md has no frontmatter" >&2; exit 1; }
grep -qx 'name: backstage' "$skill/SKILL.md" \
  || { echo "FAIL: SKILL.md does not name itself" >&2; exit 1; }
grep -q 'Excludes development of the' "$skill/SKILL.md" \
  || { echo "FAIL: SKILL.md does not say what it is not for" >&2; exit 1; }

# Every guide is reachable from the entry point, and every link resolves. A
# page nobody links to is a page nobody reads.
for page in "${pages[@]:1}"; do
  grep -q "($page)" "$skill/SKILL.md" \
    || { echo "FAIL: SKILL.md never links to $page" >&2; exit 1; }
done
linked="$(grep -oE '\(([a-z-]+\.md)\)' "$skill/SKILL.md" | tr -d '()' | sort -u)"
for page in $linked; do
  [ -f "$skill/$page" ] || { echo "FAIL: SKILL.md links to a missing $page" >&2; exit 1; }
done

# And the installer's two refusals, against a home of its own.
home="$(mktemp -d)"
trap 'rm -rf "$home"' EXIT

mkdir -p "$home/.agents/skills/backstage"
echo "somebody else's work" > "$home/.agents/skills/backstage/SKILL.md"
if HOME="$home" mise run install:skill >/dev/null 2>&1; then
  echo "FAIL: install replaced a skill it did not put there" >&2; exit 1
fi
if HOME="$home" mise run uninstall:skill >/dev/null 2>&1; then
  echo "FAIL: uninstall removed a skill it did not put there" >&2; exit 1
fi
[ -f "$home/.agents/skills/backstage/SKILL.md" ] \
  || { echo "FAIL: uninstall destroyed somebody else's skill" >&2; exit 1; }

# Then the ordinary path, twice, because installing is something people do
# again without thinking about it.
rm -rf "$home/.agents"
HOME="$home" mise run install:skill >/dev/null
[ -L "$home/.agents/skills/backstage" ] \
  || { echo "FAIL: install wrote a copy rather than a link" >&2; exit 1; }
HOME="$home" mise run install:skill >/dev/null \
  || { echo "FAIL: installing twice is an error" >&2; exit 1; }
HOME="$home" mise run uninstall:skill >/dev/null
[ -e "$home/.agents/skills/backstage" ] \
  && { echo "FAIL: uninstall left the link behind" >&2; exit 1; }

echo "OK: the agent skill and its installer hold"
