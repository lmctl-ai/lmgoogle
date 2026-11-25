#!/usr/bin/env bash
# Select by emitted bytecode capability, not CLI version or schema support alone.
set -euo pipefail
WORK="$(mktemp -d "${TMPDIR:-/tmp}/lmgoogle-compiler-XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
WORK_FOR_COMPILER="$WORK"
if command -v cygpath >/dev/null 2>&1; then
  WORK_FOR_COMPILER="$(cygpath -m "$WORK")"
fi
candidate="${LMSCRIPT_BIN:-$(command -v lmscript || true)}"
printf '%s\n' '{"dsl.schema":2,"name":"compiler_probe","types":{},"functions":{},"context":{}}' > "$WORK/probe.schema.json"
signatures=""
if [ -n "$candidate" ]; then
  signatures="$("$candidate" --lmcode-signatures "$WORK_FOR_COMPILER/probe.schema.json" 2>/dev/null || true)"
fi
cat > "$WORK/check.lms" <<LMS
s = load_schema(schema="$WORK_FOR_COMPILER/probe.schema.json");
p = _lmcode_ s;
  let x = 1;
end _lmcode_
r = run_lmcode(p, {command:["probe","--lmcode","\$DSLFILE"]});
LMS

if [ -z "$candidate" ] ||
   ! grep -q '^fn template_render(string, Json) -> string$' <<<"$signatures" ||
   ! "$candidate" --lmcode-schema "$WORK_FOR_COMPILER/probe.schema.json" --json >/dev/null 2>&1
then
  echo "error: no implicit-std lmcode v6 compiler found; install @lmctl-ai/lmctl or set LMSCRIPT_BIN" >&2
  exit 2
fi

if ! "$candidate" --check "$WORK_FOR_COMPILER/check.lms" >/dev/null 2>&1; then
  echo "error: compiler lacks the current load_schema/_lmcode_/run_lmcode API" >&2
  exit 2
fi
printf '%s\n' "$candidate"
