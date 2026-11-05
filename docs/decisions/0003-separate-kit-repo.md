# ADR-0003: `nova-agent-kit` is a separate repo

- **Date:** 2026-10-31
- **Status:** accepted

## Context

The agent content (ADR-0002) needs a home. Options are this repo or a separate one.

## Decision

The kit lives in its own repo, **`nova-agent-kit`**.

- Its scope goes beyond the CLI (SDKs, app templates, Omniverse, robot programming).
  Other teams contribute without touching Go code.
- Skill edits must not trigger semantic-release CLI prereleases.
- The name is agent-neutral and matches `nova agent install`.

## Alternatives

- **Same repo as the CLI:** versions always match (Impeccable does this [S17]), but its
  CLI exists only to serve the skill. `nova` is a general product with its own users and
  releases.
- **Names:** `nova-agent-skills` (undersells adapters and MCP config), `nova-ai-devkit`
  (crowded term), `nova-agents` (sounds like agent implementations).

## Consequences

- Version drift between CLI and kit needs checks (ADR-0007).
- **Visibility is decided per content, not per repo.** Some content depends on internal
  infrastructure (e.g. `nova virtual` uses internal clusters, the dev Nucleus server) and
  must not ship in a public kit: either a separate internal plugin, or an internal repo.
- Plugin names must not start with reserved prefixes (`claude-`, `anthropic-`,
  `anthropics-`, `cc-plugin-`) [S14].
