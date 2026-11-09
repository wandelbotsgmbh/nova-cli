# ADR-0013: `nova api`: authenticated access to the instance API

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** done for the v2 API (passthrough, `search`, `schema`, `--dry-run`);
  guard rails dropped (see ADR-0009 addendum 2026-11-09); `--paginate`, `--jq` not started

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
nova api /api/v2/cells/{cell}/controllers        # GET; host + auth from nova config
nova api /api/v2/cells/{cell}/apps -X POST -f name=foo  # -f/-F body fields like gh
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
- Streams and long-poll endpoints are refused with a typed error (amended: long polls are
  plain HTTP and allowed, see the implementation notes); binary responses need
  `--output <file>`.
- ~~Classification, `--read-only` and `--confirm` per ADR-0009, from the shared package.~~
  Dropped, see the ADR-0009 addendum of 2026-11-09.

## Alternatives

- **`curl` plus a token from `nova login token`:** tokens in the transcript, no guard
  rails, no discovery.
- **MCP tools for every operation:** see ADR-0008.

## Consequences

- New NOVA endpoints are usable immediately, with no wrapper code.
- The skill teaches the loop: `search` → `schema` → `--dry-run` → run.

## Implementation notes (2026-11-09)

- **Paths are complete** (`/api/v2/...`): the CLI adds no prefix, so app APIs on the
  instance (NovaFlow, …) stay reachable through the same command. `search`, `schema` and
  the classification cover the v2 API only; the v1 API answers `410 Gone` on 26.x.
- **Spec:** `/api/v2/ui/public.openapi.yaml`, cached as JSON under
  `~/.nova/cache/api/<host>/<NOVA version>.json`. The key is the NOVA version
  (`/api/v2/system/version`, e.g. 26.8.0), not `info.version` (2.8.0), which stays the
  same across releases whose spec changes.
- **No guard rails** (ADR-0009 addendum 2026-11-09): every request is sent as given, with
  the instance's credentials. A classification by operationId with `--read-only`,
  `--confirm` and a virtual-controller check was built and removed again before release.
- **Answers are passed through as they arrive** (like `gh api` and `curl`), so chunked and
  slow answers show up progressively. Long polls (`waitForIOEvent`) are plain HTTP and
  allowed; `--timeout` limits only the wait for the headers, `--timeout 0` lifts it.
- **WebSocket streams** (`x-serverstream`, `x-bidistream` in the spec) are refused with
  `STREAM_NOT_SUPPORTED` (exit 3): the command speaks plain HTTP. This is the same line
  `gh api`, `glab api`, `kubectl get --raw` and `stripe get` draw: the generic command is
  request/response, streams get dedicated commands. Without a spec (path outside
  `/api/v2`, spec not loadable) the request is sent unchecked.
- **Typed errors** (ADR-0010): exit 3 for refusals, `Hint:` line, and when stderr is no
  terminal one JSON line `{"error": {"code", "message", "hint", "retryable"}}`. A missing or
  expired login (including the ingress' 401 page with HTTP 200) is `UNAUTHORIZED`, exit 1.
- **Answers:** the body goes to stdout unchanged; with an HTTP error the body is still
  printed and nova exits with 1 (like `gh api`). Binary answers need `--output <file>`
  (`-o` names a file here, like in `flow get`). The binary check needs the answer's
  headers, so the GET has been sent by then.
- **Fields** follow `gh api` (`-f`, `-F`, nested `a[b]`, `a[]`, `@file`; fields make the
  request a POST unless `-X` is given), except that `-F` converts numbers with a fraction
  too: poses and joints are floats.
- **Later** (not in this implementation):
  - Dedicated commands for the server streams that matter, with built-in limits and one
    JSON line per message, e.g. `nova controller state --watch [--duration 10s]`,
    `nova io watch` (like `kubectl get --watch`, `glab ci trace`).
  - Bidirectional streams (jogging, trajectory execution) stay with the SDK and apps;
    trajectory execution may get its own integration later, not a generic channel in
    `nova api`.
- Shared code: `pkg/novaapi` (client bound to the instance host, spec, search, schema,
  gh-style fields), `pkg/ingress` (login redirect and error pages, also used by
  `pkg/novaflow`).
