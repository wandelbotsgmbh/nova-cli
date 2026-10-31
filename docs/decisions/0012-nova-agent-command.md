# ADR-0012: `nova agent install / update / check`

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** deferred until the kit exists

## Context

Agents other than Claude Code have no plugin marketplace, or a different one. Developers
need one onboarding step regardless of the agent, also on on-prem networks without
internet access.

## Decision

```
nova agent install [--agent claude|cursor|codex|…] [--from <path|tarball>]
nova agent update
nova agent check     # kit version vs. CLI version vs. instance version
```

- Writes the matching adapter of a released kit into the project (default
  `.agents/skills/`) or home directory; auto-detects the agent (as Impeccable's
  `install/update/check` [S17]).
- `--from` supports offline and air-gapped networks and internal kit content.
- `nova app create` may suggest it after creating a project; templates contain no agent
  content themselves (ADR-0002).

## Alternatives

- **Plugins only:** covers Claude Code and Cursor, not the rest.
- **Embedding the kit in the binary:** ties kit releases to CLI releases (ADR-0002).

## Consequences

- The CLI downloads release artifacts of the kit; its location and integrity check need
  to be defined.
