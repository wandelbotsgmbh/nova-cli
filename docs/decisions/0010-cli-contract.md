# ADR-0010: CLI contract and result format

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** not started

## Context

Agents parse CLI output and recover from errors. Free-form tables and ambiguous errors cost
retries. Today no command offers `-o json`.

## Decision

A `docs/CLI-CONTRACT.md` specifies, for every command agents use, arguments, stdout and
stderr, exit codes and files written; golden tests replay it (as Impeccable's
`CLI-CONTRACT.md` and `tests/oracle/` do [S17]).

- **`-o json`** with a stable shape, first on `status`, `app list`, `cell list`,
  `controller list`, `catalog list`. When stdout is not a TTY, stdout carries only data;
  everything else goes to stderr [S10][S15]. gh-style field selection later
  (`--json` lists fields, `--json f1,f2` selects) [S15].
- **Existing commands stay non-interactive.** `cell delete`, `app delete`,
  `instance delete`, `backup restore`, `version set` and `virtual create` run without
  prompts today, and scripts rely on that. No new prompts.
- **A refused or aborted action never exits 0**: a distinct non-zero exit code plus a
  typed error on stderr, e.g.
  `{"error": {"code": "CONFIRMATION_REQUIRED", "retryable": false, "hint": "pass --confirm=<cell>"}}`.
  (Cloudflare's `cf` exits 0 on abort; we don't copy that [S10].)
- **Error codes** are a fixed set in `UPPER_SNAKE_CASE`, shared with the MCP servers on
  the instance where they apply (`INVALID_ARGUMENT`, `NOT_FOUND`, `OUT_OF_WORKSPACE`,
  `NO_IK_SOLUTION`, `COLLISION`, `ROBOT_BUSY`, `EXECUTION_FAILED`, `APPROVAL_REQUIRED`, …).
- **MCP tool results** are compact: one line `CODE: sentence; key=value; ...` plus data
  only where needed, images only on request. API failures are
  results with `isError: true`, not protocol errors [S6].
- Unknown commands suggest the closest matches. Idempotent where possible.

## Alternatives

- **Prompting by default with `--yes` to skip:** breaks existing scripts and CI.
- **Full JSON results in MCP:** costs tokens on every turn and can leak fields.

## Consequences

- Golden tests make output changes visible in review.
- The kit's examples (ADR-0004) depend on this.
