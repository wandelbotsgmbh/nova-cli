package bootstrap

import (
	"fmt"

	"github.com/wandelbotsgmbh/nova-cli/pkg/portal"
)

// DefaultChartVersion is the sentinel version that triggers resolving the
// latest release from the developer portal instead of pinning a fixed version.
const DefaultChartVersion = "x.x.x"

// DefaultReleaseChannel is the portal release channel used when resolving the
// latest version.
const DefaultReleaseChannel = "stable"

// resolveChartVersion returns the chart version to deploy. When the configured
// version is empty or the DefaultChartVersion sentinel, the latest version is
// fetched from the developer portal for the configured release channel.
func resolveChartVersion(cfg Config) (string, error) {
	if cfg.ChartVersion != "" && cfg.ChartVersion != DefaultChartVersion {
		return cfg.ChartVersion, nil
	}

	env, err := loadEnvironment()
	if err != nil {
		return "", err
	}
	p, err := portal.NewPortal(env)
	if err != nil {
		return "", fmt.Errorf("create portal client: %w", err)
	}

	channel := portal.ReleaseChannel(cfg.ReleaseChannel)
	if channel == "" {
		channel = portal.ReleaseChannel(DefaultReleaseChannel)
	}

	// A fresh install has no current version; "0.0.0" signals that any released
	// version is newer, so the portal returns the latest. The query argument is
	// required by the endpoint.
	version, err := p.GetLatestVersion(channel, "0.0.0")
	if err != nil {
		return "", fmt.Errorf("resolve latest version from portal (pass --version to pin): %w", err)
	}
	if version == "" {
		return "", fmt.Errorf("portal returned an empty version (pass --version to pin)")
	}
	return version, nil
}

// WriteServiceManager writes the HelmChart CR manifest that tells k3s to
// deploy the service-manager chart.
func WriteServiceManager(cfg Config) error {
	version, err := resolveChartVersion(cfg)
	if err != nil {
		return err
	}
	cfg.ChartVersion = version

	manifest, err := renderManifest("service-manager.yaml.tmpl", cfg)
	if err != nil {
		return err
	}
	return applyManifest("service-manager", manifest)
}
