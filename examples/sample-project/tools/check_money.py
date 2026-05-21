#!/usr/bin/env python3
"""Structural check: flag raw float money handling in checkout code.

Lattice spawns this as a subprocess, passing {scope, config, repo_path} as
JSON on stdin and expecting {violations: [...]} on stdout. This sample check
finds no violations in the clean sample.
"""

import json
import sys


def main() -> int:
    try:
        json.load(sys.stdin)
    except json.JSONDecodeError:
        print("could not parse stdin", file=sys.stderr)
        return 1

    # The sample checkout code uses integer minor units throughout, so there
    # are no raw-float-money violations to report.
    json.dump({"violations": []}, sys.stdout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
