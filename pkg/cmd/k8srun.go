package cmd

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
)

// name used by 'nova k8srun create' without --name
const defaultK8sRunName = "k8srun"

// k8srunMaxMinutes is the longest lifetime the cluster service grants (12 hours).
const k8srunMaxMinutes = 720

// clusterServiceURL is a variable so tests can point it to a fake service.
var clusterServiceURL = "http://k8s.wabo.run/instance"

var (
	virtualNovaVersion string
	k8srunName         string
	k8srunReplace      bool
	k8srunNoSelect     bool
	k8srunPruneDryRun  bool
)

type virtualInstance struct {
	// Base64 encoded kubeconfig
	Kubeconfig string `json:"kubeconfig"`
	Id         string `json:"id"`
	Ip         string `json:"ip"`
}

// newK8sRunCmd builds the command group; it is registered as 'k8srun' and, deprecated
// and hidden, as 'virtual'.
func newK8sRunCmd(use, deprecated string) *cobra.Command {
	group := &cobra.Command{
		Use:   use,
		Short: "(Wandelbots internal) Create or destroy Kubernetes clusters with Wandelbots NOVA OS and a kubeconfig",
		Long: `(Wandelbots internal) Create or destroy Kubernetes clusters with Wandelbots NOVA OS.

These clusters are provided by a Wandelbots-internal service and only work from the
Wandelbots network; outside Wandelbots, use a cloud instance ('nova instance create') or
your own device ('nova instance add').

Unlike a cloud instance, a k8srun cluster comes with a kubeconfig, i.e. access to the
Kubernetes API. That makes it the choice when a workflow needs Kubernetes (e.g. skaffold,
helm or kubectl during app development) and no physical device is at hand. A cluster
expires after 12 hours.

Each cluster is added to the local instance list as kind "k8srun" (see 'nova instance').
Its kubeconfig is stored in ~/.nova/instances/<name>/kubeconfig; point your shell to it
with 'eval "$(nova instance env <name>)"'.

'nova virtual' is a deprecated alias of this command.`,
		Example: `$ nova k8srun create --name dev
$ eval "$(nova instance env dev)"
$ skaffold dev`,
	}

	create := &cobra.Command{
		Use:         "create",
		Args:        cobra.NoArgs,
		Annotations: noTarget,
		Short:       "Create a Kubernetes cluster with Wandelbots NOVA OS",
		Long: `Create a Kubernetes cluster with Wandelbots NOVA OS, add it to the local instance list
and select it (--no-select keeps the current selection, e.g. while other sessions or agents
work against it).

With --name, the cluster is added under that name; if the name is taken, the command
fails unless --replace is given, which destroys the existing cluster of that name first.

Without --name, the cluster is named "k8srun" and replaces the previous unnamed cluster
(previous behaviour; named clusters are never touched). The kubeconfig is then also
written to ./kubeconfig.`,
		Example: `# several clusters side by side
$ nova k8srun create --name sim-a
$ nova k8srun create --name sim-b
$ nova --instance sim-a app list

# replace the unnamed cluster
$ nova k8srun create`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, replace := k8srunName, k8srunReplace
			if name == "" {
				name, replace = defaultK8sRunName, true
			}
			if err := instances.ValidateName(name); err != nil {
				return err
			}

			r, err := loadRegistry()
			if err != nil {
				return err
			}
			if existing, ok := r.Get(name); ok {
				if existing.Kind != instances.KindK8sRun {
					return fmt.Errorf("%w: %q is a %s instance, choose another --name", instances.ErrExists, name, existing.Kind)
				}
				if !replace {
					return fmt.Errorf("%w: %q; pass --replace to destroy and recreate it, or choose another --name", instances.ErrExists, name)
				}
				if existing.ID != "" {
					if err := destroyVirtual(existing.ID); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "destroying previous cluster %q failed: %v\n", name, err)
					}
				}
			}

			instance, err := requestCluster(virtualNovaVersion)
			if err != nil {
				return err
			}

			kubeBytes, err := base64.StdEncoding.DecodeString(instance.Kubeconfig)
			if err != nil {
				return err
			}
			kubeconfig, err := writeInstanceKubeconfig(name, kubeBytes)
			if err != nil {
				return err
			}

			if err := r.Put(instances.Instance{
				Name:       name,
				Kind:       instances.KindK8sRun,
				Host:       instance.Ip,
				ID:         instance.Id,
				Kubeconfig: kubeconfig,
			}); err != nil {
				return err
			}
			if !k8srunNoSelect {
				r.Current = name
			}
			if err := saveRegistry(r); err != nil {
				return err
			}

			if k8srunName == "" {
				if err := os.WriteFile("kubeconfig", kubeBytes, 0o600); err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "created cluster %q:\n ID: %s\n IP %s\n kubeconfig: %s\n", name, instance.Id, instance.Ip, kubeconfig)
			return nil
		},
	}

	destroy := &cobra.Command{
		Use:               "destroy [name]",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeLocalInstance,
		Short:             "Destroy a Kubernetes cluster with Wandelbots NOVA OS",
		Long: `Destroy a cluster and remove it from the local instance list.
Without a name, the targeted instance is destroyed; it must be a cluster.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var inst instances.Instance
			if len(args) > 0 {
				r, err := loadRegistry()
				if err != nil {
					return err
				}
				var ok bool
				if inst, ok = r.Get(args[0]); !ok {
					return fmt.Errorf("%w: %q (see 'nova k8srun list')", instances.ErrNotFound, args[0])
				}
			} else {
				t, err := ensureTarget()
				if err != nil {
					return err
				}
				if t.Name == "" {
					return fmt.Errorf("%w: pass the name of the cluster", instances.ErrNoTarget)
				}
				inst = t
			}
			if err := requireKind(inst, instances.KindK8sRun, "destroy"); err != nil {
				return err
			}
			if inst.ID == "" {
				return fmt.Errorf("cluster %q has no id; use 'nova instance remove %s' to forget it", inst.Name, inst.Name)
			}

			if err := destroyVirtual(inst.ID); err != nil {
				return err
			}

			r, err := loadRegistry()
			if err != nil {
				return err
			}
			if err := r.Remove(inst.Name); err != nil {
				return err
			}
			if err := saveRegistry(r); err != nil {
				return err
			}
			if dir, err := instanceDir(inst.Name); err == nil {
				_ = os.RemoveAll(dir)
			}
			return nil
		},
	}

	list := &cobra.Command{
		Use:         "list",
		Args:        cobra.NoArgs,
		Annotations: noTarget,
		Short:       "List the clusters in the local instance list",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := loadRegistry()
			if err != nil {
				return err
			}
			clusters := &instances.Registry{Current: r.Current}
			for _, i := range r.Instances {
				if i.Kind == instances.KindK8sRun {
					clusters.Instances = append(clusters.Instances, i)
				}
			}
			printInstanceList(cmd.OutOrStdout(), clusters, nil)
			return nil
		},
	}

	prune := &cobra.Command{
		Use:         "prune",
		Args:        cobra.NoArgs,
		Annotations: noTarget,
		Short:       "Remove clusters that no longer exist from the local instance list",
		Long: `Remove clusters that no longer exist (expired after 12 hours, or destroyed elsewhere)
from the local instance list, together with their stored kubeconfig.

Each cluster is looked up on the cluster service; an entry is only removed if the service
answers that the cluster id is unknown. Entries that can't be checked (network error, any
other answer) are kept.`,
		Example: `$ nova k8srun prune --dry-run
$ nova k8srun prune`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := loadRegistry()
			if err != nil {
				return err
			}
			errOut := cmd.ErrOrStderr()
			var gone []string
			unchecked := 0
			for _, i := range r.Instances {
				if i.Kind != instances.KindK8sRun {
					continue
				}
				if i.ID == "" {
					fmt.Fprintf(errOut, "skipping %q: no cluster id (use 'nova instance remove %s' to forget it)\n", i.Name, i.Name)
					continue
				}
				exists, err := clusterExists(i.ID)
				if err != nil {
					fmt.Fprintf(errOut, "keeping %q: %v\n", i.Name, err)
					unchecked++
					continue
				}
				if !exists {
					gone = append(gone, i.Name)
				}
			}

			verb := "removed"
			if k8srunPruneDryRun {
				verb = "would remove"
			}
			for _, name := range gone {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", verb, name)
			}
			if len(gone) > 0 && !k8srunPruneDryRun {
				selected := r.Current
				for _, name := range gone {
					if err := r.Remove(name); err != nil {
						return err
					}
				}
				if err := saveRegistry(r); err != nil {
					return err
				}
				for _, name := range gone {
					if dir, err := instanceDir(name); err == nil {
						_ = os.RemoveAll(dir)
					}
				}
				if selected != "" && r.Current == "" {
					fmt.Fprintf(errOut, "the selected instance %q was removed; select another with 'nova instance select <name>'\n", selected)
				}
			}
			if unchecked > 0 {
				return fmt.Errorf("%d cluster(s) could not be checked and were kept", unchecked)
			}
			return nil
		},
	}

	create.Flags().StringVarP(&virtualNovaVersion, "version", "v", "x.x.x", "Version of the service manager to install.")
	create.Flags().StringVarP(&k8srunName, "name", "n", "", "Name of the cluster in the local instance list (default \"k8srun\", replacing the previous unnamed one)")
	create.Flags().BoolVar(&k8srunReplace, "replace", false, "Destroy an existing cluster with the same name first")
	create.Flags().BoolVar(&k8srunNoSelect, "no-select", false, "Add the cluster to the local list without selecting it (target it with --instance)")
	prune.Flags().BoolVar(&k8srunPruneDryRun, "dry-run", false, "Only print the entries that would be removed")

	for _, c := range []*cobra.Command{create, destroy, list, prune} {
		c.Deprecated = deprecated
		group.AddCommand(c)
	}
	group.Deprecated = deprecated
	group.Hidden = deprecated != ""
	return group
}

// localUser returns user@host of this machine, so the owner of a forgotten cluster can be
// found; parts that can't be determined are left out or replaced by "unknown".
func localUser() string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	name = cmp.Or(name, os.Getenv("USER"), os.Getenv("USERNAME"), "unknown")
	if host, err := os.Hostname(); err == nil && host != "" {
		name += "@" + host
	}
	return name
}

func requestCluster(version string) (virtualInstance, error) {
	var instance virtualInstance

	baseUrl, err := url.Parse(clusterServiceURL)
	if err != nil {
		return instance, err
	}

	params := url.Values{}
	params.Add("duration", strconv.Itoa(k8srunMaxMinutes))
	params.Add("comment", "created by nova-cli for "+localUser())
	// TODO in future we need some possibility to configure the version of the SM
	params.Add("install_service_manager", "true")
	params.Add("service_manager_version", version)
	baseUrl.RawQuery = params.Encode()

	resp, err := httpx.Client().Get(baseUrl.String())
	if err != nil {
		return instance, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return instance, err
	}

	if err := json.Unmarshal(bodyBytes, &instance); err != nil {
		return instance, err
	}

	switch {
	case instance.Id == "":
		return instance, errors.New("no instance id returned")
	case instance.Ip == "":
		return instance, errors.New("no instance ip returned")
	case instance.Kubeconfig == "":
		return instance, errors.New("no instance kubeconfig returned")
	}
	return instance, nil
}

// clusterExists asks the cluster service whether the cluster id is still assigned. Only
// a 404 counts as gone; any other answer is an error, so callers never drop an entry
// they couldn't verify. The kubeconfig in the answer is discarded.
func clusterExists(id string) (bool, error) {
	resp, err := httpx.Client().Get(clusterServiceURL + "/" + url.PathEscape(id) + "/kubeconfig")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("cluster service answered with status code %d", resp.StatusCode)
}

func destroyVirtual(instanceId string) error {
	return setClusterExpiry(instanceId, 0)
}

// setClusterExpiry lets the cluster expire in minutes (at most k8srunMaxMinutes) from
// now; 0 releases it immediately.
func setClusterExpiry(instanceId string, minutes int) error {
	content := fmt.Sprintf(`{"instance_id": "%s", "duration": %d}`, instanceId, minutes)

	baseUrl, err := url.Parse(clusterServiceURL)
	if err != nil {
		return err
	}

	params := url.Values{}
	params.Add("duration", strconv.Itoa(minutes))
	params.Add("instance_id", instanceId)
	baseUrl.RawQuery = params.Encode()

	req, err := http.NewRequest(http.MethodPut, baseUrl.String(), bytes.NewBuffer([]byte(content)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := httpx.Client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("setting the cluster's expiry failed with status code %d", resp.StatusCode)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(newK8sRunCmd("k8srun", ""))
	rootCmd.AddCommand(newK8sRunCmd("virtual", "use 'nova k8srun' instead"))
}
