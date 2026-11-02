# App Name

A NOVA app written in Rust ([axum](https://github.com/tokio-rs/axum) web server).

- `src/api.rs`: the HTTP routes. The OpenAPI spec (`/openapi.json`) is generated from the `#[utoipa::path]` annotations
  and rendered as API docs on `/`.
- `src/nova.rs`: a minimal client for the parts of the [NOVA API](https://docs.wandelbots.io) the app uses. Extend it as needed.
- `src/env.rs`: configuration from the environment variables NOVA sets for the app.

## Development

- `nova app update-env` writes the settings for your configured instance (`nova config view`) into the gitignored `.env.local`:
  `NOVA_API`, `CELL_NAME`, `BASE_PATH` and, for portal instances, a fresh `ACCESS_TOKEN`.
  Run it again when the token expired (the instance answers `Error 401: Unauthorized`) or after switching the instance.
- run locally with `cargo run` (served on `http://localhost:8080` + `BASE_PATH`), or press `F5` in vscode to run/debug
  (needs the CodeLLDB extension)
    - the app loads `.env.local` on startup; variables set in your environment take precedence
    - `LOG_LEVEL` (`debug`, `info`, `warn`, `error`) is set to `debug` there, change it as you like
    - ATTENTION: `.env.local` contains the access token, keep it out of git
- run the tests with `cargo test`
- build docker and deploy with `nova app install`

## NOVA App Store

You can open a PR in https://github.com/wandelbotsgmbh/catalog to integrate your application into the NOVA app store.
