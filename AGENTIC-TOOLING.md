# Agentic tooling: design decisions

How `nova` supports AI coding agents (Claude Code, Cursor, Codex, Copilot, Gemini, …)
in NOVA development workflows: building and installing apps, spinning up Omniverse,
working with Nucleus, installing catalog entries such as cameras.

Each decision lists its status: **Decided**, **Planned** (decided, not yet built) or **Open**.
Sources are listed at the end and referenced as [S1], [S2], ….

## Overview

| Layer | Lives in | Responsibility |
|---|---|---|
| `nova` CLI | this repo | Capabilities: deterministic, testable commands for humans, CI and agents |
| `nova mcp` | this repo | MCP server for the NOVA API (credentialed, robot-facing calls) |
| `nova-agent-kit` | separate repo | Workflow knowledge (skills), MCP launch config, per-agent packaging |

Rule of thumb: **the CLI does things, skills explain how to combine them, the kit distributes them.**

---

## D0: What belongs in the CLI vs. a skill

**Status:** Decided

> **If it's the same every time, write code for it. If it needs judgment, write a skill
> for it.** Skills call the CLI; they don't replace it.

A capability belongs in the CLI only if it is one of these:

- **Auth and connection:** portal login, token refresh, TLS settings, keeping credentials
  out of agent commands and transcripts.
- **Protocols and tricky mechanics:** Nucleus (custom websocket protocol), `--copy-image`
  (OCI layer diffing, containerd import over ssh, fallbacks), ssh/sudoers setup, IPC
  bootstrap. An agent rebuilding these from instructions is slow, expensive and
  non-deterministic.
- **Safety gates:** enforced in code (and ultimately on the instance, see D14), not
  requested in prose.
- **Generic API access:** `nova api` (D12) and the MCP proxy (D14).
- **Deterministic, frequently repeated procedures** that benefit from tests and CI.

Everything else is a skill:

- Multi-step workflows that need judgment, such as setting up a camera from the catalog
  and checking the stream, or creating an app, deploying it, reading logs and fixing
  issues.
- Workflows over single API calls: the skill uses `nova api`.

Consequences:

- **No new thin wrapper commands.** A command that only maps one API endpoint to a nicer
  output is not added; `nova api` plus a skill covers it.
- Existing thin wrappers may be deprecated over time in favour of `nova api` (to be
  reviewed per command).
- The CLI stays useful without agents: humans, CI and scripts rely on the same commands.

---

## D1: Skills are not embedded in the CLI

**Status:** Decided

Skills (markdown workflow knowledge) live in `nova-agent-kit`, not in the `nova` binary.

- Skills change far more often than the CLI and need no build or release of Go code.
- The CLI stays useful without any agent.
- Agents load skills on demand; the CLI only has to expose good `--help` and stable output.

**Consequence:** skills may refer to commands that changed. See D6.

## D2: The agent kit is a separate repo, not part of nova-cli

**Status:** Decided. Repo name: `nova-agent-kit`.

- Scope goes beyond the CLI (SDKs, app templates, Omniverse, robot programming).
  Other teams contribute without touching Go code.
- Skill edits must not trigger semantic-release CLI prereleases.
- Visibility can be chosen separately (this repo is public; some content is internal).
- The repo can act as a marketplace with several plugins (`nova`, `nova-omniverse`, …).
  Plugin names must not start with `claude-` (reserved) [S14].

## D3: One shared source, thin per-agent adapters

**Status:** Decided

Modelled on `cloudflare/skills` and `hashicorp/agent-skills` [S11]:

```
skills/<name>/SKILL.md            # Agent Skills format [S12], agent-neutral wording
.mcp.json                         # one shared MCP definition (`nova mcp`)
.claude-plugin/{plugin,marketplace}.json
.codex-plugin/plugin.json
.cursor-plugin/{plugin,marketplace}.json
.agents/plugins/marketplace.json
```

- Skills follow the spec: `name` matches the directory (lowercase, hyphens), a
  `description` that says what *and when*, SKILL.md under ~500 lines, references one
  level deep [S12].
- Skills say "run `nova catalog list -o json`", not "use the Bash tool".
- Repo-local installs target **`.agents/skills/`**, the de facto cross-agent location read
  by Codex, Cursor, Gemini CLI and VS Code/Copilot. Claude Code uses `.claude/skills` or
  the plugin [S13].
- Claude Code gets the full plugin (skills, hooks, slash commands). Host/token are plugin
  `userConfig` with `sensitive: true` (keychain) where needed; no `bin/` directory, since
  claude.ai/Cowork refuse plugins that ship one [S14].
- Kit CI runs `skills-ref validate` and `claude plugin validate --strict`.

## D4: CLI via shell for local work, MCP for the NOVA API

**Status:** Decided

- **CLI through the agent's shell:** app create/build/install (incl. `--copy-image`),
  `nucleus` file operations, `catalog`, `config`, `virtual`. Local developer work, where
  models already handle shell commands well and MCP adds nothing.
- **`nova mcp`:** the NOVA API. Credentialed, acts on hardware, benefits from typed
  schemas, annotations and server-side restrictions.
- We do **not** wrap nucleus/catalog/app commands as MCP tools unless a concrete need
  appears.

Matches the hybrid recommendation in Malik et al. (arXiv 2610.30157), a position paper
without its own experiments: we use its ideas, not its numbers. Token-cost comparisons
in blog posts mostly re-cite vendor numbers and ignore prompt caching.

## D5: The MCP server lives in the CLI; the kit only references it

**Status:** Decided

- The kit ships only the launch entry (`nova mcp`). No MCP code, no secrets.
- Host and tokens come from `nova config`, set once per user and shared by all agents.
  Credentials are never exposed to the model.
- Missing binary: the Claude adapter's SessionStart hook checks `command -v nova` and
  prints install instructions. Fallback for any agent: the published docker image
  running `mcp`.

## D6: The tool surface is versioned and checked

**Status:** Planned

- `nova mcp --list-tools -o json` prints tool names, schemas and annotations without
  starting a server.
- **Tool snapshots**, as in `github-mcp-server` [S7]: one JSON snapshot per tool checked
  into the repo; CI fails on unreviewed diffs. Tool names derive from NOVA API
  operationIds, so API renames would otherwise silently break users.
- A deprecation-alias map keeps renamed tools callable for a while.
- `tools/list` is returned in deterministic order (a SHOULD since MCP 2026-07-28, for
  prompt caching) [S5].
- `nova version -o json` reports the CLI version. Each kit release declares a minimum
  (`"requires": { "nova": ">=X.Y.Z" }`), checked by the SessionStart hook or
  `nova agent init`.
- Kit CI installs the latest **stable** `nova` (from `release`, never `rc`) and fails if a
  skill references a command, flag or tool that does not exist.
- Removing or renaming tools without an alias is a breaking change (`feat!:`).

## D7: Keep the MCP context small

**Status:** Planned. *Revised:* the earlier plan made a search/get/call mode the eventual
default. Research changed that.

Today `nova mcp` generates one tool per OpenAPI operation (`pkg/mcp/mcp.go`).

**Default: real per-operation tools, well described, in toolsets.**

- `--toolsets` grouped by OpenAPI tag, with a small named `default` set plus `all`, and
  `--tools` for single extras [S7].
- Claude Code already defers MCP tools and searches them itself (tool search is on by
  default) [S4]; the OpenAI and Anthropic APIs have native tool search too [S3][S15].
  A server-side meta-layer would duplicate that and lose per-tool permission rules and
  per-tool annotations.
- GitHub removed its equivalent (`--dynamic-toolsets`) in May 2026 for lack of use [S8].
- Clear server instructions (≤4,096 chars, keywords first): Claude sees only those and
  tool names until it searches [S4].
- A context tool (see D10) is marked `_meta["anthropic/alwaysLoad"]: true`.
- Good tool descriptions with input examples; paginate and truncate large results by
  default, with a hint on how to narrow the query; optional `fields` /
  concise-vs-detailed output [S1][S3][S7].

**Opt-in: `--mode=discover`** for clients without tool search or whole-API exploration,
modelled on Cloudflare's `search` + `execute` over the OpenAPI spec [S9]. The `call` path
must enforce `--read-only` and the motion gate (D8) server-side using the target
operation's annotations.

## D8: Safety: annotations, read-only, and a motion gate

**Status:** Planned

**Annotations are derived per tool** from the HTTP method, with an override table:

| Method | Annotations |
|---|---|
| GET / HEAD | `readOnlyHint: true` |
| PUT | `idempotentHint: true`, `destructiveHint: true` |
| DELETE | `destructiveHint: true` |
| POST | not read-only, destructive (default) |

The override table covers POST endpoints that are pure reads (kinematics/compute/search)
and marks everything that moves hardware or switches I/O.

- `--read-only` filters **server-side** on these annotations, because clients must treat
  annotations from servers as untrusted hints [S6]. It wins over `--tools`/`--toolsets`
  and also applies in discover mode [S7]. Enabled by default in the agent kit.
- In Claude Code, `readOnlyHint` lets read tools run in parallel and be retried safely [S4].
- **Robot motion and I/O go beyond "destructive":** `openWorldHint: true` and
  `_meta["anthropic/requiresUserInteraction"]: true` (forces the full permission prompt),
  excluded from the default toolset, and an explicit `--allow-motion` gate.
- Confirmation flows use explicit arguments and the client's permission prompt, not MCP
  elicitation (reworked in MCP 2026-07-28) [S5].

## D9: A machine-readable CLI contract

**Status:** Planned

Every command an agent may call should offer:

- `-o json` with a stable shape. When stdout is not a TTY, stdout carries only data;
  everything else goes to stderr [S10][S15].
- gh-style field selection: `--json` with no fields lists available fields,
  `--json f1,f2` selects them [S15].
- No prompts when flags are given (`--no-input`). Confirmation scales with severity:
  `--yes` for moderate actions, a typed `--confirm=<cell/controller>` for severe ones
  such as anything that moves a robot; `--dry-run` prints the request [S15].
- An aborted action **never exits 0**. A distinct non-zero exit code plus a typed error:
  `{"error": {"code": "confirmation_required", "retryable": false, "hint": "pass --yes"}}`
  (a mistake in Cloudflare's `cf` CLI we should not copy) [S10].
- Unknown commands suggest the closest matches.
- Idempotent where possible (installing something already installed is not an error).
- MCP tools report API failures as results with `isError: true` and actionable text, not
  as protocol errors, so the model can correct itself [S6]. `outputSchema` /
  `structuredContent` only for clients on the newer protocol [S7].

## D10: `nova status` as the single context command

**Status:** Planned

One command that reports config, reachable instance and version, Nucleus connectivity and
installed apps (`-o json`). The same information as an always-loaded MCP context tool
(like GitHub's `context` toolset) [S7]. Used by the kit's SessionStart hook and as the
first step of most skills.

## D11: `nova agent init` as the cross-agent installer

**Status:** Planned

```
nova agent init [--agent claude|cursor|codex|copilot|gemini]   # auto-detects by default
```

Writes the matching adapter of a released `nova-agent-kit` into the project (default
`.agents/skills/`) or home directory. One onboarding command for every agent;
`nova app create` may call it for new projects.

## D12: `nova api`: authenticated API access and discovery through the CLI

**Status:** Planned

An authenticated passthrough to the NOVA API in the style of `gh api` / `glab api` [S16],
plus discovery commands following Cloudflare's `cf` CLI [S10]. Every agent with a shell
gets the whole API, with progressive disclosure, without needing MCP:

```
nova api search "<task>"                         # local, no credentials, top-N operations as JSON
nova api schema <operation>                      # method, path, parameters, body fields
nova api /cells/{cell}/controllers               # GET; host + auth from nova config
nova api /cells/{cell}/apps -X POST -f name=foo  # -f/-F body fields like gh
nova api ... --paginate --jq '<expr>' --dry-run
```

- The command shape mirrors `gh api` on purpose: models already know it.
- New NOVA endpoints work immediately, with no wrapper code.
- Credentials stay in `nova config`; tokens never appear in the agent's commands.
- Paths must start with `/`, so they never collide with the `search` / `schema`
  subcommands.
- Placeholders such as `{cell}` are filled from config or flags, as `gh` fills
  `{owner}/{repo}`.
- **Same safety rules as `nova mcp` (D8), from one shared Go package:** read-only mode
  (config or `NOVA_READ_ONLY=1`) blocks non-read operations, motion/I/O endpoints need
  `--confirm=<cell>`, and blocked calls return a typed error with a non-zero exit code
  (D9). A raw passthrough must not become the way around the gates.
- A skill teaches the loop: `search` → `schema` → `--dry-run` → run.

With this in place, MCP `--mode=discover` (D7) becomes low priority.

## D13: Track the MCP spec and Go SDK

**Status:** Planned

- MCP 2026-07-28 is stateless (no `initialize`; `server/discover`), deprecates roots,
  sampling and logging, and replaces server-initiated elicitation with multi
  round-trip requests [S5]. Avoid building on deprecated features.
- Evaluate moving to the official `modelcontextprotocol/go-sdk` (GitHub's server did).
- Long-running robot jobs fit the tasks extension (`resultType: "task"`), which Claude
  Code supports [S4].

## D14: MCP hosted on the instance, CLI as proxy with local fallback

**Status:** Open. Proposed direction; needs the NOVA platform team.

```
agent ──stdio──> nova mcp ──HTTP──> instance MCP endpoint   (NOVA API tools, safety enforced here)
                    │
                    ├─ adds auth from nova config (token refresh, TLS flags)
                    ├─ optional local toolsets (nucleus, app build) merged in
                    └─ fallback: no MCP endpoint on the instance → OpenAPI generation (today's behaviour)
```

| | Hosted on the instance | Started locally (today) |
|---|---|---|
| Version match | Always matches that instance's API | Reads the instance's OpenAPI; drift possible |
| Safety | Enforced next to the robot; cannot be bypassed by the client | Guard against agent mistakes only |
| Clients without a shell (claude.ai, Cowork, ChatGPT) | Can connect directly as a remote MCP server | Not possible |
| Auth | Needs MCP OAuth for direct connections | Uses `nova config` tokens |
| Local capabilities (docker, ssh, Nucleus files) | Impossible | Natural fit |
| Rollout | Needs a platform release | Works with every instance now |

- The kit's launch entry stays `nova mcp`; users don't notice where tools come from.
- MCP 2026-07-28 is stateless, which makes proxying simple [S5].
- Clients with remote MCP and OAuth support may connect to the instance directly.
- On-prem IPCs are often reachable only on the local network: proxying through the CLI
  works there; direct connections from cloud clients don't.

**Effect on other decisions once adopted:**

- **D8:** real enforcement moves to the instance, tied to token scopes (e.g. read-only
  tokens, tokens without motion rights). CLI-side `--read-only` and the motion gate
  remain as convenience and fallback, and still apply to `nova api` (D12).
- **D6:** snapshots of the API tool surface live with the instance's API. The CLI
  versions only its local toolsets and proxy behaviour.

## Open questions

- **Omniverse spin-up:** what is the actual lifecycle today (catalog app, virtual
  instance, manual)? It should become a CLI command (e.g. `nova omniverse up/down`)
  before a skill is written on top of it.
- **Plugin split in the kit:** one `nova` plugin, or separate `nova-omniverse` /
  `nova-sdk-*` plugins from the start?
- **Kit visibility:** public, internal, or one of each?
- **Default toolset:** which NOVA API tags belong in `default`?
- **Motion gate:** `--allow-motion` flag only, or also tied to a per-cell setting on the
  instance?
- **Instance-hosted MCP (D14):** does the platform team want to own an MCP endpoint, OAuth
  and token scopes (read-only, no-motion)? Which NOVA version could ship it?
- **Thin wrapper review (D0):** which existing commands are 1:1 API wrappers that
  `nova api` could replace?
- **Evals:** how do we measure whether a skill or tool change helps (an eval harness, as
  recommended in [S1])?

## Sources

Checked October 2026. Vendor numbers are vendor-chosen examples.

- [S1] Anthropic: Writing effective tools for agents. https://www.anthropic.com/engineering/writing-tools-for-agents
- [S2] Anthropic: Code execution with MCP. https://www.anthropic.com/engineering/code-execution-with-mcp
- [S3] Anthropic: Advanced tool use (tool search, input examples). https://www.anthropic.com/engineering/advanced-tool-use
- [S4] Claude Code MCP docs (tool search, limits, `_meta` keys, annotations). https://code.claude.com/docs/en/mcp
- [S5] MCP spec changelog 2026-07-28. https://modelcontextprotocol.io/specification/2026-07-28/changelog
- [S6] MCP schema 2025-11-25 (ToolAnnotations, `isError`). https://modelcontextprotocol.io/specification/2025-11-25/schema
- [S7] GitHub MCP server (toolsets, read-only, tool snapshots, aliases). https://github.com/github/github-mcp-server
- [S8] GitHub MCP server: removal of dynamic toolsets (#2512, May 2026). https://github.com/github/github-mcp-server
- [S9] Cloudflare: Code Mode MCP server (search + execute over OpenAPI). https://blog.cloudflare.com/code-mode-mcp/
- [S10] Cloudflare `cf` CLI for agents. https://developers.cloudflare.com/cf/agents/index.md
- [S11] Cross-agent skill repos: https://github.com/cloudflare/skills, https://github.com/hashicorp/agent-skills
- [S12] Agent Skills specification. https://agentskills.io/specification.md
- [S13] Skill locations: https://cursor.com/docs/context/skills, https://geminicli.com/docs/cli/skills/, https://code.visualstudio.com/docs/copilot/customization/agent-skills, https://learn.chatgpt.com/docs/build-skills
- [S14] Claude Code plugins reference and marketplaces. https://code.claude.com/docs/en/plugins-reference, https://code.claude.com/docs/en/plugin-marketplaces
- [S15] CLI and function design: https://clig.dev/, https://cli.github.com/manual/gh_help_formatting, https://agents.md/, https://developers.openai.com/api/docs/guides/function-calling
- [S16] Authenticated API passthroughs: https://cli.github.com/manual/gh_api, https://gitlab.com/gitlab-org/cli/-/blob/main/docs/source/api/_index.md
- Malik et al.: MCP vs CLI for AI Agents (position paper). https://arxiv.org/html/2610.30157v1
