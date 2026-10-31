# Agentic tooling: decision records

How `nova` supports AI coding agents (Claude Code, Cursor, Codex, Copilot, Gemini, …)
in NOVA development workflows: building and installing apps, spinning up Omniverse,
working with Nucleus, installing catalog entries such as cameras.

Each decision is one ADR (architecture decision record). New decisions copy
[`0000-template.md`](0000-template.md); changes to an accepted ADR go into an addendum or
a new ADR that supersedes it. References like [S7] point to the source list below.

## Overview

| Layer | Lives in | Responsibility |
|---|---|---|
| `nova` CLI | this repo | Capabilities: deterministic, testable commands for humans, CI and agents |
| `nova mcp` | this repo | Local entry point to the MCP servers on the instance (ADR-0008, ADR-0015) |
| `nova-agent-kit` | separate repo | All agent content (skill, prompts, instructions), MCP launch config, per-agent packaging |
| NOVA apps | instance | Curated MCP servers for their domain (commissioning, motion, cameras, …) |

Rule of thumb: **the CLI does things, the skill explains how to combine them, the kit
distributes it.**

## Decisions

| ADR | Title | Status |
|---|---|---|
| [0001](0001-cli-vs-skill.md) | What belongs in the CLI vs. the skill | accepted |
| [0002](0002-agent-content-in-the-kit.md) | All agent content lives in the kit, not in the CLI | accepted |
| [0003](0003-separate-kit-repo.md) | `nova-agent-kit` is a separate repo | accepted |
| [0004](0004-one-skill-generated-adapters.md) | One `nova` skill, generated adapters | accepted |
| [0005](0005-shell-for-local-work.md) | CLI via shell for local work, MCP for the instance | accepted |
| [0006](0006-credentials.md) | Credentials | accepted |
| [0007](0007-versioning.md) | Versioning on three axes | accepted |
| [0008](0008-curated-mcp.md) | Curated MCP tools, `nova api` for the long tail | accepted |
| [0009](0009-guard-rails-and-motion.md) | Guard rails; free motion on virtual robots, approved motion on real robots | accepted |
| [0010](0010-cli-contract.md) | CLI contract and result format | accepted |
| [0011](0011-nova-status.md) | `nova status` as the single context command | accepted |
| [0012](0012-nova-agent-command.md) | `nova agent install / update / check` | accepted |
| [0013](0013-nova-api.md) | `nova api`: authenticated access to the instance API | accepted |
| [0014](0014-mcp-spec-and-sdk.md) | Track the MCP spec and Go SDK | accepted |
| [0015](0015-mcp-gateway-to-the-instance.md) | `nova mcp` as gateway to MCP servers on the instance | proposed |

## Glossary

- **Skill:** markdown instructions an agent loads on demand (Agent Skills format [S12]).
- **Kit:** `nova-agent-kit`, the repo that holds the skill, MCP launch config and
  per-agent packaging.
- **Adapter:** the generated packaging of the kit for one agent (Claude plugin, Cursor
  plugin, `.agents/skills/`, …).
- **Curated tool:** an MCP tool designed for a task, with a small result and validated
  arguments, as opposed to a tool generated per OpenAPI operation.
- **Annotation:** a hint on an MCP tool (`readOnlyHint`, `destructiveHint`, …) that
  clients may use for permissions and parallelism. Hints, not enforcement.
- **Guard rail:** a check in the CLI that stops an agent from doing something by
  mistake. Not a security or safety function (see "Threat model").

## Current state (October 2026)

- `nova mcp` is EXPERIMENTAL and **does not start against v2-only instances**: the spec
  URL is hardcoded to `/api/v1/ui/public.openapi.yaml` (`pkg/nova/oapispec.go`), which
  returns 404 there. It mirrors every OpenAPI operation, without annotations, and
  streaming (WebSocket) endpoints hang the tool call. Two diagnostics go to stdout and
  corrupt the stdio transport. A separate PR fixes the immediate problems.
- No command offers `-o json` (the only `--json` is `cell save`, meaning file format).
- Secrets are visible to agents: `nova config view` prints `nucleus-token`,
  `nova login token` prints the access token.
- Stored auth is attached to requests regardless of host, and non-`wandelbots.io` hosts
  use plain `http://`.
- Other MCP work exists: an instance MCP service for commissioning (experimental,
  planned for a NOVA release; curated tools, no motion) and MCP servers inside apps.

## Threat model

Everything the CLI does to restrict agents (`--read-only`, confirmation flags) is a
**guard rail**:

- It protects against **mistakes and prompt injection** (an agent misreading a task,
  or following instructions hidden in a log line or catalog entry).
- It does **not** protect against a determined agent or user with a shell: anyone who
  can run `nova login token` or read the config file can call the API directly, and an
  agent can pass `--confirm` itself.
- It is **not functional safety.** Physical safety stays with the robot controller's
  safety system and the cell's safety configuration.

So the goal for software is narrower: an agent that makes a mistake or follows injected
instructions never moves a real robot unexpectedly. Virtual robots move freely; real
robots move only after a human approved a previewed plan in an app or UI the agent can't
operate. Deliberate misuse is covered by physical safety at the cell. See ADR-0009.

## Roadmap

1. **Fix `nova mcp`** (separate PR): spec discovery, diagnostics to stderr.
2. **`-o json` and `nova status`,** non-TTY data-only stdout, secrets redacted in
   `config view`, auth bound to the configured host.
3. **`nova api`** (GET plus an explicit write allow-list, `--dry-run`, `search`,
   `schema`) with the shared guard-rail package.
4. **Minimal `nova-agent-kit`:** the `nova` skill with 3–4 reference files (status and
   connect, app dev loop incl. the content removed from the NovaX template, catalog incl.
   cameras, Nucleus files), `.mcp.json`, Claude plugin plus `.agents/skills/`, CI against
   the latest stable `nova`.
5. **Align with the MCP owners:** the instance MCP service (curated tools), app teams
   (app MCP servers), platform team (`x-nova-agent` classification,
   controller type in the API, MCP discovery on the instance, possibly a shared approval
   UI for motions on real robots).

Deferred: `nova agent install/update/check`, further adapters, multi-plugin marketplace,
the gateway (ADR-0015).

## Open questions

- **Approval UI for real robots (ADR-0009):** does every app build its own preview and
  approval, or does NOVA offer a shared one?

- **Where do curated tools live?** The instance MCP service for general NOVA tools, or tools in the
  CLI for things only the CLI can do? (ADR-0008)
- **App MCP discovery:** how does an app advertise its MCP endpoint on the instance?
  (ADR-0015)
- **Omniverse spin-up:** what is the actual lifecycle today (catalog app, virtual
  instance, manual)? It should become a CLI command before skill content is written on
  top of it.
- **Kit visibility (ADR-0003):** which content is public, which internal?
- **Evals:** how do we measure whether a skill or tool change helps? A run log per task
  (every tool call and result, plus tokens and cost) is a good starting point.

## Sources

Checked October 2026. Vendor numbers are vendor-chosen examples. Sources marked † were
not independently re-verified in review.

- [S1] † Anthropic: Writing effective tools for agents. https://www.anthropic.com/engineering/writing-tools-for-agents
- [S2] † Anthropic: Code execution with MCP. https://www.anthropic.com/engineering/code-execution-with-mcp
- [S3] † Anthropic: Advanced tool use (tool search, input examples). https://www.anthropic.com/engineering/advanced-tool-use
- [S4] Claude Code MCP docs (tool search, limits, `_meta` keys, annotations). https://code.claude.com/docs/en/mcp
- [S5] MCP spec changelog 2026-07-28. https://modelcontextprotocol.io/specification/2026-07-28/changelog
- [S6] MCP schema 2025-11-25 (ToolAnnotations, `isError`). https://modelcontextprotocol.io/specification/2025-11-25/schema
- [S7] GitHub MCP server (toolsets, read-only, `context` toolset, tool snapshots, aliases). https://github.com/github/github-mcp-server
- [S8] GitHub MCP server: removal of dynamic toolsets (#2512, May 2026). https://github.com/github/github-mcp-server/pull/2512
- [S9] Cloudflare: Code Mode MCP server (search + execute over OpenAPI). https://blog.cloudflare.com/code-mode-mcp/
- [S10] Cloudflare `cf` CLI for agents. https://developers.cloudflare.com/cf/agents/index.md
- [S11] Cross-agent skill repos: https://github.com/cloudflare/skills, † https://github.com/hashicorp/agent-skills
- [S12] † Agent Skills specification. https://agentskills.io/specification.md
- [S13] † Skill locations: https://cursor.com/docs/context/skills, https://geminicli.com/docs/cli/skills/, https://code.visualstudio.com/docs/copilot/customization/agent-skills, https://learn.chatgpt.com/docs/build-skills
- [S14] Claude Code plugins reference and marketplaces. https://code.claude.com/docs/en/plugins-reference, https://code.claude.com/docs/en/plugin-marketplaces
- [S15] CLI and function design: https://clig.dev/, https://cli.github.com/manual/gh_help_formatting, https://agents.md/, https://developers.openai.com/api/docs/guides/function-calling
- [S16] † Authenticated API passthroughs: https://cli.github.com/manual/gh_api, https://gitlab.com/gitlab-org/cli/-/blob/main/docs/source/api/_index.md
- [S17] Impeccable (one skill with reference files, generated adapters, CLI contract, harness matrix). https://github.com/pbakaus/impeccable
- Malik et al.: MCP vs CLI for AI Agents (position paper). https://arxiv.org/html/2610.30157v1
