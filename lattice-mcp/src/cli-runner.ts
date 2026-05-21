// cli-runner wraps subprocess invocations of the `lattice` Go binary. The MCP
// server depends only on the CLI's JSON output contract, never on Go internals.

import { execa } from "execa";

/** Configuration resolved from the environment. */
export interface RunnerConfig {
  /** Path to the lattice binary; defaults to "lattice" on PATH. */
  binary: string;
  /** Repository the CLI operates on. */
  repo: string;
}

/** resolveConfig reads the runner configuration from the environment. */
export function resolveConfig(): RunnerConfig {
  return {
    binary: process.env.LATTICE_BINARY || "lattice",
    repo: process.env.LATTICE_REPO || process.cwd(),
  };
}

/** The structured error a failed CLI call surfaces. */
export interface CliError {
  error: string;
  code?: string;
  next_action?: unknown;
}

/** Result of a CLI invocation. */
export interface CliResult {
  ok: boolean;
  /** Parsed JSON stdout when the call succeeded. */
  data?: unknown;
  /** Structured error when the call failed. */
  error?: CliError;
  /** Raw stdout, always populated. */
  raw: string;
}

/**
 * runCli invokes `lattice <args> --json`, optionally piping `stdin`, and
 * parses the JSON result. CLI errors are returned as structured CliError
 * objects rather than thrown, so tool handlers can surface next_action.
 */
export async function runCli(
  cfg: RunnerConfig,
  args: string[],
  stdin?: string,
): Promise<CliResult> {
  const fullArgs = ["--repo", cfg.repo, ...args, "--json"];
  try {
    const result = await execa(cfg.binary, fullArgs, {
      input: stdin,
      reject: false,
    });
    const raw = result.stdout ?? "";
    let data: unknown;
    try {
      data = raw ? JSON.parse(raw) : undefined;
    } catch {
      data = undefined;
    }
    if (result.exitCode === 0) {
      return { ok: true, data, raw };
    }
    const error: CliError =
      data && typeof data === "object"
        ? (data as CliError)
        : { error: result.stderr || raw || "lattice command failed" };
    return { ok: false, error, raw };
  } catch (e) {
    return {
      ok: false,
      error: { error: e instanceof Error ? e.message : String(e), code: "CLI_SPAWN_FAILED" },
      raw: "",
    };
  }
}
