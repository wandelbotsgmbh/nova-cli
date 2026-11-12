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
	"strings"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"golang.org/x/term"
)

// name used by 'nova k8srun create' without --name
const defaultK8sRunName = "k8srun"

// k8srunMaxMinutes is the longest lifetime the cluster service grants (12 hours).
const k8srunMaxMinutes = 720

// afterPruneConfirm runs after the prune question was answered; tests use it to change
// the list meanwhile.
var afterPruneConfirm = func() {}

// clusterServiceURL is a variable so tests can point it to a fake service.
var clusterServiceURL = "http://k8s.wabo.run/instance"

var (
	virtualNovaVersion string
	k8srunName         string
	k8srunReplace      bool
	k8srunNoSelect     bool
	k8srunPruneDryRun  bool
	k8srunPruneYes     bool
)

type virtualInstance struct {
	// Base64 encoded kubeconfig
	Kubeconfig string `json:"kubeconfig"`
	Id         string `json:"id"`
	Ip         string `json:"ip"`
}

// newK8sRunCmd builds the command group; it is registered as 'k8srun' and, through
// deprecatedAlias, as 'virtual' (cobra needs a separate tree per parent).
func newK8sRunCmd(use string) *cobra.Command {
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

	// -o/--output per command tree ('k8srun' and the 'virtual' alias)
	var createOutput, listOutput, pruneOutput *outputFormat

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

			entry := instances.Instance{
				Name:       name,
				Kind:       instances.KindK8sRun,
				Host:       instance.Ip,
				ID:         instance.Id,
				Kubeconfig: kubeconfig,
			}
			if err := r.Put(entry); err != nil {
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
			if *createOutput != outputJSON {
				return nil
			}
			created := k8srunCreatedJSON{instanceJSON: newInstanceJSON(entry, r.Current)}
			if k8srunName == "" {
				created.KubeconfigCopy = absPath("kubeconfig")
			}
			return writeJSON(cmd, created)
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
			if *listOutput == outputJSON {
				return writeJSON(cmd, instanceListJSON(clusters, nil))
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

An entry counts as gone only on a definite answer:
  - the cluster service doesn't know the cluster id, or
  - it does, but the cluster's Kubernetes API rejects the stored kubeconfig (a released
    VM keeps its IP but gets new credentials, so the address now serves another cluster).
Entries that can't be checked (network error, any other answer) are kept, and a cluster
that doesn't answer at all is kept too. An entry whose kubeconfig holds more than the
cluster's one context (e.g. kubectl or kind added theirs after 'nova instance env') is
never pruned: remove it explicitly with 'nova instance remove <name>'.

The entries found are listed with the reason, and the command asks before removing them;
-y removes them without asking (required without a terminal, e.g. for agents and scripts).`,
		Example: `$ nova k8srun prune --dry-run
$ nova k8srun prune
$ nova k8srun prune -y`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := loadRegistry()
			if err != nil {
				return err
			}
			errOut := cmd.ErrOrStderr()
			var gone []string
			checkedID := map[string]string{}
			var unchecked []pruneEntryJSON
			failed := 0
			for _, i := range r.Instances {
				if i.Kind != instances.KindK8sRun {
					continue
				}
				if i.ID == "" {
					fmt.Fprintf(errOut, "skipping %q: no cluster id (use 'nova instance remove %s' to forget it)\n", i.Name, i.Name)
					unchecked = append(unchecked, pruneEntryJSON{Name: i.Name, Kind: string(i.Kind), Reason: "no cluster id"})
					continue
				}
				if why := kubeconfigShared(i); why != "" {
					fmt.Fprintf(errOut, "skipping %q: %s; remove it explicitly with 'nova instance remove %s' (deletes that kubeconfig)\n", i.Name, why, i.Name)
					unchecked = append(unchecked, pruneEntryJSON{Name: i.Name, Kind: string(i.Kind), Reason: why})
					continue
				}
				isGone, reason, err := clusterGone(i, true)
				if err != nil {
					fmt.Fprintf(errOut, "keeping %q: %v\n", i.Name, err)
					unchecked = append(unchecked, pruneEntryJSON{Name: i.Name, Kind: string(i.Kind), Reason: err.Error()})
					failed++
					continue
				}
				if isGone {
					fmt.Fprintf(errOut, "%q is gone: %s\n", i.Name, reason)
					gone = append(gone, i.Name)
					checkedID[i.Name] = i.ID
				}
			}

			if len(gone) > 0 && !k8srunPruneDryRun &&
				!confirmWith(cmd, k8srunPruneYes, fmt.Sprintf("Remove %s from the local instance list?", strings.Join(gone, ", ")), false) {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return errors.New("nothing removed: without a terminal, confirm with -y")
				}
				return errors.New("nothing removed")
			}
			if len(gone) > 0 && !k8srunPruneDryRun {
				afterPruneConfirm()
				// another session may have replaced an entry while the question was open
				// (e.g. 'nova k8srun create' under the same name): remove only the
				// clusters that were checked
				if r, err = loadRegistry(); err != nil {
					return err
				}
				var still []string
				for _, name := range gone {
					if i, ok := r.Get(name); ok && i.ID == checkedID[name] {
						still = append(still, name)
					} else {
						fmt.Fprintf(errOut, "keeping %q: it was changed meanwhile\n", name)
					}
				}
				gone = still
			}
			// entries without id are reported as unchecked, but (as before) don't fail the command
			if err := removeGoneEntries(cmd, r, gone, k8srunPruneDryRun, pruneOutput, unchecked); err != nil {
				return err
			}
			if failed > 0 {
				return fmt.Errorf("%d cluster(s) could not be checked and were kept", failed)
			}
			return nil
		},
	}

	create.Flags().StringVarP(&virtualNovaVersion, "version", "v", "x.x.x", "Version of the service manager to install.")
	create.Flags().StringVarP(&k8srunName, "name", "n", "", "Name of the cluster in the local instance list (default \"k8srun\", replacing the previous unnamed one)")
	create.Flags().BoolVar(&k8srunReplace, "replace", false, "Destroy an existing cluster with the same name first")
	create.Flags().BoolVar(&k8srunNoSelect, "no-select", false, "Add the cluster to the local list without selecting it (target it with --instance)")
	createOutput = addOutputFlag(create)
	listOutput = addOutputFlag(list)
	pruneOutput = addOutputFlag(prune)
	prune.Flags().BoolVar(&k8srunPruneDryRun, "dry-run", false, "Only print the entries that would be removed")
	prune.Flags().BoolVarP(&k8srunPruneYes, "yes", "y", false, "Remove the entries without asking")

	group.AddCommand(create, destroy, list, prune)
	return group
}

// deprecatedAlias hides group and marks it and its subcommands deprecated in favour of
// 'nova <replacement>'. cobra prints 'Command "<name>" is deprecated, <message>', so
// each message names the group and the replacement; otherwise 'nova virtual list'
// reads as if only 'list' were deprecated.
func deprecatedAlias(group *cobra.Command, replacement string) *cobra.Command {
	group.Hidden = true
	group.Deprecated = fmt.Sprintf("'nova %s' was renamed: use 'nova %s'", group.Name(), replacement)
	for _, c := range group.Commands() {
		c.Deprecated = fmt.Sprintf("'nova %s' was renamed: use 'nova %s %s'", group.Name(), replacement, c.Name())
	}
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
// they couldn't verify.
func clusterExists(id string) (bool, error) {
	exists, _, err := clusterKubeconfig(id)
	return exists, err
}

// clusterKubeconfig asks the cluster service for the kubeconfig of a cluster id, which
// also tells whether the id is still assigned (see clusterExists). The kubeconfig is
// returned as the service sends it (possibly base64, see decodeServiceKubeconfig).
func clusterKubeconfig(id string) (bool, []byte, error) {
	resp, err := httpx.Client().Get(clusterServiceURL + "/" + url.PathEscape(id) + "/kubeconfig")
	if err != nil {
		return false, nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return false, nil, err
		}
		return true, body, nil
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, resp.Body)
		return false, nil, nil
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return false, nil, fmt.Errorf("cluster service answered with status code %d", resp.StatusCode)
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
	rootCmd.AddCommand(newK8sRunCmd("k8srun"))
	rootCmd.AddCommand(deprecatedAlias(newK8sRunCmd("virtual"), "k8srun"))
}
