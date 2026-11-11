# Free drive

Put the controller into free drive mode so a person can move the robot by hand. Operation:
`streamFreeDrive` (WebSocket).

    wss://<host>/api/v2/cells/<cell>/controllers/<controller>/free-drive-stream?response_rate=200

- Free drive lasts exactly as long as the connection is open; closing it ends free drive.
- While open, no other endpoint can control or move the robot.
- Only controllers that support it (in particular cobots); check with `listRobotControllers` /
  `getRobotController`.
- The stream sends `RobotControllerState` at `response_rate` (ms, default 200). If activation fails,
  the state tells why.

**Danger (from the API description):** before opening the connection make sure the robot is in a
safe state, the right payload is set, and no person or object is within the robot's reach. Free
drive is for a human at the robot; an agent never opens it on its own.

## Example (Python, plain WebSocket)

```python
import asyncio, json, os, websockets

URL = "wss://HOST/api/v2/cells/cell/controllers/ur5e/free-drive-stream?response_rate=500"
HEADERS = {"Authorization": f"Bearer {os.environ['NOVA_TOKEN']}"}

async def main(seconds=30):
    async with websockets.connect(URL, additional_headers=HEADERS) as ws:
        async def show():
            async for message in ws:
                state = json.loads(message)
                print(state["mode"], [mg["joint_position"] for mg in state["motion_groups"]])
        try:
            await asyncio.wait_for(show(), timeout=seconds)
        except asyncio.TimeoutError:
            pass  # leaving the block closes the socket and ends free drive

asyncio.run(main())
```
