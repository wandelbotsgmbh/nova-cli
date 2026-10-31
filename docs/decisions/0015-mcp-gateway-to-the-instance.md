# ADR-0015: `nova mcp` as gateway to MCP servers on the instance

- **Date:** 2026-10-31
- **Status:** proposed (needs the platform team and app owners)

## Context

Curated MCP tools are served on the instance (ADR-0008): an MCP service for
commissioning (experimental, planned for a NOVA release), and MCP servers inside apps for
their own domain. So the instance doesn't have one MCP server but several. Agents on a developer
machine need auth, and on-prem IPCs are often reachable only on the local network.

## Decision

`nova mcp` becomes a local stdio gateway:

```
agent ──stdio──> nova mcp ──HTTP──> instance: MCP service       /…/mcp
                    │                         camera app        /…/mcp
                    │                         other apps        /…/mcp
                    ├─ discovers MCP endpoints of installed apps
                    ├─ adds auth from nova config (token refresh, TLS flags)
                    ├─ prefixes tool names per app, filters by --apps
                    └─ applies --read-only on annotations as a guard rail
```

- Installing an app from the catalog (e.g. a camera) makes its tools available to the
  agent automatically.
- Clients with remote MCP and OAuth support may connect to an app's endpoint directly.
- The CLI sends `User-Agent: nova-cli/<version> agent=<client>` (today:
  `wos-cli-mcp/1.0`), so instance logs can tell agent-driven calls apart.

| | Gateway to app servers | Generated locally (today) |
|---|---|---|
| Version match | Each app serves its own tools | Reads the instance's OpenAPI; drift possible |
| Motion on real robots | Apps own preview and approval (ADR-0009) | Refused |
| Clients without a shell | Can connect to app endpoints directly | Not possible |
| Rollout | Needs app discovery on the instance | Works now (once fixed) |

## Alternatives

- **One MCP server hosted by the platform:** a single owner for all tools; doesn't fit
  apps that already serve their own.
- **Agents configure each app's endpoint themselves:** no auth handling, one config entry
  per app and instance.

## Consequences

- Needs a way for apps to advertise their MCP endpoint (e.g. in the app manifest or a
  well-known path). A platform decision.
- MCP 2026-07-28 is stateless, which makes proxying simple [S5]; SDK choice per ADR-0014.
- Supersedes the earlier proposal of a single instance-hosted MCP endpoint with local
  OpenAPI fallback.
