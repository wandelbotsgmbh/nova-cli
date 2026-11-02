# ADR-0016: Named instances and a per-invocation target

- **Date:** 2026-11-02
- **Status:** proposed
- **Implementation:** in progress

## Context

`nova` targets exactly one instance: the `host` key in `~/.nova/config.yaml`. Developers
and agents work against several at once: a cloud instance from portal.wandelbots.io, an
on-prem IPC or any other reachable NOVA host, and Wandelbots-internal Kubernetes
clusters. Today:

- Switching means rewriting the shared config (`nova config set host`,
  `nova instance select`). Two agent sessions on the same machine fight over it: one
  switches, the other silently follows.
- `nova virtual create` destroys whatever `virtualid` points to before creating a new
  cluster, so only one exists at a time. `instance create/select` write the same
  `virtualid` key, so that can even be a cloud instance ID.
- `nucleus-host` and `nucleus-token` are global, so after switching the host they are
  sent to a Nucleus server that belongs to a different instance (ADR-0006: credentials
  stay with what they were configured for).
- `nova mcp` reads only the config, so a plugin can't pin it to an instance
  (`nova-agent-kit` needs this for its `.mcp.json`).
- ADR-0009 uses *virtual* and *physical* for **controllers**. A cloud instance and an
  IPC can both run virtual controllers, so instance kinds named after robots would blur
  the motion rule.

## Decision

`nova instance` becomes a local registry of named instances, and every command can be
pointed at one entry for a single invocation.

- **Entries** live under `instances.<name>` in the config, with a `kind`:

  | Kind | What it is | Registered by |
  |---|---|---|
  | `cloud` | portal.wandelbots.io instance | `nova instance create`, `nova instance select <cloud-id>` |
  | `host` | any reachable NOVA host (on-prem IPC, own server) | `nova instance add <name> --host <url>` |
  | `k8srun` | Wandelbots-internal Kubernetes cluster with NOVA, comes with a kubeconfig | `nova k8srun create` |

  The kind says **where the instance runs**, never whether its robots are real; that
  stays a per-controller lookup (ADR-0009).
- **Per-entry settings:** `host`, `cellname`, `id` (cloud/k8srun ID), `nucleus-host`,
  `nucleus-token`, `kubeconfig`. Global settings (editor, image registry, container
  runtime, catalogs, portal login) stay top-level. The portal login is a
  portal-account credential (one per environment), not an instance credential.
- **Target resolution:** `--instance <name>` flag, then `NOVA_INSTANCE`, then
  `current-instance` from the config. An unknown name is an error (`NOT_FOUND`); it
  never falls back to another entry. Resolving an override **never writes the config**,
  so concurrent sessions don't interfere. Only explicit writes (`config set`,
  `instance` and `k8srun` commands) touch the config.
- **No raw `--host` override.** A named entry keeps credentials bound to the host they
  were configured for; an ad-hoc host has no credentials to pick, and guessing would
  send the wrong ones.
- **Commands:**
  - `instance add/remove` manage local entries of kind `host` (`remove` works for every
    kind and only forgets the entry). `instance delete` keeps deleting a cloud instance.
  - `instance list/select/info` work for every kind; `list` marks the selected entry
    and, when logged in, appends cloud instances that aren't registered yet. The old
    portal listing stays available with `--cloud`.
  - `instance create/delete/start/stop` are cloud-only and return a typed error with a
    hint for other kinds.
  - `nova virtual` is renamed `nova k8srun` (after the internal service; "(Wandelbots
    internal)" in its help); `virtual` stays as a hidden, deprecated copy of the group.
    `k8srun create --name <n>` registers a named cluster; `k8srun create` without
    `--name` means `--name k8srun --replace`, which keeps the old "replace the previous
    one" behaviour but only for that unnamed entry. An existing name without
    `--replace` is an error. `k8srun destroy [name]` and `k8srun list` are added. No
    prompts (ADR-0010).
- **Secrets:** `config view` and `instance info` redact tokens unless `--show-secrets`
  (ADR-0006).
- **Migration:** on first run with a bare top-level `host`, the CLI adds an entry
  `default` (kind guessed: `wandelbots.io` host → `cloud`, `virtualid` set → `k8srun`,
  else `host`) and copies `cellname` and the Nucleus keys into it. Legacy keys are
  kept, and `select` mirrors `host`, `cellname` and `virtualid` back, so an older binary
  still works against the same config. `nova config set host` writes to the targeted
  entry, or creates `default` when there is none.

## Alternatives

- **`--host` / `NOVA_HOST` override:** simplest for scripts, but credentials can't be
  bound to an ad-hoc host (ADR-0006).
- **One config file per instance (`NOVA_CONFIG`):** isolates sessions, but duplicates
  global settings and the portal login in every file.
- **A separate `nova physical` group:** a second mental model next to `nova instance`
  for the same question ("which instance am I working against").
- **Kinds `portal` / `physical` / `virtual`:** `physical` and `virtual` collide with the
  controller terms of ADR-0009; `portal` names the website, not the product.
- **Kind `cluster` for the internal clusters:** too generic; a host can run Kubernetes
  too. `k8srun` is the name the service is known by internally.

## Consequences

- `nova-agent-kit` can set `NOVA_INSTANCE` from a plugin `userConfig` and keep its
  `.mcp.json` static. This refines ADR-0006 ("plugin config holds at most the host"): it
  holds at most an instance **name**, never a host or credentials.
- `nova status` (ADR-0011) should report the active instance, its kind and where the
  choice came from (flag, env, config).
- Follow-ups:
  - Attach the portal token only to cloud hosts and basic auth only to its entry
    (ADR-0006, "auth only to the configured host").
  - Write the config atomically with a lock: token refresh and `config set` rewrite the
    whole file and can still race between sessions.
  - `-o json` for `instance list/info` (ADR-0010).
