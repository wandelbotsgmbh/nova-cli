package docker

import "testing"

func TestValidatePlatform(t *testing.T) {
	for _, p := range SupportedPlatforms {
		if err := ValidatePlatform(p); err != nil {
			t.Errorf("ValidatePlatform(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range []string{"", "amd64", "linux/arm/v7", "windows/amd64", "linux/amd64,linux/arm64"} {
		if err := ValidatePlatform(p); err == nil {
			t.Errorf("ValidatePlatform(%q) = nil, want error", p)
		}
	}
}
