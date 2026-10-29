# provide the host where the nova api is running (e.g. your virtual dev instance)
# always provide the full URL, including the protocol (e.g. https:// or http://)
NOVA_API=https://{{ .InstanceInformation.NovaApiHost }}
CELL_NAME="cell"
LOG_LEVEL=info

###
# SECRETS
#
# These should never be committed. Keep them in the gitignored .env only, or set
# them via the environment variables of the deployment.
###
NOVA_ACCESS_TOKEN="{{ .InstanceInformation.AccessToken }}"