# MCP setup

The Lattice MCP server exposes the `lattice` CLI to MCP hosts — Claude Desktop,
IDE extensions, agent runtimes — over stdio. It is a separate npm package,
`@salahmyn/mcp-server`, that wraps the Go binary as a subprocess.

## Prerequisites

- The `lattice` binary on `PATH` (or set `LATTICE_BINARY`).
- Node.js 18 or newer.

## Claude Desktop

Add this to your Claude Desktop MCP configuration:

```json
{
  "mcpServers": {
    "lattice": {
      "command": "npx",
      "args": ["-y", "@salahmyn/mcp-server"],
      "env": { "LATTICE_REPO": "/path/to/your/repo" }
    }
  }
}
```

Restart Claude Desktop. The 21 Lattice tools (`lattice_list_features`,
`lattice_validate`, `lattice_get_agent_context`, …) become available.

## Environment

| Variable | Purpose | Default |
|---|---|---|
| `LATTICE_REPO` | Repository the tools operate on | current directory |
| `LATTICE_BINARY` | Path to the `lattice` binary | `lattice` on PATH |

## Running standalone

```sh
npx -y @salahmyn/mcp-server
```

The server speaks the MCP stdio protocol; pair it with any MCP-capable host.

## Why a separate process

The MCP SDK is most mature in TypeScript, while the engine's workload (parsing,
graph queries, SCIP orchestration) is stronger in Go. The seam between them is
the **stable JSON CLI contract** — every tool wraps a `lattice … --json` call
— so either side can be rewritten independently.
