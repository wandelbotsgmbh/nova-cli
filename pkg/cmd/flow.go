package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cli/browser"
	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/catalog"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/novaflow"
	"golang.org/x/term"
)

var (
	flowYes bool

	flowInstallVersion   string
	flowInstallForce     bool
	flowInstallInstaller bool
	flowNoWait           bool
	flowWaitTimeout      time.Duration

	flowGetOutput string

	flowPushID       string
	flowPushName     string
	flowPushForce    bool
	flowPushValidate bool

	flowBackupIncludeObjects bool
	flowBackupObjectPrefix   string

	flowRestoreOnly        []string
	flowRestoreOverwrite   bool
	flowRestoreSkipGlobals bool
	flowRestoreDryRun      bool
)

var flowCmd = &cobra.Command{
	Use:     "flow",
	Short:   "Manage NovaFlow and its flows on the current cell",
	Long:    flowLong,
	Example: flowExample,
}

var flowStatusCmd = &cobra.Command{
	Use:     "status",
	Short:   "Show whether and how NovaFlow is installed on the cell",
	Example: flowStatusExample,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newFlowClient()
		if err != nil {
			return err
		}
		inst, err := c.Inspect(cmd.Context())
		if err != nil {
			return err
		}
		installer := installerAppPresent(c.Cell())
		var installerStatus *novaflow.InstallerStatus
		if installer {
			installerStatus, _ = c.InstallerStatus(cmd.Context())
		}
		printFlowStatus(cmd.OutOrStdout(), c, inst, installer, installerStatus)
		if inst.Flags != nil && inst.Flags.State() == novaflow.StateFailed {
			return errors.New("the NovaFlow deployment failed")
		}
		return nil
	},
}

var flowInstallCmd = &cobra.Command{
	Use:     "install",
	Short:   "Install or update NovaFlow on the cell",
	Long:    flowInstallLong,
	Example: flowInstallExample,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newFlowClient()
		if err != nil {
			return err
		}
		return installNovaflow(cmd, c, flowInstallVersion, flowInstallForce, flowInstallInstaller)
	},
}

var flowUninstallCmd = &cobra.Command{
	Use:     "uninstall",
	Short:   "Remove NovaFlow from the cell",
	Long:    flowUninstallLong,
	Example: flowUninstallExample,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newFlowClient()
		if err != nil {
			return err
		}
		inst, err := c.Inspect(cmd.Context())
		if err != nil {
			return err
		}
		viaInstaller := !inst.ViaFeatureFlag() && installerAppPresent(c.Cell())
		if !inst.ViaFeatureFlag() && !viaInstaller {
			if inst.Installed() {
				return fmt.Errorf("NovaFlow on cell %q was not installed by NOVA or the %s app (e.g. with helm): remove it the way it was installed", c.Cell(), novaflow.InstallerApp)
			}
			if inst.Flags == nil && !errors.Is(inst.FlagsErr, novaflow.ErrUnreachable) {
				return inst.FlagsErr
			}
			cmd.Printf("NovaFlow is not installed on cell %q\n", c.Cell())
			return nil
		}
		if !confirm(cmd, fmt.Sprintf("Remove NovaFlow from cell %q? Its flows stay on the cell.", c.Cell()), false) {
			return errors.New("aborted, use --yes to remove it without asking")
		}
		if viaInstaller {
			if _, err := c.InstallerUninstall(cmd.Context()); err != nil {
				return err
			}
		} else if _, err := c.SetNovaflow(cmd.Context(), false, ""); err != nil {
			return err
		}
		if flowNoWait {
			cmd.Printf("NovaFlow is being removed from cell %q\n", c.Cell())
			return nil
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), flowWaitTimeout)
		defer cancel()
		if viaInstaller {
			err = c.WaitForRemoval(ctx, 0, waitProgress(cmd))
		} else {
			err = c.WaitForDeployment(ctx, novaflow.WaitOptions{Enabled: false, Progress: waitProgress(cmd)})
		}
		if err != nil {
			return err
		}
		cmd.Printf("NovaFlow is removed from cell %q\n", c.Cell())
		if viaInstaller {
			cmd.Printf("The %s app stays installed, remove it with 'nova app delete %s'\n", novaflow.InstallerApp, novaflow.InstallerApp)
		}
		return nil
	},
}

var flowOpenCmd = &cobra.Command{
	Use:     "open",
	Short:   "Open the NovaFlow editor in a browser",
	Example: flowOpenExample,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newFlowClient()
		if err != nil {
			return err
		}
		if err := ensureNovaflow(cmd, c, true); err != nil {
			return err
		}
		u := c.AppURL(novaflow.EditorApp) + "/"
		fmt.Fprintln(cmd.OutOrStdout(), u)
		return browser.OpenURL(u)
	},
}

var flowListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List the flows of the cell",
	Example: flowListExample,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newFlowClient()
		if err != nil {
			return err
		}
		if err := ensureNovaflow(cmd, c, false); err != nil {
			return err
		}
		list, err := c.ListFlows(cmd.Context())
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tUPDATED")
		for _, f := range list.Flows {
			fmt.Fprintf(w, "%s\t%s\t%s\n", f.ID, f.Name, f.UpdatedAt.Local().Format(time.DateTime))
		}
		if err := w.Flush(); err != nil {
			return err
		}
		for _, cf := range list.Corrupted {
			fmt.Fprintf(cmd.ErrOrStderr(), "WARN: flow %s can't be read: %s\n", cf.ID, cf.Error)
		}
		return nil
	},
}

var flowGetCmd = &cobra.Command{
	Use:     "get <id>",
	Short:   "Print a flow as JSON",
	Long:    flowGetLong,
	Example: flowGetExample,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newFlowClient()
		if err != nil {
			return err
		}
		if err := ensureNovaflow(cmd, c, false); err != nil {
			return err
		}
		raw, err := c.GetFlowRaw(cmd.Context(), args[0])
		if novaflow.IsStatus(err, http.StatusNotFound) {
			return fmt.Errorf("flow %q not found on cell %q", args[0], c.Cell())
		}
		if err != nil {
			return err
		}
		out, err := indentJSON(raw)
		if err != nil {
			return err
		}
		if flowGetOutput == "" || flowGetOutput == "-" {
			_, err = cmd.OutOrStdout().Write(out)
			return err
		}
		return os.WriteFile(flowGetOutput, out, 0o644)
	},
}

var flowPushCmd = &cobra.Command{
	Use:     "push <file>",
	Short:   "Create or update a flow from a JSON file",
	Long:    flowPushLong,
	Example: flowPushExample,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		f, err := parseFlowFile(data, flowPushID, flowPushName)
		if err != nil {
			return fmt.Errorf("%s: %w", args[0], err)
		}
		c, err := newFlowClient()
		if err != nil {
			return err
		}
		if err := ensureNovaflow(cmd, c, true); err != nil {
			return err
		}
		ctx := cmd.Context()

		if flowPushValidate {
			res, err := c.ValidateTree(ctx, f.Tree)
			if err != nil {
				return fmt.Errorf("validating: %w", err)
			}
			for _, w := range res.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "WARN: %s: %s\n", w.Code, w.Message)
			}
			if !res.Valid {
				for _, e := range res.Errors {
					fmt.Fprintf(cmd.ErrOrStderr(), "ERROR: %s: %s\n", e.Code, e.Message)
				}
				return fmt.Errorf("flow %s is not valid, nothing was saved", f.ID)
			}
		}

		_, err = c.GetFlow(ctx, f.ID)
		switch {
		case novaflow.IsStatus(err, http.StatusNotFound):
			if err := c.CreateFlow(ctx, f); err != nil {
				return err
			}
			cmd.Printf("Created flow %s\n", f.ID)
			return nil
		case err != nil:
			return err
		}

		var ifRevision *int
		if !flowPushForce {
			if f.Revision == nil {
				return fmt.Errorf("flow %s exists on cell %q and the file has no revision to check it against: use --force to replace it", f.ID, c.Cell())
			}
			ifRevision = f.Revision
		}
		if err := c.UpdateFlow(ctx, f, ifRevision); err != nil {
			if errors.Is(err, novaflow.ErrFlowChanged) {
				return fmt.Errorf("%w: get it again with 'nova flow get %s', or use --force to replace it", err, f.ID)
			}
			return err
		}
		cmd.Printf("Updated flow %s\n", f.ID)
		return nil
	},
}

var flowDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Short:   "Delete a flow",
	Long:    flowDeleteLong,
	Example: flowDeleteExample,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		c, err := newFlowClient()
		if err != nil {
			return err
		}
		if err := ensureNovaflow(cmd, c, false); err != nil {
			return err
		}
		ctx := cmd.Context()

		f, err := c.GetFlow(ctx, id)
		if novaflow.IsStatus(err, http.StatusNotFound) {
			return fmt.Errorf("flow %q not found on cell %q", id, c.Cell())
		}
		if err != nil {
			return err
		}
		if !confirm(cmd, fmt.Sprintf("Delete flow %s (%s) from cell %q?", id, f.Name, c.Cell()), false) {
			return errors.New("aborted, use --yes to delete without asking")
		}
		// only delete what was confirmed: fail if the flow changed in between
		if err := c.DeleteFlow(ctx, id, f.Revision); err != nil {
			return err
		}
		cmd.Printf("Deleted flow %s\n", id)
		return nil
	},
}

var flowBackupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Back up and restore flows, globals and the cell object store",
}

var flowBackupDownloadCmd = &cobra.Command{
	Use:     "download [filename]",
	Short:   "Download a backup of the cell's flows and globals",
	Long:    flowBackupDownloadLong,
	Example: flowBackupDownloadExample,
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outname := "novaflow-backup.tar.gz"
		if len(args) > 0 {
			outname = args[0]
		}
		if _, err := os.Stat(outname); err == nil {
			return fmt.Errorf("file '%s' already exists", outname)
		}
		c, err := newFlowClient()
		if err != nil {
			return err
		}
		if err := ensureNovaflow(cmd, c, false); err != nil {
			return err
		}

		out, err := os.Create(outname)
		if err != nil {
			return err
		}
		m, err := c.Backup(cmd.Context(), out, novaflow.BackupOptions{
			IncludeObjects: flowBackupIncludeObjects,
			ObjectPrefix:   flowBackupObjectPrefix,
			Progress:       progressPrinter(cmd),
		})
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if m == nil {
			_ = os.Remove(outname)
			return err
		}
		cmd.Printf("Saved %d flows, %d globals", len(m.Flows), m.Globals)
		if flowBackupIncludeObjects {
			cmd.Printf(", %d objects", len(m.Objects))
		}
		cmd.Printf(" of cell %q to %s\n", c.Cell(), outname)
		if err != nil || *flowBackupOutput != outputJSON {
			return err
		}
		res := flowBackupResult{Cell: c.Cell(), Path: absPath(outname), Flows: len(m.Flows), Globals: m.Globals}
		if flowBackupIncludeObjects {
			n := len(m.Objects)
			res.Objects = &n
		}
		return writeJSON(cmd, res)
	},
}

// flowBackupResult is the JSON result of 'flow backup download -o json'.
type flowBackupResult struct {
	Cell    string `json:"cell"`
	Path    string `json:"path"`
	Flows   int    `json:"flows"`
	Globals int    `json:"globals"`
	// only with --include-objects
	Objects *int `json:"objects,omitempty"`
}

// -o/--output of 'flow backup download'
var flowBackupOutput *outputFormat

var flowBackupRestoreCmd = &cobra.Command{
	Use:     "restore <filename>",
	Short:   "Restore a backup to the cell",
	Long:    flowBackupRestoreLong,
	Example: flowBackupRestoreExample,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bc, err := novaflow.ReadBackup(args[0])
		if err != nil {
			return err
		}
		c, err := newFlowClient()
		if err != nil {
			return err
		}
		if err := ensureNovaflow(cmd, c, !flowRestoreDryRun); err != nil {
			return err
		}
		ctx := cmd.Context()

		if v := bc.Manifest.NovaflowVersion; v != "" {
			if f, err := c.FeatureFlags(ctx); err == nil && f.Status != nil && f.Status.DeployedVersion != "" && f.Status.DeployedVersion != v {
				fmt.Fprintf(cmd.ErrOrStderr(), "WARN: the backup was made with NovaFlow %s, cell %q runs %s\n", v, c.Cell(), f.Status.DeployedVersion)
			}
		}
		if cmd.Flags().Changed("include-objects") && len(bc.Objects) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "WARN: the backup contains no objects")
		}

		opts := novaflow.RestoreOptions{
			Only:           flowRestoreOnly,
			Overwrite:      flowRestoreOverwrite,
			SkipGlobals:    flowRestoreSkipGlobals,
			IncludeObjects: flowBackupIncludeObjects,
			DryRun:         flowRestoreDryRun,
		}
		if !flowRestoreDryRun {
			opts.Progress = progressPrinter(cmd)
		}
		items, err := c.Restore(ctx, bc, opts)
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		for _, it := range items {
			fmt.Fprintf(w, "%s\t%s\t%s\n", it.Kind, it.Key, it.Action)
		}
		if ferr := w.Flush(); err == nil {
			err = ferr
		}
		if err != nil {
			return err
		}
		if flowRestoreDryRun {
			cmd.Println("Dry run, nothing was changed")
		} else if slices.ContainsFunc(items, func(it novaflow.RestoreItem) bool { return it.Action == novaflow.Skipped }) {
			cmd.Println("Existing items were kept, use --overwrite to replace them")
		}
		return nil
	},
}

func newFlowClient() (*novaflow.Client, error) {
	return novaflow.NewClient(currentHost(), currentCell())
}

// ensureNovaflow returns nil when NovaFlow answers on the cell. Otherwise it
// explains why, and with offerInstall asks to install it.
func ensureNovaflow(cmd *cobra.Command, c *novaflow.Client, offerInstall bool) error {
	inst, err := c.Inspect(cmd.Context())
	if err != nil {
		return err
	}
	if inst.Installed() {
		return nil
	}
	if inst.Flags != nil {
		switch inst.Flags.State() {
		case novaflow.StateFailed:
			return fmt.Errorf("deploying NovaFlow on cell %q failed: %s", c.Cell(), inst.Flags.Status.Reason)
		case novaflow.StateUpdating:
			return fmt.Errorf("NovaFlow is being deployed on cell %q, try again shortly ('nova flow status')", c.Cell())
		}
		if inst.Flags.Novaflow {
			return fmt.Errorf("NovaFlow is enabled on cell %q but does not answer (%v), see 'nova flow status'", c.Cell(), inst.ProbeErr)
		}
	}
	notInstalled := fmt.Errorf("NovaFlow is not installed on cell %q, install it with 'nova flow install'", c.Cell())
	if !errors.Is(inst.ProbeErr, novaflow.ErrUnreachable) {
		// Forest answered, but with an error: not a missing installation
		return fmt.Errorf("NovaFlow on cell %q does not answer properly: %w", c.Cell(), inst.ProbeErr)
	}
	if inst.Flags == nil && !errors.Is(inst.FlagsErr, novaflow.ErrUnreachable) {
		return fmt.Errorf("%w (%v)", notInstalled, inst.FlagsErr)
	}
	if !offerInstall || !confirm(cmd, fmt.Sprintf("NovaFlow is not installed on cell %q. Install it now?", c.Cell()), true) {
		return notInstalled
	}
	return installNovaflow(cmd, c, "", false, false)
}

// installNovaflow deploys NovaFlow with service-manager's feature flag, or
// with the novaflow-installer app where that is in use, asked for
// (useInstaller) or the instance doesn't expose the feature flag API.
func installNovaflow(cmd *cobra.Command, c *novaflow.Client, version string, force, useInstaller bool) error {
	ctx := cmd.Context()
	inst, err := c.Inspect(ctx)
	if err != nil {
		return err
	}
	if inst.Flags == nil && !errors.Is(inst.FlagsErr, novaflow.ErrUnreachable) {
		return inst.FlagsErr
	}
	installer := installerAppPresent(c.Cell())
	if useInstaller || installer || inst.Flags == nil {
		if inst.ViaFeatureFlag() && !force {
			return fmt.Errorf("NovaFlow on cell %q is managed by NOVA: remove it with 'nova flow uninstall' before using the %s app (--force skips this check)", c.Cell(), novaflow.InstallerApp)
		}
		return installWithInstaller(cmd, c, inst, installer, version, force)
	}
	if version != "" && inst.Flags.Status == nil {
		return errors.New("--version needs NOVA 26.8 or newer, older versions install the NovaFlow version they ship with")
	}
	if !force {
		if inst.Installed() && !inst.Flags.Novaflow {
			return fmt.Errorf("NovaFlow already runs on cell %q, but was not installed by NOVA (e.g. with helm): remove that installation first (--force skips this check)", c.Cell())
		}
	}
	if s := inst.Flags.Status; inst.Flags.Novaflow && inst.Installed() && s != nil && s.State == novaflow.StateDeployed &&
		(version == "" || version == s.DeployedVersion || version == novaflow.DefaultVersion && inst.Flags.NovaflowVersion == "") {
		cmd.Printf("NovaFlow %s is already installed on cell %q\n", s.DeployedVersion, c.Cell())
		return nil
	}

	if _, err := c.SetNovaflow(ctx, true, version); err != nil {
		return err
	}
	if flowNoWait {
		cmd.Printf("NovaFlow is being installed on cell %q, see 'nova flow status'\n", c.Cell())
		return nil
	}
	want := version
	if want == novaflow.DefaultVersion {
		want = "" // whatever the NOVA release ships
	}
	if err := waitForNovaflow(cmd, c, novaflow.WaitOptions{Enabled: true, Version: want}); err != nil {
		return err
	}
	deployed := ""
	if f, err := c.FeatureFlags(ctx); err == nil && f.Status != nil {
		deployed = f.Status.DeployedVersion + " "
	}
	cmd.Printf("NovaFlow %sis installed on cell %q: %s/\n", deployed, c.Cell(), c.AppURL(novaflow.EditorApp))
	return nil
}

func waitForNovaflow(cmd *cobra.Command, c *novaflow.Client, opts novaflow.WaitOptions) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), flowWaitTimeout)
	defer cancel()
	opts.Progress = waitProgress(cmd)
	return c.WaitForDeployment(ctx, opts)
}

func waitProgress(cmd *cobra.Command) func(string) {
	return func(state string) { fmt.Fprintf(cmd.ErrOrStderr(), "NovaFlow is %s…\n", state) }
}

// installWithInstaller installs the novaflow-installer app from the preview
// catalog if needed and lets it deploy NovaFlow.
func installWithInstaller(cmd *cobra.Command, c *novaflow.Client, inst *novaflow.Installation, installerPresent bool, version string, force bool) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), flowWaitTimeout)
	defer cancel()
	if !installerPresent {
		if inst.Installed() && !force {
			return fmt.Errorf("NovaFlow already runs on cell %q, but was not installed by NOVA or the %s app (e.g. with helm): remove that installation first (--force skips this check)", c.Cell(), novaflow.InstallerApp)
		}
		if err := installInstallerApp(c.Cell()); err != nil {
			return fmt.Errorf("installing the %s app: %w", novaflow.InstallerApp, err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Waiting for the %s app…\n", novaflow.InstallerApp)
		if err := c.WaitForInstaller(ctx, 0); err != nil {
			return fmt.Errorf("the %s app does not start: %w", novaflow.InstallerApp, err)
		}
	}
	status, err := c.InstallerStatus(ctx)
	if err != nil {
		return err
	}
	if version == "" || version == novaflow.DefaultVersion {
		version = status.RecommendedVersion
	}
	if inst.Installed() && status.InstalledVersion == version {
		cmd.Printf("NovaFlow %s is already installed on cell %q\n", version, c.Cell())
		return nil
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Installing NovaFlow %s with the %s app…\n", version, novaflow.InstallerApp)
	if _, err := c.InstallerInstall(ctx, version); err != nil {
		return err
	}
	if flowNoWait {
		cmd.Printf("NovaFlow %s is being installed on cell %q, see 'nova flow status'\n", version, c.Cell())
		return nil
	}
	if err := c.WaitForInstallerDeployment(ctx, version, 0, waitProgress(cmd)); err != nil {
		return err
	}
	cmd.Printf("NovaFlow %s is installed on cell %q: %s/\n", version, c.Cell(), c.AppURL(novaflow.EditorApp))
	return nil
}

// installInstallerApp installs the novaflow-installer app from the preview catalog.
func installInstallerApp(cell string) error {
	cat, err := catalog.NewDefault(true)
	if err != nil {
		return err
	}
	entry, _, err := cat.GetManifest(novaflow.InstallerApp)
	if err != nil {
		return err
	}
	return installFromCatalog(currentHost(), cell, novaflow.InstallerApp, entry)
}

func installerAppPresent(cell string) bool {
	apps, err := nova.ListApps(currentHost(), cell)
	return err == nil && slices.Contains(apps, novaflow.InstallerApp)
}

func printFlowStatus(out io.Writer, c *novaflow.Client, inst *novaflow.Installation, installerApp bool, installerStatus *novaflow.InstallerStatus) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	defer w.Flush()
	fmt.Fprintf(w, "Cell:\t%s\n", c.Cell())

	how := ""
	switch {
	case inst.ViaFeatureFlag():
		how = " (managed by NOVA)"
	case installerApp && inst.Installed():
		how = " (managed by the " + novaflow.InstallerApp + " app)"
	case inst.Installed():
		how = " (not managed by NOVA)"
	}
	installed := "no"
	if inst.Installed() {
		installed = "yes"
	} else if inst.ViaFeatureFlag() {
		installed = "enabled, not answering yet"
	}
	fmt.Fprintf(w, "Installed:\t%s%s\n", installed, how)

	if f := inst.Flags; f != nil {
		if s := f.Status; s != nil {
			fmt.Fprintf(w, "State:\t%s\n", s.State)
			if s.DeployedVersion != "" {
				fmt.Fprintf(w, "Version:\t%s\n", s.DeployedVersion)
			}
			if s.Reason != "" {
				fmt.Fprintf(w, "Reason:\t%s\n", s.Reason)
			}
		}
		if f.NovaflowVersion != "" {
			fmt.Fprintf(w, "Requested version:\t%s\n", f.NovaflowVersion)
		}
	} else if installerStatus == nil && errors.Is(inst.FlagsErr, novaflow.ErrUnreachable) {
		fmt.Fprintf(w, "Installs with:\tthe %s app, the instance doesn't expose NOVA's NovaFlow feature flag\n", novaflow.InstallerApp)
	} else if installerStatus == nil && inst.FlagsErr != nil {
		fmt.Fprintf(w, "Feature flags:\tunavailable: %v\n", inst.FlagsErr)
	}
	if s := installerStatus; s != nil && !inst.ViaFeatureFlag() {
		if s.InstalledVersion != "" {
			fmt.Fprintf(w, "Version:\t%s\n", s.InstalledVersion)
		}
		if s.RecommendedVersion != "" && s.RecommendedVersion != s.InstalledVersion {
			fmt.Fprintf(w, "Available version:\t%s\n", s.RecommendedVersion)
		}
	}
	if inst.Installed() {
		fmt.Fprintf(w, "Editor:\t%s/\n", c.AppURL(novaflow.EditorApp))
	}
}

// confirm asks a yes/no question. Without a terminal it answers no, unless
// --yes was given.
func confirm(cmd *cobra.Command, question string, defaultYes bool) bool {
	if flowYes {
		return true
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s %s ", question, hint)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "":
		return defaultYes
	case "y", "yes":
		return true
	}
	return false
}

func progressPrinter(cmd *cobra.Command) func(string) {
	return func(msg string) { fmt.Fprintln(cmd.ErrOrStderr(), msg) }
}

// parseFlowFile reads a flow as written by 'nova flow get', or a bare tree
// ({"version": ..., "root": ...}).
func parseFlowFile(data []byte, id, name string) (*novaflow.Flow, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	var f novaflow.Flow
	switch {
	case probe["tree"] != nil:
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, err
		}
	case probe["root"] != nil:
		f.Tree = data
	default:
		return nil, errors.New("neither a flow (with \"tree\") nor a tree (with \"root\")")
	}
	if id != "" {
		f.ID = id
	}
	if name != "" {
		f.Name = name
	}
	if f.ID == "" {
		return nil, errors.New("the file has no flow id, set one with --id")
	}
	if f.Name == "" {
		f.Name = f.ID
	}
	return &f, nil
}

// indentJSON keeps the field order of the instance (id and name first).
func indentJSON(raw []byte) ([]byte, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

func init() {
	flowCmd.PersistentFlags().BoolVarP(&flowYes, "yes", "y", false, "Answer yes to questions, e.g. to install NovaFlow when it is missing")

	for _, c := range []*cobra.Command{flowInstallCmd, flowUninstallCmd} {
		c.Flags().BoolVar(&flowNoWait, "no-wait", false, "Return without waiting for the deployment")
		c.Flags().DurationVar(&flowWaitTimeout, "timeout", 10*time.Minute, "How long to wait for the deployment")
	}
	flowInstallCmd.Flags().StringVar(&flowInstallVersion, "version", "", "NovaFlow chart version, or 'default' for the one the NOVA version ships with (default: keep the version selected before)")
	flowInstallCmd.Flags().BoolVar(&flowInstallForce, "force", false, "Install even if NovaFlow was installed another way")
	flowInstallCmd.Flags().BoolVar(&flowInstallInstaller, "installer", false, "Install with the "+novaflow.InstallerApp+" app (preview catalog) even if NOVA can deploy NovaFlow itself")

	flowGetCmd.Flags().StringVarP(&flowGetOutput, "output", "o", "", "Write to this file instead of stdout")

	flowPushCmd.Flags().StringVar(&flowPushID, "id", "", "Flow id (default: the id in the file)")
	flowPushCmd.Flags().StringVar(&flowPushName, "name", "", "Flow name (default: the name in the file, or the id)")
	flowPushCmd.Flags().BoolVar(&flowPushForce, "force", false, "Replace the flow even if it changed on the instance")
	flowPushCmd.Flags().BoolVar(&flowPushValidate, "validate", false, "Validate the tree on the instance first and only save it if it is valid")

	flowBackupDownloadCmd.Flags().BoolVar(&flowBackupIncludeObjects, "include-objects", false, "Include the cell object store (shared by all apps of the cell)")
	flowBackupDownloadCmd.Flags().StringVar(&flowBackupObjectPrefix, "object-prefix", "", "Only include objects whose key starts with this prefix")

	flowBackupRestoreCmd.Flags().StringSliceVar(&flowRestoreOnly, "only", nil, "Only restore these flow ids (comma-separated)")
	flowBackupRestoreCmd.Flags().BoolVar(&flowRestoreOverwrite, "overwrite", false, "Replace existing flows, globals and objects (default: keep them)")
	flowBackupRestoreCmd.Flags().BoolVar(&flowRestoreSkipGlobals, "skip-globals", false, "Don't restore globals")
	flowBackupRestoreCmd.Flags().BoolVar(&flowBackupIncludeObjects, "include-objects", false, "Restore the objects in the backup to the cell object store")
	flowBackupRestoreCmd.Flags().BoolVar(&flowRestoreDryRun, "dry-run", false, "Only show what would be restored")

	flowBackupOutput = addOutputFlag(flowBackupDownloadCmd)
	flowBackupCmd.AddCommand(flowBackupDownloadCmd, flowBackupRestoreCmd)
	flowCmd.AddCommand(flowStatusCmd, flowInstallCmd, flowUninstallCmd, flowOpenCmd, flowListCmd, flowGetCmd, flowPushCmd, flowDeleteCmd, flowBackupCmd)
	rootCmd.AddCommand(flowCmd)
}
