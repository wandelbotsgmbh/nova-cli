package cmd

const createAppTxtLong = `This will create a new app project for NOVA OS to get you started quickly.
It contains a basic structure to build and deploy your app.

Different programming languages are supported. Use with --generator to choose between python (python_app),
typescript (nextjs_app), go (golang_app) and rust (rust_app).

The project gets a gitignored env file with the configured instance, cell and access token
(see 'nova app update-env'), so it runs locally against the instance right away. Run
'nova app update-env' in the project when the access token expired.`

const installAppTxtLong = `Installs an app to Wandelbots NOVA OS
This command will install the app depending on the provided source.

If the source is an app manifest according to the spec (http://host/api/v1/ui/#/operations/addApp), this will be installed directly,
without rebuilding and pushing the image.

For everything that is not an app manifest it is expected that a Dockerfile and a .nova file is present in the provided source.
In this case, the command is a shortcut for building the container image, pushing it to the registry
and installing it to the NOVA cell via the nova api and the provided spec within the .nova file.
This might be useful during development of your application for quick iteration.

Docker and podman are supported for building and pushing the image. The runtime is auto-detected
(docker preferred) and can be forced via 'nova config set container-runtime <docker|podman>'.

Make sure to have a registry configured via 'nova config set image-registry <your-registry>'
and that you are logged in to it via 'docker login <your-registry>' (or 'podman login <your-registry>'
when using podman).
You can override the global settings by adjusting the .nova file accordingly.

If the instance cannot pull from your registry (e.g. air-gapped or shop-floor networks), use --copy-image:
instead of pushing, the built image is copied to the instance via ssh/scp and imported directly.
Only the image layers the instance is missing are transferred, so repeated installs are fast.

To avoid repeated password prompts, run once with --setup-ssh-key: it installs your SSH public key on the
instance and (unless --no-sudo) grants passwordless sudo for k3s via /etc/sudoers.d. After that, copy-image
installs run without any prompt.

In any case, if an app already is installed under that name, it will be updated.
`

const installAppTxtExample = `# install from the current directory (.nova and Dockerfile must be present)
nova app install

# install from the current directory (.nova and Dockerfile must be present)
nova app install .

# install from the specified directory (.nova and Dockerfile must be present)
nova app install your-nova-app

# install with different secrets (overrides .nova and global settings)
nova app install your-nova-app --registry-secret pull-secret-my-registry

# install with nova default secrets (overrides .nova and global settings)
nova app install your-nova-app --omit-credentials

# copy the image to the instance (configured host) via ssh/scp instead of pushing it to the registry
nova app install --copy-image --ssh-user nova.admin

# same as root without sudo on a custom ssh port
nova app install --copy-image --ssh-user root --no-sudo --ssh-port 2222

# one-time: set up key-based ssh and passwordless sudo, then install without prompts afterwards
nova app install --copy-image --ssh-user nova.admin --setup-ssh-key

# install from app manifest available as json
nova app install path/to/app.json

# install from app manifest available as yaml
nova app install path/to/app.yaml

# or
nova app install path/to/app.yml

# install from json or yaml from remote location
nova app install https://somehost.com/path/to/app.yaml?latest=true

# Complete example for working with azure acr.
# Please note: Azure credentials optained by the process below, have a limited lifetime.
# If you want to share youre app with others, consider creating specific pull tokens with the azure cli.
az login
az acr login --name your-registry
nova config set image-registry company.azurecr.io/your-repo
nova app install path/to/your/app
`

const editAppTxtLong = `Edits the configuration of an app directly in your favorite editor. By default the platform editor is used (e.g. vi on linux).
If the files content changed, the app configuration is updated after closing the editor.

If you want to change the editor you can call 'nova config set editor "code --wait"' to use Visual Studio Code.`

const updateEnvAppTxtLong = `Updates the local development settings of an app project in its gitignored env file,
so the app can be run locally (e.g. F5 in vscode, cargo run, go run ., uv run app, pnpm dev) against
the configured instance.

What gets written:
  - the access token: always replaced. For portal instances (*.wandelbots.io) it is refreshed first,
    like 'nova login token' does (starts a login if needed). Other hosts need no token, it is left empty.
  - the instance settings (instance url, cell, base path): only added if missing, so values you
    edited by hand are kept. --reset replaces them with the targeted instance ('nova config view').
  - everything else in the file (comments, your own variables) stays untouched.

When to run it:
  - the app gets "Error 401: Unauthorized" from the instance (portal tokens are valid for 24h).
    The python and nextjs apps may only hang or retry instead, run it first if they don't connect.
  - with --reset after switching the instance or cell ('nova instance select', 'nova config set cellname').
  'nova app create' already runs it (with --reset) for a new project.

File and variables per project type (detected by Cargo.toml, go.mod, package.json, pyproject.toml):
  rust_app, golang_app  .env.local  token ACCESS_TOKEN, instance NOVA_API CELL_NAME BASE_PATH, LOG_LEVEL if missing
  nextjs_app            .env.local  token NOVA_DEV_ACCESS_TOKEN, instance NOVA_DEV_INSTANCE_URL CELL_ID
  python_app            .env        token NOVA_ACCESS_TOKEN, instance NOVA_API CELL_NAME BASE_PATH

The apps read the file on startup: restart the app after updating (nextjs dev reloads it by itself).
The token is never printed. The output lists the instance settings in effect and notes the ones that
differ from the targeted instance. -o json names the file (absolute path), what happened to the token
(refreshed; not_needed; missing: the instance needs one but the login has none; unused) and each
variable of the layout as set or kept, with differs for instance settings that differ from the
targeted instance; it holds no values.`

const updateEnvAppTxtExample = `# refresh the access token of the app project in the current directory
nova app update-env

# after 'nova instance select <other-instance>': point the app to the selected instance
nova app update-env --reset

# point the app to another registered instance without selecting it
nova --instance lab-ipc app update-env --reset

# app project in another directory
nova app update-env path/to/your-nova-app`
