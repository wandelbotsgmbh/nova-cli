package novaflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Deployment states reported by service-manager in novaflow_status.state.
const (
	StateDisabled = "disabled"
	StateUpdating = "updating"
	StateDeployed = "deployed"
	StateFailed   = "failed"
)

// DeploymentStatus is the observed state of the NovaFlow chart on a cell.
// service-manager reports it from 26.8 on; older versions (with the flag API from 26.4) leave it out.
type DeploymentStatus struct {
	Enabled         bool   `json:"enabled"`
	DeployedVersion string `json:"deployed_version"`
	State           string `json:"state"`
	Reason          string `json:"reason"`
}

// FeatureFlags is the cell's service-manager feature flag configuration.
type FeatureFlags struct {
	Novaflow        bool
	NovaflowVersion string
	Status          *DeploymentStatus

	// raw keeps every field as read: the PUT replaces the whole object, so
	// flags this client doesn't know (e.g. controller_connector) must be sent
	// back unchanged.
	raw map[string]json.RawMessage
}

func (f *FeatureFlags) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	var known struct {
		Novaflow        bool              `json:"novaflow"`
		NovaflowVersion string            `json:"novaflow_version"`
		Status          *DeploymentStatus `json:"novaflow_status"`
	}
	if err := json.Unmarshal(b, &known); err != nil {
		return err
	}
	*f = FeatureFlags{Novaflow: known.Novaflow, NovaflowVersion: known.NovaflowVersion, Status: known.Status, raw: raw}
	return nil
}

func (f *FeatureFlags) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(f.raw)+2)
	for k, v := range f.raw {
		out[k] = v
	}
	// read-only, service-manager ignores it on write
	delete(out, "novaflow_status")
	out["novaflow"], _ = json.Marshal(f.Novaflow)
	if f.NovaflowVersion != "" {
		out["novaflow_version"], _ = json.Marshal(f.NovaflowVersion)
	} else {
		delete(out, "novaflow_version")
	}
	return json.Marshal(out)
}

// State returns the deployment state, or "" if the NOVA version doesn't report it.
func (f *FeatureFlags) State() string {
	if f.Status == nil {
		return ""
	}
	return f.Status.State
}

func (c *Client) featureFlagsPath() string {
	return "/api/v2/internal/cells/" + url.PathEscape(c.cell) + "/feature-flags"
}

// FeatureFlags reads the cell's feature flags.
func (c *Client) FeatureFlags(ctx context.Context) (*FeatureFlags, error) {
	var f FeatureFlags
	if _, err := c.do(ctx, request{method: http.MethodGet, path: c.featureFlagsPath()}, &f); err != nil {
		return nil, featureFlagsError(err)
	}
	return &f, nil
}

// DefaultVersion as version for SetNovaflow removes a selected version, so the
// cell gets the NovaFlow version its NOVA release ships with again.
const DefaultVersion = "default"

// SetNovaflow switches the NovaFlow deployment of the cell on or off. A
// non-empty version selects the chart version (DefaultVersion: the one the NOVA
// release ships with); an empty one keeps the configured version.
func (c *Client) SetNovaflow(ctx context.Context, enabled bool, version string) (*FeatureFlags, error) {
	f, err := c.FeatureFlags(ctx)
	if err != nil {
		return nil, err
	}
	f.Novaflow = enabled
	switch version {
	case "":
	case DefaultVersion:
		f.NovaflowVersion = ""
	default:
		f.NovaflowVersion = version
	}
	var updated FeatureFlags
	if _, err := c.do(ctx, request{method: http.MethodPut, path: c.featureFlagsPath(), jsonBody: f}, &updated); err != nil {
		return nil, featureFlagsError(err)
	}
	return &updated, nil
}

func featureFlagsError(err error) error {
	if IsStatus(err, http.StatusForbidden) {
		return fmt.Errorf("%w (managing NovaFlow needs the permission to manage cells)", err)
	}
	if IsStatus(err, http.StatusNotFound) {
		return fmt.Errorf("%w (does the cell exist?)", err)
	}
	if errors.Is(err, ErrUnreachable) {
		return fmt.Errorf("%w (the instance has no NovaFlow feature flag API: NOVA too old, or its internal API is not exposed)", err)
	}
	return err
}
