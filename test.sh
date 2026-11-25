#!/usr/bin/env bash
# Verifies build, linkage, version, notices, schema parity, memory budget, safety rules, Go unit tests, and all .lms examples.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$HERE"

unset LMGOOGLE_LIVE

BUILD_DIR="${BUILD_DIR:-bin}"
mkdir -p "$BUILD_DIR"
ext=""
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) ext=".exe" ;; esac
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$BUILD_DIR/lmgoogle$ext" ./cmd/lmgoogle
HOST="$BUILD_DIR/lmgoogle$ext"

pass=0
ok() { pass=$((pass+1)); echo "ok $pass - $1"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

[ -x "$HOST" ] || fail "$HOST not executable"
ok "binary exists and is executable ($HOST)"

python3 tests/check-host-flags.py schema/lmgoogle.dsl.json "$HOST" || fail "hard flag cutover"
ok "hard flag cutover passed"

# 1. Platform linkage check
uname_s="$(uname -s)"
if [ "$uname_s" = "Linux" ]; then
  if command -v ldd >/dev/null 2>&1; then
    ldd_out="$(ldd "$HOST" 2>&1 || true)"
    case "$ldd_out" in
      *"not a dynamic executable"*|*"statically linked"*) ;;
      *)
        bad="$(echo "$ldd_out" | awk '/=>/ {print $1}' | grep -vE '^(libc\.so\.6|libm\.so\.6|libpthread\.so\.0|libdl\.so\.2|librt\.so\.1|ld-linux.*|linux-vdso\.so\..*)$' || true)"
        [ -z "$bad" ] || fail "disallowed dynamic libs on Linux: $bad"
        ;;
    esac
  fi
  ok "Linux static/allowlist linkage check passed"
fi

# 2. Version, help, notices
want_v="$(head -n1 VERSION | tr -d '[:space:]')"
want="lmgoogle $want_v"
got="$("$HOST" --version | tr -d '\r')"
[ "$got" = "$want" ] || fail "--version was '$got', want '$want'"
ok "--version prints '$want'"

help_out="$("$HOST" --help | tr -d '\r')"
grep -Fq "LMGOOGLE_MAX_MEMORY_MB" <<<"$help_out" || fail "--help missing LMGOOGLE_MAX_MEMORY_MB"
grep -Fq "auth login" <<<"$help_out" || fail "--help missing auth login"
ok "--help documents memory budget and OAuth2 auth login"

notices_out="$("$HOST" --notices | tr -d '\r')"
grep -Fq "Apache License 2.0" <<<"$notices_out" || fail "--notices missing Apache License 2.0"
grep -Fq "lmcode-go" <<<"$notices_out" || fail "--notices missing lmcode-go"
ok "--notices prints embedded third-party notices"

# 3. Schema & signature parity
sdk_lock_prefix="$(awk '{print substr($2,1,12)}' sdk.lock | tr -d '\r')"
go_mod_prefix="$(awk '/github\.com\/lmctl-ai\/lmcode-go/ {sub(/.*-/, "", $2); print $2}' go.mod | tr -d '\r')"
[ -n "$sdk_lock_prefix" ] && [ "$sdk_lock_prefix" = "$go_mod_prefix" ] || fail "sdk.lock ($sdk_lock_prefix) does not match go.mod lmcode-go ($go_mod_prefix)"
LMSCRIPT="$(scripts/select-compiler.sh)"
diff -u <("$LMSCRIPT" --lmcode-signatures schema/lmgoogle.dsl.json | tr -d '\r') <("$HOST" --lmcode-signatures | tr -d '\r') >/dev/null || fail "schema/host signature drift"
diff -u <("$LMSCRIPT" --lmcode-signatures schema/lmgoogle.dsl.json --without-std | tr -d '\r') <("$HOST" --lmcode-signatures --without-std | tr -d '\r') >/dev/null || fail "host-only signature drift"
cmp <("$HOST" --lmcode-schema --json) schema/lmgoogle.dsl.json >/dev/null || fail "embedded schema differs from schema/lmgoogle.dsl.json"
ok "schema/host signature parity and embedded schema byte-identity"

# 4. Invalid memory budget env returns exit 2
for bad_mb in "0" "-10" "notanumber" ""; do
  set +e
  err_out="$(LMGOOGLE_MAX_MEMORY_MB="$bad_mb" "$HOST" --version 2>&1)"
  code=$?
  set -e
  [ "$code" -eq 2 ] || fail "LMGOOGLE_MAX_MEMORY_MB='$bad_mb' exited $code, want 2"
  grep -Fq "LMGOOGLE_MAX_MEMORY_MB" <<<"$err_out" || fail "stderr did not name LMGOOGLE_MAX_MEMORY_MB"
done
ok "invalid LMGOOGLE_MAX_MEMORY_MB values exit 2"

# 4b. Auth setup/status/logout CLI smoke test (isolated config dir)
auth_dir="$(mktemp -d)"
auth_status_out="$(XDG_CONFIG_HOME="$auth_dir" "$HOST" auth status)"
XDG_CONFIG_HOME="$auth_dir" "$HOST" auth logout >/dev/null || fail "auth logout failed"
"$HOST" auth setup --link-out "$auth_dir/setup.html" >/dev/null || fail "auth setup --link-out failed"
grep -Fq "analyticsadmin.googleapis.com" "$auth_dir/setup.html" || fail "setup HTML missing Analytics link"
grep -Fq "youtube.googleapis.com" "$auth_dir/setup.html" || fail "setup HTML missing YouTube link"
rm -rf "$auth_dir"
grep -Fq "oauth2_token: absent" <<<"$auth_status_out" || fail "auth status did not report absent oauth2_token"
grep -Fq "live_auth_resolution:" <<<"$auth_status_out" || fail "auth status did not report live_auth_resolution"
ok "auth setup/status/logout CLI subcommands succeeded"

# 5. Memory budget enforcement via lmscript
export PATH="$PWD/$BUILD_DIR:$PATH"
mkdir -p build
mem_dir="$(mktemp -d build/mem.XXXXXX)"
trap 'rm -rf "$mem_dir"' EXIT
cat > "$mem_dir/oom.lms" <<LMS
s = load_schema(cli="lmgoogle");
p = _lmcode_ s;
  let ok = google.alloc_scratch_mb(32);
end _lmcode_
set_env("LMGOOGLE_MAX_MEMORY_MB", "8");
r = run_lmcode(p);
assert(!r.ok && r.code == "RUN_HOST" && contains(r.message, "LMGOOGLE_MAX_MEMORY_MB"), "memory budget failure envelope");
LMS
"$LMSCRIPT" "$mem_dir/oom.lms" >/dev/null || fail "memory budget test failed"
ok "memory budget enforcement returns RUN_HOST envelope"

# 6. Go vet & uncached Go tests across all packages
go vet ./... || fail "go vet failed"
go test -count=1 -v ./... || fail "Go tests failed"
ok "go vet and all Go tests passed"

# 7. Run all .lms examples (check, dry-run, and real against in-process fake)
for ex in examples/*.lms; do
  "$LMSCRIPT" --check "$ex" || fail "--check failed on $ex"
  "$LMSCRIPT" --dry-run "$ex" >/dev/null || fail "--dry-run failed on $ex"
  "$LMSCRIPT" "$ex" || fail "execution failed on $ex"
  ok "example passed (--check, --dry-run, run): $ex"
done

# 8. External lmmock integration suite (when lmmock binary is available)
lmmock_bin="${LMMOCK_BIN:-}"
if [ -z "$lmmock_bin" ] && [ -x "../lmmock/build/lmmock" ]; then
  lmmock_bin="../lmmock/build/lmmock"
elif [ -z "$lmmock_bin" ] && command -v lmmock >/dev/null 2>&1; then
  lmmock_bin="$(command -v lmmock)"
fi
if [ -n "$lmmock_bin" ]; then
  mock_dir="$(mktemp -d build/lmmock.XXXXXX)"
  "$lmmock_bin" serve --builtin google-apis --listen 127.0.0.1:0 --ready-file "$mock_dir/ready.json" >/dev/null 2>&1 &
  mock_pid=$!
  trap 'kill "$mock_pid" 2>/dev/null || true; rm -rf "$mem_dir" "$mock_dir"' EXIT
  for _ in $(seq 1 100); do
    [ -f "$mock_dir/ready.json" ] && break
    sleep 0.05
  done
  [ -f "$mock_dir/ready.json" ] || fail "lmmock did not become ready"
  mock_url="$(python3 -c "import json; print(json.load(open('$mock_dir/ready.json'))['url'])")"
  for ex in examples/*.lms; do
    LMGOOGLE_ENDPOINT="$mock_url" LMGOOGLE_ACCESS_TOKEN="fixture-token" "$LMSCRIPT" "$ex" >/dev/null || fail "lmmock integration failed on $ex"
  done
  kill "$mock_pid" 2>/dev/null || true
  wait "$mock_pid" 2>/dev/null || true
  rm -rf "$mock_dir"
  ok "all examples passed against external lmmock (--builtin google-apis)"
fi

echo "ALL $pass CHECKS PASSED"
