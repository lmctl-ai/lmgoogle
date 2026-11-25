#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
current="$(head -n1 VERSION | tr -d '[:space:]')"
[ -n "$current" ] || { echo "cannot find VERSION" >&2; exit 1; }
if [ "${1:-}" = "--base" ]; then
  echo "${current%.*}"
  exit 0
fi
v="${1:-}"
[ -n "$v" ] || exit 0
printf '%s\n' "$v" > VERSION
printf '%s\n' "$v" > cmd/lmgoogle/VERSION
echo "stamped version $v (was $current)"
