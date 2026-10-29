package cmd

// This file contains the long-form descriptions and examples for the bootstrap
// command and its subcommands. They live here to keep bootstrap.go focused on
// command wiring.

const bootstrapLong = `Bootstrap a NOVA installation on an on-premise IPC.

Prerequisites:
  - Run "nova login" first to authenticate with the developer portal.
  - kubectl must be configured to access the target cluster e.g. "kubectl config use-context" or
  "export KUBECONFIG=path/to/config" 

The bootstrap process:
  1. Creates the target namespace (nova bootstrap namespace)
  2. Deploys Ember Stack Reflector for cross-namespace secret mirroring (nova bootstrap reflector)
  3. Applies pull secrets from the developer portal (nova bootstrap pull-secrets)
  4. Applies the service-manager HelmChart CR (nova bootstrap service-manager)
  5. Applies the system-owner Secret (nova bootstrap system-owner)

All manifests are applied to the cluster via "kubectl apply".

Use --dry-run to print manifests to stdout instead of applying them.
Use "nova bootstrap all" to run all steps in sequence.`

const bootstrapExample = ` # Point kubectl to the target cluster context
 export KUBECONFIG=~/.kube/gpu-workstation

 # Login to the developer portal
 nova login

 # Bootstrap NOVA
 nova bootstrap all

# If running behind some proxy that performs SSL inspection, 
# set proxy config and inject the proxy CA certs.
# Make sure cluster internal and local addresses are included in the no-proxy list.
# 
# You might want to check k3s settings at "/etc/systemd/system/k3s.service.env" and use these.
nova bootstrap all \
 --use-k3s-proxy-env \
 --ca-certificates cert.pem

# To explicitly set proxy config and inject the proxy CA certs.
# Make sure cluster internal and local addresses are included in the no-proxy list.
# 
# You might want to check k3s settings at "/etc/systemd/system/k3s.service.env" and use these.
nova bootstrap all \
 --http-proxy http://192.168.56.1:8080 \
 --https-proxy http://192.168.56.1:8080 \
 --no-proxy localhost,127.0.0.0/8,::1,0.0.0.0,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,192.0.2.1,.svc,.cluster.local,.cluster.local. \
 --ca-certificates cert.pem
`

const bootstrapNamespaceLong = `Creates the target Kubernetes namespace.

This ensures the namespace exists before other resources that depend on it
are applied. Safe to run if the namespace already exists (kubectl apply is
idempotent).

Requires a configured kubectl context.`

const bootstrapReflectorLong = `Applies a HelmChart CR that deploys Ember Stack Reflector.

Reflector automatically mirrors Secrets and ConfigMaps across namespaces
based on annotations. It is required so that pull secrets created in
kube-system are available in all other namespaces.

Requires a configured kubectl context.`

const bootstrapPullSecretsLong = `Fetches image-registry credentials from the developer portal API and
applies them as Kubernetes Secrets via kubectl.

The secrets include Reflector annotations so they are automatically mirrored
to all namespaces.

Requires prior authentication via "nova login" and a configured kubectl context.`

const bootstrapServiceManagerLong = `Applies a HelmChart custom resource to the cluster via kubectl.

k3s deploys HelmChart CRs via its built-in Helm controller.

By default the latest available release is resolved from the developer portal
(use --channel stable|next, or --version to pin a specific version).`

const bootstrapSystemOwnerLong = `Applies a Kubernetes Secret containing the authenticated user's email
and refresh token to the cluster via kubectl.

The Service Manager Backend reads this secret at runtime for:
  - Unattended communication with the developer portal API
  - License activation via Cryptlex

Requires prior authentication via "nova login" and a configured kubectl context.`

const bootstrapCACertsLong = `Injects one or more proxy CA certificates into the system trust store of
workloads in the target namespace.

Useful when the cluster sits behind a proxy that performs SSL inspection: the
proxy re-signs TLS connections with its own CA, which workloads would otherwise
reject. This installs Kyverno (via the k3s HelmChart controller) and a
ClusterPolicy that, for every Pod in the target namespace, merges the system
root CAs with the provided proxy CA and mounts the result over
/etc/ssl/certs/ca-certificates.crt (also setting SSL_CERT_FILE).

This fixes both the Cryptlex LexActivator (libcurl) license calls and Go's
net/http calls (developer portal API, Auth0) in the Service Manager Backend.

Pass one or more PEM or DER certificate files as positional arguments
(DER is converted to PEM automatically):

  nova bootstrap ca-certificates proxy-root.pem proxy-intermediate.der

Note: if Cryptlex SSL pinning is enabled, CA injection will not help and you
must exclude *.cryptlex.com from SSL inspection on the proxy instead.`

const bootstrapAllLong = `Runs the complete bootstrap sequence:
  1. Create target namespace
  2. Inject proxy CA certificates (only with --ca-certificates; runs before
     workloads so the Kyverno policy mutates pods at admission)
  3. Deploy Ember Stack Reflector
  4. Apply pull secrets
  5. Apply service-manager HelmChart CR
  6. Apply system-owner Secret

Requires prior authentication via "nova login", a configured kubectl context,
and --version to specify the service-manager chart version (defaults to latest).`
