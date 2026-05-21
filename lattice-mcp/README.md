# @salahmyn/mcp-server

The Model Context Protocol server for [Lattice](../README.md). It is a thin
TypeScript process that wraps the `lattice` Go CLI and exposes its operations
as MCP tools to hosts like Claude Desktop, IDE extensions, and agent runtimes.

## Why a separate process

- The MCP SDK is most polished in TypeScript.
- The Go binary stays small and self-contained — users who do not need MCP
  install only `lattice`.
- The seam between the two is the **stable JSON CLI contract**, not Go
  internals. Either side can be rewritten independently.

## Install

```sh
npx -y @salahmyn/mcp-server
```

The `lattice` binary must be on `PATH` (or set `LATTICE_BINARY`).

## Claude Desktop configuration

```json
{
  "mcpServers": {
    "lattice": {
      "command": "npx",
      "args": ["-y", "@salahmyn/mcp-server"],
      "env": { "LATTICE_REPO": "/path/to/repo" }
    }
  }
}
```

## Environment

| Variable | Purpose | Default |
|---|---|---|
| `LATTICE_REPO` | Repository the CLI operates on | current directory |
| `LATTICE_BINARY` | Path to the `lattice` binary | `lattice` on PATH |

## Tools

21 tools, each wrapping one `lattice` subcommand — see `src/tools/index.ts`.
Every tool description follows a strict template (purpose, when-to-call,
returns, common errors); `npm test` fails the build if any deviates.

## Development

```sh
npm install
npm run build      # compile to dist/
npm test           # description-template + integration tests
```

Requires Node 18+.
