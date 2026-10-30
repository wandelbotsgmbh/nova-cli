# Releasing

The Nova CLI is released in two channels:

| Channel | Comes from | Version | Published to |
|---|---|---|---|
| **Release candidate** | every merge to `main` | `x.y.z-rc.N` | GitHub *prerelease* (all archives), `ghcr.io/wandelbotsgmbh/nova-cli:x.y.z-rc.N` |
| **Stable** | promoting a verified rc to the `release` branch | `x.y.z` | GitHub release (`latest`), Homebrew tap, winget, `ghcr.io/wandelbotsgmbh/nova-cli:{x.y.z,latest}`, CLI reference on GitHub Pages |

GitHub's `releases/latest` skips prereleases. Everything that resolves "latest"
therefore only ever gets a stable, verified version: `install_nova.sh`, CI
pipelines that download the latest release, Homebrew and winget.

## Versions

[semantic-release](https://semantic-release.gitbook.io/) derives the version
from the [Conventional Commits](https://www.conventionalcommits.org/) since the
last stable release. PRs are squash-merged, so the **PR title** decides:

| PR title | Bump |
|---|---|
| `fix: …` | patch (`0.0.x`) |
| `feat: …` | minor (`0.x.0`) |
| `feat!: …` / `BREAKING CHANGE:` | major |
| `docs:`, `ci:`, `chore:`, `refactor:`, `test:` | none, so no rc either |

Example: `0.1.0` is the latest stable release. A merged `fix:` gives
`0.1.1-rc.1`. A following `feat:` gives `0.2.0-rc.1`, and the next merge gives
`0.2.0-rc.2`. Promoting `0.2.0-rc.2` publishes `0.2.0` with the release notes of
everything since `0.1.0`.

## 1. Release candidate (automatic)

Merging a release-worthy PR into `main` runs the *Release* workflow
(`.github/workflows/release.yml`):

1. semantic-release creates the tag `x.y.z-rc.N` and a **draft** GitHub release
   with the release notes.
2. GoReleaser builds the archives, attaches them to the draft and publishes it
   as a prerelease. A release is only visible once its archives are there.
3. The docker image `ghcr.io/wandelbotsgmbh/nova-cli:x.y.z-rc.N` is pushed. It
   is not tagged `latest`.

## 2. Verify the rc

Install the rc explicitly:

```bash
curl -fsSL https://raw.githubusercontent.com/wandelbotsgmbh/nova-cli/main/install_nova.sh | NOVA_VERSION=0.2.0-rc.2 bash
# or download it from https://github.com/wandelbotsgmbh/nova-cli/releases
# or docker run ghcr.io/wandelbotsgmbh/nova-cli:0.2.0-rc.2
```

Test it against a real instance. If something is broken, merge a fix to `main`;
that produces the next rc.

## 3. Promote to stable

Run **Actions → Promote → Run workflow** with the verified rc tag (e.g.
`0.2.0-rc.2`) as `ref`. The *Promote* workflow (`.github/workflows/promote.yml`):

1. checks that the commit is on `main` and that `release` can be fast-forwarded
   to it,
2. pushes the commit to the `release` branch,
3. dispatches the *Release* workflow on `release`. That cuts `x.y.z` from exactly
   the verified commit and publishes the GitHub release, Homebrew formula,
   winget update, docker `:latest` and the CLI reference.

By hand, this is the same as:

```bash
git fetch origin
git push origin 0.2.0-rc.2^{commit}:refs/heads/release
```

`main` can move on in the meantime. Only the promoted commit is released.

## Rules

* Never commit to `release` directly. It only moves by fast-forwarding to a
  commit on `main`.
* Only promote rc versions that were actually verified.
* Do not delete rc tags. semantic-release uses them to number the next rc.

## Repository settings

Publishing is gated by repository variables (Settings → Secrets and variables
→ Actions → Variables). While a variable isn't `true`, the step runs as a dry
run:

| Variable | Controls |
|---|---|
| `RELEASE_ENABLED` | tags, GitHub releases, archives (both channels) |
| `HOMEBREW_ENABLED` | push to `wandelbotsgmbh/homebrew-wandelbots` (stable only) |
| `WINGET_ENABLED` | winget update (stable only) |
| `DOCKER_PUSH_ENABLED` | real docker tags (`latest` for stable only); otherwise only `:<ver>-snapshot` |
| `DOCS_ENABLED` | CLI reference deploy to GitHub Pages (stable only) |

If the `release` branch is protected, allow GitHub Actions to push to it.
Otherwise only the manual `git push` way of promoting works.

## Dry run

Actions → Release → Run workflow (any branch, `dry_run` checked) shows the
version that would be released and builds snapshot archives, without tagging or
publishing anything.
