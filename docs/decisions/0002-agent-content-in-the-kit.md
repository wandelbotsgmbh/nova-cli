# ADR-0002: All agent content lives in the kit, not in the CLI

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** in progress (NovaX template cleanup)

## Context

Agent content (skills, prompt files, agent instructions) changes much faster than the
CLI. The NovaX template of `nova app create` (`pkg/generator/PythonApp/.github/`) shipped
GitHub Copilot prompt files and `copilot-instructions.md`. Every generated app got a copy
that never updates, and by October 2026 they were stale: pinned to "Claude Sonnet 4", a
manual `docker buildx` step described as temporary, links to the 25.6 docs, and an
instruction to run `nova config view`, which prints secrets.

## Decision

- No agent content in the `nova` binary: no embedded skills, and **no skills, prompts or
  agent instructions in the `nova app create` templates.**
- All agent content lives in `nova-agent-kit` (ADR-0003). The useful parts of the removed
  NovaX prompts (local setup, start server, new program, test program, deploy) and of
  `copilot-instructions.md` (project structure, Python guidelines) move into the kit's
  reference files.
- Projects get agent content through the kit (plugin, `.agents/skills/`, or
  `nova agent install`, ADR-0012).

## Alternatives

- **Keep the prompts in the template and update them:** every generated app still freezes
  a copy, and the CLI release cycle drives content changes.
- **Embed the skill in the CLI (`go:embed`) and write it on demand:** keeps versions in
  sync (Impeccable does this [S17]), but ties skill releases to CLI releases.

## Consequences

- Apps created between the template change and the first kit release get no agent
  content. The removed content stays in git history.
- The CLI only needs to stay compatible with the kit (ADR-0007).
