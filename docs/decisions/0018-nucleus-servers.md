# ADR-0018: Nucleus servers are their own target

- **Date:** 2026-11-05
- **Status:** proposed

## Context

ADR-0016 made `nucleus-host` and `nucleus-token` settings of an instance entry, so a
Nucleus token is never sent to a server that belongs to another instance (ADR-0006). In
practice a Nucleus server doesn't belong to an instance:

- The servers in use are shared: `nucleus.dev.wandelbots.io` and
  `nucleus.wb.wandelbots.io`. Any instance, or none, works with either.
- Every instance entry needs its own copy of host and token. Removing an entry that is
  gone (e.g. the migrated `default` of a deleted cloud instance) drops the Nucleus
  settings with it.
- `nova nucleus` resolves an instance before it runs, although it never talks to one: a
  wrong `--instance` breaks `nova nucleus ls`.
- There is no way to switch between Nucleus servers (dev vs. wb) without switching the
  instance or passing `--nucleus-host` and `--nucleus-token` on every call.

## Decision

Nucleus servers get their own registry, built like the instance registry of ADR-0016,
and `nova nucleus` no longer resolves an instance.

- **Entries** live under `nucleus-servers` in the config, with `name`, `host`, `token`
  and `tls` (default `true`), plus `current-nucleus`:

  ```yaml
  nucleus-servers:
    - name: dev
      host: nucleus.dev.wandelbots.io
      token: …
    - name: wb
      host: nucleus.wb.wandelbots.io
      token: …
  current-nucleus: wb
  ```

  Environments (dev, production, a customer's on-prem server) are just entries. They
  are not tied to the portal environment (`--env`): Nucleus uses its own API tokens, not
  the portal login.
- **Target resolution:** `--nucleus <name>`, then `NOVA_NUCLEUS`, then
  `current-nucleus`. An unknown name is an error (`NOT_FOUND`), never a fallback.
  Resolving never writes the config. `--nucleus-host`, `--nucleus-token`,
  `NUCLEUS_TOKEN` and `--nucleus-tls` stay as per-invocation overrides of the resolved
  entry, or replace it when there is none.
- **Commands:** `nova nucleus server add <name> --host <host> [--token-stdin] [--tls=false]`,
  `server list` (marks the selected entry, never shows tokens), `server select <name>`,
  `server remove <name>`. No prompts (ADR-0010). `nova nucleus ls/cp/mv/rm/mkdir/stat`
  carry `annotationNoTarget`.
- **`nova config set nucleus-host|nucleus-token`** keep working and write the resolved
  Nucleus server (`--nucleus`, `NOVA_NUCLEUS`, `current-nucleus`). With no server
  registered, `config set nucleus-host` creates one named after the host (see
  migration) and selects it. `--instance` has no effect on these keys.
- **Credentials:** a token is only sent to the host of its entry (ADR-0006).
  `config view` and `server list` redact it unless `--show-secrets`.
- **Migration**, on the first run without `nucleus-servers`: the Nucleus keys of all
  instance entries and the legacy top-level keys become server entries, one per host
  (the first token found for a host wins; the selected instance's comes first). An
  entry is named after the first label of its host that isn't `nucleus`
  (`nucleus.dev.wandelbots.io` → `dev`, `nucleus.wb.wandelbots.io` → `wb`), with `-2`,
  `-3`, … on collisions. `current-nucleus` is the server of the selected instance, else
  the first entry. The old keys stay untouched for older binaries, which keep reading
  them; the new binary ignores them once `nucleus-servers` exists.
- **Later, only with a real case:** an instance entry may name its server
  (`nucleus: <name>`, e.g. an IPC with an on-prem Nucleus), resolved after
  `NOVA_NUCLEUS` and before `current-nucleus`. A reference, never a copy of host and
  token.

## Alternatives

- **Keep Nucleus per instance (ADR-0016):** binds a shared server to an arbitrary
  instance; settings are duplicated and lost with the entry.
- **Global `nucleus-host`/`nucleus-token` as a fallback for instances without their
  own:** smallest change, but only one server at a time; switching between dev and wb
  means rewriting the config.
- **Couple Nucleus servers to the portal environment (`--env dev` → dev Nucleus):**
  Nucleus doesn't use the portal login, and an on-prem server has no portal
  environment. Worth revisiting only if Nucleus auth moves to the portal login.

## Consequences

- Agents and the kit can pin a Nucleus server with `NOVA_NUCLEUS`, independent of
  `NOVA_INSTANCE` (as ADR-0016 does for instances); the kit holds at most a name.
- `nova status` (ADR-0011) reports the Nucleus server and where the choice came from.
- Supersedes the Nucleus parts of ADR-0016 ("Per-entry settings", "Migration").
- Changes made with the new binary (new servers, a new token) aren't seen by older
  binaries, which keep reading the old keys.
- Follow-up: `-o json` for `nucleus server list` (ADR-0010).
