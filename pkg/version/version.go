package version

import (
	"context"
	"net/http"
)

// Gets injected during build via linker flags. Must be from type var.
var cliVersion string = "dev"

func GetCliVersion() string {
	return cliVersion
}

// SetUserAgent sets the User-Agent header for the request.
func SetUserAgent(ctx context.Context, req *http.Request) error {
	if req == nil {
		return nil
	}
	req.Header.Set("User-Agent", "Wandelbots-Nova-CLI/"+cliVersion)
	return nil
}
