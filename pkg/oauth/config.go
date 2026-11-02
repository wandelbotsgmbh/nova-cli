package oauth

import (
	"fmt"

	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

const (
	// legacy single login; still written for the default environment so older CLI
	// versions keep working
	tokenKey = "token"
	// one login per environment: logins.<env>
	loginsKey = "logins"
	// environment used when neither --env / NOVA_ENV nor the target instance sets one
	defaultEnvKey = "default-env"
)

type WaboAuthToken struct {
	OAuth2Token     oauth2.Token    `json:"token"`
	AuthEnvironment AuthEnvironment `json:"authEnvironment"`
}

// Environments lists the known portal environments.
var Environments = []AuthEnvironment{AuthProd, AuthStg, AuthDev}

// ParseEnvironment validates an environment name.
func ParseEnvironment(s string) (AuthEnvironment, error) {
	for _, env := range Environments {
		if AuthEnvironment(s) == env {
			return env, nil
		}
	}
	return "", fmt.Errorf("invalid environment %q: use prod, stg or dev", s)
}

// active is the environment of the current invocation, set once by the command layer.
var active AuthEnvironment

// SetActiveEnvironment sets the environment whose login LoadToken returns and
// SetAuthForRequest attaches for this invocation.
func SetActiveEnvironment(env AuthEnvironment) {
	active = env
}

// ActiveEnvironment returns the environment of this invocation, or the default.
func ActiveEnvironment() AuthEnvironment {
	if active != "" {
		return active
	}
	return DefaultEnvironment()
}

// DefaultEnvironment is the environment of the last 'nova login'; for configs written
// by older CLI versions the environment of the legacy login; otherwise prod.
func DefaultEnvironment() AuthEnvironment {
	if env, err := ParseEnvironment(viper.GetString(defaultEnvKey)); err == nil {
		return env
	}
	if legacy, err := loadLegacyToken(); err == nil && legacy.OAuth2Token.AccessToken != "" {
		return legacy.AuthEnvironment
	}
	return AuthProd
}

// SetDefaultEnvironment makes env the default and mirrors its login into the legacy
// key. Called after an explicit 'nova login', never on token refresh.
func SetDefaultEnvironment(env AuthEnvironment) error {
	keepLegacyLogin()
	viper.Set(defaultEnvKey, string(env))
	if token, err := LoadTokenFor(env); err == nil && token.OAuth2Token.AccessToken != "" {
		viper.Set(tokenKey, token)
	}
	return viper.WriteConfig()
}

// StoreToken stores the login of env. It doesn't change the default environment; the
// legacy key is only updated if env is the default.
func StoreToken(token *oauth2.Token, env AuthEnvironment) error {
	waboToken := WaboAuthToken{
		OAuth2Token:     *token,
		AuthEnvironment: env,
	}
	keepLegacyLogin()
	viper.Set(loginsKey+"."+string(env), waboToken)
	if env == DefaultEnvironment() {
		viper.Set(tokenKey, waboToken)
	}
	return viper.WriteConfig()
}

// LoadToken loads the login of the active environment.
func LoadToken() (WaboAuthToken, error) {
	return LoadTokenFor(ActiveEnvironment())
}

// LoadTokenFor loads the login of env. A legacy login counts for its own environment.
func LoadTokenFor(env AuthEnvironment) (WaboAuthToken, error) {
	key := loginsKey + "." + string(env)
	if viper.IsSet(key) {
		var token WaboAuthToken
		if err := viper.UnmarshalKey(key, &token); err != nil {
			return WaboAuthToken{}, err
		}
		token.AuthEnvironment = env
		// an older CLI version may have refreshed the legacy copy since (refresh tokens
		// can be rotated), so the newer one wins
		if legacy, err := loadLegacyToken(); err == nil && legacy.AuthEnvironment == env && legacy.OAuth2Token.Expiry.After(token.OAuth2Token.Expiry) {
			return legacy, nil
		}
		return token, nil
	}
	if legacy, err := loadLegacyToken(); err == nil && legacy.AuthEnvironment == env {
		return legacy, nil
	}
	return WaboAuthToken{AuthEnvironment: env}, nil
}

// keepLegacyLogin copies a login that only exists in the legacy key (written by older
// CLI versions) to logins.<env> before the legacy key may be overwritten.
func keepLegacyLogin() {
	legacy, err := loadLegacyToken()
	if err != nil || legacy.OAuth2Token.AccessToken == "" && legacy.OAuth2Token.RefreshToken == "" {
		return
	}
	key := loginsKey + "." + string(legacy.AuthEnvironment)
	if !viper.IsSet(key) {
		viper.Set(key, legacy)
	}
}

func loadLegacyToken() (WaboAuthToken, error) {
	var token WaboAuthToken
	if err := viper.UnmarshalKey(tokenKey, &token); err != nil {
		return token, err
	}
	if token.AuthEnvironment == "" {
		token.AuthEnvironment = AuthProd
	}
	return token, nil
}
