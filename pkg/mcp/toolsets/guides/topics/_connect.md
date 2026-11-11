## Connecting from a script

- WebSocket endpoints use the REST path with `ws://` / `wss://` instead of `http://` / `https://`:
  `wss://<host>/api/v2/cells/<cell>/...`.
- Cloud instances (`*.wandelbots.io`) need `Authorization: Bearer <token>`; get the token with
  `nova login token`. Instances on the local network usually need no token.
- Messages are JSON text frames. Requests carry `message_type`, responses `kind`.
- Physical controllers move for real. Run scripts against them only when the user asked for it and
  is at the cell; prefer a virtual controller for development.
- Python: `pip install websockets` (v14+: `additional_headers=`, older versions: `extra_headers=`).
  The `wandelbots-nova` Python SDK wraps these endpoints; check its docs for the matching calls.
- Exact message schemas: `describe_api_operation` with the operation id named below.
