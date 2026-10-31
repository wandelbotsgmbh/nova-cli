# ADR-0013: `nova api`: authenticated access to the instance API

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** not started

## Context

Curated MCP tools (ADR-0008) and CLI commands (ADR-0001) cover common tasks. The rest of
the NOVA API still needs to be reachable, without agents handling tokens in `curl`
commands.

## Decision

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
  host and API version.
- Paths must start with `/`, so they never collide with subcommands. Placeholders such as
  `{cell}` are filled from config or flags.
- Auth only to the configured host (ADR-0006).
- Streams and long-poll endpoints are refused with a typed error; binary responses need
  `--output <file>`.
- Classification, `--read-only` and `--confirm` per ADR-0009, from the shared package.

## Alternatives

- **`curl` plus a token from `nova login token`:** tokens in the transcript, no guard
  rails, no discovery.
- **MCP tools for every operation:** see ADR-0008.

## Consequences

- New NOVA endpoints are usable immediately, with no wrapper code.
- The skill teaches the loop: `search` → `schema` → `--dry-run` → run.
