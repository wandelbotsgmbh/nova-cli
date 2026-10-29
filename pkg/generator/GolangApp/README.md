# App Name

## Development

- (re-)generate api stubs with `go generate ./...`
- in vscode press `F5` to run/debug localy
    - make sure following env vars are properly set in your environment or `.vscode/launch.json`:
        - `BASE_PATH`
        - `NOVA_API`
        - `CELL`
        - `ACCESS_TOKEN`
    - ATTENTION: make sure not to leak the `ACCESS_TOKEN` in any git repository
- build docker and deploy with `nova app install`

## NOVA App Store

You can open a PR in https://github.com/wandelbotsgmbh/catalog to integrate your application into the NOVA app store.
