# Waiting for an I/O value

Block until an input/output meets a condition. Operation: `waitForIOEvent` (REST, long-running).

    POST /api/v2/cells/<cell>/controllers/<controller>/ios/wait-for

The call returns `true` as soon as the condition is met, or fails. Use a long client timeout (or
none) and cancel by closing the request.

## Body (`WaitForIOEventRequest`)

- `io`: the value to compare with, `{"io": "<id>", "value_type": "boolean" | "integer" | "float",
  "value": ...}` (integer values as strings). Use the `value_type` the I/O has in `listIODescriptions`.
- `comparator`: `COMPARATOR_EQUALS`, `COMPARATOR_NOT_EQUALS`, `COMPARATOR_GREATER`,
  `COMPARATOR_GREATER_EQUAL`, `COMPARATOR_LESS`, `COMPARATOR_LESS_EQUAL`. The measured value is the
  left side: "measured > value".

(The prose in the API description still shows older field names `comparison_type`; the schema
above is what the endpoint accepts.)

## Alternatives

- Inside a trajectory execution use `start_on_io` / `pause_on_io` of `StartMovementRequest`
  (guide `trajectory-execution`).
- To watch several values, use the I/O stream (guide `io-streams`).

## Example (Python, requests)

```python
import os, requests

url = "https://HOST/api/v2/cells/cell/controllers/ur5e/ios/wait-for"
headers = {"Authorization": f"Bearer {os.environ['NOVA_TOKEN']}"}
body = {"io": {"io": "tool_in[0]", "value_type": "boolean", "value": True},
        "comparator": "COMPARATOR_EQUALS"}
print(requests.post(url, json=body, headers=headers, timeout=None).json())  # True once met
```
