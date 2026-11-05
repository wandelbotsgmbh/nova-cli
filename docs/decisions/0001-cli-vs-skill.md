# ADR-0001: What belongs in the CLI vs. the skill

- **Date:** 2026-10-31
- **Status:** accepted

## Context

With good skill content, an agent can run many workflows itself. The question is what
still needs to be a CLI command and what should be written as instructions for the agent.

## Decision

> **If it's the same every time, write code for it. If it needs judgment, write a skill
> for it.** The skill calls the CLI; it doesn't replace it.

A capability belongs in the CLI if it is one of these:

- **Auth and connection:** portal login, token refresh, TLS settings, keeping credentials
  out of agent commands and transcripts.
- **Protocols and tricky mechanics:** Nucleus (custom websocket protocol), `--copy-image`
  (OCI layer diffing, containerd import over ssh, fallbacks), ssh/sudoers setup, IPC
  bootstrap.
- **Guard rails:** checked in code rather than requested in prose (ADR-0009).
- **Generic API access:** `nova api` (ADR-0013) and `nova mcp` (ADR-0008, ADR-0015).
- **Deterministic, frequently repeated procedures** that benefit from tests and CI.
- **Context and onboarding** every skill relies on: `nova status` (ADR-0011),
  `nova agent` (ADR-0012).

Everything else is skill content: multi-step workflows that need judgment (set up a
camera from the catalog and check the stream; create an app, deploy it, read logs, fix
issues), and workflows over single API calls through `nova api`.

## Alternatives

- **Skill only, no new CLI work:** the agent rebuilds protocols and auth from
  instructions on every run: slow, expensive, non-deterministic, credentials in the
  transcript, no tests.
- **CLI commands for every workflow:** workflows that need judgment don't fit into flags;
  the CLI grows without bound.

## Consequences

- **No new command whose only value is nicer output of one endpoint.** `nova api` plus
  the skill covers it.
- **Existing commands stay**, including 1:1 wrappers such as `controller list`,
  `cell list/add/delete`, `app list/delete`, `license get/activate`: humans and CI use
  them. They gain `-o json` (ADR-0010).
- The CLI stays useful without agents.
