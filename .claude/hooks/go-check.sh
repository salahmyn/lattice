#!/bin/sh
# PostToolUse hook: after an edit to a .go file, run `go build` + `go vet` and
# surface any failure back to Claude (exit 2 feeds stderr to the model). This
# catches import cycles and compile errors the moment they're introduced.
# Non-.go edits and missing-toolchain cases exit 0 (no-op).

file=$(jq -r '.tool_input.file_path // empty' 2>/dev/null)

case "$file" in
  *.go) ;;
  *) exit 0 ;;
esac

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
command -v go >/dev/null 2>&1 || exit 0

if ! out=$(go build ./... 2>&1); then
  printf 'go build failed after editing %s:\n%s\n' "$file" "$out" >&2
  exit 2
fi

if ! out=$(go vet ./... 2>&1); then
  printf 'go vet failed after editing %s:\n%s\n' "$file" "$out" >&2
  exit 2
fi

exit 0
