# App Name

## Development

- (re-)generate api stubs with `go generate ./...`
- `nova app env` writes the settings for your configured instance (`nova config view`) into the gitignored `.env.local`:
  `NOVA_API`, `CELL_NAME`, `BASE_PATH` and, for portal instances, a fresh `ACCESS_TOKEN`.
  Run it again when the token expired (the instance answers `Error 401: Unauthorized`) or after switching the instance.
- run locally with `go run .` (served on `http://localhost:8080` + `BASE_PATH`), or press `F5` in vscode to run/debug
    - the app loads `.env.local` on startup; variables set in your environment take precedence
    - ATTENTION: `.env.local` contains the access token, keep it out of git
- build docker and deploy with `nova app install`

## NOVA App Store

You can open a PR in https://github.com/wandelbotsgmbh/catalog to integrate your application into the NOVA app store.
