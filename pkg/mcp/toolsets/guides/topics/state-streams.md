# State streams

Continuous state of a motion group or a whole controller (WebSocket, server to client only).

| Operation | Path |
|---|---|
| `streamMotionGroupState` | `/cells/<cell>/controllers/<controller>/motion-groups/<motion-group>/state-stream` |
| `streamRobotControllerState` | `/cells/<cell>/controllers/<controller>/state-stream` |

For a single snapshot use the REST twins `getCurrentMotionGroupState`
(`.../motion-groups/<motion-group>/state`) and `getCurrentRobotControllerState` (`.../state`).

## Query parameters

- `response_rate`: update interval in ms. Without it, the stream runs at the controller's step rate,
  which is a lot of data; 100–200 ms is plenty for monitoring.
- `response_coordinate_system` (motion group only): coordinate system for cartesian values, default
  world.
- `add_controller_timeout` (controller only): ms to wait for a controller that is being added.

## Messages

- `MotionGroupState`: `joint_position`, `joint_limit_reached`, `standstill`, `flange_pose`,
  `tcp_pose` (mm, rad), `tcp`, `coordinate_system`, optional `joint_torque` / `joint_current`, and
  `execute` with the last command NOVA sent (`details.kind` `TRAJECTORY` or `JOGGING`).
- `RobotControllerState`: `mode` (`MODE_MONITOR`, `MODE_CONTROL` while NOVA moves it, `MODE_FREE_DRIVE`, …),
  `operation_mode`, `safety_state`, `velocity_override`, `last_error`, `motion_groups` (one
  `MotionGroupState` each).
- The controller stream can be opened before the controller exists; it sends initialization updates
  until the controller reaches `MODE_MONITOR`. The server closes motion group streams when the
  controller disconnects; reconnect with backoff.

## Example (Python, plain WebSocket)

```python
import asyncio, json, os, websockets

URL = ("wss://HOST/api/v2/cells/cell/controllers/ur5e/motion-groups/0@ur5e/state-stream"
       "?response_rate=200")
HEADERS = {"Authorization": f"Bearer {os.environ['NOVA_TOKEN']}"}

async def main():
    async with websockets.connect(URL, additional_headers=HEADERS) as ws:
        async for message in ws:
            state = json.loads(message)
            print(state["joint_position"], "standstill" if state["standstill"] else "moving")

asyncio.run(main())
```
