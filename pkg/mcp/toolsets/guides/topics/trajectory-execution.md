# Trajectory execution

Execute a planned trajectory, with optional I/O actions. Operation: `executeTrajectory` (WebSocket).

    wss://<host>/api/v2/cells/<cell>/controllers/<controller>/execution/trajectory

Virtual controllers apply the commanded joint positions immediately; physical controllers move to
them if possible.

## Before you start

1. Plan with `planTrajectory` (`POST /cells/<cell>/trajectory-planning/plan-trajectory`). On success
   `response` is a `JointTrajectory` (`joint_positions`, `times`, `locations`); otherwise a failure
   with the reason.
2. Optionally store it with `addTrajectory`
   (`POST /cells/<cell>/controllers/<controller>/trajectories`, body `motion_group`, `trajectory`,
   optional `tcp`). Check `error` in the answer first; `trajectory` is the id.
3. The motion group must stand at the trajectory's start (`initial_location`, default 0) and not be
   claimed by another endpoint.

Location: a number from 0 to n along the trajectory, n = number of motion commands; 3.0 to 3.999
is the fourth command.

## Message flow

1. `InitializeMovementRequest` with `trajectory`, either
   `{"message_type": "TrajectoryId", "id": "<id from addTrajectory>"}` or
   `{"message_type": "TrajectoryData", "motion_group": "0@ur5e", "data": <JointTrajectory>, "tcp": ...}`.
   Optional `initial_location`. Locks the trajectory to this connection and sets control mode.
   Answer `INITIALIZE_RECEIVED` (`message` / `add_trajectory_error` on failure).
2. Optional `PlaybackSpeedRequest` (`playback_speed_in_percent`) to slow down; repeat any time.
   Answer `PLAYBACK_SPEED_RECEIVED`.
3. `StartMovementRequest`, optional `direction` (`DIRECTION_FORWARD` default, `DIRECTION_BACKWARD`),
   `target_location`, `set_outputs`, `start_on_io`, `pause_on_io`. Answer `START_RECEIVED`.
4. `PauseMovementRequest` pauses (answer `PAUSE_RECEIVED`, not a confirmation of standstill); a new
   `StartMovementRequest` resumes.

Answers acknowledge requests. Progress and the end of the movement show in the motion group state
stream (guide `state-streams`): `execute.details` with `kind: "TRAJECTORY"` carries `location` and
`state.kind`: `RUNNING` (with `time_to_end` in ms), `PAUSED_BY_USER`, `END_OF_TRAJECTORY`,
`WAIT_FOR_IO`, `PAUSED_ON_IO`. `{"kind": "MOTION_ERROR"}` reports errors during the movement.

## Example (Python, plain WebSocket)

```python
import asyncio, json, os, websockets

BASE = "wss://HOST/api/v2/cells/cell/controllers/ur5e"
HEADERS = {"Authorization": f"Bearer {os.environ['NOVA_TOKEN']}"}

async def execute(trajectory_id):
    async with websockets.connect(f"{BASE}/execution/trajectory", additional_headers=HEADERS) as ws, \
               websockets.connect(f"{BASE}/motion-groups/0@ur5e/state-stream?response_rate=100",
                                  additional_headers=HEADERS) as state:
        await ws.send(json.dumps({"message_type": "InitializeMovementRequest",
                                  "trajectory": {"message_type": "TrajectoryId", "id": trajectory_id}}))
        print(await ws.recv())
        await ws.send(json.dumps({"message_type": "PlaybackSpeedRequest", "playback_speed_in_percent": 50}))
        print(await ws.recv())
        await ws.send(json.dumps({"message_type": "StartMovementRequest"}))
        print(await ws.recv())
        async for message in state:
            details = (json.loads(message).get("execute") or {}).get("details") or {}
            if details.get("kind") == "TRAJECTORY" and details["state"]["kind"] == "END_OF_TRAJECTORY":
                break
```
