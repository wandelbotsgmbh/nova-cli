# ADR-0014: Track the MCP spec and Go SDK

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** not started

## Context

`nova mcp` uses `mark3labs/mcp-go` v1.4.0. MCP 2026-07-28 is stateless (no `initialize`;
`server/discover`), deprecates roots, sampling and logging, and replaces server-initiated
elicitation with multi round-trip requests [S5]. The official SDKs already speak it.

## Decision

- Evaluate moving to the official `modelcontextprotocol/go-sdk` (GitHub's server did)
  when `nova mcp` becomes the gateway (ADR-0015).
- Don't build on deprecated features (sampling, logging, old-style elicitation).
- `tools/list` in deterministic order, covered by a test (a SHOULD since 2026-07-28, for
  prompt caching) [S5].

## Alternatives

- **Stay on `mcp-go`:** fine while `nova mcp` is a small generator; its support for
  2026-07-28 is unverified.

## Consequences

- The tasks extension (`resultType: "task"`) for long-running jobs requires a server on
  2026-07-28.
