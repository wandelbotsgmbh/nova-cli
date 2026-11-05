# ADR-0010: CLI contract and result format

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** in progress (#40: suggestions for unknown subcommands, flag
  constraints; data on stdout, messages on stderr, `--debug` / `NOVA_DEBUG`, output tail in
  tool failures; `-o json`,
  typed errors and exit codes not started)

## Context

Agents parse CLI output and recover from errors. Free-form tables and ambiguous errors cost
retries. Today no command offers `-o json`.

## Decision

A `docs/CLI-CONTRACT.md` specifies, for every command agents use, arguments, stdout and
stderr, exit codes and files written; golden tests replay it (as Impeccable's
`CLI-CONTRACT.md` and `tests/oracle/` do [S17]).

- **`-o json`** with a stable shape, first on `status`, `app list`, `cell list`,
  `controller list`, `catalog list`. gh-style field selection later
  (`--json` lists fields, `--json f1,f2` selects) [S15].
- **Existing commands stay non-interactive.** `cell delete`, `app delete`,
  `instance delete`, `backup restore`, `version set` and `k8srun create` run without
  prompts today, and scripts rely on that. No new prompts. A preview by default plus
  `--confirm`, as agent-friendly CLI design recommends [S18], would break those scripts
  and needs its own ADR with a deprecation window.
- **Data on stdout, everything else on stderr, always** (not only without a TTY):
  progress, warnings, prompts and the output of child processes (docker, ssh, kubectl)
  go to stderr, so `$(nova cell list)` and pipes get only the data [S10][S15]. Contract tests run
  the commands like the real process (`runAsProcess` in `pkg/cmd/output_contract_test.go`).
- **`--debug` / `NOVA_DEBUG=1`** writes diagnostics to stderr, prefixed `debug: `: the
  resolved instance and portal environment, every HTTP request and response (headers,
  first 4 KiB of text bodies) and every external command line. It catches what error
  messages lose, e.g. ingress error pages served as HTTP 200. Secrets (tokens, passwords,
  cookies, kubeconfig credentials, JWTs) are redacted before they are written. The env var
  lets an agent switch it on once for a session; stdout is unchanged either way.
- **Failures of external tools explain themselves.** When a docker/podman build or push or
  the ssh image import fails and stderr is not a terminal, the error carries the last 20
  lines of the tool's output (the failing step, the compiler error), so an agent finds the
  cause next to the error instead of in a long, possibly truncated log. On a terminal the
  tool keeps its TTY and interactive progress view.
- **A refused or aborted action never exits 0**: a distinct non-zero exit code plus a
  typed error on stderr, e.g.
  `{"error": {"code": "CONFIRMATION_REQUIRED", "retryable": false, "hint": "pass --confirm=<cell>"}}`.
  (Cloudflare's `cf` exits 0 on abort; we don't copy that [S10].) Hints in errors and
  distinct exit codes are what agent harnesses rely on to recover without retries
  [S18][S20][S21].
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
