#!/usr/bin/env bash
# Tests scripts/ci-lattice-check.sh against this repository with a fake `gh`.
# Run from the repository root with a built binary:
#
#   go build -o dist/lattice ./cmd/lattice && scripts/ci-lattice-check.test.sh [base-rev]
#
# The base defaults to HEAD~1, so HEAD must have a parent. The fake gh serves
# no artifact, then one, so both paths of `fetch-base` and `check` run.
set -euo pipefail

BASE="${1:-HEAD~1}"
SCRIPT="$PWD/scripts/ci-lattice-check.sh"
export LATTICE_BIN="${LATTICE_BIN:-$PWD/dist/lattice}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fail=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }

mb="$(git merge-base "$BASE" HEAD)"
audit_dir="lattice/.cache/audit"

# A fake gh. FAKE_ARTIFACT=none serves no run; =present serves run 42 whose
# artifact is the audit snapshot saved in $tmp/snap.json. `gh api` calls are
# logged to $tmp/api.log; comments already on the PR are $tmp/comments.json.
mkdir -p "$tmp/bin"
cat >"$tmp/bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
  "run list")
    [ "${FAKE_ARTIFACT:-none}" = present ] && echo 42
    exit 0 ;;
  "run download")
    dir=""; while [ $# -gt 0 ]; do [ "$1" = --dir ] && dir="$2"; shift; done
    [ "${FAKE_ARTIFACT:-none}" = present ] || exit 1
    cp "$FAKE_SNAP" "$dir/$FAKE_SHA.json"
    exit 0 ;;
  "api "*)
    echo "$*" >>"$FAKE_LOG"
    # a listing call asks for --jq; answer with the id of an existing comment, if any
    case "$*" in
      *--paginate*) [ -f "$FAKE_COMMENT" ] && cat "$FAKE_COMMENT" || true ;;
    esac
    exit 0 ;;
esac
echo "fake gh: unexpected $*" >&2
exit 9
GH
chmod +x "$tmp/bin/gh"
export GH_BIN="$tmp/bin/gh" FAKE_LOG="$tmp/api.log" FAKE_COMMENT="$tmp/comment-id" FAKE_SHA="$mb"
export GITHUB_REPOSITORY=example/repo GITHUB_STEP_SUMMARY="$tmp/summary.md"
: >"$FAKE_LOG"

# touches-go: the change from BASE touches Go here; an empty range does not.
[ "$("$SCRIPT" touches-go "$BASE")" = true ] && ok "touches-go sees a Go change" || bad "touches-go sees a Go change"
[ "$("$SCRIPT" touches-go HEAD)" = false ] && ok "touches-go on an empty range is false" || bad "touches-go on an empty range is false"

# snapshot: the audit of HEAD is saved and its path printed.
head_sha="$(git rev-parse HEAD)"
out="$("$SCRIPT" snapshot)" && [ "$out" = "$audit_dir/$head_sha.json" ] && ok "snapshot saves the audit of HEAD" || bad "snapshot saves the audit of HEAD"
cp "$audit_dir/$head_sha.json" "$tmp/snap.json"
rm -f "$audit_dir/$mb.json"

# fetch-base with no artifact: says so, succeeds, leaves nothing behind.
msg="$(FAKE_ARTIFACT=none "$SCRIPT" fetch-base "$BASE")"
case "$msg" in *"structural comparison only"*) [ ! -e "$audit_dir/$mb.json" ] && ok "fetch-base without an artifact degrades" || bad "fetch-base left a file" ;; *) bad "fetch-base without an artifact: $msg" ;; esac

# check with no base audit: report written, marker first, never a hard failure.
FAKE_ARTIFACT=none "$SCRIPT" check "$BASE" "$tmp/none.md" >/dev/null && ok "check without a base audit exits 0" || bad "check without a base audit exits 0"
[ "$(head -n1 "$tmp/none.md")" = '<!-- lattice-check -->' ] && ok "report starts with the marker" || bad "report starts with the marker"
grep -q 'no base audit\|unknown' "$tmp/none.md" && ok "report names the missing base audit" || bad "report names the missing base audit"
grep -q 'lattice-check' "$GITHUB_STEP_SUMMARY" && ok "report goes to the job summary" || bad "report goes to the job summary"

# fetch-base with an artifact: restored, then check compares with it.
msg="$(FAKE_ARTIFACT=present "$SCRIPT" fetch-base "$BASE")"
case "$msg" in *"restored"*) [ -s "$audit_dir/$mb.json" ] && ok "fetch-base restores the artifact" || bad "fetch-base did not write the file" ;; *) bad "fetch-base with an artifact: $msg" ;; esac
"$SCRIPT" check "$BASE" "$tmp/with.md" >/dev/null && ok "check with a base audit exits 0" || bad "check with a base audit exits 0"
if grep -q 'no base audit' "$tmp/with.md"; then bad "check still reports no base audit"; else ok "check used the base audit"; fi
rm -f "$audit_dir/$mb.json"

# a verdict exit of 1 does not fail; a usage error (exit 2) does.
"$SCRIPT" check "not-a-rev" "$tmp/bad.md" >/dev/null 2>&1 && bad "a usage error must fail" || ok "a usage error (bad base) still fails"

# comment: creates when no marked comment exists, updates when one does.
: >"$FAKE_LOG"
rm -f "$FAKE_COMMENT"
"$SCRIPT" comment 7 "$tmp/with.md" >/dev/null
grep -q -- '-X POST repos/example/repo/issues/7/comments' "$FAKE_LOG" && ok "comment creates the first time" || bad "comment creates the first time"
echo 555 >"$FAKE_COMMENT"
: >"$FAKE_LOG"
"$SCRIPT" comment 7 "$tmp/with.md" >/dev/null
grep -q -- '-X PATCH repos/example/repo/issues/comments/555' "$FAKE_LOG" && ok "comment updates the sticky one" || bad "comment updates the sticky one"
printf 'no marker\n' >"$tmp/plain.md"
"$SCRIPT" comment 7 "$tmp/plain.md" >/dev/null 2>&1 && bad "comment must refuse a file without the marker" || ok "comment refuses a file without the marker"

exit "$fail"
