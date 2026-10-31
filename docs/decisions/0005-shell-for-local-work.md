# ADR-0005: CLI via shell for local work, MCP for the instance

- **Date:** 2026-10-31
- **Status:** accepted

## Context

Agents can use `nova` through their shell or through MCP tools. Each costs context and
has different strengths.

## Decision

- **CLI through the agent's shell:** app create/build/install (incl. `--copy-image`),
  `nucleus` file operations, `catalog`, `config`, `virtual`, and `nova api` for the
  instance API (ADR-0013). Local developer work, where models already handle shell
  commands well.
- **MCP for curated, task-level tools on the instance** (ADR-0008), for agents and clients
  that work better with typed tools or have no shell.
- Local commands (nucleus, catalog, app) are **not** wrapped as MCP tools unless a
  concrete need appears.

## Alternatives

- **Everything as MCP tools:** every tool costs context; local mechanics gain nothing.
- **Everything through the shell:** clients without a shell (claude.ai, ChatGPT) are left
  out, and app-specific tools on the instance can't be offered.

## Consequences

Matches the hybrid recommendation in Malik et al. (arXiv 2610.30157), a position paper
without its own experiments: we use its ideas, not its numbers. Token-cost comparisons in
blog posts mostly re-cite vendor numbers and ignore prompt caching.
