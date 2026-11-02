package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
)

// instanceFlag is bound to the global --instance flag.
var instanceFlag string

// annotationNoTarget marks commands that manage the registry themselves and must
// run even if --instance or NOVA_INSTANCE name an entry that doesn't exist (yet).
const annotationNoTarget = "nova/no-target"

var (
	target         instances.Instance
	targetSource   = instances.SourceNone
	targetResolved bool
)

// resolveTarget picks the instance this invocation works against. It never writes
// the config, so concurrent sessions with different overrides don't interfere.
func resolveTarget(cmd *cobra.Command, _ []string) error {
	if skipsTarget(cmd) {
		return nil
	}
	_, err := ensureTarget()
	return err
}

func skipsTarget(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if _, ok := c.Annotations[annotationNoTarget]; ok {
			return true
		}
		if c.Name() == "completion" || c.Name() == cobra.ShellCompRequestCmd || c.Name() == cobra.ShellCompNoDescRequestCmd {
			return true
		}
	}
	return false
}

func ensureTarget() (instances.Instance, error) {
	if targetResolved {
		return target, nil
	}
	r, err := instances.Load(viper.GetViper())
	if err != nil {
		return instances.Instance{}, err
	}
	t, src, err := r.Resolve(instanceFlag, os.Getenv(instances.EnvVar))
	if err != nil {
		return instances.Instance{}, err
	}
	target, targetSource, targetResolved = t, src, true
	return target, nil
}

// currentTarget returns the resolved target, resolving it lazily for code paths
// that don't run the pre-run hook (shell completion). Errors yield an empty target.
func currentTarget() instances.Instance {
	t, _ := ensureTarget()
	return t
}

func currentHost() string { return currentTarget().Host }

func currentCell() string { return currentTarget().Cell() }

// resetTarget forgets the resolved target after the registry changed.
func resetTarget() {
	target, targetSource, targetResolved = instances.Instance{}, instances.SourceNone, false
}

// targetOverride is the --instance / NOVA_INSTANCE override, if any.
func targetOverride() string {
	if instanceFlag != "" {
		return instanceFlag
	}
	return os.Getenv(instances.EnvVar)
}
