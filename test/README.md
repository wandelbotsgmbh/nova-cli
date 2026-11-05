# App Name

## Development

- (re-)generate api stubs with `go generate ./...`
- the gitignored `.env.local` holds the settings for local runs: `NOVA_API`, `CELL_NAME`, `BASE_PATH` and, for portal
  instances, `ACCESS_TOKEN`. `nova app create` filled it for the instance configured then (`nova config view`).
    - token expired (portal tokens last 24h, the app answers with `Error 401: Unauthorized`): `nova app update-env`
      refreshes `ACCESS_TOKEN` and keeps your other values
    - switched the instance or cell (`nova config set host|cellname`): `nova app update-env --reset`
- run locally with `go run .` (served on `http://localhost:8080` + `BASE_PATH`), or press `F5` in vscode to run/debug
    - the app loads `.env.local` on startup; variables set in your environment take precedence
    - ATTENTION: `.env.local` contains the access token, keep it out of git
- build docker and deploy with `nova app install`

## NOVA App Store

You can open a PR in https://github.com/wandelbotsgmbh/catalog to integrate your application into the NOVA app store.
