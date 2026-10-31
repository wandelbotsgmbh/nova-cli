# ADR-0008: Curated MCP tools, `nova api` for the long tail

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** not started (the generator is fixed in the meantime, see "Current state")

## Context

`nova mcp` generates one MCP tool per OpenAPI operation of the instance (181 on a current
instance), with full JSON results and no annotations; streaming endpoints can't work
through it. Internal MCP work at Wandelbots independently chose curated tools instead:

- **An instance MCP service for commissioning** (experimental, planned for a NOVA
  release): a handful of tools for cells, controllers and virtual controllers; motion
  deliberately excluded.
- **MCP servers inside robot apps:** a few planner-backed tools (look, locate, move,
  grip); compact text results with failure codes; no raw joint, jogging or script tools.

External precedents agree: GitHub's MCP server is hand-designed tools in toolsets [S7];
Anthropic recommends a few workflow-level tools over one per endpoint [S1]. Clients
(Claude Code, the OpenAI and Anthropic APIs) defer and search tools themselves [S3][S4],
so a generated mirror mostly adds noise.

## Decision

- **MCP tools are curated, never generated from the OpenAPI spec.** Each tool is designed
  for a task, validates its arguments on the server (clients don't enforce `inputSchema`),
  returns a small result (ADR-0010) and carries annotations.
- **Curated tools are served on the instance:** the instance MCP service for
  commissioning, apps for their own domain (e.g. camera or robot apps). `nova mcp` is the
  local entry point to them (ADR-0015).
- **The long tail of the API goes through `nova api`** (ADR-0013), with discovery through
  `nova api search/schema`.
- **Tool design rules** (from the internal MCP servers, GitHub and Anthropic [S1][S7]):
  - Small default toolsets; more on request.
  - Numbered rules in the server `instructions` (≤4,096 chars, keywords first [S4]):
    discover before asking, confirm destructive actions, verify a pending result before
    building on it.
  - Only deliberately user-facing errors reach the client; everything else goes to the
    log with a pointer.
  - Paginate and truncate large results with a hint on how to narrow the query.
  - A context tool marked `_meta["anthropic/alwaysLoad"]: true`.

## Alternatives

- **Generated tools with toolsets, annotations and a classification table** (the previous
  plan): keeps the whole API reachable, but large results, no argument validation, no
  task-level design, and a classification that must be maintained per operation.
- **Server-side search/get/call meta-tools:** duplicates the clients' own tool search
  and loses per-tool permissions and annotations; GitHub removed its equivalent [S8].
  Cloudflare's search + execute over OpenAPI [S9] needs a code sandbox.
- **Curated tools inside the CLI:** possible for things only the CLI can do, but
  instance tools belong with the instance's version and auth (ADR-0015).

## Consequences

- The OpenAPI generator in `pkg/mcp` is replaced, not extended. It stays only as long as
  nothing else exists; the fix PR keeps it working until then.
- Coordination with the owners of the instance MCP service and the app teams is needed
  (README roadmap).
- Open: whether any curated tools should live in the CLI (e.g. for local workflows), or
  whether shell commands cover them (ADR-0005).
