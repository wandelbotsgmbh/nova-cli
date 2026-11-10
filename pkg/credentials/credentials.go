package credentials

import (
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/configfile"
	"github.com/zalando/go-keyring"
)

const (
	basicAuthKey     = "wandelbots_novacli_basic_auth"
	basicAuthUserKey = "basic_user"
)

type Credentials struct {
	Username string
	Password string
}

// SetBasicAuth sets the credentials for the basic auth
func SetBasicAuth(user string, pwd string) error {
	if err := configfile.Set(viper.GetViper(), basicAuthUserKey, user); err != nil {
		return err
	}
	return keyring.Set(basicAuthKey, user, pwd)
}

// GetCredentials for basic auth
func GetBasicAuth() (Credentials, error) {
	user := viper.GetString(basicAuthUserKey)
	pwd, err := keyring.Get(basicAuthKey, user)
	return Credentials{Username: user, Password: pwd}, err
}
