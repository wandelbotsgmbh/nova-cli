# ADR-0011: `nova status` as the single context command

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** not started

## Context

Every workflow starts with the same questions: which instance, is it reachable, which
version, which apps are installed, is Nucleus configured. Today that takes several commands,
and `nova config view` prints secrets.

## Decision

`nova status` reports config (without secrets), reachable instance, CLI and instance API
version, Nucleus connectivity and installed apps; `-o json`. It replaces a separate
`version -o json`. It is the first step of the skill and of the SessionStart hook.

## Alternatives

- **`nova doctor`:** suggests repair; status first, a doctor mode can follow.
- **Several commands:** more turns, more tokens, more chances to print secrets.

## Consequences

- Its JSON shape is part of the CLI contract (ADR-0010) and versioned (ADR-0007).
- MCP servers offer the same information as an always-loaded context tool (ADR-0008).
