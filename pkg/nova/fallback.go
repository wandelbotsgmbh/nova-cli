package nova

import (
	"strings"
	"sync"

	gv "github.com/hashicorp/go-version"
)

// apiCapability represents which API versions a Nova instance supports
type apiCapability int

const (
	capUnknown apiCapability = iota
	capV1Only
	capV2Only
)

// Public constants for external packages (e.g. cmd)
const (
	CapV1Only = capV1Only
	CapV2Only = capV2Only
)

const v2OnlyFromVersion = "26.0.0"

// hostCapabilities caches the API capabilities for each host
// Key: host URL, Value: apiCapability
var (
	hostCapabilities sync.Map
)

// cacheHostCapability stores which API version(s) a host supports
func cacheHostCapability(host string, cap apiCapability) {
	hostCapabilities.Store(host, cap)
}

func parseCapabilityFromVersion(version string) (apiCapability, error) {
	trimmed := strings.Trim(strings.TrimSpace(version), "\"")

	currentVersion, err := gv.NewVersion(trimmed)
	if err != nil {
		return capUnknown, err
	}

	v2OnlyVersion, err := gv.NewVersion(v2OnlyFromVersion)
	if err != nil {
		return capUnknown, err
	}

	if currentVersion.GreaterThanOrEqual(v2OnlyVersion) {
		return capV2Only, nil
	}

	return capV1Only, nil
}

func detectHostCapability(host string) (apiCapability, error) {
	// Try V2 first
	version, err := GetNovaVersionV2Impl(host)
	if err == nil {
		return parseCapabilityFromVersion(version)
	}

	// If the v2 endpoint is not available (404), fall back to v1
	if strings.Contains(err.Error(), "404") {
		version, err = getNovaVersionV1Impl(host)
		if err != nil {
			return capUnknown, err
		}
		return parseCapabilityFromVersion(version)
	}

	// For other errors, return the error (don't fall back to v1)
	return capUnknown, err
}

// forceRefreshHostCapability detects capability from version and updates the cache.
func forceRefreshHostCapability(host string) (apiCapability, error) {
	cap, err := detectHostCapability(host)
	if err != nil {
		return capUnknown, err
	}

	cacheHostCapability(host, cap)
	return cap, nil
}

// refreshHostCapabilityOnFailure refreshes capability and reports whether it changed.
func refreshHostCapabilityOnFailure(host string, previous apiCapability) (apiCapability, bool, error) {
	updated, err := forceRefreshHostCapability(host)
	if err != nil {
		return previous, false, err
	}

	if updated != previous {
		return updated, true, nil
	}

	return updated, false, nil
}

// updateHostCapabilityFromTargetVersion updates cached capability from the requested target version.
// If the version cannot be parsed (e.g. channel names), it falls back to server-side detection.
func updateHostCapabilityFromTargetVersion(host string, targetVersion string) {
	cap, err := parseCapabilityFromVersion(targetVersion)
	if err == nil {
		cacheHostCapability(host, cap)
		return
	}

	_, _ = forceRefreshHostCapability(host)
}

// getHostCapability retrieves API capability for a host.
func getHostCapability(host string) apiCapability {
	if val, ok := hostCapabilities.Load(host); ok {
		return val.(apiCapability)
	}

	cap, err := detectHostCapability(host)
	if err != nil {
		return capUnknown
	}

	cacheHostCapability(host, cap)
	return cap
}

// GetHostCapabilityPublic retrieves API capability for a host (public version for external packages).
func GetHostCapabilityPublic(host string) apiCapability {
	return getHostCapability(host)
}

// clearCapabilityCache clears the capability cache (useful for testing or manual reset)
func clearCapabilityCache() {
	hostCapabilities.Range(func(key, value interface{}) bool {
		hostCapabilities.Delete(key)
		return true
	})
}
