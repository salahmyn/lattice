#!/usr/bin/env bash
# Helpers for .github/workflows/lattice-check.yml. Each step of the workflow is
# one subcommand, so the logic can be run and tested away from GitHub Actions.
#
#   ci-lattice-check.sh touches-go <base-ref>      print true|false: does the change touch Go?
#   ci-lattice-check.sh snapshot                   save the audit of HEAD as lattice/.cache/audit/<sha>.json
#   ci-lattice-check.sh fetch-base <base-ref>      restore the audit of merge-base(<base-ref>, HEAD), if any
#   ci-lattice-check.sh check <base-ref> <out.md>  run `lattice check --ci --markdown`; never fails on a verdict
#   ci-lattice-check.sh comment <pr> <file.md>     post or update the one sticky PR comment
#
# Environment:
#   LATTICE_BIN   the lattice binary (default dist/lattice)
#   GH_BIN        the GitHub CLI (default gh); GH_TOKEN and GITHUB_REPOSITORY are read by it
#   LATTICE_CI_WORKFLOW   workflow file that publishes the audit artifact (default lattice-check.yml)
#   GITHUB_STEP_SUMMARY   when set, the report is appended to it
#
# The verdict never blocks yet. `check` exits 0 for ASSURED and UNPROVEN and for
# BROKEN or a disallowed UNPROVEN (exit 1 from lattice); it exits non-zero only
# for a real setup error (lattice exit 2, a crash), so a broken setup still shows.
# Making the verdict block is the owner's decision: see lattice-check.yml.
set -euo pipefail

LATTICE_BIN="${LATTICE_BIN:-dist/lattice}"
GH_BIN="${GH_BIN:-gh}"
WORKFLOW="${LATTICE_CI_WORKFLOW:-lattice-check.yml}"
MARKER='<!-- lattice-check -->'

log() { printf '%s\n' "$*" >&2; }

summary() {
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    cat >>"$GITHUB_STEP_SUMMARY"
  else
    cat >/dev/null
  fi
}

merge_base() {
  git merge-base "$1" HEAD
}

cmd_touches_go() {
  local base="${1:?usage: touches-go <base-ref>}" mb
  mb="$(merge_base "$base")"
  if git diff --name-only "$mb" HEAD | grep -Eq '(\.go$|^go\.(mod|sum)$|^lattice/breaks/)'; then
    echo true
  else
    echo false
  fi
}

cmd_snapshot() {
  local head path
  head="$(git rev-parse HEAD)"
  path="lattice/.cache/audit/${head}.json"
  "$LATTICE_BIN" check --snapshot >&2
  if [ ! -s "$path" ]; then
    log "no audit snapshot at $path: the working tree must be clean"
    return 1
  fi
  echo "$path"
}

# fetch-base restores the audit of the merge base from the latest successful run
# of the workflow on that commit. A missing artifact is not an error: check then
# compares structural signals only and reads UNPROVEN, never BROKEN.
cmd_fetch_base() {
  local base="${1:?usage: fetch-base <base-ref>}" sha dest run
  sha="$(merge_base "$base")"
  dest="lattice/.cache/audit"
  mkdir -p "$dest"
  if [ -s "$dest/$sha.json" ]; then
    echo "base audit: already present for ${sha:0:10}"
    return 0
  fi
  run="$("$GH_BIN" run list --workflow "$WORKFLOW" --branch master --commit "$sha" \
    --status success --limit 1 --json databaseId --jq '.[0].databaseId // empty' 2>/dev/null || true)"
  if [ -z "$run" ]; then
    echo "base audit: no successful $WORKFLOW run found for ${sha:0:10}; structural comparison only"
    return 0
  fi
  if "$GH_BIN" run download "$run" --name "lattice-audit-$sha" --dir "$dest" >/dev/null 2>&1 && [ -s "$dest/$sha.json" ]; then
    echo "base audit: restored lattice-audit-${sha:0:10} from run $run"
  else
    rm -f "$dest/$sha.json"
    echo "base audit: run $run has no lattice-audit-${sha:0:10} artifact (expired?); structural comparison only"
  fi
}

cmd_check() {
  local base="${1:?usage: check <base-ref> <out.md>}" out="${2:?usage: check <base-ref> <out.md>}" rc=0
  "$LATTICE_BIN" check --base "$base" --ci --markdown >"$out" || rc=$?
  case "$rc" in
    0 | 1) ;;
    *)
      log "lattice check failed with exit $rc (a setup error, not a verdict):"
      cat "$out" >&2 || true
      return "$rc"
      ;;
  esac
  # The sticky comment is found by its marker: it must lead the file.
  if ! head -n1 "$out" | grep -qxF "$MARKER"; then
    { echo "$MARKER"; cat "$out"; } >"$out.tmp" && mv "$out.tmp" "$out"
  fi
  {
    cat "$out"
    echo
    if [ "$rc" -eq 1 ]; then
      echo "_Report only: this verdict does not block the merge yet._"
    fi
  } | summary
  echo "lattice check verdict exit code: $rc (report only, not blocking)"
}

cmd_comment() {
  local pr="${1:?usage: comment <pr> <file.md>}" file="${2:?usage: comment <pr> <file.md>}" repo id
  repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is not set}"
  if ! head -n1 "$file" | grep -qxF "$MARKER"; then
    log "$file does not start with $MARKER"
    return 1
  fi
  id="$("$GH_BIN" api --paginate "repos/$repo/issues/$pr/comments" \
    --jq ".[] | select(.user.login == \"github-actions[bot]\" and (.body | startswith(\"$MARKER\"))) | .id" | sed -n 1p)"
  if [ -n "$id" ]; then
    "$GH_BIN" api -X PATCH "repos/$repo/issues/comments/$id" -F "body=@$file" >/dev/null
    echo "updated comment $id on #$pr"
  else
    "$GH_BIN" api -X POST "repos/$repo/issues/$pr/comments" -F "body=@$file" >/dev/null
    echo "posted a new comment on #$pr"
  fi
}

sub="${1:-}"
[ $# -gt 0 ] && shift
case "$sub" in
  touches-go) cmd_touches_go "$@" ;;
  snapshot) cmd_snapshot "$@" ;;
  fetch-base) cmd_fetch_base "$@" ;;
  check) cmd_check "$@" ;;
  comment) cmd_comment "$@" ;;
  *)
    sed -n '2,14p' "$0" >&2
    exit 2
    ;;
esac
