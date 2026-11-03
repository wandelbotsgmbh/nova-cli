package novaflow

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Installation is what is known about NovaFlow on a cell.
type Installation struct {
	// Flags is nil when the instance has no feature flag API.
	Flags *FeatureFlags
	// FlagsErr is why Flags could not be read.
	FlagsErr error
	// Reachable reports whether Forest answers on the cell.
	Reachable bool
	// ProbeErr is why Forest did not answer.
	ProbeErr error
}

// Installed reports whether NovaFlow runs on the cell, however it was deployed.
func (i *Installation) Installed() bool { return i.Reachable }

// ViaFeatureFlag reports whether service-manager deploys NovaFlow on the cell.
func (i *Installation) ViaFeatureFlag() bool { return i.Flags != nil && i.Flags.Novaflow }

// Inspect checks how NovaFlow is installed on the cell. It only fails on
// errors that make the answer meaningless, e.g. a rejected token.
func (c *Client) Inspect(ctx context.Context) (*Installation, error) {
	var inst Installation
	inst.Flags, inst.FlagsErr = c.FeatureFlags(ctx)
	if errors.Is(inst.FlagsErr, ErrUnauthorized) {
		return nil, inst.FlagsErr
	}
	err := c.Probe(ctx)
	if errors.Is(err, ErrUnauthorized) {
		return nil, err
	}
	inst.Reachable = err == nil
	inst.ProbeErr = err
	return &inst, nil
}

// WaitOptions control WaitForDeployment.
type WaitOptions struct {
	Enabled  bool
	Version  string // expected deployed version; empty accepts any
	Interval time.Duration
	Progress func(state string)
}

// WaitForDeployment polls until service-manager reports the requested state
// and, when enabling, Forest answers. ctx bounds the wait.
func (c *Client) WaitForDeployment(ctx context.Context, opts WaitOptions) error {
	return poll(ctx, opts.Interval, opts.Progress, func() (bool, string, error) {
		return c.deploymentDone(ctx, opts)
	})
}

func (c *Client) deploymentDone(ctx context.Context, opts WaitOptions) (bool, string, error) {
	f, err := c.FeatureFlags(ctx)
	if err != nil {
		return false, "", err
	}
	if s := f.Status; s != nil {
		switch {
		case s.State == StateFailed:
			return false, "", fmt.Errorf("deploying NovaFlow failed: %s", s.Reason)
		case !opts.Enabled && s.State != StateDisabled && s.Enabled:
			return false, "being removed", nil
		case !opts.Enabled:
			// disabled is reported once the release is updated, the apps go a few seconds later
			return c.gone(ctx)
		case s.State == StateUpdating:
			return false, "being deployed", nil
		case s.State != StateDeployed || !s.Enabled:
			return false, "waiting for the deployment to start", nil
		case opts.Version != "" && s.DeployedVersion != opts.Version:
			return false, "waiting for version " + opts.Version, nil
		}
	} else if !opts.Enabled {
		// NOVA versions before 26.8 don't report the state
		return c.gone(ctx)
	}
	if err := c.Probe(ctx); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return false, "", err
		}
		return false, "starting", nil
	}
	return true, "", nil
}

// gone reports whether Forest stopped answering on the cell.
func (c *Client) gone(ctx context.Context) (bool, string, error) {
	err := c.Probe(ctx)
	switch {
	case errors.Is(err, ErrUnauthorized):
		return false, "", err
	case errors.Is(err, ErrUnreachable):
		return true, "", nil
	}
	return false, "being removed", nil
}
