package oauth

import (
	"context"
	"fmt"

	"github.com/cli/browser"
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
	fmt.Printf("warning: unknown auth environment '%s'", env)
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
	config := NewAuth0Config(env)

	if !force {
		// Try to load existing token
		if oldToken, err := LoadToken(); err == nil {
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

	fmt.Println("Open the URL in the browser to get API access: ", deviceCodeResp.VerificationURIComplete)
	fmt.Printf("Verify that your Device Verification Code \"%s\" is shown in the authentication dialog that opens in your browser\n", deviceCodeResp.UserCode)
	browser.OpenURL(deviceCodeResp.VerificationURIComplete)

	token, err := config.DeviceAccessToken(ctx, deviceCodeResp)
	if err != nil {
		return fmt.Errorf("failed to get token %w", err)
	}

	if err := StoreToken(token, env); err != nil {
		return err
	}

	if _, err := LoadToken(); err != nil {
		return err
	}

	return nil
}

func LoginWithProvidedRefreshToken(ctx context.Context, env AuthEnvironment, refreshToken string) error {
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
