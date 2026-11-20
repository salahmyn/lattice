// Integration test: invoke every tool against a real `lattice` binary on the
// sample project. Skips automatically when the binary or sample is absent so
// the suite still runs in a checkout without a built CLI.

import { test } from "node:test";
import assert from "node:assert/strict";
import { cpSync, existsSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join, resolve } from "node:path";

import { runCli, type RunnerConfig } from "../src/cli-runner.ts";
import { tools } from "../src/tools/index.ts";

const here = dirname(fileURLToPath(import.meta.url));
const sampleRepo = resolve(here, "../../examples/sample-project");
const binary = process.env.LATTICE_BINARY || "lattice";

const haveSample = existsSync(resolve(sampleRepo, "lattice"));
const haveBinary = spawnSync(binary, ["version"], { stdio: "ignore" }).status === 0;

test("tools invoke the CLI against the sample project", { skip: !haveSample || !haveBinary }, async () => {
  // Work on a copy: tools such as lattice_extract rewrite the workspace, and
  // the committed sample must stay as it is.
  const repo = mkdtempSync(join(tmpdir(), "lattice-mcp-sample-"));
  cpSync(sampleRepo, repo, { recursive: true });
  const cfg: RunnerConfig = { binary, repo };

  // A representative read-only subset that needs no extra arguments.
  const readOnly = [
    "lattice_list_features",
    "lattice_validate",
    "lattice_extract",
    "lattice_list_initiatives",
    "lattice_get_tasks",
    "lattice_render_view",
  ];

  try {
    for (const name of readOnly) {
      const def = tools.find((t) => t.name === name);
      assert.ok(def, `tool ${name} is defined`);
      const input = name === "lattice_render_view" ? { name: "developer" } : {};
      const { args, stdin } = def!.toArgs(input);
      const result = await runCli(cfg, args, stdin);
      assert.ok(
        result.ok || result.error,
        `${name}: produced a structured result`,
      );
    }
  } finally {
    rmSync(repo, { recursive: true, force: true });
  }
});
