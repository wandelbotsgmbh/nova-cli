# Agent instructions

## Building & testing

* On mac, build with `./build.sh` — produces `./bin/nova`
* After building, use `nova` in terminal for testing
* Use `nova config view` for getting configured values (host, nucleus-token, etc.)
* The command reference is not committed: it lives on the CLI reference site (GitHub Pages, deployed by the release workflow for stable releases). Look up commands with `nova <command> --help`.
* Site build: `hack/docsite/build.sh [version]` → `_site/`. Generates markdown into a temp dir (`go run . gendoc` alone writes to the gitignored `_docs/`) and renders it with `hack/docsite` (goldmark + template styled with `@wandelbots/design-tokens` / `@wandelbots/fonts`, vendored from npm at build time — bump the pinned versions in `build.sh`).

## App install: `--copy-image` (registry-less)

* `nova app install <app> --copy-image --ssh-user <user>` builds locally, copies the image to the configured `host` over ssh/scp, and imports it into the instance's k3s containerd — no registry push. Works with docker and podman (auto-detected).
* Only layers missing from the target's containerd content store are transferred; on any error it falls back to whole-image transfer.
* `--setup-ssh-key` (one-time per instance): installs the local ssh key + a k3s-scoped NOPASSWD rule in `/etc/sudoers.d/nova-cli`. The single sudo prompt during setup is the unavoidable bootstrap (needs sudo once to grant passwordless sudo); installs afterwards are prompt-free.
* Target needs sudo for `ctr` (containerd socket is root-only); k3s is invoked by absolute path so the NOPASSWD rule matches. Use `--no-sudo` for root logins.
* Code: `pkg/cmd/app.go` (`installAppViaCopy`/`copyIncremental`/`copyWholeImage`), `pkg/remote/` (ssh/scp + `setup.go`), `pkg/docker/` (`BuildImageOCI`, `save.go`, `ocilayout.go`).

## App templates: local dev settings (`nova app update-env`)

* Templates live in `pkg/generator/<Type>App` (`*.tpl` files are rendered, `__Field__` folders renamed). Generated projects keep instance url, cell, base path and access token in a gitignored env file: `.env.local` (rust/golang/nextjs) or `.env` (python, python-decouple doesn't read `.env.local`). Go/Rust apps load it themselves on startup (godotenv/dotenvy), so launch configs carry no env and no token.
* `nova app update-env [path]` (`pkg/appenv`, `writeAppEnv` in `pkg/cmd/app.go`): always replaces the token (refreshed for `*.wandelbots.io`, empty otherwise), adds instance settings only if missing (`--reset` replaces them), keeps all other lines. `nova app create` runs it with reset. Variable names differ per template, see `appenv.Layout`.
* Instance ingress answers a missing/expired token with plain text `Error 401: Unauthorized` and **HTTP 200**; the Go/Rust templates check the content type and return 502 with a hint. Portal tokens last 24h.
* Python/novax registers its programs in the cell's program store under the app name from `BASE_PATH` (default `novax`) and removes them on shutdown: keep `BASE_PATH` unique when running locally against a shared instance.
* Verify template changes by generating with the built CLI (`bin/nova app create x -g <type>`) and running the app against an instance; `go test ./pkg/generator` only checks rendering.

## NovaFlow (`nova flow`)

* Code: `pkg/novaflow` (client), `pkg/cmd/flow.go`. Source of NovaFlow: `wandelbotsgmbh/novaflow` (Forest at `/<cell>/forest/trees`, control plane at `/<cell>/controlplane/api/v1`, editor at `/<cell>/flow`).
* Install = service-manager feature flag `GET/PUT /api/v2/internal/cells/{cell}/feature-flags` (`novaflow`, `novaflow_version`, read-only `novaflow_status`; flag API since service-manager 26.4, version/status since 26.8). Not in `nova-api-client-go`. **PUT replaces the whole object**: read-modify-write so `controller_connector` etc. survive (`FeatureFlags` keeps the raw fields).
* Verified live on a `nova virtual create` instance (service-manager 26.8.0) and a dev cloud instance (portal login reaches the internal flag API there too): install takes ~1 min; `disabled` is reported before the apps are gone, so uninstall also waits for Forest to stop answering; flows survive uninstall/reinstall (Forest keeps them in the object store). Versions other than the shipped one come from `oci://wandelbots.azurecr.io/helm/novaflow` and may not be pullable (401) — the old version keeps running, state `failed`, and the pin stays until `--version default`.
* The standalone `novaflow-installer` catalog app deploys the same chart: never combine it with the flag on one cell (`install` refuses without `--force`).
* Ingress error pages arrive with **HTTP 200**: plain text `Error 401…`, or JSON `{"error": true, "code": 401, …}` when the request accepts JSON. Unauthenticated app paths answer 302 to `/oauth2/start`. `novaflow.Client` maps all three to `ErrUnauthorized` and doesn't follow redirects; never decode an answer without that check (a 401 page once decoded as "all flags false").
* Action services: `nova flow action create <name> [--action Name[:kind]]` (`pkg/cmd/flowaction.go`; deliberately not a `nova app create` generator for now) runs `uvx copier copy --trust --quiet` on `gh:wandelbotsgmbh/novaflow-action-template` (private for now: access is checked with `git ls-remote` first, copier would print a traceback). Interactive only with a terminal and no `--action`. Projects keep the copier link (`uvx copier update --trust`). The `.env` layout (`appenv.novaflowActionLayout`) writes `NOVA_API` **without** `/api` (the SDK appends `/api/v2`; the template's `.env.template` is wrong there), `CELL_NAME`, `NATS_BROKER=ws(s)://<host>/api/nats` and no token. Running locally only works against LAN/IPC instances (cloud rejects external NATS). Stopped services stay in the registry with `online_instances: 0`. Conditions are plain actions with a bool `result` tagged `condition` (the registry reports them as `operation_type: action`). Deploy = plain `nova app install .` (needs `uv.lock` from `uv sync`; the NOVA app operator injects `NATS_BROKER`/`NOVA_API`/`CELL_NAME`). The template's `.dockerignore` doesn't exclude `.env`, so it lands in the image (harmless here: no token, injected env wins).
* Backups (`nova flow backup`): `manifest.json`, `globals.json`, `flows/<id>.json`, optional `objects/<key>`. Restore order globals → flows → objects; existing items are kept unless `--overwrite`. The object store is the cell's NOVA store, shared with other apps. **Forest persists flows in it as `trees/<id>`** (its source of truth): object backup/restore skips that prefix, flows only go through Forest's API (caches, change events).

## Nucleus / Omniverse

* Protocol docs, reference JS bundles and how to fetch fresh ones: internal repo `wb-int/nucleus-api` (private, not part of this repo)
* Live test server: `https://nucleus.dev.wandelbots.io`
* Key gotcha: most connlib commands use **nested path objects** `{"path": {"path": "...", "branch": "..."}}`, not flat strings. See `protocol.md` in `wb-int/nucleus-api` for the exact schema per command.
* `create_directory` requires trailing `/` on paths or returns `INVALID_URI`
* `rename2` is the only file op that returns a stream; others return single response
* The ingress in front of LFT may replace 5xx/404 from the service with a plain-text error page served as **HTTP 200** (ingress-nginx `custom-http-errors` with an error-pages backend that defaults to 200). Never trust the status code of an LFT download alone — `ReadFile` verifies the body against the `size`/`hash_value` from the `read` response and retries (issue #41).
* The API WebSocket can die mid-session (laptop sleep, network change, proxy idle timeout). `connlib` reports that as `ErrClosed`; `List`/`ReadFile` re-dial and re-authenticate automatically (`Client.Reconnects()`), mutating ops surface the error instead.
* Request ids must reach the server in increasing order per connection, or it answers `INVALID_COMMAND` (same status as an unknown command). `Client.withCmd` allocates the id and sends under one lock; keep it that way when adding commands.
* The LFT service of a typical deployment is small: more than ~2–4 parallel large downloads push it into 5xx. Keep `-j` modest and don't load-test against a shared server.
* `hash_type: sha-256-flat` is **not** a plain SHA-256: split the file into `hash_bsize` blocks (1 MiB), SHA-256 each, then SHA-256 the concatenated raw digests (`nucleus.FlatHash`). A one-block file hashes to sha256(sha256(data)). Verified live; `protocol.md` in `wb-int/nucleus-api` doesn't document this yet.

## Simulation streaming (`nova simulation`, Wandelbots internal)

* Manages GPU streaming sessions (Isaac Sim, Blender) on the streaming session broker (dev: `https://broker.omni.dev.wandelbots.io`, viewer: `https://viewer.omni.dev.wandelbots.io/omniverse-stream`).
* The broker only accepts tokens of the viewer's **Microsoft Entra ID** app, not portal/Auth0 tokens from `nova login` (even though its spec says "NOVA access token"). `nova simulation login` runs the Entra device flow (public client, no secret) and stores the token under `simulation-token`, separate from the portal `token`.
* The same token authorizes the APIs inside a session: `https://<route host>:<kit port, default 8080>/wandelbots-stream/api/v1` and `/omniservice/api/v2`, each with an `openapi.json` (no hosted docs page; the viewer renders them itself).
* Code: `pkg/simulation` (client generated from the broker's `openapi.json`; `overlay.yaml` adds `type: string` to `LeaseStatus`, which the broker emits as `oneOf` + `const` without a type), commands in `pkg/cmd/simulation.go`. Only dev is configured in `environments`.

## Repository

* Go module: `github.com/wandelbotsgmbh/nova-cli` (public repo)
* Merges to `main` publish `x.y.z-rc.N` prereleases (semantic-release); stable versions (Homebrew, winget, docker `latest`, docs) come from fast-forwarding the `release` branch via the Promote workflow. Use Conventional Commit messages / PR titles (`fix:`, `feat:`, `docs:`, `ci:`, …) — they decide the version bump. See `RELEASING.md`.
