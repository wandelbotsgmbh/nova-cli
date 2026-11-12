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
  cloud   the portal doesn't list the instance for the account logged in to the
          entry's environment (an instance of another account counts as gone)
  k8srun  the cluster service doesn't know the cluster id, or has a kubeconfig with
          another CA for it than the stored one ('nova k8srun prune' also asks the
          cluster); never if the stored kubeconfig has more than one context (remove
          it explicitly)
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
		var unchecked []pruneEntryJSON
		skipped := 0

		// cloud entries: one portal listing per environment, in that environment
		byEnv := map[oauth.AuthEnvironment][]instances.Instance{}
		for _, i := range r.Instances {
			if i.Kind != instances.KindCloud {
				continue
			}
			env, err := oauth.ParseEnvironment(i.Env)
			if i.Env == "" {
				// as every other command does (resolveEnv)
				env, err = oauth.DefaultEnvironment(), nil
			}
			if err != nil || i.ID == "" {
				reason := fmt.Sprintf("can't check it (environment %q, id %q)", i.Env, i.ID)
				fmt.Fprintf(errOut, "keeping %q: %s\n", i.Name, reason)
				unchecked = append(unchecked, pruneEntryJSON{Name: i.Name, Kind: string(i.Kind), Reason: reason})
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
					reason := fmt.Sprintf("can't list the %s instances: %v", env, err)
					fmt.Fprintf(errOut, "keeping %q: %s\n", i.Name, reason)
					unchecked = append(unchecked, pruneEntryJSON{Name: i.Name, Kind: string(i.Kind), Reason: reason})
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
				unchecked = append(unchecked, pruneEntryJSON{Name: i.Name, Kind: string(i.Kind), Reason: "no cluster id"})
				continue
			}
			if why := kubeconfigShared(i); why != "" {
				fmt.Fprintf(errOut, "keeping %q: %s; remove it explicitly with 'nova instance remove %s' (deletes that kubeconfig)\n", i.Name, why, i.Name)
				unchecked = append(unchecked, pruneEntryJSON{Name: i.Name, Kind: string(i.Kind), Reason: why})
				// a rule, not a failed check: it doesn't fail the command (as in 'k8srun prune')
				skipped++
				continue
			}
			isGone, reason, err := clusterGone(i, false)
			if err != nil {
				fmt.Fprintf(errOut, "keeping %q: %v\n", i.Name, err)
				unchecked = append(unchecked, pruneEntryJSON{Name: i.Name, Kind: string(i.Kind), Reason: err.Error()})
				continue
			}
			if isGone {
				fmt.Fprintf(errOut, "%q is gone: %s\n", i.Name, reason)
				gone = append(gone, i.Name)
			}
		}

		if err := removeGoneEntries(cmd, r, gone, instancePruneDryRun, instancePruneOutput, unchecked); err != nil {
			return err
		}
		if n := len(unchecked) - skipped; n > 0 {
			return fmt.Errorf("%d instance(s) could not be checked and were kept", n)
		}
		return nil
	},
}

// removeGoneEntries prints and (unless dryRun) removes the named entries with their
// stored files, and tells when the selected one was among them. With -o json, it prints
// the removed and the unchecked entries as pruneJSON once the entries are removed.
func removeGoneEntries(cmd *cobra.Command, r *instances.Registry, gone []string, dryRun bool, o *outputFormat, unchecked []pruneEntryJSON) error {
	result := newPruneJSON(dryRun)
	result.Unchecked = append(result.Unchecked, unchecked...)
	for _, name := range gone {
		i, _ := r.Get(name)
		result.Removed = append(result.Removed, pruneEntryJSON{Name: name, Kind: string(i.Kind)})
	}
	if *o != outputJSON {
		verb := "removed"
		if dryRun {
			verb = "would remove"
		}
		for _, name := range gone {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", verb, name)
		}
	}
	if err := removeEntries(cmd, r, gone, dryRun); err != nil {
		return err
	}
	if *o == outputJSON {
		return writeJSON(cmd, result)
	}
	return nil
}

func removeEntries(cmd *cobra.Command, r *instances.Registry, gone []string, dryRun bool) error {
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

// -o/--output of 'instance prune'; assigned in init to avoid an initialization cycle
var instancePruneOutput *outputFormat

func init() {
	instancePruneOutput = addOutputFlag(pruneInstancesCmd)
	instanceCmd.AddCommand(pruneInstancesCmd)
	pruneInstancesCmd.Flags().BoolVar(&instancePruneDryRun, "dry-run", false, "Only print the entries that would be removed")
}
