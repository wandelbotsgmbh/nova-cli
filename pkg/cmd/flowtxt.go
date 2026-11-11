package cmd

const flowLong = `Manage NovaFlow on the configured cell ('nova config set cellname <name>').

NovaFlow composes robot behavior as flows (behavior trees) that a control plane executes.
These commands install it on a cell, open its editor, and read, edit, write, delete, back up and restore flows.

Commands that need NovaFlow check that it runs on the cell. 'open', 'push' and 'backup restore'
offer to install it when it is missing (use --yes to install without asking).

Custom actions for flows are Python services: create one with 'nova flow action create <name>'.`

const flowInstallLong = `Installs NovaFlow on the cell, or switches it to another version.

NOVA deploys NovaFlow as part of the cell when its NovaFlow feature flag is set; this command sets the
flag and waits until NovaFlow answers. Without --version, the version that comes with the NOVA version
is installed (or the version selected by an earlier --version is kept; '--version default' goes back to
the one that comes with NOVA). Other versions need to be available to the instance from the Wandelbots
registry; if one can't be deployed, the previous version keeps running and 'nova flow status' shows why.

The flag needs NOVA 26.4 or newer and the permission to manage cells; --version and progress reporting
need NOVA 26.8 or newer.

Where the instance doesn't expose the feature flag API (e.g. cloud instances on portal.wandelbots.io,
or NOVA before 26.4), the command installs the novaflow-installer app from the preview catalog and lets
it deploy the NovaFlow apps instead; --installer does that everywhere. Once the novaflow-installer app is
on the cell, install and uninstall always go through it. Without --version it installs the version the
installer recommends.

The command refuses to switch between both ways, or to touch a NovaFlow installed another way (e.g.
with helm), unless --force is given.`

const flowUninstallLong = `Removes NovaFlow from the cell, the way it was installed: by clearing its NovaFlow feature
flag, or through the novaflow-installer app (which stays installed). Waits until its apps are gone.

The flows stay in the cell's object store and are back after the next 'nova flow install'.
'nova flow backup download' keeps a copy outside the cell, e.g. before deleting the cell.`

const flowGetLong = `Prints a flow (its tree, layout and metadata) as JSON.

The output can be edited and saved back with 'nova flow push': it contains the flow's revision,
so the push fails instead of overwriting changes made on the instance in the meantime.`

const flowPushLong = `Creates a flow, or updates it if a flow with that id exists.

The file is either a flow as printed by 'nova flow get', or a bare tree ({"version": ..., "root": ...})
together with --id.

An existing flow is only updated when the file carries the revision the flow still has on the instance
(as written by 'nova flow get'). Use --force to replace it regardless. The file keeps its revision after
a push: run 'nova flow get' again before pushing further changes.

With --validate, the instance checks the tree first (schema, available actions, globals) and nothing is
saved if it is not valid.`

const flowBackupDownloadLong = `Downloads the cell's flows and globals into a .tar.gz file
(default: novaflow-backup.tar.gz). Flows read globals such as poses and TCPs, so both are saved.

With --include-objects, the cell object store is saved too. The object store is shared by all apps
of the cell, so it can be large and contain data of other apps; --object-prefix narrows it down.
NovaFlow keeps the flows themselves in the object store under trees/: those keys are left out, the
flows are saved through NovaFlow instead.

If NovaFlow reports flows it can't read, the backup is still written, lists them in its manifest,
and the command fails.`

const flowBackupRestoreLong = `Restores a backup made with 'nova flow backup download' to the cell:
globals first, then flows, then (with --include-objects) objects.

Flows, globals and objects that already exist are kept unless --overwrite is given. Use --dry-run to see
what would be created, replaced or kept. Restored flows get new revisions and timestamps.

A warning is printed when the backup was made with another NovaFlow version: flows are restored
unchanged, so flows of an older tree format may need migrating on the instance.`

const flowDeleteLong = `Deletes a flow from the cell after asking for confirmation (--yes skips the question;
without a terminal, --yes is required).

The flow is only deleted if nobody changed it between the question and the deletion.
'nova flow backup download' or 'nova flow get <id> -o <file>' keep a copy beforehand.`

const flowActionCreateLong = `Creates a project for a NovaFlow action service: a Python service whose actions can be used in flows.

The project comes from the NovaFlow action template (github.com/wandelbotsgmbh/novaflow-action-template),
which is rendered with Copier via uv, so uv and git need to be installed. The template is not public yet:
you need read access to it.

Without --action, the template asks for the service's actions. With --action, or without a terminal
(scripts, agents), it runs without questions and uses defaults for everything not given.

The project's .env is set up for the configured instance ('nova app update-env' refreshes it). Run the
service with 'uv run run.py': its actions show up in the flow editor within seconds. That needs access to
the instance's NATS, which LAN/IPC instances allow but cloud instances don't.

Deploy it like any app with 'nova app install .' (after 'uv sync', the Dockerfile needs uv.lock): the image
goes to the imageRegistry in .nova (taken from 'nova config set image-registry'), or with --copy-image
straight to the instance over ssh. On the cell, NOVA provides NATS_BROKER, NOVA_API and CELL_NAME; a
stopped or removed service stays listed in NovaFlow as offline.

Later template versions are merged into the project with 'uvx copier update --trust'.`

const flowActionCreateExample = `# ask for the actions
nova flow action create gripper-actions

# no questions (scripts, agents): two actions, the second returns a boolean for conditions in flows
nova flow action create gripper-actions --action OpenGripper --action IsGripperOpen:condition

# develop: run it against a LAN/IPC instance, its actions show up in the flow editor within seconds
cd gripper-actions
uv sync          # also writes uv.lock, which the Dockerfile needs
uv run pytest
uv run run.py

# deploy it to the cell as an app (builds the image and pushes it to the imageRegistry in .nova)
nova config set image-registry <your-registry>   # before 'action create', or edit imageRegistry in .nova
nova app install .

# deploy to an instance without registry access: copy the image over ssh instead (IPCs)
nova app install . --copy-image --ssh-user <user> --setup-ssh-key   # first time
nova app install . --copy-image --ssh-user <user>

# check that it runs, remove it again
nova app list
nova app delete gripper-actions`

const flowExample = `# which cell, and does NovaFlow run there?
nova config view
nova flow status

# install it (or: answer yes when 'open', 'push' or 'backup restore' offer it)
nova flow install

# edit a flow in your editor ('nova config set editor "code --wait"'), saved when you close it
nova flow list
nova flow edit pick-and-place

# or as a file and save it back (fails if it changed on the instance meanwhile)
nova flow get pick-and-place -o pick-and-place.json
nova flow push pick-and-place.json --validate

# back up flows and globals, restore them on another cell
nova flow backup download
nova config set cellname other-cell
nova flow backup restore novaflow-backup.tar.gz`

const flowStatusExample = `nova flow status

# another cell of the configured instance
nova config set cellname other-cell && nova flow status`

const flowInstallExample = `# the NovaFlow version that comes with the NOVA version, waits until it answers
nova flow install

# a specific chart version
nova flow install --version 2026.10.69

# back to the version that comes with NOVA, e.g. after a version failed to deploy
nova flow install --version default

# with the novaflow-installer app from the preview catalog (automatic where NOVA can't deploy it itself)
nova flow install --installer

# don't wait (check later with 'nova flow status')
nova flow install --no-wait`

const flowUninstallExample = `# remove NovaFlow (asks first), the flows stay on the cell
nova flow uninstall

# keep a copy outside the cell too
nova flow backup download && nova flow uninstall

# without the question (scripts, agents)
nova flow uninstall --yes`

const flowOpenExample = `nova flow open

# install NovaFlow first if it is missing, without asking
nova flow open --yes`

const flowListExample = `nova flow list`

const flowGetExample = `# print a flow
nova flow get pick-and-place

# save it for editing and 'nova flow push'
nova flow get pick-and-place -o pick-and-place.json

# only the tree
nova flow get pick-and-place | jq .tree`

const flowPushExample = `# save a flow edited after 'nova flow get', only if nobody changed it on the instance meanwhile
nova flow push pick-and-place.json

# check it on the instance first (actions available, globals exist, ...), save only if valid
nova flow push pick-and-place.json --validate

# create a flow from a bare tree ({"version": ..., "root": ...})
nova flow push tree.json --id pick-and-place --name "Pick and place"

# replace the flow on the instance no matter what changed there
nova flow push pick-and-place.json --force

# copy a flow to another id (--force replaces an existing copy)
nova flow push pick-and-place.json --id pick-and-place-copy --force`

const flowDeleteExample = `# asks before deleting
nova flow delete pick-and-place

# without the question (scripts, agents)
nova flow delete pick-and-place --yes`

const flowBackupDownloadExample = `# flows and globals into novaflow-backup.tar.gz
nova flow backup download

# into a dated file, including the objects of one app in the cell object store
nova flow backup download cell-$(date +%F).tar.gz --include-objects --object-prefix maps/

# look inside
tar xzOf novaflow-backup.tar.gz manifest.json`

const flowBackupRestoreExample = `# see what would be created, replaced or kept
nova flow backup restore novaflow-backup.tar.gz --dry-run

# restore what is missing, keep what exists
nova flow backup restore novaflow-backup.tar.gz

# put two flows back to the state of the backup, leave the globals alone
nova flow backup restore novaflow-backup.tar.gz --only pick-and-place,place --overwrite --skip-globals

# on a fresh cell: install NovaFlow without asking, then restore everything including objects
nova flow backup restore novaflow-backup.tar.gz --include-objects --yes`

const flowEditLong = `Opens a flow as JSON (as printed by 'nova flow get') in your editor and saves it back when
you close the editor with changes. An empty file or unchanged content cancels the edit.

The editor is the platform default (e.g. vi), or the one set with 'nova config set editor', e.g.
'nova config set editor "code --wait"' for Visual Studio Code.

Before saving, the instance validates the tree (schema, available actions, globals); --no-validate skips that.
If the JSON or the tree is not valid, the editor opens again with your changes. The id can't be changed
here ('nova flow push --id' copies a flow).

The flow is only saved if nobody changed it on the instance since it was opened. If it can't be saved,
your changes are kept in a file whose path is printed. Scripts and agents use 'nova flow get' and
'nova flow push' instead: this command needs a terminal.`

const flowEditExample = `nova flow edit pick-and-place

# with Visual Studio Code
nova config set editor "code --wait"
nova flow edit pick-and-place

# save a work in progress the instance doesn't accept as valid yet
nova flow edit pick-and-place --no-validate`
