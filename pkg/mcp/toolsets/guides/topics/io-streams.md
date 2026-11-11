# I/O streams

Continuous values of a controller's inputs/outputs. Operation: `streamIOValues` (WebSocket, server
to client only).

    wss://<host>/api/v2/cells/<cell>/controllers/<controller>/ios/stream?ios=<id>&ios=<id>

- Find I/O identifiers and their `value_type` with `listIODescriptions`
  (`GET .../controllers/<controller>/ios/description`).
- One snapshot instead of a stream: `listIOValues` (`GET .../ios/values?ios=...`).
- Values are sent at the controller's update rate so nothing is lost; that is a lot of data. Request
  only the I/Os you need: too many at once makes the request fail (the limit depends on the
  controller).

## Messages

`StreamIOValuesResponse`: `io_values` (list of `{io, value, value_type}` with `value_type`
`boolean`, `integer` (value as a string) or `float`), `timestamp`, `sequence_number`.

## Example (Python, plain WebSocket)

```python
import asyncio, json, os, websockets

URL = "wss://HOST/api/v2/cells/cell/controllers/ur5e/ios/stream?ios=tool_out[0]&ios=tool_in[0]"
HEADERS = {"Authorization": f"Bearer {os.environ['NOVA_TOKEN']}"}

async def main():
    async with websockets.connect(URL, additional_headers=HEADERS) as ws:
        async for message in ws:
            for value in json.loads(message)["io_values"]:
                print(value["io"], value["value"])

asyncio.run(main())
```

Writing outputs is a REST call (`setOutputValues`, `PUT .../ios/values`); on physical controllers
it switches real hardware.
