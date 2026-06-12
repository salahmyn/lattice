---
name: preflight
description: Verify the local toolchain (Node, Xcode/CocoaPods, Go) before any build, lint, or deploy chain. Use before Expo/iOS builds, lint runs, or long Go build sessions to catch environment drift early.
---

# preflight

Run this before kicking off any build/lint/deploy flow. Local toolchain drift
(Node 17, wrong `xcode-select` path, missing CocoaPods) has repeatedly blocked
work mid-task — a 30-second check up front beats debugging the environment
later.

## Run the checks

```sh
echo "node:  $(node -v 2>&1)        (need >= 18)"
echo "xcode: $(xcode-select -p 2>&1) (must be inside Xcode.app, not CommandLineTools)"
echo "pod:   $(pod --version 2>&1)"
echo "go:    $(go version 2>&1)      (need >= 1.23)"
```

## Evaluate and STOP on mismatch

Report each result, then **stop before proceeding** if any of these fail:

- **Node < 18** (e.g. `v17.x`): breaks Expo CLI, lint validation, and npx-based
  MCP servers. Fix: `nvm install --lts && nvm use --lts` (or upgrade Node).
  This single fix also unblocks the GitHub MCP server.
- **`xcode-select -p` points at `CommandLineTools`** (only relevant for
  iOS/Expo work): repoint with
  `sudo xcode-select -s /Applications/Xcode.app/Contents/Developer`.
- **`pod --version` errors** (iOS work): install CocoaPods
  (`sudo gem install cocoapods` or via Homebrew).
- **Go < 1.23**: upgrade the Go toolchain.

If an iOS build later fails with a derived-data module-map error, clear
DerivedData and re-run `pod install` before chasing code.

Only continue to the build/lint/deploy task once the relevant checks pass.
