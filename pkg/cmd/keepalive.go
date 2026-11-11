package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"github.com/wandelbotsgmbh/nova-cli/pkg/portal"
)

const (
	keepAliveMinCheck = 30 * time.Second
	keepAliveMaxCheck = 5 * time.Minute
)

var keepAliveMargin time.Duration

// -o/--output of 'instance extend'; assigned in init to avoid an initialization cycle
var extendOutput *outputFormat

var extendInstanceCmd = &cobra.Command{
	Use:               "extend [name]",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeInstance,
	Short:             "Extend the lifetime of a cloud instance or k8srun cluster",
	Long: `Extend the lifetime of a cloud instance or k8srun cluster once, so it doesn't expire.
If no name is given, the targeted instance is extended. Prints the new expiry (RFC 3339).

A cloud instance then expires after the portal's fixed extension period (7 days from now
on portal.wandelbots.io, 24 hours on the dev portal), a k8srun cluster 12 hours from now.

To keep an instance alive while you or an agent work with it, use 'nova instance keep-alive'.`,
	Example: `$ nova instance extend
$ nova instance extend my-instance`,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, inst, _, err := lifetimeTarget(cmd, args, "extend")
		if err != nil {
			return err
		}
		before, err := c.lifetime()
		if err != nil {
			return err
		}
		if err := c.extend(); err != nil {
			return err
		}
		after, err := c.lifetime()
		if err != nil {
			return err
		}
		extended := after.expires.After(before.expires)
		if !extended {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: the request was accepted but didn't move the expiry; the instance may have reached its maximum lifetime")
		}
		if *extendOutput == outputJSON {
			return writeJSON(cmd, newLifetimeJSON(inst, after.expires, extended))
		}
		fmt.Fprintln(cmd.OutOrStdout(), after.expires.Format(time.RFC3339))
		return nil
	},
}

var keepAliveInstanceCmd = &cobra.Command{
	Use:               "keep-alive [name] [-- command [args...]]",
	Args:              keepAliveArgs,
	ValidArgsFunction: completeInstance,
	Short:             "Keep a cloud instance or k8srun cluster from expiring while you work with it",
	Long: `Keep a cloud instance or k8srun cluster from expiring while you or an agent work with
it, like caffeinate does for a Mac. Extends the lifetime whenever less than --margin is
left. If no name is given, the targeted instance is kept alive.

Without a command, runs until interrupted (Ctrl-C or SIGTERM). With a command after
'--', runs it and keeps the instance alive until it exits; nova then exits with the
command's exit code.

A stopped cloud instance is not started: the command waits until it runs again. Once the
instance no longer exists, keep-alive stops (with a command, the command keeps running).
Progress goes to stderr.`,
	Example: `# keep the targeted instance alive until Ctrl-C
$ nova instance keep-alive

# keep it alive in the background of an agent session
$ nova instance keep-alive my-instance &

# keep it alive while a command runs
$ nova instance keep-alive -- claude
$ nova instance keep-alive my-cluster -- skaffold dev`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if keepAliveMargin <= 0 {
			return errors.New("--margin must be positive")
		}
		name, command := splitAtDash(cmd, args)
		c, _, label, err := lifetimeTarget(cmd, name, "keep-alive")
		if err != nil {
			return err
		}
		k := &keeper{client: c, name: label, margin: keepAliveMargin, log: cmd.ErrOrStderr(), now: time.Now}

		if len(command) == 0 {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			fmt.Fprintf(k.log, "keeping %s alive, stop with Ctrl-C\n", label)
			return k.run(ctx)
		}
		return k.runWith(cmd, command)
	},
}

func keepAliveArgs(cmd *cobra.Command, args []string) error {
	name, command := splitAtDash(cmd, args)
	if len(name) > 1 {
		return fmt.Errorf("accepts at most 1 instance name before '--', received %d", len(name))
	}
	if cmd.ArgsLenAtDash() >= 0 && len(command) == 0 {
		return errors.New("no command after '--'")
	}
	return nil
}

func splitAtDash(cmd *cobra.Command, args []string) (before, after []string) {
	if n := cmd.ArgsLenAtDash(); n >= 0 {
		return args[:n], args[n:]
	}
	return args, nil
}

// lifetimeID is the id lifetimeTarget extends: the portal instance id or the cluster id.
func lifetimeID(inst instances.Instance) string {
	if inst.Kind == instances.KindCloud {
		return cloudID(inst, inst.Name)
	}
	return inst.ID
}

// lifetimeTarget returns the lifetime client for the named instance (a local entry, else
// a cloud instance id) or the targeted one, the instance, and a label for messages.
func lifetimeTarget(cmd *cobra.Command, args []string, verb string) (lifetimeClient, instances.Instance, string, error) {
	var inst instances.Instance
	if len(args) > 0 {
		name := strings.TrimSpace(args[0])
		r, err := loadRegistry()
		if err != nil {
			return nil, inst, "", err
		}
		var ok bool
		if inst, ok = lookupCloudEntry(r, name); !ok {
			inst = instances.Instance{Name: name, Kind: instances.KindCloud, ID: name}
		} else if err := useInstanceEnv(inst); err != nil {
			return nil, inst, "", err
		}
	} else {
		t, err := ensureTarget()
		if err != nil {
			return nil, inst, "", err
		}
		if t.Name == "" {
			return nil, inst, "", fmt.Errorf("%w: select one with 'nova instance select <name>'", instances.ErrNoTarget)
		}
		inst = t
	}

	switch inst.Kind {
	case instances.KindCloud:
		id := cloudID(inst, inst.Name)
		p, err := portal.NewPortal(portalEnvironment)
		if err != nil {
			return nil, inst, "", err
		}
		env := portalEnvironment
		return &cloudLifetime{
			portal:  p,
			id:      id,
			refresh: func() error { return oauth.RefreshToken(cmd.Context(), env) },
		}, inst, "cloud instance " + id, nil
	case instances.KindK8sRun:
		if inst.ID == "" {
			return nil, inst, "", fmt.Errorf("cluster %q has no id; use 'nova instance remove %s' to forget it", inst.Name, inst.Name)
		}
		return &k8srunLifetime{id: inst.ID, now: time.Now}, inst, "cluster " + inst.Name, nil
	}
	return nil, inst, "", fmt.Errorf("can't %s instance %q: it is a %s instance, '%s' only works for cloud and k8srun instances",
		verb, inst.Name, inst.Kind, verb)
}

// errInstanceGone means there is nothing left to keep alive.
var errInstanceGone = errors.New("the instance no longer exists")

type lifetimeState struct {
	running bool
	status  string
	// zero if unknown
	expires time.Time
}

type lifetimeClient interface {
	lifetime() (lifetimeState, error)
	extend() error
}

type cloudLifetime struct {
	portal  *portal.Portal
	id      string
	refresh func() error
}

func (c *cloudLifetime) lifetime() (lifetimeState, error) {
	// the login outlives the 24 h access token only if it is refreshed
	if err := c.refresh(); err != nil {
		return lifetimeState{}, err
	}
	inst, err := c.portal.GetInstance(c.id)
	if errors.Is(err, portal.ErrInstanceNotFound) {
		return lifetimeState{}, errInstanceGone
	}
	if err != nil {
		return lifetimeState{}, err
	}
	// not in the spec's status enum, but reported until the instance disappears
	if inst.Status == "deleting" {
		return lifetimeState{}, errInstanceGone
	}
	return lifetimeState{
		running: inst.Status == portal.UserInstanceStatusRunning,
		status:  string(inst.Status),
		expires: time.Unix(int64(inst.ExpiresAt), 0),
	}, nil
}

func (c *cloudLifetime) extend() error {
	return c.portal.ExtendInstance(c.id)
}

// k8srunLifetime keeps track of the expiry itself: the cluster service can set it but
// doesn't report it.
type k8srunLifetime struct {
	id      string
	now     func() time.Time
	expires time.Time
}

func (c *k8srunLifetime) lifetime() (lifetimeState, error) {
	exists, err := clusterExists(c.id)
	if err != nil {
		return lifetimeState{}, err
	}
	if !exists {
		return lifetimeState{}, errInstanceGone
	}
	return lifetimeState{running: true, status: "running", expires: c.expires}, nil
}

func (c *k8srunLifetime) extend() error {
	// setting the maximum never shortens: no cluster lives longer than that from now
	if err := setClusterExpiry(c.id, k8srunMaxMinutes); err != nil {
		return err
	}
	c.expires = c.now().Add(k8srunMaxMinutes * time.Minute)
	return nil
}

// keeper extends an instance's lifetime whenever less than margin is left.
type keeper struct {
	client     lifetimeClient
	name       string
	margin     time.Duration
	log        io.Writer
	now        func() time.Time
	lastStatus string
}

func (k *keeper) run(ctx context.Context) error {
	for {
		d, err := k.check()
		if err != nil {
			return err
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}

// runWith keeps the instance alive while command runs and returns its exit code as error.
func (k *keeper) runWith(cmd *cobra.Command, command []string) error {
	// make sure the instance survives until the first regular check
	delay, err := k.check()
	if err != nil {
		return err
	}

	child := debug.Command(command[0], command[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
	// Ctrl-C reaches the child through the terminal's process group: survive it and wait
	// for the child. SIGTERM is meant for nova, pass it on.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	if err := child.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()

	t := time.NewTimer(delay)
	defer t.Stop()
	for {
		select {
		case err := <-done:
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return exitCodeError{code: exit.ExitCode()}
			}
			return err
		case s := <-sigs:
			if s != os.Interrupt {
				_ = child.Process.Signal(s)
			}
		case <-t.C:
			d, err := k.check()
			if err != nil {
				fmt.Fprintf(k.log, "%v, stopped keeping it alive\n", err)
				continue
			}
			t.Reset(d)
		}
	}
}

// check extends the instance if needed and returns the delay until the next check.
// Failures are reported and retried; only errInstanceGone is returned.
func (k *keeper) check() (time.Duration, error) {
	st, err := k.client.lifetime()
	if errors.Is(err, errInstanceGone) {
		return 0, fmt.Errorf("%s: %w", k.name, err)
	}
	if err != nil {
		fmt.Fprintf(k.log, "warning: can't read the lifetime of %s: %v\n", k.name, err)
		return keepAliveMinCheck, nil
	}
	if st.status != k.lastStatus {
		switch {
		case st.status == string(portal.UserInstanceStatusStopped):
			fmt.Fprintf(k.log, "%s is stopped, waiting until it runs (start it with 'nova instance start')\n", k.name)
		case !st.running:
			fmt.Fprintf(k.log, "%s is %s, waiting until it runs\n", k.name, st.status)
		case k.lastStatus != "":
			fmt.Fprintf(k.log, "%s is running again\n", k.name)
		}
		k.lastStatus = st.status
	}
	if !st.running {
		return keepAliveMaxCheck, nil
	}

	if left := st.expires.Sub(k.now()); left > k.margin {
		return clampCheck(left - k.margin), nil
	}
	if err := k.client.extend(); err != nil {
		fmt.Fprintf(k.log, "warning: can't extend %s: %v\n", k.name, err)
		return keepAliveMinCheck, nil
	}
	after, err := k.client.lifetime()
	if err != nil {
		fmt.Fprintf(k.log, "warning: extended %s, but can't read the new expiry: %v\n", k.name, err)
		return keepAliveMinCheck, nil
	}
	if !after.expires.After(st.expires) {
		fmt.Fprintf(k.log, "warning: the expiry of %s didn't move (%s); it may have reached its maximum lifetime\n", k.name, st.expires.Local().Format(time.DateTime))
		return keepAliveMaxCheck, nil
	}
	fmt.Fprintf(k.log, "extended %s until %s\n", k.name, after.expires.Local().Format(time.DateTime))
	if d := after.expires.Sub(k.now()) - k.margin; d > 0 {
		return clampCheck(d), nil
	}
	// extensions are shorter than --margin: don't extend on every check
	return keepAliveMaxCheck, nil
}

func clampCheck(d time.Duration) time.Duration {
	return min(max(d, keepAliveMinCheck), keepAliveMaxCheck)
}

func init() {
	instanceCmd.AddCommand(extendInstanceCmd)
	instanceCmd.AddCommand(keepAliveInstanceCmd)
	extendOutput = addOutputFlag(extendInstanceCmd)
	keepAliveInstanceCmd.Flags().DurationVar(&keepAliveMargin, "margin", 15*time.Minute, "Extend the lifetime when less than this is left")
}
