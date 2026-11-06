package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"github.com/wandelbotsgmbh/nova-cli/pkg/portal"
)

var instancePruneDryRun bool

// listPortalInstancesIn lists the portal instances of env. Replaced in tests.
var listPortalInstancesIn = func(env oauth.AuthEnvironment) ([]portal.UserInstance, error) {
	p, err := portal.NewPortalNonInteractive(env)
	if err != nil {
		return nil, err
	}
	return p.ListInstances()
}

var pruneInstancesCmd = &cobra.Command{
	Use:         "prune",
	Args:        cobra.NoArgs,
	Annotations: noTarget,
	Short:       "Remove instances that no longer exist from the local list",
	Long: `Remove entries of instances that no longer exist from the local list.

Only a definite answer removes an entry:
  cloud   the portal doesn't list the instance in the entry's environment
  k8srun  the cluster service doesn't know the cluster id (as 'nova k8srun prune')
  host    never removed: an unreachable host can't be told apart from one that is off

Entries that can't be checked (no login for their environment, network error, any other
answer) are kept, and the command fails so that scripts notice.`,
	Example: `$ nova instance prune --dry-run
$ nova instance prune`,
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := loadRegistry()
		if err != nil {
			return err
		}
		errOut := cmd.ErrOrStderr()
		var gone []string
		unchecked := 0

		// cloud entries: one portal listing per environment, in that environment
		byEnv := map[oauth.AuthEnvironment][]instances.Instance{}
		for _, i := range r.Instances {
			if i.Kind != instances.KindCloud {
				continue
			}
			env, err := oauth.ParseEnvironment(i.Env)
			if i.Env == "" {
				env, err = oauth.AuthProd, nil
			}
			if err != nil || i.ID == "" {
				fmt.Fprintf(errOut, "keeping %q: can't check it (environment %q, id %q)\n", i.Name, i.Env, i.ID)
				unchecked++
				continue
			}
			byEnv[env] = append(byEnv[env], i)
		}
		envs := make([]string, 0, len(byEnv))
		for env := range byEnv {
			envs = append(envs, string(env))
		}
		sort.Strings(envs)
		active := oauth.ActiveEnvironment()
		for _, e := range envs {
			env := oauth.AuthEnvironment(e)
			oauth.SetActiveEnvironment(env)
			listed, err := listPortalInstancesIn(env)
			if err != nil {
				for _, i := range byEnv[env] {
					fmt.Fprintf(errOut, "keeping %q: can't list the %s instances: %v\n", i.Name, env, err)
					unchecked++
				}
				continue
			}
			exists := map[string]bool{}
			for _, c := range listed {
				exists[c.InstanceId] = true
			}
			for _, i := range byEnv[env] {
				if !exists[i.ID] {
					gone = append(gone, i.Name)
				}
			}
		}
		oauth.SetActiveEnvironment(active)

		for _, i := range r.Instances {
			if i.Kind != instances.KindK8sRun {
				continue
			}
			if i.ID == "" {
				fmt.Fprintf(errOut, "keeping %q: no cluster id\n", i.Name)
				unchecked++
				continue
			}
			ok, err := clusterExists(i.ID)
			if err != nil {
				fmt.Fprintf(errOut, "keeping %q: %v\n", i.Name, err)
				unchecked++
				continue
			}
			if !ok {
				gone = append(gone, i.Name)
			}
		}

		if err := removeGoneEntries(cmd, r, gone, instancePruneDryRun); err != nil {
			return err
		}
		if unchecked > 0 {
			return fmt.Errorf("%d instance(s) could not be checked and were kept", unchecked)
		}
		return nil
	},
}

// removeGoneEntries prints and (unless dryRun) removes the named entries with their
// stored files, and tells when the selected one was among them.
func removeGoneEntries(cmd *cobra.Command, r *instances.Registry, gone []string, dryRun bool) error {
	verb := "removed"
	if dryRun {
		verb = "would remove"
	}
	for _, name := range gone {
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", verb, name)
	}
	if len(gone) == 0 || dryRun {
		return nil
	}
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
			if err := os.RemoveAll(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: can't remove %s: %v\n", dir, err)
			}
		}
	}
	if selected != "" && r.Current == "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "the selected instance %q was removed; select another with 'nova instance select <name>'\n", selected)
	}
	return nil
}

func init() {
	instanceCmd.AddCommand(pruneInstancesCmd)
	pruneInstancesCmd.Flags().BoolVar(&instancePruneDryRun, "dry-run", false, "Only print the entries that would be removed")
}
