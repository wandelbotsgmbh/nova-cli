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

// UserAgent is the User-Agent the CLI sends with its HTTP requests.
func UserAgent() string {
	return "Wandelbots-Nova-CLI/" + cliVersion
}

// SetUserAgent sets the User-Agent header for the request.
func SetUserAgent(ctx context.Context, req *http.Request) error {
	if req == nil {
		return nil
	}
	req.Header.Set("User-Agent", UserAgent())
	return nil
}
