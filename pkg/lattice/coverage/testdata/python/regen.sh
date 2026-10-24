#!/bin/sh
# Regenerates coverage.json with coverage.py and pytest (one run, per-test
# contexts from dynamic_context = test_function). Verified with coverage.py
# 7.10.6 and pytest 8.4.2:
#
#   python3 -m venv /tmp/venv && /tmp/venv/bin/pip install coverage==7.10.6 pytest==8.4.2
#   PY=/tmp/venv/bin/python ./regen.sh
set -eu
cd "$(dirname "$0")"
py=${PY:-python3}
"$py" -m coverage erase
"$py" -m coverage run -m pytest -q tests
"$py" -m coverage json --show-contexts -o coverage.json
rm -f .coverage
rm -rf .pytest_cache src/__pycache__ tests/__pycache__
