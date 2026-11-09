# ADR-0019: `nova mcp` serves the instance MCP toolsets locally until the service ships

- **Date:** 2026-11-09
- **Status:** proposed
- **Implementation:** done

## Context

ADR-0008 replaces the OpenAPI generator in `nova mcp` with curated tools served on the
instance; ADR-0015 makes `nova mcp` a gateway to them. The instance MCP service exists
as a draft (service-manager MR !2924): 8 tools in 4 toolsets over the public NOVA v2 API,
plus a roadmap of 16 toolsets (about 100 tools) with an approval gate for physical motion.
Deploying it on every instance needs the platform team and a NOVA release. Until then
`nova mcp` kept the generator, which doesn't start against v2-only instances, mirrors
~180 operations without annotations and hangs on streaming endpoints.

## Decision

`nova mcp` implements the service's toolsets itself, over stdio, against the NOVA v2 API
of the targeted instance.

- **Same surface:** the 8 tools of the MR are ported unchanged (names, schemas,
  annotations, result lines, instructions). The other toolsets of the roadmap use its
  names and rules, so they can move into the service as they are.
- **Same hardening:** response structs declare only the fields a tool hands on, so
  connection details, credentials, environment values and tokens can't reach the model.
- **Default and opt-in toolsets** as in the roadmap: `planning`, `stores`, `motion`,
  `programs` and `maintenance` need `--toolsets`; `--read-only` drops every write.
- **Physical targets:** motion, I/O writes and mode changes run only on virtual
  controllers (allow-list on the controller kind, looked up on the instance). Physical
  controllers get `APPROVAL_REQUIRED`: this is the roadmap's layer 1 default
  (`allowPhysicalMotion: false`). The CLI doesn't implement the approval flow for physical
  motion; that stays with the instance service (ADR-0009 addendum).
- **Human approval** for deleting a cell, restoring a backup and updating NOVA: MCP
  elicitation (`input_required` on 2026-07-28, `elicitation/create` before); clients
  without elicitation get `APPROVAL_REQUIRED`.
- **Local differences:**
  - Auth comes from the CLI login: the portal token is refreshed per call when it has
    expired, never interactively (a device login would block the tool call).
    A missing or expired login is `UNAUTHENTICATED` with a `nova login` hint.
  - Ingress error pages served as HTTP 200 are mapped to their real status in the HTTP
    transport before any decoding.
  - Transport errors reach the client as `UNAVAILABLE` with their text (it only carries
    what the user configured); other unexpected answers go to stderr.
  - Downloads (diagnosis package, backup) are saved to `--download-dir` and returned as a
    path instead of a download link.
  - The API is called with plain HTTP and local structs: the published Go clients are
    older than the instances (no datasets, session, network or model endpoints).
  - `--toolsets` and `--read-only` are flags instead of query parameters; an unknown
    toolset is an error, not ignored.
- `mark3labs/mcp-go` and `libopenapi` are dropped for `modelcontextprotocol/go-sdk`
  (ADR-0014), the SDK the service uses.

## Alternatives

- **Keep the generator until the service ships:** no work now, but the known problems
  (v1 spec, no annotations, hanging streams) stay, and agents learn a tool surface that
  goes away.
- **Only the 8 tools of the MR:** less to keep in sync, but far less useful until the
  service grows.
- **Physical motion with the approval dialog in the CLI:** possible with elicitation, but
  the gate belongs with the instance (operator switch, binding to one trajectory, velocity
  override) and would have to be built twice.

## Consequences

- Tools must be kept in sync with the service by hand until the gateway lands; the tool
  snapshot of both is the signal. Toolsets can be upstreamed one at a time.
- Once the service is deployed, `nova mcp` proxies to it (ADR-0015); instances without
  the service keep the local tools as fallback.
