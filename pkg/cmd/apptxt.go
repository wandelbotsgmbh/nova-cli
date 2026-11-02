package cmd

const createAppTxtLong = `This will create a new app project for NOVA OS to get you started quickly.
It contains a basic structure to build and deploy your app.

Different programming languages are supported. Use with --generator to choose between python (python_app),
typescript (nextjs_app), go (golang_app) and rust (rust_app).`

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

const envAppTxtLong = `Writes the local development settings of an app project into its gitignored env file:
the configured instance (host), the cell and, for portal instances (*.wandelbots.io), a fresh access token.
The access token is refreshed (or a login is started) like 'nova login token' does.

Run it again when the access token expired (the app gets "Error 401: Unauthorized" from the instance)
or after switching the instance with 'nova config set host'.

Existing lines in the file are kept, only the managed variables are replaced:
  rust_app, golang_app: .env.local       NOVA_API, CELL_NAME, BASE_PATH, ACCESS_TOKEN (and LOG_LEVEL if missing)
  nextjs_app:           .env.local       NOVA_DEV_INSTANCE_URL, NOVA_DEV_ACCESS_TOKEN, CELL_ID
  python_app:           .env             NOVA_API, CELL_NAME, BASE_PATH, NOVA_ACCESS_TOKEN

'nova app create' runs this once for a new project.`

const envAppTxtExample = `# update the env file of the app project in the current directory
nova app update-env

# update the env file of another app project
nova app update-env path/to/your-nova-app`
