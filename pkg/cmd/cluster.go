package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
)

// name used by 'nova cluster create' without --name
const defaultClusterName = "cluster"

// clusterServiceURL is a variable so tests can point it to a fake service.
var clusterServiceURL = "http://k8s.wabo.run/instance"

var (
	virtualNovaVersion string
	clusterName        string
	clusterReplace     bool
)

type virtualInstance struct {
	// Base64 encoded kubeconfig
	Kubeconfig string `json:"kubeconfig"`
	Id         string `json:"id"`
	Ip         string `json:"ip"`
}

// newClusterCmd builds the command group; it is registered as 'cluster' and, deprecated
// and hidden, as 'virtual'.
func newClusterCmd(use, deprecated string) *cobra.Command {
	group := &cobra.Command{
		Use:   use,
		Short: "(Wandelbots internal) Create or destroy Kubernetes clusters with Wandelbots NOVA OS",
		Long: `(Wandelbots internal) Create or destroy Kubernetes clusters with Wandelbots NOVA OS.

Each cluster is added to the local instance list as kind "cluster" (see 'nova instance').
Its kubeconfig is stored in ~/.nova/instances/<name>/kubeconfig.

'nova virtual' is a deprecated alias of this command.`,
	}

	create := &cobra.Command{
		Use:         "create",
		Args:        cobra.NoArgs,
		Annotations: noTarget,
		Short:       "Create a Kubernetes cluster with Wandelbots NOVA OS",
		Long: `Create a Kubernetes cluster with Wandelbots NOVA OS, add it to the local instance list
and select it.

With --name, the cluster is added under that name; if the name is taken, the command
fails unless --replace is given, which destroys the existing cluster of that name first.

Without --name, the cluster is named "cluster" and replaces the previous unnamed cluster
(previous behaviour; named clusters are never touched). The kubeconfig is then also
written to ./kubeconfig.`,
		Example: `# several clusters side by side
$ nova cluster create --name sim-a
$ nova cluster create --name sim-b
$ nova --instance sim-a app list

# replace the unnamed cluster
$ nova cluster create`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, replace := clusterName, clusterReplace
			if name == "" {
				name, replace = defaultClusterName, true
			}
			if err := instances.ValidateName(name); err != nil {
				return err
			}

			r, err := loadRegistry()
			if err != nil {
				return err
			}
			if existing, ok := r.Get(name); ok {
				if existing.Kind != instances.KindCluster {
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
			kubeconfig, err := writeClusterKubeconfig(name, kubeBytes)
			if err != nil {
				return err
			}

			if err := r.Put(instances.Instance{
				Name:       name,
				Kind:       instances.KindCluster,
				Host:       instance.Ip,
				ID:         instance.Id,
				Kubeconfig: kubeconfig,
			}); err != nil {
				return err
			}
			r.Current = name
			if err := saveRegistry(r); err != nil {
				return err
			}

			if clusterName == "" {
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
					return fmt.Errorf("%w: %q (see 'nova cluster list')", instances.ErrNotFound, args[0])
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
			if err := requireKind(inst, instances.KindCluster, "destroy"); err != nil {
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
			if dir, err := clusterDir(inst.Name); err == nil {
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
				if i.Kind == instances.KindCluster {
					clusters.Instances = append(clusters.Instances, i)
				}
			}
			printInstanceList(cmd.OutOrStdout(), clusters, nil)
			return nil
		},
	}

	create.Flags().StringVarP(&virtualNovaVersion, "version", "v", "x.x.x", "Version of the service manager to install.")
	create.Flags().StringVarP(&clusterName, "name", "n", "", "Name of the cluster in the local instance list (default \"cluster\", replacing the previous unnamed one)")
	create.Flags().BoolVar(&clusterReplace, "replace", false, "Destroy an existing cluster with the same name first")

	for _, c := range []*cobra.Command{create, destroy, list} {
		c.Deprecated = deprecated
		group.AddCommand(c)
	}
	group.Deprecated = deprecated
	group.Hidden = deprecated != ""
	return group
}

func requestCluster(version string) (virtualInstance, error) {
	var instance virtualInstance

	baseUrl, err := url.Parse(clusterServiceURL)
	if err != nil {
		return instance, err
	}

	params := url.Values{}
	// duration in minutes
	params.Add("duration", "720")
	params.Add("comment", "created by nova-cli")
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

func clusterDir(name string) (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nova", "instances", name), nil
}

func writeClusterKubeconfig(name string, content []byte) (string, error) {
	dir, err := clusterDir(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "kubeconfig")
	return path, os.WriteFile(path, content, 0o600)
}

func destroyVirtual(instanceId string) error {
	content := fmt.Sprintf(`{"instance_id": "%s", "duration": %d}`, instanceId, 0)

	baseUrl, err := url.Parse(clusterServiceURL)
	if err != nil {
		return err
	}

	params := url.Values{}
	// duration in minutes
	params.Add("duration", "0")
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
		return fmt.Errorf("destroying cluster failed with status code %d", resp.StatusCode)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(newClusterCmd("cluster", ""))
	rootCmd.AddCommand(newClusterCmd("virtual", "use 'nova cluster' instead"))
}
