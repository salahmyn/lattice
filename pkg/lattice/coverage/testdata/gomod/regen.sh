#!/bin/sh
# Regenerates the Go coverage profiles with the go tool: the whole suite, and
# one run per test from a binary built once (-c -cover), named by the slug
# `lattice results tests` prints.
set -eu
cd "$(dirname "$0")"
rm -rf coverage
mkdir -p coverage/per-test
go test -count=1 -covermode=set -coverprofile=coverage/all.cover ./...
go test -c -cover -covermode=set -o coverage/wallet.test ./wallet
for t in TestRejectsAnOverdraft TestRejectsANonPositiveDeposit TestDeposits; do
  slug=$(printf '%s' "$t" | tr 'A-Z' 'a-z')
  (cd wallet && ../coverage/wallet.test -test.run "^${t}\$" -test.coverprofile="../coverage/per-test/${slug}.cover" >/dev/null)
done
rm -f coverage/wallet.test
