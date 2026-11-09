# Visualisation

Show a robot, a planned trajectory or the live state on the developer's machine. The MCP tools
return ids and summary numbers, never geometry; render it from a script.

## What the API offers

- Live joint positions and poses: the state streams (guide `state-streams`).
- A planned trajectory: `joint_positions`, `times` and `locations` of the `JointTrajectory` from
  `planTrajectory`, or a stored one via `getTrajectory`
  (`GET /cells/<cell>/controllers/<controller>/trajectories/<trajectory>`).
- Robot models: `getMotionGroupGlbModel` (`GET /motion-group-models/<model>/glb`, binary glTF) and
  `getMotionGroupKinematicModel` (`.../kinematic`); collision geometry via
  `getMotionGroupCollisionModel`.
- Cartesian poses for joint positions: `forwardKinematics`
  (`POST /cells/<cell>/kinematic/forward`).
- Planner debug output: `configureRerunLogging`
  (`PUT /experimental/cells/<cell>/trajectory-planning/rerun-logging`, body `{"enabled": true}`) turns
  on Rerun logging of new planning operations. Experimental, process-wide, and it slows down every
  planning request: switch it off again (`{"enabled": false}`).

## Rerun from Python

[Rerun](https://rerun.io) (`pip install rerun-sdk`) is a quick way to plot joint values over time:

```python
import rerun as rr

def show_trajectory(trajectory):  # JointTrajectory from planTrajectory
    rr.init("nova-trajectory", spawn=True)
    for t, joints in zip(trajectory["times"], trajectory["joint_positions"]):
        rr.set_time_seconds("time", t)
        for i, value in enumerate(joints):
            rr.log(f"joints/{i}", rr.Scalar(value))
```

Function names differ between `rerun-sdk` versions (`set_time_seconds` / `set_time`, `Scalar` /
`Scalars`); check the installed version. The `wandelbots-nova` Python SDK has its own viewer
integration; prefer it when the project uses that SDK.

Show the user the rendering of a planned motion before it runs on a physical robot.
