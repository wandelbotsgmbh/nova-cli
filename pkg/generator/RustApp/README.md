# App Name

A NOVA app written in Rust ([axum](https://github.com/tokio-rs/axum) web server).

- `src/api.rs`: the HTTP routes. The OpenAPI spec (`/openapi.json`) is generated from the `#[utoipa::path]` annotations
  and rendered as API docs on `/`.
- `src/nova.rs`: a minimal client for the parts of the [NOVA API](https://docs.wandelbots.io) the app uses. Extend it as needed.
- `src/env.rs`: configuration from the environment variables NOVA sets for the app.

## Development

- run locally with `cargo run`, or press `F5` in vscode to run/debug (needs the CodeLLDB extension)
    - make sure the following env vars are properly set in your environment or `.vscode/launch.json`:
        - `BASE_PATH` (e.g. `/cell/my-app`, the app is then served on `http://localhost:8080/cell/my-app`)
        - `NOVA_API` (e.g. `https://xdxqzvpu.instance.wandelbots.io`, `http://` is assumed if no scheme is given)
        - `CELL_NAME` (defaults to `cell`)
        - `ACCESS_TOKEN` (only needed for cloud instances, e.g. `ACCESS_TOKEN=$(nova login token) cargo run`; tokens expire, so refresh it when you get `Error 401: Unauthorized`)
        - `LOG_LEVEL` (optional: `debug`, `info`, `warn`, `error`)
    - ATTENTION: make sure not to leak the `ACCESS_TOKEN` in any git repository
- run the tests with `cargo test`
- build docker and deploy with `nova app install`

## NOVA App Store

You can open a PR in https://github.com/wandelbotsgmbh/catalog to integrate your application into the NOVA app store.
