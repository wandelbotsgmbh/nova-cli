package novaflow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
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
	body := map[string]string{"version": version}
	for attempt := 1; ; attempt++ {
		var results []InstallerResult
		if _, err := c.do(ctx, request{method: http.MethodPost, path: c.installerPath("/api/install-all"), jsonBody: body}, &results); err != nil {
			return nil, err
		}
		// Right after an uninstall the installer can still see apps that are being
		// deleted and try to update them; the update then fails with "not found".
		// Sent again, it creates them.
		if attempt >= installerAttempts || !onlyNotFound(results) {
			return results, failedResults("installing", results)
		}
		select {
		case <-ctx.Done():
			return results, failedResults("installing", results)
		case <-time.After(installerRetryDelay):
		}
	}
}

// installerAttempts and installerRetryDelay bound the retries of InstallerInstall.
var (
	installerAttempts   = 4
	installerRetryDelay = 10 * time.Second
)

// onlyNotFound reports whether some apps failed and all of them because they were
// not found.
func onlyNotFound(results []InstallerResult) bool {
	failed := false
	for _, r := range results {
		if r.Success {
			continue
		}
		if !strings.Contains(r.Message, "404") || !strings.Contains(r.Message, "not found") {
			return false
		}
		failed = true
	}
	return failed
}

// WaitForAppsRemoved polls the cell's app list until none of apps is listed. ctx
// bounds the wait.
func (c *Client) WaitForAppsRemoved(ctx context.Context, apps []string, interval time.Duration, progress func(string)) error {
	return poll(ctx, interval, progress, func() (bool, string, error) {
		var listed []string
		if _, err := c.do(ctx, request{method: http.MethodGet, path: "/api/v2/cells/" + url.PathEscape(c.cell) + "/apps"}, &listed); err != nil {
			if errors.Is(err, ErrUnauthorized) {
				return false, "", err
			}
			// a passing gateway error while the apps go away: ask again
			return false, "being removed", nil
		}
		var left []string
		for _, a := range apps {
			if slices.Contains(listed, a) {
				left = append(left, a)
			}
		}
		if len(left) == 0 {
			return true, "", nil
		}
		return false, "removing " + strings.Join(left, ", "), nil
	})
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
