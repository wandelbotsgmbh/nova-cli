# ADR-0007: Versioning on three axes

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** not started

## Context

Three things change independently: the CLI, the kit and the NOVA instance. The v1 API is
already gone on current instances, which broke `nova mcp`.

## Decision

| Axis | Mechanism |
|---|---|
| CLI ↔ kit | The kit declares `requires.nova`; `nova agent check` and the SessionStart hook verify it. Kit CI installs the latest **stable** `nova` (never `rc`) and fails if the skill references a command or flag that doesn't exist. |
| Kit ↔ instance | Skill content declares `requires.nova-api` where it depends on endpoints from a given NOVA version. Kit CI runs against a pinned instance version. |
| CLI ↔ instance | `nova status -o json` reports the instance API version. The CLI discovers the spec (v2, then v1) instead of hardcoding a path. |

Snapshots only of what this repo owns: the `nova api` classification tables (ADR-0009),
and the JSON shapes of `nova status`, `nova api` and other `-o json` output (ADR-0010).

## Alternatives

- **Snapshot the NOVA API's tool surface in this repo** (as `github-mcp-server` does for
  its own tools [S7]): it would capture one arbitrary instance's API and churn with every
  NOVA release; that surface is owned by the instance.

## Consequences

- MCP tools are versioned by whoever serves them (ADR-0008, ADR-0015).
- A `nova` release that changes `-o json` shapes needs a matching kit check.
