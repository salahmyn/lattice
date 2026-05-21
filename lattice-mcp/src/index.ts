#!/usr/bin/env node
// Lattice MCP server. A long-running process that exposes the lattice CLI to
// MCP hosts (Claude Desktop, IDE extensions, agent runtimes) over stdio.

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";

import { resolveConfig, runCli } from "./cli-runner.js";
import { renderDescription, tools } from "./tools/index.js";

async function main(): Promise<void> {
  const cfg = resolveConfig();
  const server = new McpServer({ name: "lattice", version: "1.0.0" });

  for (const def of tools) {
    server.tool(
      def.name,
      renderDescription(def),
      def.inputSchema,
      async (input: Record<string, unknown>) => {
        const { args, stdin } = def.toArgs(input);
        const result = await runCli(cfg, args, stdin);
        if (!result.ok) {
          return {
            isError: true,
            content: [{ type: "text", text: JSON.stringify(result.error, null, 2) }],
          };
        }
        return {
          content: [{ type: "text", text: result.raw || "{}" }],
        };
      },
    );
  }

  const transport = new StdioServerTransport();
  await server.connect(transport);
}

main().catch((err) => {
  console.error("lattice-mcp fatal:", err);
  process.exit(1);
});
