## nova app install

Installs an app to Wandelbots NOVA OS

### Synopsis

Installs an app to Wandelbots NOVA OS
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


```
nova app install [source] [flags]
```

### Examples

```
# install from the current directory (.nova and Dockerfile must be present)
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

```

### Options

```
      --copy-image               Copy the image to the instance via ssh/scp and import it into containerd instead of pushing it to the registry. The configured host is used as ssh host.
  -h, --help                     help for install
      --import-command string    Remote command for --copy-image that imports an image tar from stdin. Note: plain ctr needs '-n k8s.io --address /run/k3s/containerd/containerd.sock'. (default "k3s ctr images import")
      --no-sudo                  Run the remote import for --copy-image without sudo (e.g. when connecting as root).
  -o, --omit-credentials         Omit obtaining registry credentials from your local docker config. Default wandelbots registry pull secrets will be used.
  -r, --registry-secret string   Provide a custom secret name for instead of optaining credentials from your local docker config. Make sure the secret exists in the cell namespace.
      --remote-dir string        Remote directory for --copy-image where the image archive is placed temporarily. (default "/tmp")
      --setup-ssh-key            Before copying, set up password-free access on the instance for --copy-image: install your SSH public key and (unless --no-sudo) grant passwordless sudo for k3s via /etc/sudoers.d. Needs the SSH and one sudo password once.
      --ssh-port int             SSH port for --copy-image. If 0, your ssh config or the default port applies.
      --ssh-user string          SSH user for --copy-image. If empty, your ssh config or the current user applies.
```

### Options inherited from parent commands

```
      --insecure-skip-tls-verify   If true, the server's certificate will not be checked for validity (disables TLS verification for all requests)
```

### SEE ALSO

* [nova app](nova_app.md)	 - Create and install apps on Wandelbots NOVA OS

###### Auto generated by spf13/cobra on 16-Oct-2026
