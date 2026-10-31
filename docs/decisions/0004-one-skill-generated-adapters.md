# ADR-0004: One `nova` skill, generated adapters

- **Date:** 2026-10-31
- **Status:** accepted

## Context

The kit serves several agents (Claude Code, Cursor, Codex, Copilot, Gemini, …) with
different packaging formats. The content should be written once.

## Decision

**One `nova` skill with reference files** (the Impeccable model [S17]):

```
skill/
├── SKILL.md                    # entry point: what nova is, run `nova status` first, routing table
├── reference/
│   ├── app-dev.md              # create → build → install (--copy-image) → logs → fix
│   ├── catalog.md              # find and install catalog entries (e.g. cameras)
│   ├── nucleus.md              # files on Nucleus
│   ├── omniverse.md            # once the lifecycle is a command
│   ├── api.md                  # nova api: search → schema → --dry-run → run
│   ├── troubleshoot.md
│   └── degraded/…              # fallbacks for agents without hooks or subagents
└── agents/…                    # optional subagent definitions
```

**Generated adapters.** A build script transforms `skill/` into per-agent output, and a
workflow regenerates and commits it on every change to the source (as Impeccable's
`sync-generated-output.yml` does [S17]):

```
plugin/                         # Claude Code plugin (.claude-plugin/plugin.json, skills/, hooks/, .mcp.json)
cursor-plugin/                  # Cursor plugin
.agents/skills/nova/            # plain skill tree for agents that read it [S13]
.claude-plugin/marketplace.json # points at ./plugin
```

## Alternatives

- **Several separate skills** (`nova-app-dev`, `nova-catalog`, …): more entry points to
  discover; can still be split out later if one reference file grows large.
- **Hand-maintained adapters per agent:** duplicated text drifts.

## Consequences

- `SKILL.md` follows the spec: `name` matches the directory, `description` says what and
  when, under ~500 lines, references one level deep [S12].
- Content is agent-neutral ("run `nova catalog list -o json`", not "use the Bash tool").
  Such examples depend on ADR-0010.
- Start with **Claude Code plus `.agents/skills/`**; Cursor or Codex next; others on
  demand.
- A `HARNESSES.md` records which agent supports which skill fields, hooks and subagents,
  with "last verified" dates [S17].
- The skill works without hooks or subagents (degraded mode), e.g. by telling the agent to
  run `nova status` itself.
- The Claude plugin stores at most the **host** in `userConfig`, never tokens (ADR-0006),
  and has no `bin/` directory (claude.ai/Cowork refuse those) [S14].
- Kit CI runs `skills-ref validate` and `claude plugin validate --strict`.
- Skill content treats catalog entries, logs and resource names as untrusted data, never
  as instructions (ADR-0009).
