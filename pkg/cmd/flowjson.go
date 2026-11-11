package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/novaflow"
)

// -o json of the flow commands (ADR-0010). The shapes are part of the CLI
// contract: fields may be added, never renamed or removed.

// -o/--output of 'flow list', 'flow status', 'flow backup restore' and 'flow action create'
var flowListOutput, flowStatusOutput, flowRestoreOutput, flowActionCreateOutput *outputFormat

func init() {
	flowListOutput = addOutputFlag(flowListCmd)
	flowStatusOutput = addOutputFlag(flowStatusCmd)
	flowRestoreOutput = addOutputFlag(flowBackupRestoreCmd)
	flowActionCreateOutput = addOutputFlag(flowActionCreateCmd)
}

// flowListItem is one entry of 'flow list -o json'.
type flowListItem struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// writeFlowListJSON prints the readable flows as a JSON array; flows Forest
// can't read are warned about on stderr, as in the text output.
func writeFlowListJSON(cmd *cobra.Command, list *novaflow.FlowList) error {
	items := make([]flowListItem, 0, len(list.Flows))
	for _, f := range list.Flows {
		items = append(items, flowListItem{ID: f.ID, Name: f.Name, CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt})
	}
	if err := writeJSON(cmd, items); err != nil {
		return err
	}
	for _, cf := range list.Corrupted {
		fmt.Fprintf(cmd.ErrOrStderr(), "WARN: flow %s can't be read: %s\n", cf.ID, cf.Error)
	}
	return nil
}

// flowStatusResult is the JSON result of 'flow status -o json'. It carries the
// same facts as the text output.
type flowStatusResult struct {
	Cell string `json:"cell"`
	// NovaFlow answers on the cell
	Installed bool `json:"installed"`
	// "nova" (service-manager's feature flag is on, also while it is not answering
	// yet), "novaflow-installer" (the installer app) or "unmanaged"; empty if
	// NovaFlow is not installed
	ManagedBy string `json:"managed_by,omitempty"`
	// service-manager's deployment state (e.g. deployed, updating, failed)
	State  string `json:"state,omitempty"`
	Reason string `json:"reason,omitempty"`
	// the running version
	Version string `json:"version,omitempty"`
	// the version pinned with 'flow install --version'
	RequestedVersion string `json:"requested_version,omitempty"`
	// the version the installer app recommends, if it differs from Version
	AvailableVersion string `json:"available_version,omitempty"`
	EditorURL        string `json:"editor_url,omitempty"`
}

// flowStatus collects what printFlowStatus prints.
func flowStatus(c *novaflow.Client, inst *novaflow.Installation, installerApp bool, installerStatus *novaflow.InstallerStatus) flowStatusResult {
	r := flowStatusResult{Cell: c.Cell(), Installed: inst.Installed()}
	switch {
	case inst.ViaFeatureFlag():
		r.ManagedBy = "nova"
	case installerApp && inst.Installed():
		r.ManagedBy = novaflow.InstallerApp
	case inst.Installed():
		r.ManagedBy = "unmanaged"
	}
	if f := inst.Flags; f != nil {
		if s := f.Status; s != nil {
			r.State, r.Version, r.Reason = s.State, s.DeployedVersion, s.Reason
		}
		r.RequestedVersion = f.NovaflowVersion
	}
	if s := installerStatus; s != nil && !inst.ViaFeatureFlag() {
		if s.InstalledVersion != "" {
			r.Version = s.InstalledVersion
		}
		if s.RecommendedVersion != s.InstalledVersion {
			r.AvailableVersion = s.RecommendedVersion
		}
	}
	if inst.Installed() {
		r.EditorURL = c.AppURL(novaflow.EditorApp) + "/"
	}
	return r
}

// flowRestoreResult is the JSON result of 'flow backup restore -o json'.
type flowRestoreResult struct {
	Cell string `json:"cell"`
	// the backup file
	Path string `json:"path"`
	// with --dry-run the actions are what a restore would do
	DryRun bool              `json:"dry_run"`
	Items  []flowRestoreItem `json:"items"`
}

type flowRestoreItem struct {
	// flow, global or object
	Kind string `json:"kind"`
	// flow id, global key or object key
	Key string `json:"key"`
	// create, replace or skip (exists, without --overwrite)
	Action string `json:"action"`
}

func newFlowRestoreResult(cell, path string, dryRun bool, items []novaflow.RestoreItem) flowRestoreResult {
	r := flowRestoreResult{Cell: cell, Path: absPath(path), DryRun: dryRun, Items: make([]flowRestoreItem, 0, len(items))}
	for _, it := range items {
		action := string(it.Action)
		if it.Action == novaflow.Skipped {
			action = "skip"
		}
		r.Items = append(r.Items, flowRestoreItem{Kind: it.Kind, Key: it.Key, Action: action})
	}
	return r
}

// createdFlowAction is the JSON result of 'flow action create -o json'.
type createdFlowAction struct {
	Name string `json:"name"`
	// the project directory
	Path string `json:"path"`
}
