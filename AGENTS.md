# Agent instructions

## Building & testing

* On mac, build with `./build.sh` — produces `./bin/nova`
* After building, use `nova` in terminal for testing
* Use `nova config view` for getting configured values (host, nucleus-token, etc.)
* Do not rebuild the docs - this is a manual process for the user

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

## Repository

* Go module: `github.com/wandelbotsgmbh/nova-cli` (public repo)
* Releases are automatic on merge to `main` (semantic-release): use Conventional Commit messages / PR titles (`fix:`, `feat:`, `docs:`, `ci:`, …) — they decide the version bump. See `CONTRIBUTING.md`.
