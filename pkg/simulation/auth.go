package simulation

import (
	"context"
	"errors"
	"fmt"

	"github.com/cli/browser"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

// The broker only accepts tokens of the viewer's Entra ID app, not the
// portal's Auth0 tokens of "nova login". The app allows public client flows,
// so the device flow works without a client secret.

// Environment selects the Entra ID app and broker a login belongs to.
type Environment string

const (
	EnvDev Environment = "dev"

	tokenKey = "simulation-token"
	// BrokerKey overrides the broker URL of the logged-in environment.
	BrokerKey = "simulation-broker"
)

type environment struct {
	tenant   string
	clientID string
	broker   string
	viewer   string
}

var environments = map[Environment]environment{
	EnvDev: {
		tenant:   "c2abe300-42c3-4c05-ba87-2a49bb7cb460",
		clientID: "6d120209-98c5-43fd-a752-f38056505e6d",
		broker:   "https://broker.omni.dev.wandelbots.io",
		viewer:   "https://viewer.omni.dev.wandelbots.io/omniverse-stream",
	},
}

// Environments lists the known environments, for flag help.
func Environments() []Environment {
	return []Environment{EnvDev}
}

// ErrNotLoggedIn is returned when no simulation login is stored.
var ErrNotLoggedIn = errors.New("not logged in to simulation, run: nova simulation login")

type storedToken struct {
	OAuth2Token oauth2.Token `json:"token"`
	Environment Environment  `json:"environment"`
}

func lookupEnvironment(env Environment) (environment, error) {
	e, ok := environments[env]
	if !ok {
		return environment{}, fmt.Errorf("unknown simulation environment %q", env)
	}
	return e, nil
}

func oauthConfig(e environment) *oauth2.Config {
	base := "https://login.microsoftonline.com/" + e.tenant + "/oauth2/v2.0"
	return &oauth2.Config{
		ClientID: e.clientID,
		// .default requests the app's own API; offline_access the refresh token
		Scopes: []string{e.clientID + "/.default", "openid", "profile", "offline_access"},
		Endpoint: oauth2.Endpoint{
			AuthURL:       base + "/authorize",
			DeviceAuthURL: base + "/devicecode",
			TokenURL:      base + "/token",
			AuthStyle:     oauth2.AuthStyleInParams,
		},
	}
}

// Login runs the device flow against Entra ID and stores the token.
func Login(ctx context.Context, env Environment, print func(format string, a ...any)) error {
	e, err := lookupEnvironment(env)
	if err != nil {
		return err
	}
	config := oauthConfig(e)

	resp, err := config.DeviceAuth(ctx)
	if err != nil {
		return fmt.Errorf("starting device login: %w", err)
	}

	// Entra returns no verification_uri_complete, the code has to be typed in
	url := resp.VerificationURIComplete
	if url == "" {
		url = resp.VerificationURI
	}
	print("Open %s and enter the code %s\n", url, resp.UserCode)
	_ = browser.OpenURL(url)

	token, err := config.DeviceAccessToken(ctx, resp)
	if err != nil {
		return fmt.Errorf("failed to get token: %w", err)
	}
	return storeToken(token, env)
}

// Endpoints are the URLs of the logged-in environment.
type Endpoints struct {
	Broker string
	Viewer string
}

// AccessToken returns a valid access token, refreshing and storing it if it
// expired, together with the endpoints of the logged-in environment.
func AccessToken(ctx context.Context) (token string, endpoints Endpoints, err error) {
	var stored storedToken
	if err := viper.UnmarshalKey(tokenKey, &stored); err != nil || stored.OAuth2Token.RefreshToken == "" {
		return "", Endpoints{}, ErrNotLoggedIn
	}
	e, err := lookupEnvironment(stored.Environment)
	if err != nil {
		return "", Endpoints{}, err
	}

	fresh, err := oauthConfig(e).TokenSource(ctx, &stored.OAuth2Token).Token()
	if err != nil {
		return "", Endpoints{}, fmt.Errorf("refreshing simulation login failed, run: nova simulation login: %w", err)
	}
	if fresh.AccessToken != stored.OAuth2Token.AccessToken {
		// Entra rotates refresh tokens, keep the new one
		if err := storeToken(fresh, stored.Environment); err != nil {
			return "", Endpoints{}, err
		}
	}

	endpoints = Endpoints{Broker: e.broker, Viewer: e.viewer}
	if b := viper.GetString(BrokerKey); b != "" {
		endpoints.Broker = b
	}
	return fresh.AccessToken, endpoints, nil
}

// Logout removes the stored simulation login.
func Logout() error {
	viper.Set(tokenKey, nil)
	return viper.WriteConfig()
}

func storeToken(token *oauth2.Token, env Environment) error {
	viper.Set(tokenKey, storedToken{OAuth2Token: *token, Environment: env})
	return viper.WriteConfig()
}
