# ADR-0006: Credentials

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** not started

## Context

Agents run `nova` commands and read their output. Today `nova config view` prints
`nucleus-token`, `nova login token` prints the access token, stored auth is attached to
requests regardless of host, and non-`wandelbots.io` hosts use plain `http://`.

## Decision

- **One source of truth:** `nova config`. The kit ships only the launch entry
  (`nova mcp`), no secrets; plugin config holds at most the host.
- **Redact secrets:** `nova config view` hides tokens unless `--show-secrets`;
  `nova status` never prints them. `nova login token` stays for humans; the skill never
  uses it.
- **Auth only to the configured host:** never attach stored credentials to other hosts,
  and never follow an absolute `servers[0].url` from a spec to a different host.
- **Plain HTTP:** warn when credentials go to a non-TLS host.
- **MCP results never contain credentials by construction:** responses are parsed into
  small types with only the needed fields, pinned by a test.

## Alternatives

- **Tokens in plugin `userConfig`:** stored in secure storage, but a second copy of the
  credentials outside `nova config`, passed through the plugin environment.

## Consequences

- **Missing binary:** the Claude adapter's SessionStart hook checks `command -v nova` and
  prints install instructions. The published docker image (`nova mcp`) is a fallback
  **for MCP only**; shell workflows need the installed CLI.
- `AGENTS.md` in this repo still recommends `nova config view`; that is safe once
  redaction lands.
