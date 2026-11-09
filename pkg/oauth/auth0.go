package oauth

import (
	"context"
	"fmt"
	"github.com/spf13/viper"
	"os"

	"github.com/cli/browser"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"golang.org/x/oauth2"
)

const (
	// Please also adjust nova/utils.go where the correct auth gets injected.
	// Having bearer token + basic auth, seems not to work, so it is hard to slowly migrate.
	auth0DomainDev = "https://auth.portal.dev.wandelbots.io"
	clientIdDev    = "fLbJD0RLp5r2Dpucm5j8BjwMR6Hunfha"

	auth0DomainStg = "https://auth.portal.stg.wandelbots.io"
	clientIdStg    = "joVDeD9e786WzFNSGCqoVq7HNkWt5j6s"

	auth0DomainProd = "https://auth.portal.wandelbots.io"
	clientIdProd    = "J7WJUi38xVQdJAEBNRT9Xw1b0fXDb4J2"

	audience = "nova-api"

	// AuthEnvironment
	AuthDev  = AuthEnvironment("dev")
	AuthStg  = AuthEnvironment("stg")
	AuthProd = AuthEnvironment("prod")
)

// oauth.authDev, oauth.authStg, oauth.authProd
type AuthEnvironment string

func NewAuth0Config(env AuthEnvironment) *oauth2.Config {
	if env == AuthDev {
		return createConfig(clientIdDev, auth0DomainDev)
	} else if env == AuthStg {
		return createConfig(clientIdStg, auth0DomainStg)
	} else if env == AuthProd {
		return createConfig(clientIdProd, auth0DomainProd)
	}
	fmt.Fprintf(os.Stderr, "warning: unknown auth environment '%s'\n", env)
	return createConfig(clientIdProd, auth0DomainProd)
}

// Auth0Domain returns the Auth0 base URL for the given environment.
func Auth0Domain(env AuthEnvironment) string {
	switch env {
	case AuthDev:
		return auth0DomainDev
	case AuthStg:
		return auth0DomainStg
	default:
		return auth0DomainProd
	}
}

func ProvideAudience(env AuthEnvironment) oauth2.AuthCodeOption {
	return oauth2.SetAuthURLParam("audience", audience)
}

func createConfig(clientId, auth0Domain string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: clientId,
		Scopes:   []string{"openid", "email", "offline_access"},
		Endpoint: oauth2.Endpoint{
			AuthURL:       auth0Domain + "/oauth/authorize",
			DeviceAuthURL: auth0Domain + "/oauth/device/code",
			TokenURL:      auth0Domain + "/oauth/token",
			AuthStyle:     oauth2.AuthStyleInParams,
		},
	}
}

func LoginWithRefreshToken(ctx context.Context, env AuthEnvironment, force bool) error {
	ctx = withHTTPClient(ctx)
	config := NewAuth0Config(env)

	if !force {
		// Try to load existing token
		if oldToken, err := LoadTokenFor(env); err == nil && oldToken.OAuth2Token.RefreshToken != "" {
			tokenSource := config.TokenSource(ctx, &oldToken.OAuth2Token)
			if newToken, err := tokenSource.Token(); err == nil {
				return StoreToken(newToken, env)
			}
		}
	}

	// If no valid token or refresh failed, proceed with device auth
	deviceCodeResp, err := config.DeviceAuth(ctx, ProvideAudience(env))
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "Open the URL in the browser to get API access: ", deviceCodeResp.VerificationURIComplete)
	fmt.Fprintf(os.Stderr, "Verify that your Device Verification Code \"%s\" is shown in the authentication dialog that opens in your browser\n", deviceCodeResp.UserCode)
	browser.OpenURL(deviceCodeResp.VerificationURIComplete)

	token, err := config.DeviceAccessToken(ctx, deviceCodeResp)
	if err != nil {
		return fmt.Errorf("failed to get token %w", err)
	}

	if err := StoreToken(token, env); err != nil {
		return err
	}

	if _, err := LoadTokenFor(env); err != nil {
		return err
	}

	return nil
}

// refreshConfig is the OAuth config RefreshToken uses (replaced in tests).
var refreshConfig = NewAuth0Config

// RefreshToken refreshes the stored login of env without any interaction; it fails if
// there is none or it can't be refreshed.
//
// It works on the config file as it is now, not on this process's copy: long-running
// processes (nova mcp, keep-alive) see logins other processes refreshed meanwhile
// (refresh tokens can be rotated), and writing back changes only the login, so it never
// reverts what others changed in the config (selected instance, new instances).
func RefreshToken(ctx context.Context, env AuthEnvironment) error {
	ctx = withHTTPClient(ctx)
	disk := configOnDisk()
	src := viper.GetViper()
	if disk != nil {
		src = disk
	}
	old, err := loadTokenFor(src, env)
	if err != nil {
		return err
	}
	if old.OAuth2Token.AccessToken == "" && old.OAuth2Token.RefreshToken == "" {
		return fmt.Errorf("not logged in to the %s environment", env)
	}
	if old.OAuth2Token.Valid() {
		if disk != nil {
			adoptToken(old, env)
		}
		return nil
	}
	newToken, err := refreshConfig(env).TokenSource(ctx, &old.OAuth2Token).Token()
	if err != nil {
		return fmt.Errorf("login of the %s environment expired, run 'nova login --env %s': %w", env, env, err)
	}
	if disk == nil {
		return StoreToken(newToken, env)
	}
	setToken(disk, newToken, env)
	if err := disk.WriteConfig(); err != nil {
		return err
	}
	adoptToken(WaboAuthToken{OAuth2Token: *newToken, AuthEnvironment: env}, env)
	return nil
}

func LoginWithProvidedRefreshToken(ctx context.Context, env AuthEnvironment, refreshToken string) error {
	ctx = withHTTPClient(ctx)
	config := NewAuth0Config(env)

	// Create a token with the provided refresh token
	token := &oauth2.Token{
		RefreshToken: refreshToken,
	}

	// Use the token source to get a new access token
	tokenSource := config.TokenSource(ctx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		return fmt.Errorf("failed to get new access token: %w", err)
	}

	return StoreToken(newToken, env)
}

// withHTTPClient makes the oauth2 library use the CLI's HTTP client (TLS setting,
// User-Agent) for the device flow and token refreshes.
func withHTTPClient(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, httpx.Client())
}
