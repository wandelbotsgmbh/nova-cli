package oauth

import (
	"context"
	"fmt"
	"os"

	"github.com/cli/browser"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/configfile"
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
// The login in the config file counts, not only this process's copy: long-running
// processes (nova mcp, keep-alive) see logins other processes refreshed meanwhile.
// The refresh itself runs under the config lock (configfile.Update), so two processes
// never refresh with the same refresh token (they can be rotated: the second one
// would be left with a dead login); the one that waited finds the new login.
func RefreshToken(ctx context.Context, env AuthEnvironment) error {
	ctx = withHTTPClient(ctx)
	v := viper.GetViper()
	memory, err := LoadTokenFor(env)
	if err != nil {
		return err
	}
	// the common cases without the lock
	if disk := configOnDisk(); disk != nil {
		onDisk, err := loadTokenFor(disk, env)
		switch {
		case err != nil:
		case onDisk.OAuth2Token.Valid() && !onDisk.OAuth2Token.Expiry.Before(memory.OAuth2Token.Expiry):
			if onDisk.OAuth2Token.AccessToken != memory.OAuth2Token.AccessToken {
				return v.ReadInConfig() // refreshed elsewhere: use it
			}
			return nil
		case !loggedIn(onDisk) && memory.OAuth2Token.Valid():
			// no login in the file (an older CLI version is writing it right now, or
			// it was removed), but this process has a valid one: nothing to do
			return nil
		}
	}
	return configfile.Update(v, func(disk *viper.Viper) error {
		// of the login in the file and the one of this process the newer one counts:
		// an older CLI version may have written its older copy over the file
		login := memory
		onDisk, err := loadTokenFor(disk, env)
		fromDisk := err == nil && loggedIn(onDisk) && (!loggedIn(login) || !onDisk.OAuth2Token.Expiry.Before(login.OAuth2Token.Expiry))
		if fromDisk {
			login = onDisk
		}
		if !loggedIn(login) {
			return fmt.Errorf("not logged in to the %s environment", env)
		}
		token := &login.OAuth2Token
		if token.Valid() {
			if fromDisk {
				return configfile.ErrUnchanged // refreshed while waiting for the lock
			}
		} else if token, err = refreshConfig(env).TokenSource(ctx, token).Token(); err != nil {
			return fmt.Errorf("login of the %s environment expired, run 'nova login --env %s': %w", env, env, err)
		}
		setToken(disk, token, env)
		return nil
	})
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
