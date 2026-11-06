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
  | `k8srun` | Kubernetes cluster with NOVA from a Wandelbots-internal service (Wandelbots network only), comes with a kubeconfig: for workflows that need the Kubernetes API (skaffold, helm, kubectl) when no device is at hand | `nova k8srun create` |

  Cloud entries are named after their instance id; if a local entry already has that
  name, the first free `<id>-2`, `<id>-3`, … is used, so a created instance always ends
  up in the list and no existing entry is touched. `nova k8srun create` names its entry
  `k8srun` (replacing the previous unnamed cluster) or `--name`; `nova k8srun prune`
  removes entries whose cluster the service no longer knows.

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
- **Kubeconfig and shell shortcut:** `nova instance kubeconfig [name]` prints the path of
  the instance's kubeconfig: stored on `k8srun create`, fetched once over ssh for `host`
  entries (`sudo -n <k3s> kubectl config view --raw`, server rewritten from 127.0.0.1 to
  the host, stored with mode 0600 next to the entry; `--refresh` fetches again;
  `--setup-ssh-key` reuses the k3s-scoped sudo rule of `app install --copy-image`; or
  imported from a file with `--kubeconfig <file|->` on `instance add` / `instance kubeconfig`,
  without ssh), not
  available for `cloud`. `nova instance env [name]` prints `NOVA_INSTANCE` and
  `KUBECONFIG` exports for `eval` in the syntax of the calling shell (sh, fish, PowerShell; detected from the parent process), so a shell can be pinned to an
  instance without changing the selection. Only paths are printed, never the content; it
  is fetched only on these explicit commands, never as a side effect of `add` or
  `select`.
- **Portal logins per environment** (Wandelbots-internal environments `prod`, `stg`,
  `dev`): each environment keeps its own login under `logins.<env>`; `nova login --env X`
  stores it there and makes X the default (`default-env`, as the single login did before);
  token refreshes never change the default. Cloud entries record their environment
  (`env`). The environment of an invocation is `--env`, then `NOVA_ENV`, then the cloud
  target's, then the default, then `prod`; an explicit environment that contradicts the
  cloud target's is an error. Commands on another cloud entry (`instance stop <name>`, …)
  use that entry's environment.
- **The portal login only goes to wandelbots.io hosts** (the portal and cloud
  instances), never to `host` or `k8srun` instances (ADR-0006, "auth only to the
  configured host"). If these ever need it for permissions, that is a new decision.
- **Secrets:** `config view` and `instance info` redact tokens unless `--show-secrets`
  (ADR-0006).
- **Migration:** an existing `token` counts as the login of its environment, which
  becomes the default; it is copied to `logins.<env>` before it is ever overwritten, and
  the legacy key keeps the default environment's login for older binaries. On first run
  with a bare top-level `host`, the CLI adds an entry
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
- A kubeconfig is cluster admin on the instance. For an instance that drives real robots
  it falls under ADR-0009's "credentials for a real cell are not given to an agent working
  unattended"; the kit must not fetch it on its own.
- Follow-ups:
  - Bind the deprecated basic auth (keyring) to its entry as well; it is still attached
    to every request.
  - Write the config atomically with a lock: token refresh and `config set` rewrite the
    whole file and can still race between sessions.
  - `-o json` for `instance list/info` (ADR-0010).

## Addendum 2026-11-05

- **`--no-select` on `instance create` and `k8srun create`.** Both select the new entry,
  as before named instances, and scripts like `nova instance create && nova app install .`
  rely on that. With `--no-select` the entry is only added, so a session that creates an
  instance doesn't switch the target of other sessions on the same machine ("one
  switches, the other silently follows", see Context); the new entry is targeted with
  `--instance`. Agents working in parallel should pass it.
- **Nucleus settings move out of instance entries** into their own registry (ADR-0018).
  `nucleus-host` and `nucleus-token` are no longer per-entry settings. Entries keep them
  for older binaries (the migration above still copies them into `default`), but the CLI
  ignores them and `instance info` doesn't show them.
- **`nova instance prune [--dry-run]`** removes entries whose instance is gone, as
  `nova k8srun prune` does for clusters. Only a definite answer counts: for `cloud`
  entries the portal not listing the instance for the account logged in to the entry's
  environment, for `k8srun` entries a 404 from the cluster service. The portal only
  answers per account (its per-id endpoints also answer 404 for another account's
  instance), so an entry for an instance of another account counts as gone. Any other answer (no login for that
  environment, network error, 5xx) keeps the entry. `host` entries are never pruned: an
  unreachable host can't be told apart from one that is switched off. If the selected
  entry is removed, no instance is selected. No prompts (ADR-0010).
