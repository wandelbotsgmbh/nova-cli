# Jogging

Move a motion group with live velocity commands (joints or TCP). The target changes at any time, so
nothing is planned upfront. Operation: `executeJogging` (WebSocket).

    wss://<host>/api/v2/cells/<cell>/controllers/<controller>/execution/jogging

Precondition: no other endpoint moves the motion group.

## Message flow

1. Send `InitializeJoggingRequest`: `motion_group` (e.g. `0@ur5e`) and, for robot arms, `tcp` (so TCP
   limits are respected). Sets the controller to control mode and claims the motion group.
   Answer: `{"kind": "INITIALIZE_RECEIVED"}` (a `message` field means the request was invalid).
2. Send `JointVelocityRequest` (`velocity`: one value per joint in rad/s) or `TcpVelocityRequest`
   (`translation` and `rotation`, each [x, y, z]; world coordinates unless
   `use_tool_coordinate_system` is true). Answers: `JOINT_VELOCITY_RECEIVED` / `TCP_VELOCITY_RECEIVED`.
3. Change velocities by sending further velocity requests. Stop with zero velocities or
   `PauseJoggingRequest` (answer `PAUSE_RECEIVED`).

- Answers only confirm receipt. Whether the robot moves shows in the motion group state stream
  (guide `state-streams`).
- `{"kind": "MOTION_ERROR", "message": ...}` reports errors such as a controller disconnect.
- Commands are processed at the controller's cycle rate; sending faster drops commands. Pace them
  with the state stream.
- Keep the connection open until the motion is stopped; closing it stops the motion.

## Example (Python, plain WebSocket)

```python
import asyncio, json, os, websockets

URL = "wss://HOST/api/v2/cells/cell/controllers/ur5e/execution/jogging"
HEADERS = {"Authorization": f"Bearer {os.environ['NOVA_TOKEN']}"}

async def main():
    async with websockets.connect(URL, additional_headers=HEADERS) as ws:
        await ws.send(json.dumps({"message_type": "InitializeJoggingRequest",
                                  "motion_group": "0@ur5e", "tcp": "Flange"}))
        print(await ws.recv())
        # rotate joint 1 at 0.1 rad/s for 2 s
        await ws.send(json.dumps({"message_type": "JointVelocityRequest",
                                  "velocity": [0.1, 0, 0, 0, 0, 0]}))
        print(await ws.recv())
        await asyncio.sleep(2)
        await ws.send(json.dumps({"message_type": "PauseJoggingRequest"}))
        print(await ws.recv())

asyncio.run(main())
```
