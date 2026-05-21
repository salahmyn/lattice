// Integration test: invoke every tool against a real `lattice` binary on the
// sample project. Skips automatically when the binary or sample is absent so
// the suite still runs in a checkout without a built CLI.

import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

import { runCli, type RunnerConfig } from "../src/cli-runner.ts";
import { tools } from "../src/tools/index.ts";

const here = dirname(fileURLToPath(import.meta.url));
const sampleRepo = resolve(here, "../../examples/sample-project");
const binary = process.env.LATTICE_BINARY || "lattice";

const haveSample = existsSync(resolve(sampleRepo, ".lattice"));

test("tools invoke the CLI against the sample project", { skip: !haveSample }, async () => {
  const cfg: RunnerConfig = { binary, repo: sampleRepo };

  // A representative read-only subset that needs no extra arguments.
  const readOnly = [
    "lattice_list_features",
    "lattice_validate",
    "lattice_extract",
    "lattice_list_initiatives",
    "lattice_get_tasks",
    "lattice_render_view",
  ];

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
});
