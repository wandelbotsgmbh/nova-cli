package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/wandelbotsgmbh/nova-cli/pkg/credentials"
)

const (
	WABO_CLOUD_HOST = "wandelbots.io"
)

func SetAuthForClient(ctx context.Context, req *http.Request) error {
	return SetAuthForRequest(req)
}

// Adds the auth header information for the request if it is a wandelbots cloud instance.
func SetAuthForRequest(req *http.Request) error {
	tokenSet, basicSet := false, false
	if auth, err := credentials.GetBasicAuth(); err == nil {
		req.SetBasicAuth(auth.Username, auth.Password)
		basicSet = true
	}

	// Adding both does not work in prod where basic auth is still in place.
	// So if someone once logged in with the token, we use that.
	if token, err := LoadToken(); err == nil && token.OAuth2Token.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+token.OAuth2Token.AccessToken)
		tokenSet = true
	}

	if strings.Contains(req.Host, WABO_CLOUD_HOST) && !tokenSet && !basicSet {
		return errors.New("no auth available for wabo cloud instance. please login first or set basic auth")
	}
	return nil
}

// For wandelbots.io hosts (portal instances), it will try to refresh the token automatically with the previous used AuthEnvironment.
// The refreshed valid token will be stored. If an error is returned, the user might need to login again (following the device auth flow).
func RefreshTokenOrLogin(host string) error {
	if strings.Contains(host, "wandelbots.io") {
		ctx := context.Background()
		if token, err := LoadToken(); err == nil && token.OAuth2Token.AccessToken != "" {
			return LoginWithRefreshToken(ctx, AuthEnvironment(token.AuthEnvironment), false)
		}
		return fmt.Errorf("login failed. please login with nova login")
	}
	return nil
}
