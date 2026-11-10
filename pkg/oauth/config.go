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
func DefaultEnvironment() AuthEnvironment { return defaultEnvironment(viper.GetViper()) }

func defaultEnvironment(v *viper.Viper) AuthEnvironment {
	if env, err := ParseEnvironment(v.GetString(defaultEnvKey)); err == nil {
		return env
	}
	if legacy, err := loadLegacyToken(v); err == nil && legacy.OAuth2Token.AccessToken != "" {
		return legacy.AuthEnvironment
	}
	return AuthProd
}

// SetDefaultEnvironment makes env the default and mirrors its login into the legacy
// key. Called after an explicit 'nova login', never on token refresh.
func SetDefaultEnvironment(env AuthEnvironment) error {
	keepLegacyLogin(viper.GetViper())
	viper.Set(defaultEnvKey, string(env))
	if token, err := LoadTokenFor(env); err == nil && token.OAuth2Token.AccessToken != "" {
		viper.Set(tokenKey, token)
	}
	return viper.WriteConfig()
}

// StoreToken stores the login of env. It doesn't change the default environment; the
// legacy key is only updated if env is the default.
func StoreToken(token *oauth2.Token, env AuthEnvironment) error {
	setToken(viper.GetViper(), token, env)
	return viper.WriteConfig()
}

// setToken sets the login of env in v without writing it.
func setToken(v *viper.Viper, token *oauth2.Token, env AuthEnvironment) {
	waboToken := WaboAuthToken{
		OAuth2Token:     *token,
		AuthEnvironment: env,
	}
	keepLegacyLogin(v)
	v.Set(loginsKey+"."+string(env), waboToken)
	if env == defaultEnvironment(v) {
		v.Set(tokenKey, waboToken)
	}
}

// LoadToken loads the login of the active environment.
func LoadToken() (WaboAuthToken, error) {
	return LoadTokenFor(ActiveEnvironment())
}

// LoadTokenFor loads the login of env. A legacy login counts for its own environment.
func LoadTokenFor(env AuthEnvironment) (WaboAuthToken, error) {
	return loadTokenFor(viper.GetViper(), env)
}

func loadTokenFor(v *viper.Viper, env AuthEnvironment) (WaboAuthToken, error) {
	key := loginsKey + "." + string(env)
	if v.IsSet(key) {
		var token WaboAuthToken
		if err := v.UnmarshalKey(key, &token); err != nil {
			return WaboAuthToken{}, err
		}
		token.AuthEnvironment = env
		// an older CLI version may have refreshed the legacy copy since (refresh tokens
		// can be rotated), so the newer one wins
		if legacy, err := loadLegacyToken(v); err == nil && legacy.AuthEnvironment == env && legacy.OAuth2Token.Expiry.After(token.OAuth2Token.Expiry) {
			return legacy, nil
		}
		return token, nil
	}
	if legacy, err := loadLegacyToken(v); err == nil && legacy.AuthEnvironment == env {
		return legacy, nil
	}
	return WaboAuthToken{AuthEnvironment: env}, nil
}

// keepLegacyLogin copies a login that only exists in the legacy key (written by older
// CLI versions) to logins.<env> before the legacy key may be overwritten.
func keepLegacyLogin(v *viper.Viper) {
	legacy, err := loadLegacyToken(v)
	if err != nil || legacy.OAuth2Token.AccessToken == "" && legacy.OAuth2Token.RefreshToken == "" {
		return
	}
	key := loginsKey + "." + string(legacy.AuthEnvironment)
	if !v.IsSet(key) {
		v.Set(key, legacy)
	}
}

func loadLegacyToken(v *viper.Viper) (WaboAuthToken, error) {
	var token WaboAuthToken
	if err := v.UnmarshalKey(tokenKey, &token); err != nil {
		return token, err
	}
	if token.AuthEnvironment == "" {
		token.AuthEnvironment = AuthProd
	}
	return token, nil
}

// configOnDisk returns the config file as it is now, separate from this process's
// in-memory config, or nil if there is none.
func configOnDisk() *viper.Viper {
	file := viper.ConfigFileUsed()
	if file == "" {
		return nil
	}
	v := viper.New()
	v.SetConfigFile(file)
	if err := v.ReadInConfig(); err != nil {
		return nil
	}
	return v
}

func loggedIn(t WaboAuthToken) bool {
	return t.OAuth2Token.AccessToken != "" || t.OAuth2Token.RefreshToken != ""
}

// adoptToken makes a login read from disk the one this process uses, without writing.
func adoptToken(token WaboAuthToken, env AuthEnvironment) {
	setToken(viper.GetViper(), &token.OAuth2Token, env)
}
