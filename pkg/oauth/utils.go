package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/wandelbotsgmbh/nova-cli/pkg/credentials"
)

const (
	WABO_CLOUD_HOST = "wandelbots.io"
)

func SetAuthForClient(ctx context.Context, req *http.Request) error {
	return SetAuthForRequest(req)
}

// SendsTokenTo reports whether the portal login is attached to requests to host: only
// the portal and cloud instances (*.wandelbots.io), never on-prem hosts or other clusters.
func SendsTokenTo(host string) bool {
	if u, err := url.Parse(host); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	} else if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == WABO_CLOUD_HOST || strings.HasSuffix(host, "."+WABO_CLOUD_HOST)
}

// Adds the auth header information for the request. The portal login of the active
// environment is only attached to wandelbots.io hosts (see SendsTokenTo).
func SetAuthForRequest(req *http.Request) error {
	tokenSet, basicSet := false, false
	if auth, err := credentials.GetBasicAuth(); err == nil {
		req.SetBasicAuth(auth.Username, auth.Password)
		basicSet = true
	}

	cloud := SendsTokenTo(req.URL.Host)

	// Adding both does not work in prod where basic auth is still in place.
	// So if someone once logged in with the token, we use that.
	if cloud {
		if token, err := LoadToken(); err == nil && token.OAuth2Token.AccessToken != "" {
			req.Header.Set("Authorization", "Bearer "+token.OAuth2Token.AccessToken)
			tokenSet = true
		}
	}

	if cloud && !tokenSet && !basicSet {
		return errors.New("no auth available for wabo cloud instance. please login first or set basic auth")
	}
	return nil
}

// For wandelbots.io hosts (portal and cloud instances), it will try to refresh the token of the active environment automatically.
// The refreshed valid token will be stored. If an error is returned, the user might need to login again (following the device auth flow).
func RefreshTokenOrLogin(host string) error {
	if SendsTokenTo(host) {
		ctx := context.Background()
		env := ActiveEnvironment()
		if token, err := LoadTokenFor(env); err == nil && token.OAuth2Token.AccessToken != "" {
			return LoginWithRefreshToken(ctx, env, false)
		}
		return fmt.Errorf("not logged in to the %s environment. please login with 'nova login --env %s'", env, env)
	}
	return nil
}
