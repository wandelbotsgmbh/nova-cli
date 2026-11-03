package docker

import (
	"fmt"
	"slices"
	"strings"
)

// DefaultPlatform is the platform app images are built for unless overridden.
// NOVA instances are linux/amd64 unless stated otherwise.
const DefaultPlatform = "linux/amd64"

// SupportedPlatforms are the platforms an app image can be built for.
var SupportedPlatforms = []string{"linux/amd64", "linux/arm64"}

// ValidatePlatform returns an error unless platform is one of SupportedPlatforms.
func ValidatePlatform(platform string) error {
	if slices.Contains(SupportedPlatforms, platform) {
		return nil
	}
	return fmt.Errorf("unsupported platform %q: must be one of %s", platform, strings.Join(SupportedPlatforms, ", "))
}
