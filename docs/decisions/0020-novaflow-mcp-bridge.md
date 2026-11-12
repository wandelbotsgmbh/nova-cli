# ADR-0020: `nova flow mcp` bridges NovaFlow's MCP endpoint to stdio

- **Date:** 2026-11-12
- **Status:** proposed
- **Implementation:** done

## Context

NovaFlow's control plane serves an MCP endpoint for flow authoring
(`POST /<cell>/controlplane/api/v1/mcp`, stateless Streamable HTTP, tools such as
`list_node_types`, `validate_tree`, `save_tree`; `docs/mcp-server.md` in
`wandelbotsgmbh/novaflow`). NovaFlow documents `claude mcp add --transport http`
with the URL. On cloud instances the gateway wants the portal bearer token, which
lasts 24 h: a header configured once stops working the next day, and the client has
to know the instance url and cell. `nova mcp` (ADR-0019) serves the instance
toolsets, not NovaFlow's.

## Decision

`nova flow mcp` serves NovaFlow's MCP endpoint of the targeted instance and cell over
stdio: it forwards each JSON-RPC message unchanged to the endpoint, authenticated per
request like `nova mcp` (refresh without interactive login), and writes the answers
(JSON or SSE) to stdout, one message per line. `--print` shows the endpoint and the
`claude mcp add` command.

- The bridge is generic (`pkg/mcpbridge`) and knows no tools: NovaFlow owns the tool
  surface, the CLI only adds target resolution and credentials.
- Every request gets an answer: transport, login and HTTP failures become JSON-RPC
  errors with a hint (`nova login`, `nova flow status`).
- The target is resolved once at start (`--instance` pins it); no instance switching.

## Alternatives

- **Only print the URL and a token header:** simplest, but breaks after a day.
- **Proxy NovaFlow's tools as a toolset of `nova mcp`:** one server for agents, but
  couples `nova mcp`'s pinned tool surface (ADR-0019) to NovaFlow releases and would
  duplicate tool definitions. Can come later on top of the bridge.

## Consequences

- Agents register NovaFlow next to `nova mcp` with one command that keeps working
  across token expiry and instance changes.
- The CLI has no view of the tools: safety rules for `save_tree` etc. stay in NovaFlow.
- When the instance MCP service ships (ADR-0015), NovaFlow's endpoint may move behind
  it; the bridge then points there.
