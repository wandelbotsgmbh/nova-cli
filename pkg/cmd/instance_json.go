package cmd

import (
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/portal"
)

// JSON shapes of the instance and k8srun commands (-o json, ADR-0010). They are part of
// the CLI contract: fields may be added, never renamed or removed. They never carry
// tokens or kubeconfig contents, only the kubeconfig's path.

// instanceJSON is an entry of the local instance list ('instance list', 'k8srun list',
// 'instance add/create', 'k8srun create', the "instance" of 'instance info').
type instanceJSON struct {
	Name string `json:"name"`
	// cloud, host or k8srun
	Kind string `json:"kind"`
	Host string `json:"host"`
	// the cell commands use on this instance (default "cell")
	Cell string `json:"cell"`
	// portal instance id (cloud) or cluster id (k8srun)
	ID string `json:"id,omitempty"`
	// portal environment of a cloud instance (prod, stg, dev)
	Env string `json:"env,omitempty"`
	// absolute path of the stored kubeconfig (k8srun, host)
	Kubeconfig string `json:"kubeconfig,omitempty"`
	// the selected instance ('nova instance select'); --instance / NOVA_INSTANCE don't change it
	Selected bool `json:"selected"`
	// false for cloud instances of the portal account that aren't in the local list yet
	Local bool `json:"local"`
}

func newInstanceJSON(i instances.Instance, current string) instanceJSON {
	j := instanceJSON{
		Name:     i.Name,
		Kind:     string(i.Kind),
		Host:     i.Host,
		Cell:     i.Cell(),
		ID:       i.ID,
		Env:      i.Env,
		Selected: i.Name != "" && i.Name == current,
		Local:    true,
	}
	if i.Kubeconfig != "" {
		j.Kubeconfig = absPath(i.Kubeconfig)
	}
	return j
}

// instanceListJSON is the result of 'instance list' and 'k8srun list': the local entries,
// then cloud instances that aren't in the local list (local: false, named after their id).
func instanceListJSON(r *instances.Registry, unregistered []portal.UserInstance) []instanceJSON {
	list := make([]instanceJSON, 0, len(r.Instances)+len(unregistered))
	for _, i := range r.Instances {
		list = append(list, newInstanceJSON(i, r.Current))
	}
	for _, c := range unregistered {
		list = append(list, instanceJSON{
			Name: c.InstanceId,
			Kind: string(instances.KindCloud),
			Host: c.Host,
			Cell: instances.DefaultCell,
			ID:   c.InstanceId,
			Env:  string(portalEnvironment),
		})
	}
	return list
}

// cloudInstanceJSON is the state of a cloud instance on the portal ('instance list
// --cloud', the "cloud" of 'instance info').
type cloudInstanceJSON struct {
	ID          string `json:"id"`
	Host        string `json:"host"`
	Status      string `json:"status"`
	SandboxName string `json:"sandbox_name"`
	// RFC 3339, empty if unknown
	ExpiresAt string `json:"expires_at"`
	// RFC 3339, empty if unknown
	ObsoleteAt string `json:"obsolete_at"`
}

func newCloudInstanceJSON(c portal.UserInstance) cloudInstanceJSON {
	return cloudInstanceJSON{
		ID:          c.InstanceId,
		Host:        c.Host,
		Status:      string(c.Status),
		SandboxName: c.SandboxName,
		ExpiresAt:   unixRFC3339(int64(c.ExpiresAt)),
		ObsoleteAt:  unixRFC3339(int64(c.ObsoleteAt)),
	}
}

func unixRFC3339(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// instanceInfoJSON is the result of 'instance info': "instance" for a local entry,
// "cloud" for the portal state of a cloud instance (when available).
type instanceInfoJSON struct {
	Instance *instanceJSON      `json:"instance,omitempty"`
	Cloud    *cloudInstanceJSON `json:"cloud,omitempty"`
}

// kubeconfigJSON is the result of 'instance kubeconfig': the path only, never the content.
type kubeconfigJSON struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// absolute path of the kubeconfig
	Path string `json:"path"`
}

// lifetimeJSON is the result of 'instance extend'.
type lifetimeJSON struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// portal instance id (cloud) or cluster id (k8srun)
	ID string `json:"id"`
	// the new expiry, RFC 3339
	ExpiresAt string `json:"expires_at"`
	// false if the request was accepted but didn't move the expiry (maximum lifetime reached)
	Extended bool `json:"extended"`
}

// k8srunCreatedJSON is the result of 'k8srun create'.
type k8srunCreatedJSON struct {
	instanceJSON
	// absolute path of the copy written to ./kubeconfig (only without --name)
	KubeconfigCopy string `json:"kubeconfig_copy,omitempty"`
}

// pruneJSON is the result of 'instance prune' and 'k8srun prune'.
type pruneJSON struct {
	// with --dry-run, "removed" lists the entries that would be removed
	DryRun  bool             `json:"dry_run"`
	Removed []pruneEntryJSON `json:"removed"`
	// entries that couldn't be checked and were kept
	Unchecked []pruneEntryJSON `json:"unchecked"`
}

type pruneEntryJSON struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Reason string `json:"reason,omitempty"`
}

func newPruneJSON(dryRun bool) pruneJSON {
	return pruneJSON{DryRun: dryRun, Removed: []pruneEntryJSON{}, Unchecked: []pruneEntryJSON{}}
}
