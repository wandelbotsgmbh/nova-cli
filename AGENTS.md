# Agent instructions

## Building & testing

* On mac, build with `./build.sh` — produces `./bin/nova`
* After building, use `nova` in terminal for testing
* Use `nova config view` for getting configured values (host, nucleus-token, etc.)
* Do not rebuild the docs - this is a manual process for the user
* CLI reference site (GitHub Pages, deployed by the release workflow): `hack/docsite/build.sh [version]` → `_site/`. Regenerates markdown into a temp dir (never touches `docs/`) and renders it with `hack/docsite` (goldmark + template styled with `@wandelbots/design-tokens` / `@wandelbots/fonts`, vendored from npm at build time — bump the pinned versions in `build.sh`).

## App install: `--copy-image` (registry-less)

* `nova app install <app> --copy-image --ssh-user <user>` builds locally, copies the image to the configured `host` over ssh/scp, and imports it into the instance's k3s containerd — no registry push. Works with docker and podman (auto-detected).
* Only layers missing from the target's containerd content store are transferred; on any error it falls back to whole-image transfer.
* `--setup-ssh-key` (one-time per instance): installs the local ssh key + a k3s-scoped NOPASSWD rule in `/etc/sudoers.d/nova-cli`. The single sudo prompt during setup is the unavoidable bootstrap (needs sudo once to grant passwordless sudo); installs afterwards are prompt-free.
* Target needs sudo for `ctr` (containerd socket is root-only); k3s is invoked by absolute path so the NOPASSWD rule matches. Use `--no-sudo` for root logins.
* Code: `pkg/cmd/app.go` (`installAppViaCopy`/`copyIncremental`/`copyWholeImage`), `pkg/remote/` (ssh/scp + `setup.go`), `pkg/docker/` (`BuildImageOCI`, `save.go`, `ocilayout.go`).

## Nucleus / Omniverse

* Protocol docs, reference JS bundles and how to fetch fresh ones: internal repo `wb-int/nucleus-api` (private, not part of this repo)
* Live test server: `https://nucleus.dev.wandelbots.io`
* Key gotcha: most connlib commands use **nested path objects** `{"path": {"path": "...", "branch": "..."}}`, not flat strings. See `protocol.md` in `wb-int/nucleus-api` for the exact schema per command.
* `create_directory` requires trailing `/` on paths or returns `INVALID_URI`
* `rename2` is the only file op that returns a stream; others return single response
* The ingress in front of LFT (`nucleus-lft`, ingress-nginx with `custom-http-errors` + `tarampampam/error-pages`, see `wb-int/evergreen`) replaces 5xx/404 from the service with a plain-text error page served as **HTTP 200**. Never trust the status code of an LFT download alone — `ReadFile` verifies the body against the `size`/`hash_value` from the `read` response and retries (issue #41).
* The server drops the API WebSocket after ~30 min of sustained downloads. `connlib` reports that as `ErrClosed`; `List`/`ReadFile` re-dial and re-authenticate automatically (`Client.Reconnects()`), mutating ops surface the error instead.
* `hash_type: sha-256-flat` is **not** a plain SHA-256: split the file into `hash_bsize` blocks (1 MiB), SHA-256 each, then SHA-256 the concatenated raw digests (`nucleus.FlatHash`). A one-block file hashes to sha256(sha256(data)). Verified live; `protocol.md` in `wb-int/nucleus-api` doesn't document this yet.

## Repository

* Go module: `github.com/wandelbotsgmbh/nova-cli` (public repo)
* Merges to `main` publish `x.y.z-rc.N` prereleases (semantic-release); stable versions (Homebrew, winget, docker `latest`, docs) come from fast-forwarding the `release` branch via the Promote workflow. Use Conventional Commit messages / PR titles (`fix:`, `feat:`, `docs:`, `ci:`, …) — they decide the version bump. See `RELEASING.md`.
