package novaflow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The novaflow-installer app (preview catalog) deploys the NovaFlow apps
// through the public apps API. It is the way to install NovaFlow where
// service-manager's internal API is not exposed, e.g. on portal instances.

// InstallerStatus is the NovaFlow version on the cell and the one the
// installer recommends.
type InstallerStatus struct {
	// InstalledVersion is controlplane's image tag, empty if NovaFlow is not installed.
	InstalledVersion   string `json:"installed_version"`
	RecommendedVersion string `json:"recommended_version"`
}

// InstallerResult is the outcome for one app of an install or uninstall.
type InstallerResult struct {
	App     string `json:"app"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// InstallerReadiness reports whether the NovaFlow apps run a version.
type InstallerReadiness struct {
	Ready      bool     `json:"ready"`
	WaitingFor []string `json:"waiting_for"`
	Failed     []string `json:"failed"`
}

func (c *Client) installerPath(path string) string {
	return c.appPath(InstallerApp, path)
}

// InstallerHealthy reports whether the installer app answers.
func (c *Client) InstallerHealthy(ctx context.Context) error {
	_, err := c.do(ctx, request{method: http.MethodGet, path: c.installerPath("/health")}, nil)
	return err
}

// InstallerStatus reads the installed and the recommended NovaFlow version.
func (c *Client) InstallerStatus(ctx context.Context) (*InstallerStatus, error) {
	var s InstallerStatus
	if _, err := c.do(ctx, request{method: http.MethodGet, path: c.installerPath("/api/status")}, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// InstallerInstall installs or updates all NovaFlow apps with version.
func (c *Client) InstallerInstall(ctx context.Context, version string) ([]InstallerResult, error) {
	var results []InstallerResult
	body := map[string]string{"version": version}
	if _, err := c.do(ctx, request{method: http.MethodPost, path: c.installerPath("/api/install-all"), jsonBody: body}, &results); err != nil {
		return nil, err
	}
	return results, failedResults("installing", results)
}

// InstallerUninstall removes all NovaFlow apps; the installer app stays.
func (c *Client) InstallerUninstall(ctx context.Context) ([]InstallerResult, error) {
	var results []InstallerResult
	if _, err := c.do(ctx, request{method: http.MethodDelete, path: c.installerPath("/api/uninstall-all")}, &results); err != nil {
		return nil, err
	}
	return results, failedResults("removing", results)
}

// InstallerReadiness reports whether the NovaFlow apps run version.
func (c *Client) InstallerReadiness(ctx context.Context, version string) (*InstallerReadiness, error) {
	var r InstallerReadiness
	q := url.Values{"version": {version}}
	if _, err := c.do(ctx, request{method: http.MethodGet, path: c.installerPath("/api/readiness"), query: q}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func failedResults(what string, results []InstallerResult) error {
	var failed []string
	for _, r := range results {
		if !r.Success {
			failed = append(failed, r.Message)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("%s NovaFlow failed: %s", what, strings.Join(failed, "; "))
}

// WaitForInstaller polls until the installer app answers. ctx bounds the wait.
func (c *Client) WaitForInstaller(ctx context.Context, interval time.Duration) error {
	return poll(ctx, interval, nil, func() (bool, string, error) {
		err := c.InstallerHealthy(ctx)
		if errors.Is(err, ErrUnauthorized) {
			return false, "", err
		}
		return err == nil, "starting", nil
	})
}

// WaitForInstallerDeployment polls until the installer reports the apps
// running version and Forest answers. ctx bounds the wait.
func (c *Client) WaitForInstallerDeployment(ctx context.Context, version string, interval time.Duration, progress func(string)) error {
	return poll(ctx, interval, progress, func() (bool, string, error) {
		r, err := c.InstallerReadiness(ctx, version)
		if err != nil {
			if errors.Is(err, ErrUnauthorized) {
				return false, "", err
			}
			return false, "being deployed", nil
		}
		if len(r.Failed) > 0 {
			return false, "", fmt.Errorf("deploying NovaFlow failed, these apps don't start: %s", strings.Join(r.Failed, ", "))
		}
		if !r.Ready {
			return false, "waiting for " + strings.Join(r.WaitingFor, ", "), nil
		}
		if err := c.Probe(ctx); err != nil {
			if errors.Is(err, ErrUnauthorized) {
				return false, "", err
			}
			return false, "starting", nil
		}
		return true, "", nil
	})
}

// WaitForRemoval polls until Forest stops answering. ctx bounds the wait.
func (c *Client) WaitForRemoval(ctx context.Context, interval time.Duration, progress func(string)) error {
	return poll(ctx, interval, progress, func() (bool, string, error) { return c.gone(ctx) })
}

// poll calls check until it is done, fails or ctx ends, and reports each new
// state to progress.
func poll(ctx context.Context, interval time.Duration, progress func(string), check func() (bool, string, error)) error {
	if interval == 0 {
		interval = 3 * time.Second
	}
	last := ""
	for {
		done, state, err := check()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if state != last && progress != nil {
			progress(state)
		}
		last = state
		select {
		case <-ctx.Done():
			return fmt.Errorf("NovaFlow is still %s: %w", last, ctx.Err())
		case <-time.After(interval):
		}
	}
}
