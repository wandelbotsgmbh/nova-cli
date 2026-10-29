package oauth

import (
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

const (
	tokenKey = "token"
)

type WaboAuthToken struct {
	OAuth2Token     oauth2.Token    `json:"token"`
	AuthEnvironment AuthEnvironment `json:"authEnvironment"`
}

// stores the token in the config
func StoreToken(token *oauth2.Token, env AuthEnvironment) error {
	waboToken := WaboAuthToken{
		OAuth2Token:     *token,
		AuthEnvironment: env,
	}
	viper.Set(tokenKey, waboToken)
	return viper.WriteConfig()
}

// Loads the token from the config
func LoadToken() (WaboAuthToken, error) {
	var token WaboAuthToken
	err := viper.UnmarshalKey(tokenKey, &token)
	return token, err
}
