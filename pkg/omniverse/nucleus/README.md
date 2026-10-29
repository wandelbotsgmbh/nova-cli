# nucleus — Pure-Go client for NVIDIA Omniverse Nucleus

A cross-platform (macOS / Linux / Windows) Go client that talks directly to
Nucleus servers over WebSocket — **no C++ library required**.

## Protocol

The full wire protocol documentation (reverse-engineered from the Navigator JS
bundles) lives in the internal repo
[wb-int/nucleus-api](https://github.com/wb-int/nucleus-api) (Wandelbots access only).

The client implements two wire protocols:

| Service     | Protocol           | Transport | Port |
| ----------- | ------------------ | --------- | ---- |
| Auth        | **SOWS** (binary)  | WebSocket | 3100 |
| Content API | **connlib** (JSON) | WebSocket | 3019 |

### Key protocol details

- Most commands use **nested path objects** `{"path": {"path": "/...", "branch": "..."}}`,
  not flat strings.
- `create_directory` requires a trailing `/` on the path or returns `INVALID_URI`.
- `rename2` returns a stream; all other write ops return a single response.
- `create` and `read` use flat `"uri"` field with binary payloads after a `\0` separator.

## Usage

```go
client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
    Host:     "nucleus.example.com",
    APIToken: os.Getenv("NUCLEUS_TOKEN"),
    TLS:      true,
})
if err != nil {
    log.Fatal(err)
}
defer client.Close()

entries, err := client.List(ctx, "/Projects/")
```

## API

| Method                                | Description              |
| ------------------------------------- | ------------------------ |
| `Dial(ctx, config)`                   | Connect & authenticate   |
| `client.List(ctx, path)`              | List directory entries   |
| `client.Stat(ctx, path)`              | Get entry metadata       |
| `client.CreateFolder(ctx, path)`      | Create a directory       |
| `client.WriteFile(ctx, path, data)`   | Upload file content      |
| `client.ReadFile(ctx, path)`          | Download file content    |
| `client.Delete(ctx, path)`            | Remove file or directory |
| `client.Copy(ctx, src, dst)`          | Copy file/directory      |
| `client.Move(ctx, src, dst)`          | Move/rename              |
| `client.Close()`                      | Disconnect               |

## CLI

The `nova nucleus` command exposes these operations:

```bash
nova nucleus ls /Projects/          # list directory
nova nucleus ls -l /Projects/       # detailed listing
nova nucleus stat /Projects/f.usd   # file metadata
nova nucleus mkdir /Projects/new/   # create directory
nova nucleus rm /Projects/old/      # delete
nova nucleus mv n:/a n:/b           # move/rename (n: prefix for nucleus paths)
nova nucleus cp ./f.usd n:/P/f.usd  # upload local file
nova nucleus cp n:/P/f.usd ./f.usd  # download file
nova nucleus cp n:/a n:/b           # server-side copy
```

## Authentication

Obtain an API token from Nucleus Navigator:
**Account menu → API Tokens → Generate**

Configure via:
- `nova config set nucleus-host nucleus.example.com`
- `nova config set nucleus-token <token>` or `export NUCLEUS_TOKEN=<token>`

## Package structure

```
nucleus/
├── client.go          # Dial, auth, connection management
├── operations.go      # List, Stat, CreateFolder, Delete, Copy, Move, Read/WriteFile
├── types.go           # Entry, ConnectionInfo, ClientConfig
├── doc.go             # Package documentation
├── connlib/           # connlib JSON-over-WebSocket protocol
├── sows/              # SOWS binary protocol
└── fakeserver/        # In-memory test server
```
