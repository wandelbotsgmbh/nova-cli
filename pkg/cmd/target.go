package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
)

// instanceFlag is bound to the global --instance flag.
var instanceFlag string

// envFlag is bound to the global --env flag (portal environment).
var envFlag string

// envVarName overrides the portal environment for one invocation.
const envVarName = "NOVA_ENV"

// envSource says where the portal environment of this invocation was chosen.
var envSource = "default"

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
		// registry commands still need the portal environment
		return applyEnv(instances.Instance{})
	}
	t, err := ensureTarget()
	if err != nil {
		return err
	}
	return applyEnv(t)
}

// applyEnv sets the portal environment for this invocation (see resolveEnv).
func applyEnv(t instances.Instance) error {
	env, src, err := resolveEnv(t)
	if err != nil {
		return err
	}
	oauth.SetActiveEnvironment(env)
	portalEnvironment, envSource = env, src
	return nil
}

// resolveEnv picks the portal environment: --env, then NOVA_ENV, then the environment
// of a cloud target, then the default (last 'nova login', else prod). An explicit
// environment that contradicts the cloud target's is an error, so a login is never
// sent to an instance of another environment.
func resolveEnv(t instances.Instance) (oauth.AuthEnvironment, string, error) {
	explicit, src := envFlag, "flag"
	if explicit == "" {
		explicit, src = os.Getenv(envVarName), "env"
	}
	if explicit != "" {
		env, err := oauth.ParseEnvironment(explicit)
		if err != nil {
			return "", "", err
		}
		if t.Kind == instances.KindCloud && t.Env != "" && t.Env != string(env) {
			return "", "", fmt.Errorf("environment %q (from %s) doesn't match instance %q, which is in %q", env, src, t.Name, t.Env)
		}
		return env, src, nil
	}
	if t.Kind == instances.KindCloud && t.Env != "" {
		env, err := oauth.ParseEnvironment(t.Env)
		if err != nil {
			return "", "", fmt.Errorf("instance %q: %w", t.Name, err)
		}
		return env, "instance", nil
	}
	return oauth.DefaultEnvironment(), "default", nil
}

// useInstanceEnv switches the portal environment to the one of a cloud entry that
// isn't the target (e.g. 'nova instance stop <name>'), unless --env / NOVA_ENV set
// another one.
func useInstanceEnv(inst instances.Instance) error {
	if inst.Kind != instances.KindCloud {
		return nil
	}
	return applyEnv(inst)
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
