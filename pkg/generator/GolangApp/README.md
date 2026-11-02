# App Name

## Development

- (re-)generate api stubs with `go generate ./...`
- in vscode press `F5` to run/debug localy
    - make sure following env vars are properly set in your environment or `.vscode/launch.json`:
        - `BASE_PATH`
        - `NOVA_API`
        - `CELL_NAME`
        - `ACCESS_TOKEN` (only needed for cloud instances, e.g. `ACCESS_TOKEN=$(nova login token) go run .`; tokens expire, so refresh it when you get `Error 401: Unauthorized`)
    - ATTENTION: make sure not to leak the `ACCESS_TOKEN` in any git repository
- build docker and deploy with `nova app install`

## NOVA App Store

You can open a PR in https://github.com/wandelbotsgmbh/catalog to integrate your application into the NOVA app store.
