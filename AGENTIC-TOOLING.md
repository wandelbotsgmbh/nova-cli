# Agentic tooling: design decisions

How `nova` supports AI coding agents (Claude Code, Cursor, Codex, Copilot, Gemini, …)
in NOVA development workflows: building and installing apps, spinning up Omniverse,
working with Nucleus, installing catalog entries such as cameras.

Each decision lists its status: **Decided**, **Planned** (decided, not yet built),
**Deferred** or **Open**. Sources are listed at the end and referenced as [S1], [S2], ….

## Glossary

- **Skill:** markdown instructions an agent loads on demand (Agent Skills format [S12]).
- **Kit:** `nova-agent-kit`, the repo that holds the skill, MCP launch config and
  per-agent packaging.
- **Adapter:** the generated packaging of the kit for one agent (Claude plugin, Cursor
  plugin, `.agents/skills/`, …).
- **Toolset:** a named group of MCP tools that can be switched on or off together.
- **Annotation:** a hint on an MCP tool (`readOnlyHint`, `destructiveHint`, …) that
  clients may use for permissions and parallelism. Hints, not enforcement.
- **Guard rail:** a check in the CLI that stops an agent from doing something by
  mistake. Not a security or safety function (see "Threat model").

## Current state (October 2026)

- `nova mcp` is EXPERIMENTAL and **does not start against v2-only instances**: the spec
  URL is hardcoded to `/api/v1/ui/public.openapi.yaml` (`pkg/nova/oapispec.go`), which
  returns 404 there; `/api/v2/ui/public.openapi.yaml` works.
- `pkg/mcp/mcp.go` writes two diagnostics to stdout, which corrupts the stdio MCP
  transport.
- MCP tools have no annotations; every operation, including streaming ones, becomes a
  tool. Streaming (WebSocket) endpoints hang the tool call because the response body is
  read to the end.
- No command offers `-o json` (the only `--json` is `cell save`, meaning file format).
- Secrets are visible to agents: `nova config view` prints `nucleus-token`,
  `nova login token` prints the access token.
- Stored auth is attached to requests regardless of host, and non-`wandelbots.io` hosts
  use plain `http://`.

The roadmap at the end starts with these.

## Overview

| Layer | Lives in | Responsibility |
|---|---|---|
| `nova` CLI | this repo | Capabilities: deterministic, testable commands for humans, CI and agents |
| `nova mcp` | this repo | MCP server (later: proxy) for the NOVA API |
| `nova-agent-kit` | separate repo | Workflow knowledge (skill), MCP launch config, per-agent packaging |

Rule of thumb: **the CLI does things, skills explain how to combine them, the kit distributes them.**

## Threat model

Everything the CLI does to restrict agents (`--read-only`, the motion gate, confirmation
flags) is a **guard rail**:

- It protects against **mistakes and prompt injection** (an agent misreading a task,
  or following instructions hidden in a log line or catalog entry).
- It does **not** protect against a determined agent or user with a shell: anyone who
  can run `nova login token` or read the config file can call the API directly.
- It is **not functional safety.** Physical safety stays with the robot controller's
  safety system and the cell's safety configuration.

Real enforcement of what an agent may do can only happen on the instance, through
scoped tokens (e.g. read-only, no-motion). See D14.

---

## D0: What belongs in the CLI vs. a skill

**Status:** Decided

> **If it's the same every time, write code for it. If it needs judgment, write a skill
> for it.** Skills call the CLI; they don't replace it.

A capability belongs in the CLI if it is one of these:

- **Auth and connection:** portal login, token refresh, TLS settings, keeping credentials
  out of agent commands and transcripts.
- **Protocols and tricky mechanics:** Nucleus (custom websocket protocol), `--copy-image`
  (OCI layer diffing, containerd import over ssh, fallbacks), ssh/sudoers setup, IPC
  bootstrap. An agent rebuilding these from instructions is slow, expensive and
  non-deterministic.
- **Guard rails:** checked in code rather than requested in prose (see "Threat model").
- **Generic API access:** `nova api` (D12) and `nova mcp` (D14).
- **Deterministic, frequently repeated procedures** that benefit from tests and CI.
- **Context and onboarding** that every skill relies on: `nova status` (D10),
  `nova agent` (D11).

Everything else is skill content:

- Multi-step workflows that need judgment, such as setting up a camera from the catalog
  and checking the stream, or creating an app, deploying it, reading logs and fixing
  issues.
- Workflows over single API calls: the skill uses `nova api`.

Consequences:

- **No new command whose only value is nicer output of one endpoint.** `nova api` plus
  the skill covers it.
- **Existing commands stay**, including 1:1 wrappers such as `controller list`,
  `cell list/add/delete`, `app list/delete`, `license get/activate`: humans and CI use
  them. They gain `-o json` (D9).

---

## D1: Skills are not embedded in the CLI

**Status:** Decided

Skill content lives in `nova-agent-kit`, not in the `nova` binary.

- Skill content changes far more often than the CLI and needs no Go build or release.
- The CLI stays useful without any agent.
- Agents load skill content on demand; the CLI only has to expose good `--help` and
  stable output.

**Consequence:** the skill may refer to commands that changed. See D6.

## D2: The agent kit is a separate repo, not part of nova-cli

**Status:** Decided. Repo name: `nova-agent-kit`.

- Scope goes beyond the CLI (SDKs, app templates, Omniverse, robot programming).
  Other teams contribute without touching Go code.
- Skill edits must not trigger semantic-release CLI prereleases.
- `nova` is a general product with its own users and releases. (Impeccable keeps CLI and
  skill in one repo [S17], but its CLI exists only to serve the skill.)
- **Visibility is decided per content, not per repo.** Some content depends on internal
  infrastructure (e.g. `nova virtual` uses internal clusters, the dev Nucleus server) and
  must not ship in a public kit. Either keep internal reference files in a separate
  internal plugin, or keep the repo internal.
- Plugin names must not start with reserved prefixes (`claude-`, `anthropic-`,
  `anthropics-`, `cc-plugin-`) [S14].

## D3: One skill, one source, generated adapters

**Status:** Decided

**One `nova` skill with reference files** (the Impeccable model [S17]):

```
skill/
├── SKILL.md                    # entry point: what nova is, run `nova status` first, routing table
├── reference/
│   ├── app-dev.md              # create → build → install (--copy-image) → logs → fix
│   ├── catalog.md              # find and install catalog entries (e.g. cameras)
│   ├── nucleus.md              # files on Nucleus
│   ├── omniverse.md            # once the lifecycle is a command (see Open questions)
│   ├── api.md                  # nova api: search → schema → --dry-run → run
│   ├── troubleshoot.md
│   └── degraded/…              # fallbacks for agents without hooks or subagents
└── agents/…                    # optional subagent definitions
```

- One entry point keeps discovery simple; the agent loads only the reference file it
  needs. Separate skills can still be split out later if one grows large.
- `SKILL.md` follows the spec: `name` matches the directory, `description` says what and
  when, under ~500 lines, references one level deep [S12].
- Content is agent-neutral ("run `nova catalog list -o json`", not "use the Bash tool").
  Examples depend on D9 (`-o json` does not exist yet).

**Generated adapters.** A build script transforms `skill/` into per-agent output, and a
workflow regenerates and commits it on every change to the source (as Impeccable's
`sync-generated-output.yml` does [S17]):

```
plugin/                         # Claude Code plugin (.claude-plugin/plugin.json, skills/, hooks/, .mcp.json)
cursor-plugin/                  # Cursor plugin
.agents/skills/nova/            # plain skill tree for agents that read it
.claude-plugin/marketplace.json # points at ./plugin
```

- Start with **Claude Code plus a plain `.agents/skills/` tree**; add Cursor or Codex
  next. Other agents later, on demand.
- Keep a `HARNESSES.md`: which agent supports which skill fields, hooks and subagents,
  with "last verified" dates [S17]. Agent capabilities change quickly.
- The skill must work without hooks or subagents (degraded mode), e.g. by telling the
  agent to run `nova status` itself.
- The Claude plugin stores only the **host** in `userConfig`, never tokens (see D5). No
  `bin/` directory, since claude.ai/Cowork refuse plugins that ship one [S14].
- Kit CI runs `skills-ref validate` and `claude plugin validate --strict`.

## D4: CLI via shell for local work, MCP for the NOVA API

**Status:** Decided

- **CLI through the agent's shell:** app create/build/install (incl. `--copy-image`),
  `nucleus` file operations, `catalog`, `config`, `virtual`. Local developer work, where
  models already handle shell commands well and MCP adds nothing.
- **`nova mcp`:** the NOVA API, for agents and clients that work better with typed tools
  or have no shell.
- We do **not** wrap nucleus/catalog/app commands as MCP tools unless a concrete need
  appears.

Matches the hybrid recommendation in Malik et al. (arXiv 2610.30157), a position paper
without its own experiments: we use its ideas, not its numbers. Token-cost comparisons
in blog posts mostly re-cite vendor numbers and ignore prompt caching.

## D5: Credentials and the binary

**Status:** Decided (direction); redaction and host binding Planned

- **One source of truth:** `nova config`. The kit ships only the launch entry
  (`nova mcp`), no secrets; plugin config holds at most the host.
- **Redact secrets:** `nova config view` hides tokens unless `--show-secrets`;
  `nova status` never prints them. `nova login token` stays for humans but skills never
  use it.
- **Auth only to the configured host:** never attach stored credentials to other hosts,
  and never follow an absolute `servers[0].url` from a spec to a different host.
- **Plain HTTP:** warn when credentials go to a non-TLS host.
- **Missing binary:** the Claude adapter's SessionStart hook checks `command -v nova`
  and prints install instructions. The published docker image (`nova mcp`) is a fallback
  **for MCP only**; shell workflows (app, nucleus, catalog) need the installed CLI.

## D6: Versioning on three axes

**Status:** Planned

| Axis | Mechanism |
|---|---|
| CLI ↔ kit | Kit declares `requires.nova`; `nova agent check` and the SessionStart hook verify it. Kit CI installs the latest **stable** `nova` (never `rc`) and fails if the skill references a command or flag that doesn't exist. |
| Kit ↔ instance | Skill content declares `requires.nova-api` where it depends on endpoints from a given NOVA version. Kit CI runs against a pinned instance version. |
| CLI ↔ instance | `nova status -o json` reports the instance API version. The CLI discovers the spec (v2, then v1) instead of hardcoding a path. |

- **Snapshots only of what this repo owns:** the read/motion classification tables (D8),
  toolset membership, and the JSON shapes of `nova status` / `nova api` /
  `--list-tools`. The NOVA API's own tool surface is not snapshotted here: it comes from
  whichever instance you connect to.
- `nova mcp --list-tools -o json` prints tool names, schemas and annotations without
  starting a server.
- `tools/list` is returned in deterministic order (a SHOULD since MCP 2026-07-28 [S5]);
  covered by a test, because SDK ordering is unverified.

## D7: Keep the MCP context small

**Status:** Planned

Today `nova mcp` generates one tool per OpenAPI operation (`pkg/mcp/mcp.go`).

- **Per-operation tools in toolsets:** `--toolsets` grouped by OpenAPI tag, with a small
  named `default` set plus `all`, and `--tools` for single extras [S7].
- **Not generated as tools:** streaming endpoints (`x-bidistream` / `x-serverstream`,
  WebSocket upgrades), long-poll endpoints (e.g. `waitForIOEvent`), and binary downloads
  (GLB/USD models, archives). They return a typed error pointing to the SDK or to a CLI
  command with `--output <file>`.
- **No server-side search layer by default.** Claude Code already defers MCP tools and
  searches them itself [S4]; the OpenAI and Anthropic APIs have native tool search
  [S3][S15]. A meta-layer would lose per-tool permission rules and annotations. GitHub
  removed its equivalent (`--dynamic-toolsets`) in May 2026 [S8].
- Clear server instructions (≤4,096 chars, keywords first) [S4]; the context tool (D10)
  marked `_meta["anthropic/alwaysLoad"]: true`.
- Good descriptions with input examples; paginate and truncate large results with a hint
  on how to narrow the query; optional `fields` or concise output [S1][S3][S7].

**Deferred: `--mode=discover`.** `nova api search/schema` (D12) covers discovery for
agents with a shell. Revisit only for MCP clients without tool search.

## D8: Guard rails: classification, read-only, motion

**Status:** Planned

**Classification is deny-by-default.** HTTP method alone is wrong for NOVA: the endpoints
that move the robot (`executeJogging`, `executeTrajectory`, `streamFreeDrive`, …) are GET
requests that upgrade to WebSockets.

1. An explicit **allow-list of reads** gets `readOnlyHint: true`.
2. An explicit **motion/I-O list** (streams, `/execution/`, jogging, free drive, I/O
   writes, operation mode) gets `destructiveHint: true`, `openWorldHint: true`.
3. Everything else: not read-only, destructive (the MCP defaults).
4. **CI fails** when an operation of the current spec is on neither list, so a new
   endpoint never becomes "read-only" by accident.

Preferred long-term: the platform team adds an `x-nova-agent` extension
(`read` / `write` / `motion`) to each operation in the OpenAPI spec, so the classification
is versioned with the API instead of in a CLI table.

**Guard rails built on it:**

- `--read-only` filters server-side on the classification, wins over
  `--tools`/`--toolsets`, and applies to `nova api` too (flag, config or
  `NOVA_READ_ONLY=1`). Enabled by default in the kit.
- **No motion in the first version** of `nova mcp` and `nova api`; streams are excluded
  anyway (D7). Whether agents should execute motion at all is an open question.
- When motion tools exist: `_meta["anthropic/requiresUserInteraction"]: true` forces a
  permission prompt in Claude Code, which also means they are **uncallable in headless
  runs** (`dontAsk`, SDK/CI). Other clients ignore `_meta` and may ignore annotations, so
  a server-side `--allow-motion` flag is the only control that works across agents.
- In Claude Code, `readOnlyHint` lets read tools run in parallel and be retried safely [S4].
- **Untrusted content:** catalog entries (from arbitrary catalog URLs), logs and resource
  names come from outside. The skill and server instructions say to treat them as data,
  never as instructions.
- Confirmation flows use explicit arguments and the client's permission prompt, not MCP
  elicitation (reworked in MCP 2026-07-28) [S5].

## D9: CLI contract

**Status:** Planned

A `docs/CLI-CONTRACT.md` specifies, for every command agents use, arguments, stdout and
stderr, exit codes and files written; golden tests replay it (as Impeccable's
`CLI-CONTRACT.md` and `tests/oracle/` do [S17]). Rules:

- `-o json` with a stable shape on `status`, `app list`, `cell list`, `controller list`,
  `catalog list` first. When stdout is not a TTY, stdout carries only data; everything
  else goes to stderr [S10][S15].
- gh-style field selection later: `--json` with no fields lists them, `--json f1,f2`
  selects [S15].
- **Existing commands stay non-interactive.** `cell delete`, `app delete`,
  `instance delete`, `backup restore`, `version set` and `virtual create` run without
  prompts today, and scripts rely on that. No new prompts.
- **Typed `--confirm=<name>` only for a short, named list of severe operations** (through
  `nova api` / MCP): I/O writes, operation mode changes, controller add/delete,
  configuration restore, NOVA version update. `--dry-run` prints the request.
- A refused or aborted action **never exits 0**: distinct non-zero exit code plus a
  typed error on stderr, e.g.
  `{"error": {"code": "confirmation_required", "retryable": false, "hint": "pass --confirm=<cell>"}}`.
  (Cloudflare's `cf` exits 0 on abort; we don't copy that [S10].)
- Unknown commands suggest the closest matches. Idempotent where possible.
- MCP tools report API failures as results with `isError: true` and actionable text, not
  as protocol errors [S6]. `outputSchema` / `structuredContent` only for clients on the
  newer protocol [S7].

## D10: `nova status` as the single context command

**Status:** Planned

Reports config (without secrets), reachable instance, CLI and instance API version,
Nucleus connectivity and installed apps; `-o json`. Replaces a separate
`version -o json`. Also exposed as an always-loaded MCP context tool (like GitHub's
`context` toolset) [S7]. First step of the skill and of the SessionStart hook.

## D11: `nova agent install | update | check`

**Status:** Planned (after the kit exists)

```
nova agent install [--agent claude|cursor|codex|…] [--from <path|tarball>]
nova agent update
nova agent check     # kit version vs. CLI version vs. instance version
```

- Writes the matching adapter of a released kit into the project (default
  `.agents/skills/`) or home directory; auto-detects the agent [S17].
- `--from` supports offline and air-gapped networks (common for on-prem IPCs) and
  internal kit content.
- `nova app create` may call it for new projects.

## D12: `nova api`: authenticated access to the instance API

**Status:** Planned

An authenticated passthrough to the **instance** NOVA API in the style of `gh api` /
`glab api` [S16], plus discovery. Portal commands (`instance`) are out of scope.

```
nova api search "<task>"                         # top-N operations as JSON
nova api schema <operationId>                    # method, path, parameters, body fields
nova api /cells/{cell}/controllers               # GET; host + auth from nova config
nova api /cells/{cell}/apps -X POST -f name=foo  # -f/-F body fields like gh
nova api ... --dry-run                           # later: --paginate, --jq
```

- The command shape mirrors `gh api` on purpose: models already know it. (The
  `search`/`schema` naming is ours; Cloudflare's `cf` uses `cf cli search` /
  `cf schema` [S10].)
- `search`/`schema` read the instance's spec, cached under the config directory keyed by
  host and API version. No network after the first fetch.
- New NOVA endpoints work immediately, with no wrapper code.
- Paths must start with `/`, so they never collide with subcommands. Placeholders such
  as `{cell}` are filled from config or flags.
- Auth only to the configured host (D5). Streams and long-poll endpoints are refused with
  a typed error; binary responses need `--output <file>`.
- **Same classification and guard rails as `nova mcp` (D8), from one shared Go package.**
  A raw passthrough must not become the way around them.

## D13: Track the MCP spec and Go SDK

**Status:** Planned

- The server uses `mark3labs/mcp-go` v1.4.0; its support for MCP 2026-07-28 is
  unverified. Evaluate moving to the official `modelcontextprotocol/go-sdk` (GitHub's
  server did).
- MCP 2026-07-28 is stateless (no `initialize`; `server/discover`), deprecates roots,
  sampling and logging, and replaces server-initiated elicitation with multi round-trip
  requests [S5]. Avoid building on deprecated features.
- The tasks extension (`resultType: "task"`) for long-running jobs requires a server on
  protocol 2026-07-28, so it depends on the SDK decision.

## D14: MCP hosted on the instance, CLI as proxy with local fallback

**Status:** Open. Proposed direction; needs the NOVA platform team.

```
agent ──stdio──> nova mcp ──HTTP──> instance MCP endpoint   (NOVA API tools, scoped tokens)
                    │
                    ├─ adds auth from nova config (token refresh, TLS flags)
                    ├─ optional local toolsets (nucleus, app build) merged in
                    └─ fallback: no MCP endpoint on the instance → OpenAPI generation (once fixed, see "Current state")
```

| | Hosted on the instance | Started locally (today) |
|---|---|---|
| Version match | Always matches that instance's API | Reads the instance's OpenAPI; drift possible |
| Enforcement | Possible through scoped tokens on the instance | Guard rails only |
| Clients without a shell (claude.ai, Cowork, ChatGPT) | Can connect directly as a remote MCP server | Not possible |
| Auth | Needs MCP OAuth for direct connections | Uses `nova config` tokens |
| Local capabilities (docker, ssh, Nucleus files) | Impossible | Natural fit |
| Rollout | Needs a platform release | Works with every instance once fixed |

- The kit's launch entry stays `nova mcp`; users don't notice where tools come from.
- MCP 2026-07-28 is stateless, which makes proxying simple [S5].
- On-prem IPCs are often reachable only on the local network: proxying through the CLI
  works there; direct connections from cloud clients don't.
- **Cheaper first steps** that don't need an MCP endpoint: `x-nova-agent` classification
  in the spec (D8) and scoped tokens (read-only, no-motion).
- **Attribution:** the CLI sends `User-Agent: nova-cli/<version> agent=<client>` (today:
  `wos-cli-mcp/1.0`), so instance logs can tell agent-driven calls apart.

---

## Roadmap

First slice, in order:

1. **Make `nova mcp` work and safe by default:** spec discovery (v2, then v1), all
   diagnostics to stderr, no streaming/long-poll/binary tools, deny-by-default
   classification with the CI check, `--read-only` and `--toolsets`, deterministic
   order. No motion.
2. **`-o json` and `nova status`,** non-TTY data-only stdout, secrets redacted in
   `config view`, auth bound to the configured host.
3. **`nova api`** (GET plus an explicit write allow-list, `--dry-run`, `search`,
   `schema`), sharing the guard-rail package with step 1.
4. **Minimal `nova-agent-kit`:** the `nova` skill with 3–4 reference files (status and
   connect, app dev loop, catalog incl. cameras, Nucleus files), `.mcp.json`, Claude
   plugin plus `.agents/skills/`, CI against the latest stable `nova`.
5. **Ask the platform team now:** `x-nova-agent` classification in the spec and scoped
   tokens.

Deferred: `--mode=discover`, `nova agent install/update/check`, further adapters,
multi-plugin marketplace, instance-hosted MCP.

## Open questions

- **Motion through agents:** should agents ever execute motion (jogging, trajectories,
  free drive), or only plan and simulate it?
- **Omniverse spin-up:** what is the actual lifecycle today (catalog app, virtual
  instance, manual)? It should become a CLI command before skill content is written on
  top of it.
- **Instance-hosted MCP (D14):** does the platform team want to own an MCP endpoint,
  OAuth and token scopes? Which NOVA version could ship it?
- **Kit visibility (D2):** which content is public, which internal?
- **Default toolset:** which NOVA API tags belong in `default`?
- **Evals:** how do we measure whether a skill or tool change helps (an eval harness, as
  recommended in [S1])?

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
- [S17] Impeccable (one skill with reference files, generated adapters, CLI contract, harness matrix). https://github.com/pbakaus/impeccable (`docs/HARNESSES.md`, `docs/CLI-CONTRACT.md`, `.github/workflows/sync-generated-output.yml`)
- Malik et al.: MCP vs CLI for AI Agents (position paper). https://arxiv.org/html/2610.30157v1
