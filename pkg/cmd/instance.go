package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"github.com/wandelbotsgmbh/nova-cli/pkg/portal"
	"gopkg.in/yaml.v3"
)

// flags
var (
	listVerboseInstances bool
	createNoWaitInstance bool
	// timeout in seconds
	timeoutCreateInstance       int
	nameCreateInstanceCmd       string
	ttlMinutesCreateInstanceCmd int
	versionCreateInstanceCmd    string
	chartCreateInstanceCmd      string

	printHostInstanceInfoCmd       bool
	printInstanceIdInstanceInfoCmd bool
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

var instanceCmd = &cobra.Command{
	Use:              "instance",
	PersistentPreRun: setPortalEnvironment,
	Short:            "Manage portal.wandelbots.io instances",
	Long:             `Manage portal.wandelbots.io instances`,
}

var deleteInstanceCmd = &cobra.Command{
	Use:               "delete <name>",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeInstance,
	Short:             "Deletes an instance",
	Long:              `Deletes an existing instance.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		instance := args[0]
		if instance == "" {
			return errors.New("instance name must not be empty")
		}
		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return err
		}

		err = p.DeleteInstance(instance)
		if err != nil {
			return err
		}

		viper.Set("host", "")
		viper.Set("virtualid", "")
		return viper.WriteConfig()
	},
}

var listInstancesCmd = &cobra.Command{
	Use:   "list",
	Args:  cobra.NoArgs,
	Short: "Lists all instances",
	Long:  `Lists all instances.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return err
		}
		instances, err := p.ListInstances()
		if err != nil {
			return err
		}

		for _, instance := range instances {
			if listVerboseInstances {
				out, err := yaml.Marshal(instance)
				if err != nil {
					return err
				}
				fmt.Println(string(out) + "\n")
			} else {
				fmt.Println(instance.InstanceId, instance.Host, instance.Status, instance.SandboxName)
			}
		}
		return nil
	},
}

var createInstancesCmd = &cobra.Command{
	Use:   "create",
	Args:  cobra.NoArgs,
	Short: "Creates a new instance",
	Long:  `Creates a new instance`,
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

		viper.Set("virtualid", instance.InstanceId)
		viper.Set("host", instance.Host)

		err = viper.WriteConfig()

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
	ValidArgsFunction: completeInstance,
	Short:             "Selects an existing cloud instance",
	Long:              `Selects an existing cloud instance`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return err
		}

		instance, err := p.GetInstance(strings.TrimSpace(args[0]))
		if err != nil {
			return err
		}

		viper.Set("host", instance.Host)
		viper.Set("virtualid", instance.InstanceId)
		return viper.WriteConfig()
	},
}

var infoInstanceCmd = &cobra.Command{
	Use:               "info [name]",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeInstance,
	Short:             "Show information about an existing cloud instance",
	Long: `Show information about an existing cloud instance.
It is possible to only return the host or the instanceId of the instance by using the --host or --instanceId flags (e.g. to write these information to a env vars).`,
	Example: `
# e.g. after creating an instance, you can use the following command to write the host to an env var
$ nova instance create
$ export NOVA_HOST=$(nova instance info --host)

# if you want to work with multiple instances, you can save the instanceID and use it to change the selected instance
$ nova instance create
$ export NOVA_INSTANCE_1=$(nova instance info --instanceId)

$ nova instance create
$ export NOVA_INSTANCE_2=$(nova instance info --instanceId)

$ nova instance select $NOVA_INSTANCE_1
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return err
		}

		instanceId := viper.GetString("virtualid")
		if len(args) > 0 {
			instanceId = strings.TrimSpace(args[0])
		}

		instance, err := p.GetInstance(instanceId)
		if err != nil {
			return err
		}

		if printHostInstanceInfoCmd {
			fmt.Println(instance.Host)
			return nil
		}

		if printInstanceIdInstanceInfoCmd {
			fmt.Println(instance.InstanceId)
			return nil
		}

		out, err := yaml.Marshal(instance)
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	},
}

var startInstanceCmd = &cobra.Command{
	Use:               "start [name]",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeInstance,
	Short:             "Start a suspended or stopped instance",
	Long: `Start a suspended or stopped instance.
If no name is given, the currently selected instance will be started.
`,
	Example: `# start the current selected instance
$ nova instance start

# start a specific instance
$ nova instance start instancename
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		started := time.Now()

		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return err
		}

		instanceId := viper.GetString("virtualid")
		if len(args) > 0 {
			instanceId = strings.TrimSpace(args[0])
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
	Short:             "Stop a running instance",
	Long: `Stop a running instance.
If no name is given, the currently selected instance will be stopped.
`,
	Example: `# stop the current selected instance
$ nova instance stop

# stop a specific instance
$ nova instance stop instancename
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return err
		}

		instanceId := viper.GetString("virtualid")
		if len(args) > 0 {
			instanceId = strings.TrimSpace(args[0])
		}

		return p.UpdateInstanceState(instanceId, portal.StateUpdateStateStopped)
	},
}

func init() {
	instanceCmd.AddCommand(deleteInstanceCmd)
	instanceCmd.AddCommand(listInstancesCmd)
	instanceCmd.AddCommand(createInstancesCmd)
	instanceCmd.AddCommand(selectInstanceCmd)
	instanceCmd.AddCommand(infoInstanceCmd)
	instanceCmd.AddCommand(startInstanceCmd)
	instanceCmd.AddCommand(stopInstanceCmd)

	infoInstanceCmd.Flags().BoolVar(&printHostInstanceInfoCmd, "host", false, "Only return the host of the instance instead of the full information.")
	infoInstanceCmd.Flags().BoolVar(&printInstanceIdInstanceInfoCmd, "instanceId", false, "Only return the instanceId of the instance instead of the full information.")

	createInstancesCmd.Flags().IntVar(&ttlMinutesCreateInstanceCmd, "ttl", 0, "(Wandelbots Internal) Instance TTL in minutes. If not set or set to 0, the portal.wandelbots.io default will be used.")
	createInstancesCmd.Flags().StringVarP(&versionCreateInstanceCmd, "version", "v", "", "(Wandelbots Internal) If specified will override the default service-manager version for the instance. If not specified, default from applications will be used.")
	createInstancesCmd.Flags().StringVar(&chartCreateInstanceCmd, "chart", "", "(Wandelbots Internal) If specified will override the default service-manager chart for the instance. If not specified, default from portal.wandelbots.io will be used.")
	createInstancesCmd.Flags().StringVarP(&nameCreateInstanceCmd, "name", "n", "", "Name of the instance.")
	createInstancesCmd.Flags().BoolVarP(&createNoWaitInstance, "no-wait", "w", false, "Do not wait for the instance to be in running state.")
	createInstancesCmd.Flags().IntVarP(&timeoutCreateInstance, "timeout", "t", defaultWaitTime, "Timeout in seconds for the instance to be in running state.")

	listInstancesCmd.Flags().BoolVarP(&listVerboseInstances, "verbose", "v", false, "Print additional information about each instance.")

	startInstanceCmd.Flags().BoolVarP(&createNoWaitInstance, "no-wait", "w", false, "Do not wait for the instance to be in running state.")

	rootCmd.AddCommand(instanceCmd)
}

func completeInstance(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// PreRun seems not to be executed for completion thats why we do it explicitly here
	setPortalEnvironment(cmd, args)
	p, err := portal.NewPortal(portalEnvironment)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	instances, err := p.ListInstances()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var comp []string
	for _, instance := range instances {
		if toComplete == "" || strings.HasPrefix(instance.InstanceId, toComplete) {
			comp = append(comp, instance.InstanceId)
		}
	}

	return comp, cobra.ShellCompDirectiveNoFileComp
}

func setPortalEnvironment(cmd *cobra.Command, args []string) {
	if token, err := oauth.LoadToken(); err == nil {
		portalEnvironment = token.AuthEnvironment
	}
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
