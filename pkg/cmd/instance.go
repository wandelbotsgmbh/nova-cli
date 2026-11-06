package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"github.com/wandelbotsgmbh/nova-cli/pkg/portal"
	"gopkg.in/yaml.v3"
)

// flags
var (
	listVerboseInstances bool
	listCloudInstances   bool
	createNoWaitInstance bool
	// timeout in seconds
	timeoutCreateInstance       int
	nameCreateInstanceCmd       string
	ttlMinutesCreateInstanceCmd int
	versionCreateInstanceCmd    string
	chartCreateInstanceCmd      string

	printHostInstanceInfoCmd       bool
	printInstanceIdInstanceInfoCmd bool
	showSecretsInstanceInfoCmd     bool

	hostAddInstanceCmd    string
	cellAddInstanceCmd    string
	selectAddInstanceCmd  bool
	sshUserAddInstanceCmd string
)

var (
	// default environment gets overridden in PersistentPreRun
	portalEnvironment = oauth.AuthProd
)

const (
	// seconds
	defaultWaitTime             = 480
	checkInstanceStatusInterval = 2 * time.Second
)

var noTarget = map[string]string{annotationNoTarget: ""}

var instanceCmd = &cobra.Command{
	Use:              "instance",
	PersistentPreRun: setPortalEnvironment,
	Short:            "Manage the NOVA instances the CLI works against",
	Long: `Manage the NOVA instances the CLI works against.

The CLI keeps a local list of named instances. Each has a kind:
  cloud    an instance on portal.wandelbots.io (create, start, stop, delete via this command)
  host     any reachable NOVA host, e.g. an on-prem IPC (register with 'nova instance add')
  k8srun   a Wandelbots-internal Kubernetes cluster with a kubeconfig (see 'nova k8srun')

Commands work against the selected instance ('nova instance select'). Override it for a
single invocation with the global --instance <name> flag or the NOVA_INSTANCE environment
variable; neither changes the selection. Settings such as host, cellname, nucleus-host and
nucleus-token are stored per instance.`,
	Example: `# register an on-prem instance and select it
$ nova instance add lab --host 192.168.1.10 --cell line1
$ nova instance select lab

# list all instances, '*' marks the selected one
$ nova instance list

# run a single command against another instance
$ nova --instance my-cloud-instance app list
$ NOVA_INSTANCE=lab nova cell list`,
}

var addInstanceCmd = &cobra.Command{
	Use:         "add <name> --host <url>",
	Args:        cobra.ExactArgs(1),
	Annotations: noTarget,
	Short:       "Registers a reachable NOVA host (e.g. an on-prem IPC) as instance",
	Long: `Registers a reachable NOVA host, e.g. an on-prem IPC or your own server, as a local
instance of kind "host". Nothing is changed on the host itself.

If no instance is selected yet, the new one is selected. Fails if the name is taken;
use 'nova --instance <name> config set host <url>' to change an existing entry.`,
	Example: `$ nova instance add lab --host 192.168.1.10 --kubeconfig ./k3s.yaml
$ nova instance add line2 --host https://ipc-line2.example.com --cell line2 --select`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if hostAddInstanceCmd == "" {
			return errors.New("--host is required")
		}
		r, err := loadRegistry()
		if err != nil {
			return err
		}
		inst := instances.Instance{
			Name:     strings.TrimSpace(args[0]),
			Kind:     instances.KindHost,
			Host:     hostAddInstanceCmd,
			CellName: cellAddInstanceCmd,
			SSHUser:  sshUserAddInstanceCmd,
		}
		if err := r.Add(inst); err != nil {
			return err
		}
		if selectAddInstanceCmd || r.Current == "" {
			r.Current = inst.Name
		}
		if err := saveRegistry(r); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "added instance %q (%s)\n", inst.Name, inst.Host)
		if kubeconfigFrom != "" {
			content, err := readKubeconfigSource(cmd.InOrStdin(), kubeconfigFrom)
			if err == nil {
				_, err = storeKubeconfig(inst, content, "")
			}
			if err != nil {
				return fmt.Errorf("instance %q was added, but its kubeconfig wasn't stored: %w", inst.Name, err)
			}
		}
		return nil
	},
}

var removeInstanceCmd = &cobra.Command{
	Use:               "remove <name>",
	Args:              cobra.ExactArgs(1),
	Annotations:       noTarget,
	ValidArgsFunction: completeLocalInstance,
	Short:             "Removes an instance from the local list (does not delete it)",
	Long: `Removes an instance of any kind from the local list. The instance itself is not
touched: a cloud instance keeps running (use 'nova instance delete' to delete it), and so
does a cluster (use 'nova k8srun destroy'). If it was selected, no instance is selected
afterwards.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := loadRegistry()
		if err != nil {
			return err
		}
		name := strings.TrimSpace(args[0])
		inst, ok := r.Get(name)
		if err := r.Remove(name); err != nil {
			return err
		}
		if err := saveRegistry(r); err != nil {
			return err
		}
		// drop a stored kubeconfig together with the entry
		if dir, err := instanceDir(name); err == nil {
			_ = os.RemoveAll(dir)
		}
		switch {
		case ok && inst.Kind == instances.KindCloud:
			fmt.Fprintf(cmd.ErrOrStderr(), "removed %q from the local list; the cloud instance still exists ('nova instance delete %s' deletes it)\n", name, inst.ID)
		case ok && inst.Kind == instances.KindK8sRun:
			fmt.Fprintf(cmd.ErrOrStderr(), "removed %q from the local list; the cluster still exists until it expires\n", name)
		default:
			fmt.Fprintf(cmd.ErrOrStderr(), "removed %q\n", name)
		}
		return nil
	},
}

var deleteInstanceCmd = &cobra.Command{
	Use:               "delete <name>",
	Args:              cobra.ExactArgs(1),
	Annotations:       noTarget,
	ValidArgsFunction: completeInstance,
	Short:             "Deletes a cloud instance",
	Long: `Deletes a cloud instance on portal.wandelbots.io and removes it from the local list.
Only works for cloud instances; use 'nova instance remove' to forget a host entry and
'nova k8srun destroy' for k8srun instances.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		if name == "" {
			return errors.New("instance name must not be empty")
		}
		r, err := loadRegistry()
		if err != nil {
			return err
		}
		inst, local := lookupCloudEntry(r, name)
		if local {
			if err := requireKind(inst, instances.KindCloud, "delete"); err != nil {
				return err
			}
			if err := useInstanceEnv(inst); err != nil {
				return err
			}
		}

		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return err
		}
		if err := p.DeleteInstance(cloudID(inst, name)); err != nil {
			return err
		}

		if !local {
			return nil
		}
		if err := r.Remove(inst.Name); err != nil {
			return err
		}
		return saveRegistry(r)
	},
}

var listInstancesCmd = &cobra.Command{
	Use:         "list",
	Args:        cobra.NoArgs,
	Annotations: noTarget,
	Short:       "Lists all instances",
	Long: `Lists all instances in the local list; '*' marks the selected one.
When logged in to portal.wandelbots.io, cloud instances that aren't in the local list yet
are appended (select them with 'nova instance select <id>').

With --cloud, lists the instances on portal.wandelbots.io in the previous format
(id, host, status, sandbox name).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if listCloudInstances || listVerboseInstances {
			return listPortalInstances(cmd.OutOrStdout())
		}

		r, err := loadRegistry()
		if err != nil {
			return err
		}

		var unregistered []portal.UserInstance
		if loggedIn() {
			if p, err := portal.NewPortalNonInteractive(portalEnvironment); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: can't list cloud instances:", err)
			} else if cloud, err := p.ListInstances(); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: can't list cloud instances:", err)
			} else {
				for _, c := range cloud {
					if _, ok := r.FindByID(instances.KindCloud, c.InstanceId); !ok {
						unregistered = append(unregistered, c)
					}
				}
			}
		}

		printInstanceList(cmd.OutOrStdout(), r, unregistered)
		if o := targetOverride(); o != "" && o != r.Current {
			fmt.Fprintf(cmd.ErrOrStderr(), "note: this invocation targets %q (--instance / %s)\n", o, instances.EnvVar)
		}
		return nil
	},
}

func printInstanceList(out io.Writer, r *instances.Registry, unregistered []portal.UserInstance) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\tNAME\tKIND\tHOST\tCELL\tENV")
	for _, i := range r.Instances {
		mark := ""
		if i.Name == r.Current {
			mark = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", mark, i.Name, i.Kind, i.Host, i.Cell(), i.Env)
	}
	for _, c := range unregistered {
		fmt.Fprintf(w, "\t%s\t%s\t%s\t%s\t%s\n", c.InstanceId, instances.KindCloud, c.Host, "(not in local list)", portalEnvironment)
	}
	w.Flush()
}

func listPortalInstances(out io.Writer) error {
	p, err := portal.NewPortal(portalEnvironment)
	if err != nil {
		return err
	}
	cloud, err := p.ListInstances()
	if err != nil {
		return err
	}
	for _, instance := range cloud {
		if listVerboseInstances {
			b, err := yaml.Marshal(instance)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, string(b)+"\n")
		} else {
			fmt.Fprintln(out, instance.InstanceId, instance.Host, instance.Status, instance.SandboxName)
		}
	}
	return nil
}

var createInstancesCmd = &cobra.Command{
	Use:         "create",
	Args:        cobra.NoArgs,
	Annotations: noTarget,
	Short:       "Creates a new cloud instance",
	Long: `Creates a new cloud instance on portal.wandelbots.io, adds it to the local list
(named after its instance id) and selects it.`,
	Example: `# create new instance with some name
$ nova instance create --name my-instance

# (internal) create instance with limited lifetime of 10 minutes
$ nova instance create --name my-instance --ttl 10
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		started := time.Now()

		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return err
		}

		var instance portal.Instance
		if chartCreateInstanceCmd != "" || versionCreateInstanceCmd != "" || ttlMinutesCreateInstanceCmd != 0 {
			chartPtr := &chartCreateInstanceCmd
			if chartCreateInstanceCmd == "" {
				chartPtr = nil
			}

			versionPtr := &versionCreateInstanceCmd
			if versionCreateInstanceCmd == "" {
				versionPtr = nil
			}

			ttlPtr := &ttlMinutesCreateInstanceCmd
			if ttlMinutesCreateInstanceCmd == 0 {
				ttlPtr = nil
			}

			instance, err = p.CreateInstanceDev(nameCreateInstanceCmd, chartPtr, versionPtr, ttlPtr)
		} else {
			instance, err = p.CreateInstance(nameCreateInstanceCmd)
		}

		if err != nil {
			return err
		}

		err = registerCloudInstance(cmd.ErrOrStderr(), instance.InstanceId, instance.Host)

		fmt.Println("InstanceId:", instance.InstanceId, "\nHost:", instance.Host)

		if !createNoWaitInstance {
			fmt.Println("Waiting for instance to be in running state...")
			// portal api just checks if the kustomization is done
			if err := waitForInstanceRunning(p, instance.InstanceId, started); err != nil {
				return err
			}

			fmt.Println("Waiting for core services to be ready...")
			// now we need to check if all nova services are in running state
			to := time.Duration(timeoutCreateInstance) * time.Second
			if err := nova.WaitForAllRunning(instance.Host, started, to, checkInstanceStatusInterval); err != nil {
				return err
			}
		}

		return err
	},
}

var selectInstanceCmd = &cobra.Command{
	Use:               "select <name>",
	Args:              cobra.ExactArgs(1),
	Annotations:       noTarget,
	ValidArgsFunction: completeInstance,
	Short:             "Selects the instance commands work against",
	Long: `Selects the instance commands work against by default.

<name> is an entry of 'nova instance list'. If there is no such entry and you are logged
in, it is looked up as a cloud instance id on portal.wandelbots.io and added.

To target an instance for a single invocation without changing the selection, use the
global --instance flag or the NOVA_INSTANCE environment variable instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		r, err := loadRegistry()
		if err != nil {
			return err
		}
		if inst, ok := lookupCloudEntry(r, name); ok {
			if err := r.Select(inst.Name); err != nil {
				return err
			}
			return saveRegistry(r)
		}

		if !loggedIn() {
			return fmt.Errorf("%w: %q (see 'nova instance list'; log in with 'nova login' to select a cloud instance by id)", instances.ErrNotFound, name)
		}
		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return err
		}
		instance, err := p.GetInstance(name)
		if err != nil {
			return err
		}
		return registerCloudInstance(cmd.ErrOrStderr(), instance.InstanceId, instance.Host)
	},
}

var infoInstanceCmd = &cobra.Command{
	Use:               "info [name]",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeInstance,
	Short:             "Show information about an instance",
	Long: `Show information about an instance of any kind; without a name, the targeted one
(selected, or set via --instance / NOVA_INSTANCE). Secrets are redacted unless
--show-secrets is given. For cloud instances, the live state from portal.wandelbots.io is
included when logged in.
It is possible to only return the host or the instanceId of the instance by using the --host or --instanceId flags (e.g. to write these information to a env vars).`,
	Example: `
# e.g. after creating an instance, you can use the following command to write the host to an env var
$ nova instance create
$ export NOVA_HOST=$(nova instance info --host)

# show a specific instance
$ nova instance info lab
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		var inst instances.Instance
		local := true
		if len(args) > 0 {
			name := strings.TrimSpace(args[0])
			r, err := loadRegistry()
			if err != nil {
				return err
			}
			if inst, local = lookupCloudEntry(r, name); !local {
				// not in the local list: treat it as a cloud instance id (previous behaviour)
				inst = instances.Instance{Name: name, Kind: instances.KindCloud, ID: name}
			} else if err := useInstanceEnv(inst); err != nil {
				return err
			}
		} else {
			t, err := ensureTarget()
			if err != nil {
				return err
			}
			if t.Name == "" {
				return fmt.Errorf("%w: select one with 'nova instance select <name>'", instances.ErrNoTarget)
			}
			inst = t
		}

		var live *portal.UserInstance
		if inst.Kind == instances.KindCloud && (!local || (!printHostInstanceInfoCmd && !printInstanceIdInstanceInfoCmd && loggedIn())) {
			i, err := fetchCloudInstance(cloudID(inst, inst.Name), !local)
			switch {
			case err == nil:
				live = &i
				if inst.Host == "" {
					inst.Host = i.Host
				}
			case !local:
				return err
			default:
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: can't fetch the cloud state:", err)
			}
		}

		if printHostInstanceInfoCmd {
			fmt.Fprintln(out, inst.Host)
			return nil
		}
		if printInstanceIdInstanceInfoCmd {
			fmt.Fprintln(out, cloudID(inst, ""))
			return nil
		}

		// Nucleus keys left in the entry for older CLI versions aren't used (ADR-0018)
		inst.NucleusHost, inst.NucleusToken = "", ""
		if !showSecretsInstanceInfoCmd {
			inst = inst.Redacted()
		}
		info := map[string]any{}
		if local {
			info["instance"] = inst
		}
		if live != nil {
			info["cloud"] = live
		}
		b, err := yaml.Marshal(info)
		if err != nil {
			return err
		}
		fmt.Fprint(out, string(b))
		return nil
	},
}

var startInstanceCmd = &cobra.Command{
	Use:               "start [name]",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeInstance,
	Short:             "Start a suspended or stopped cloud instance",
	Long: `Start a suspended or stopped cloud instance.
If no name is given, the targeted instance will be started.
`,
	Example: `# start the current selected instance
$ nova instance start

# start a specific instance
$ nova instance start instancename
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		started := time.Now()

		instanceId, err := cloudTarget(args, "start")
		if err != nil {
			return err
		}

		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return err
		}

		instanceInfo, err := p.GetInstance(instanceId)
		if err != nil {
			return err
		}

		err = p.UpdateInstanceState(instanceId, portal.StateUpdateStateRunning)
		if err != nil {
			return err
		}

		if !createNoWaitInstance {
			fmt.Println("Waiting for instance to be in running state...")
			// portal api just checks if the kustomization is done
			if err := waitForInstanceRunning(p, instanceId, started); err != nil {
				return err
			}

			fmt.Println("Waiting for core services to be ready...")
			// now we need to check if all nova services are in running state
			to := time.Duration(timeoutCreateInstance) * time.Second
			if err := nova.WaitForAllRunning(instanceInfo.Host, started, to, checkInstanceStatusInterval); err != nil {
				return err
			}
		}

		return nil
	},
}

var stopInstanceCmd = &cobra.Command{
	Use:               "stop [name]",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeInstance,
	Short:             "Stop a running cloud instance",
	Long: `Stop a running cloud instance.
If no name is given, the targeted instance will be stopped.
`,
	Example: `# stop the current selected instance
$ nova instance stop

# stop a specific instance
$ nova instance stop instancename
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceId, err := cloudTarget(args, "stop")
		if err != nil {
			return err
		}

		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return err
		}

		return p.UpdateInstanceState(instanceId, portal.StateUpdateStateStopped)
	},
}

func init() {
	instanceCmd.AddCommand(addInstanceCmd)
	instanceCmd.AddCommand(removeInstanceCmd)
	instanceCmd.AddCommand(deleteInstanceCmd)
	instanceCmd.AddCommand(listInstancesCmd)
	instanceCmd.AddCommand(createInstancesCmd)
	instanceCmd.AddCommand(selectInstanceCmd)
	instanceCmd.AddCommand(infoInstanceCmd)
	instanceCmd.AddCommand(startInstanceCmd)
	instanceCmd.AddCommand(stopInstanceCmd)

	addInstanceCmd.Flags().StringVar(&hostAddInstanceCmd, "host", "", "Host of the NOVA instance, e.g. 192.168.1.10 or https://ipc.example.com (required)")
	addInstanceCmd.Flags().StringVar(&cellAddInstanceCmd, "cell", "", "Cell to work with on this instance (default \"cell\")")
	addInstanceCmd.Flags().StringVar(&sshUserAddInstanceCmd, "ssh-user", "", "SSH user for 'nova instance kubeconfig' on this host")
	addInstanceCmd.Flags().StringVar(&kubeconfigFrom, "kubeconfig", "", "Kubeconfig file of the host ('-' for stdin); a copy is stored for 'nova instance kubeconfig/env'")
	addInstanceCmd.Flags().BoolVar(&selectAddInstanceCmd, "select", false, "Select the new instance (it is always selected if no instance is selected yet)")

	infoInstanceCmd.Flags().BoolVar(&printHostInstanceInfoCmd, "host", false, "Only return the host of the instance instead of the full information.")
	infoInstanceCmd.Flags().BoolVar(&printInstanceIdInstanceInfoCmd, "instanceId", false, "Only return the instanceId of the instance instead of the full information.")
	infoInstanceCmd.Flags().BoolVar(&showSecretsInstanceInfoCmd, "show-secrets", false, "Print tokens instead of redacting them.")

	createInstancesCmd.Flags().IntVar(&ttlMinutesCreateInstanceCmd, "ttl", 0, "(Wandelbots Internal) Instance TTL in minutes. If not set or set to 0, the portal.wandelbots.io default will be used.")
	createInstancesCmd.Flags().StringVarP(&versionCreateInstanceCmd, "version", "v", "", "(Wandelbots Internal) If specified will override the default service-manager version for the instance. If not specified, default from applications will be used.")
	createInstancesCmd.Flags().StringVar(&chartCreateInstanceCmd, "chart", "", "(Wandelbots Internal) If specified will override the default service-manager chart for the instance. If not specified, default from portal.wandelbots.io will be used.")
	createInstancesCmd.Flags().StringVarP(&nameCreateInstanceCmd, "name", "n", "", "Name of the instance.")
	createInstancesCmd.Flags().BoolVarP(&createNoWaitInstance, "no-wait", "w", false, "Do not wait for the instance to be in running state.")
	createInstancesCmd.Flags().IntVarP(&timeoutCreateInstance, "timeout", "t", defaultWaitTime, "Timeout in seconds for the instance to be in running state.")

	listInstancesCmd.Flags().BoolVar(&listCloudInstances, "cloud", false, "List the instances on portal.wandelbots.io (previous output format).")
	listInstancesCmd.Flags().BoolVarP(&listVerboseInstances, "verbose", "v", false, "Print additional information about each cloud instance (implies --cloud).")

	startInstanceCmd.Flags().BoolVarP(&createNoWaitInstance, "no-wait", "w", false, "Do not wait for the instance to be in running state.")

	rootCmd.AddCommand(instanceCmd)
}

func loadRegistry() (*instances.Registry, error) {
	return instances.Load(viper.GetViper())
}

func saveRegistry(r *instances.Registry) error {
	defer resetTarget()
	return r.Save(viper.GetViper())
}

// registerCloudInstance adds (or updates) a cloud instance in the local list and
// selects it. A new entry is named after the instance id; if a local entry already has
// that name, the first free name of <id>-2, <id>-3, … is used, so a created instance
// always ends up in the list.
func registerCloudInstance(errOut io.Writer, id, host string) error {
	r, err := loadRegistry()
	if err != nil {
		return err
	}
	inst, ok := r.FindByID(instances.KindCloud, id)
	if !ok {
		inst = instances.Instance{Name: r.FreeName(id), Kind: instances.KindCloud, ID: id}
		if inst.Name != id {
			fmt.Fprintf(errOut, "the name %q is already used by another instance, added cloud instance %s as %q\n", id, id, inst.Name)
		}
	}
	inst.Host = host
	inst.Env = string(portalEnvironment)
	if err := r.Put(inst); err != nil {
		return err
	}
	r.Current = inst.Name
	return saveRegistry(r)
}

// lookupCloudEntry finds a local entry by name, or a cloud entry by its instance id.
func lookupCloudEntry(r *instances.Registry, name string) (instances.Instance, bool) {
	if inst, ok := r.Get(name); ok {
		return inst, true
	}
	return r.FindByID(instances.KindCloud, name)
}

func cloudID(inst instances.Instance, fallback string) string {
	if inst.ID != "" {
		return inst.ID
	}
	return fallback
}

// cloudTarget returns the portal instance id for start/stop: the named entry (or
// cloud id), or the targeted instance.
func cloudTarget(args []string, verb string) (string, error) {
	if len(args) > 0 {
		name := strings.TrimSpace(args[0])
		r, err := loadRegistry()
		if err != nil {
			return "", err
		}
		inst, ok := lookupCloudEntry(r, name)
		if !ok {
			return name, nil
		}
		if err := requireKind(inst, instances.KindCloud, verb); err != nil {
			return "", err
		}
		if err := useInstanceEnv(inst); err != nil {
			return "", err
		}
		return cloudID(inst, name), nil
	}

	t, err := ensureTarget()
	if err != nil {
		return "", err
	}
	if t.Name == "" {
		return "", fmt.Errorf("%w: select one with 'nova instance select <name>'", instances.ErrNoTarget)
	}
	if err := requireKind(t, instances.KindCloud, verb); err != nil {
		return "", err
	}
	return cloudID(t, t.Name), nil
}

// requireKind returns an error with a hint if inst isn't of the given kind.
func requireKind(inst instances.Instance, kind instances.Kind, verb string) error {
	if inst.Kind == kind {
		return nil
	}
	hint := ""
	switch inst.Kind {
	case instances.KindHost:
		hint = "; use 'nova instance remove " + inst.Name + "' to forget it"
	case instances.KindK8sRun:
		hint = "; use 'nova k8srun destroy " + inst.Name + "' for k8srun instances"
	case instances.KindCloud:
		hint = "; use 'nova instance delete " + inst.Name + "' for cloud instances"
	}
	return fmt.Errorf("can't %s instance %q: it is a %s instance, '%s' only works for %s instances%s",
		verb, inst.Name, inst.Kind, verb, kind, hint)
}

// fetchCloudInstance looks up a cloud instance; best-effort lookups (interactive=false)
// never start the interactive login.
func fetchCloudInstance(id string, interactive bool) (portal.UserInstance, error) {
	newPortal := portal.NewPortalNonInteractive
	if interactive {
		newPortal = portal.NewPortal
	}
	p, err := newPortal(portalEnvironment)
	if err != nil {
		return portal.UserInstance{}, err
	}
	return p.GetInstance(id)
}

func loggedIn() bool {
	token, err := oauth.LoadToken()
	return err == nil && token.OAuth2Token.AccessToken != ""
}

func completeLocalInstance(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	r, err := loadRegistry()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var comp []string
	for _, i := range r.Instances {
		if strings.HasPrefix(i.Name, toComplete) {
			comp = append(comp, i.Name+"\t"+string(i.Kind)+" "+i.Host)
		}
	}
	return comp, cobra.ShellCompDirectiveNoFileComp
}

// completeInstance completes local entries plus, when logged in, cloud instance ids.
func completeInstance(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	comp, directive := completeLocalInstance(cmd, args, toComplete)
	if len(args) > 0 || !loggedIn() {
		return comp, directive
	}

	// PreRun seems not to be executed for completion thats why we do it explicitly here
	setPortalEnvironment(cmd, args)
	p, err := portal.NewPortalNonInteractive(portalEnvironment)
	if err != nil {
		return comp, directive
	}
	cloud, err := p.ListInstances()
	if err != nil {
		return comp, directive
	}
	r, _ := loadRegistry()
	for _, instance := range cloud {
		if r != nil {
			if _, ok := r.FindByID(instances.KindCloud, instance.InstanceId); ok {
				continue
			}
		}
		if strings.HasPrefix(instance.InstanceId, toComplete) {
			comp = append(comp, instance.InstanceId+"\tcloud "+instance.Host)
		}
	}
	return comp, directive
}

// setPortalEnvironment makes sure the portal environment is set when the root pre-run
// hook didn't run (shell completion).
func setPortalEnvironment(cmd *cobra.Command, args []string) {
	portalEnvironment = oauth.ActiveEnvironment()
}

func waitForInstanceRunning(p *portal.Portal, instanceId string, started time.Time) error {
	for {
		time.Sleep(checkInstanceStatusInterval)

		i, err := p.GetInstance(instanceId)
		if err != nil {
			return err
		}

		if i.Status == portal.UserInstanceStatusRunning {
			return nil
		}

		if time.Since(started).Seconds() > float64(timeoutCreateInstance) {
			return errors.New("timeout reached while waiting for instance to be in running state")
		}
	}
}
