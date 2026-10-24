#!/bin/sh
# Regenerates the coverage and result files under coverage/ with real tools
# (rnd/0009 prototype). Needs Node >= 22 (built-in test runner, V8 coverage
# and the lcov and junit reporters); nothing is installed.
#
#   coverage/junit.xml          results of the whole suite
#   coverage/all.lcov           line coverage of the whole suite
#   coverage/per-test/<slug>.lcov  line coverage of one test, run alone
#
# <slug> is the test title slugged the way Lattice slugs a test() title, so
# coverage/per-test/rejects-an-overdraft.lcov belongs to the test FQN
# tests.wallet.test.test.rejects-an-overdraft.
set -eu
cd "$(dirname "$0")"
rm -rf coverage
mkdir -p coverage/per-test

cov="--experimental-test-coverage --test-coverage-include=src/**"

# shellcheck disable=SC2086
node --test $cov \
  --test-reporter=lcov --test-reporter-destination=coverage/all.lcov \
  --test-reporter=junit --test-reporter-destination=coverage/junit.xml \
  tests/*.test.js

# One run per test: the cheapest way to get per-test coverage from a runner
# that only reports coverage per process.
for title in "rejects an overdraft" "rejects a non-positive deposit" \
  "formats a statement" "charges two percent in USD"; do
  slug=$(printf '%s' "$title" | tr 'A-Z' 'a-z' | sed -E 's/[^a-z0-9]+/-/g; s/^-+|-+$//g')
  # shellcheck disable=SC2086
  node --test $cov --test-name-pattern="^${title}\$" \
    --test-reporter=lcov --test-reporter-destination="coverage/per-test/${slug}.lcov" \
    tests/*.test.js
done

# Paths in the lcov files are absolute; make them relative to this directory
# so the files are reproducible and join the graph's repo-relative paths.
root=$(pwd)
for f in coverage/all.lcov coverage/per-test/*.lcov; do
  sed -i "s|^SF:${root}/|SF:|" "$f"
done
