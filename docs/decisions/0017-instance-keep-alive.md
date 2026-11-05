# ADR-0017: `nova instance keep-alive` keeps an instance from expiring

- **Date:** 2026-11-05
- **Status:** proposed

## Context

Cloud instances on portal.wandelbots.io and k8srun clusters have a lifetime; when it runs
out, a cloud instance is suspended and a cluster is released. Both services can extend it,
but the CLI had no command for that. An agent working with an instance for hours doesn't
notice the expiry and loses the instance mid-task (issue #59).

How the two services extend (checked live on 2026-11-05):

- **Portal:** `PUT /v1/instances/{id}/lifetime` (answers 201, like the `GET`) takes no
  duration. It sets `expires_at` to now + `expiryHours` and `obsolete_at` to now +
  `obsoleteHours`, both deployment settings (`developer-portal/portal-api`,
  `HelmReleaseOrchestrator.ExtendInstance`; values in `developer-portal/applications`):
  168 h / 336 h on prod and stg (chart default), 24 h / 48 h on dev (measured live, also
  for an instance created with `--ttl 20`).
- **k8srun:** `PUT /instance?instance_id=&duration=<min>` sets the expiry to now + duration
  (≤ 720). The service doesn't report the expiry. A released cluster answers 404 on both
  `PUT` and `GET …/kubeconfig` (after a few seconds), so it can't be revived by accident.

## Decision

Two commands in `nova instance` for cloud instances and k8srun clusters:

- **`extend [name]`** extends once and prints the new expiry (RFC 3339) on stdout.
- **`keep-alive [name] [-- command [args...]]`** works like `caffeinate`: it extends only
  when less than `--margin` (default 15 min) is left, then sleeps until the next margin
  crossing (30 s to 5 min). Without a command it runs until SIGINT/SIGTERM; with a
  command it keeps the instance alive until the command exits and exits with its code.
  Progress goes to stderr (ADR-0010).
- The new expiry is always read back, never assumed. For k8srun, which doesn't report
  it, keep-alive sets the maximum (12 h) at start and tracks the expiry itself.
- Failures (service errors, expired login) are reported and retried. The portal login is
  refreshed before each check, so it outlives the 24 h access token. When the instance is
  gone (portal: unknown or `deleting`; k8srun: 404), keep-alive ends with an error; a
  wrapped command keeps running.
- A stopped cloud instance is never started; keep-alive waits until it runs again.

## Alternatives

- **Extend by a small step (e.g. 15 min) so a forgotten instance expires soon:** the portal
  takes no duration (would need an optional duration in portal-api). For k8srun it would
  cut a fresh cluster's 12 h to minutes.
- **Extend on every tick:** pushes the lifetime out without need and calls the services
  far more often.
- **Detach into a background daemon (pid file, `stop` subcommand):** platform-specific and
  leaves orphans. Wrapping a command, or `&` in the shell, covers agent sessions without
  state.

## Consequences

- An agent session can be started as `nova instance keep-alive -- <agent>`.
- After keep-alive ends, a cloud instance lives up to one more portal extension
  (7 days on prod, 24 h on dev) and a cluster up to 12 h.
