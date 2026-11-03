# ADR-0009: Guard rails; free motion on virtual robots, approved motion on real robots

- **Date:** 2026-10-31
- **Status:** accepted
- **Implementation:** not started

## Context

`nova api` (ADR-0013) and MCP tools (ADR-0008) can reach every endpoint of the instance.

- On the v2 API the endpoints that move the robot (`executeJogging`, `executeTrajectory`,
  `streamFreeDrive`, …) are GET requests that upgrade to WebSockets, so classifying by
  HTTP method would mark them read-only.
- **Virtual robots** can move freely: nothing physical can be hit, and fast iteration
  without a human in the loop is the point of simulation.
- **Real robots** must not make unexpected moves. Every motion needs to be checked by a
  human before it runs.
- **Software can't stop a determined agent.** Whatever credentials the agent's tools use,
  the agent can find and use directly (read the config, fetch `openapi.json`, call the
  API). Schemes like scoped tokens or cryptographically bound approvals add a lot of
  complexity and still end at the same credentials.
- An internal prototype runs agent-driven motion on a real robot with a planner-backed
  move tool, a 3D preview, and a human approval in the app's UI, not through MCP
  elicitation, because its own agent would be the client and could confirm by itself.

So the realistic goal for software is: **an agent that makes a mistake or follows injected
instructions never moves a real robot unexpectedly.** Protection against deliberate
misuse is physical (see below).

## Decision

### Motion depends on the target controller

| Target | Motion |
|---|---|
| **Virtual controller** | Allowed without approval, through `nova api` and MCP tools. |
| **Physical controller** | Refused by `nova api` and `nova mcp` with a typed error (`APPROVAL_REQUIRED`). Motion runs through an app or UI that shows a preview and lets a human approve it. |
| **Unknown** (lookup failed, new controller type) | Treated as physical. |

- The controller type is **looked up on the instance** before every motion or I/O write,
  never taken from the agent's arguments: `getRobotController`
  (`GET /cells/{cell}/controllers/{controller}`) returns `configuration.kind`, a
  discriminator that is `VirtualController` for virtual controllers and a vendor kind
  (`AbbController`, `FanucController`, `KukaController`, `UniversalrobotsController`,
  `YaskawaController`, …) for physical ones (checked against the v2 spec, API 2.8).
- **Only `kind == VirtualController` counts as virtual.** Every other kind, a kind added
  in a later NOVA version, or a failed lookup counts as physical. The check is an
  allow-list for virtual, not a deny-list of vendors.
- The motion endpoints take the controller (or a motion group on it) in their path or
  payload, so the CLI can resolve it before forwarding the request.
- **I/O writes on physical controllers** count as motion: an output can close a gripper
  or start a conveyor.

### How a motion on a real robot is approved

The pattern that prototype uses, kept simple:

1. **NOVA plans the motion** (collision checks, limits). The model decides targets; it
   never controls joints directly.
2. **A human sees a preview** of exactly that plan (3D view, start and target, duration,
   speed) and approves or rejects it in the app or NOVA UI. Not through a CLI flag, an
   MCP argument or a prompt the agent's own client answers.
3. **The approved plan is the one that runs.** If the robot moved since planning, plan
   again.
4. **Reduced speed by default** (e.g. 50 mm/s at start, at most 250 mm/s), not
   settable by the model.
5. **A stop that always works,** for the whole run.

### What protects against deliberate misuse

Not the CLI. Physical safety at the cell:

- The robot controller's safety system (safety-rated speed and zone limits).
- Operating mode, e.g. manual mode with reduced speed and an enabling device the human
  has to hold.
- A human present at the cell with the emergency stop in reach. Agents never drive a
  real robot unattended.
- Credentials for a real cell are not given to an agent working unattended. Agents
  develop against virtual controllers; moving to the real cell is a human step.

### Other guard rails

- **Deny-by-default classification for `nova api`:**
  1. An explicit **allow-list of reads**.
  2. An explicit **motion list** (handled by controller type as above).
  3. An explicit **list of severe writes** (operation mode, controller add/delete,
     configuration restore, NOVA version update).
  4. Everything else is an ordinary write.
  5. **CI fails** when an operation of the current spec is on none of the lists.
  Long-term preference: the platform team marks each operation in the spec
  (`x-nova-agent`: `read` / `write` / `severe` / `motion`), so the classification is
  versioned with the API.
- **`--read-only`** (flag, config or `NOVA_READ_ONLY=1`) blocks everything but the read
  list. Enabled by default in the kit.
- **Severe writes need `--confirm=<name>`**, a guard against mistakes only.
- **Untrusted content:** catalog entries (from arbitrary catalog URLs), logs and resource
  names come from outside; the skill and MCP instructions treat them as data.

## Alternatives

- **No motion through agents at all:** throws away fast iteration in simulation, where
  nothing can be hit.
- **Same rules for virtual and real:** either too slow for simulation or too loose for
  real robots.
- **Scoped tokens and approvals bound to a plan hash on the instance:** stronger on paper,
  but complex for the platform and still bypassable by an agent that can reach any
  credential with motion rights. Physical safety covers deliberate misuse more reliably.
- **`--confirm` or `--allow-motion` flags for real robots:** the agent can set them
  itself.
- **Client permission prompts** (`_meta["anthropic/requiresUserInteraction"]`): an extra
  layer in Claude Code, but other clients ignore it, users can auto-approve it, and it
  shows no preview.
- **Annotations derived from the HTTP method:** marks motion endpoints read-only.

## Consequences

- One shared Go package classifies operations and resolves the controller type for
  `nova api` and any CLI-side MCP code.
- The skill teaches: iterate on a virtual controller, then hand over to an approval UI
  for the real robot, and never try to work around `APPROVAL_REQUIRED`.
- Apps that move real robots own preview and approval. A shared
  approval UI in NOVA would avoid each app building its own; that is a platform
  question, not a prerequisite.
- Virtual and real cells should share the same cell setup, so a motion verified virtually
  is meaningful for the real robot. Differences (calibration, tools, collision scene) are
  why the preview on the real robot stays.

## Addendum 2026-11-03: motion requests through the instance MCP service

The decision above said MCP tools refuse motion on physical controllers outright. The
instance MCP service now plans to offer motion tools (`execute_trajectory`,
`start_program`, output writes) for physical controllers as well. The rule is relaxed to
**agents may request motion; only a human may approve it**, under the five points of "How
a motion on a real robot is approved", mapped to the service like this:

| Point | In the instance MCP service |
|---|---|
| 1. NOVA plans | `plan_trajectory` / `plan_collision_free` return a trajectory id, duration and sample count, never joint control. |
| 2. A human sees a preview and approves | Before requesting, the agent renders the plan on the developer's machine (Rerun through the Python SDK, or a plot). On a physical target the tool does not execute: it asks the human through **MCP elicitation**. The agent's client shows a dialog with the facts of that plan (cell, motion group, controller, trajectory id, duration, maximum speed, velocity override) and the human accepts or declines it there. The model has no argument to set and never sees a field it could fill in. |
| 3. The approved plan runs | The answer is bound to one trajectory id and motion group, expires after a few minutes and is used once; the binding travels in the signed request state of the round trip, so the service stays stateless. A new plan needs a new dialog. |
| 4. Reduced speed | Motion requested through MCP runs with a velocity override the human sets in the dialog, not the model. Start value is low (e.g. 10 %). |
| 5. A stop that always works | `stop_execution` and `stop_program` are in every toolset and never gated. |

Elicitation in MCP 2026-07-28 is a multi round-trip: the tool call returns
`input_required`, the client collects the answer from the user, then retries the call with
the answer and the server's opaque state [S5]. The official Go SDK implements it. A client
that has not declared the elicitation capability gets `APPROVAL_REQUIRED` with the facts of
the plan and a pointer to an app or the NOVA UI instead; a shared approval page there is
useful for such clients, but no longer a prerequisite.

Why the dialog is acceptable now, although the decision above rejected it: the threat
model in the README is mistakes and prompt injection. A dialog the model cannot answer
covers both. The remaining gap, a user who configures the client to auto-accept
elicitations or lets the agent run its own client, is deliberate and therefore in the
same class as handing an agent credentials for a real cell: covered by physical safety,
not by software. In development the operator stands at the cell with the emergency stop in
hand; that is the setting this addendum assumes.

Deployment-side switches stay in front of all this: the motion toolsets are opt-in, and a
Helm value (`allowPhysicalMotion`, default off) keeps physical targets refused unless an
operator enables them. Jogging and other continuous streams stay out: there is no plan to
preview, so nothing to approve.

What this changes elsewhere:

- The kit's motion rule becomes "never move a physical robot from a script, and never
  answer an approval yourself"; the agent's sequence is plan, preview locally, describe
  what will move, request, wait for the human's answer.
- The prototype's reason for avoiding elicitation (its own agent is the client) still
  holds for that prototype. It does not apply to a coding agent whose client is the
  developer's tool, and the local preview covers the "shows no preview" objection.
- Protection against deliberate misuse is unchanged: physical safety at the cell.
