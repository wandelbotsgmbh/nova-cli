package oauth

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

func withConfig(t *testing.T, content string) string {
	t.Helper()
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	viper.Reset()
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	SetActiveEnvironment("")
	t.Cleanup(func() { SetActiveEnvironment(""); viper.Reset() })
	return path
}

func reread(t *testing.T, path string) {
	t.Helper()
	viper.Reset()
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
}

// as written by older CLI versions (viper marshals the struct with lowercase field names)
const legacyDevLogin = `token:
  authenvironment: dev
  oauth2token:
    accesstoken: dev-access
    refreshtoken: dev-refresh
`

func TestDefaultEnvironment(t *testing.T) {
	withConfig(t, "")
	if got := DefaultEnvironment(); got != AuthProd {
		t.Errorf("no login: default = %q, want prod", got)
	}

	withConfig(t, legacyDevLogin)
	if got := DefaultEnvironment(); got != AuthDev {
		t.Errorf("legacy dev login: default = %q, want dev", got)
	}
	if tok, _ := LoadTokenFor(AuthDev); tok.OAuth2Token.AccessToken != "dev-access" {
		t.Errorf("legacy login must count for its environment, got %+v", tok)
	}
	if tok, _ := LoadTokenFor(AuthProd); tok.OAuth2Token.AccessToken != "" {
		t.Errorf("legacy dev login must not be used for prod, got %+v", tok)
	}

	withConfig(t, legacyDevLogin+"default-env: stg\n")
	if got := DefaultEnvironment(); got != AuthStg {
		t.Errorf("explicit default = %q, want stg", got)
	}
}

func TestLoginsPerEnvironment(t *testing.T) {
	path := withConfig(t, legacyDevLogin)

	// a prod login (or refresh) must not replace the dev login nor change the default
	if err := StoreToken(&oauth2.Token{AccessToken: "prod-access", RefreshToken: "prod-refresh"}, AuthProd); err != nil {
		t.Fatal(err)
	}
	reread(t, path)
	if got := DefaultEnvironment(); got != AuthDev {
		t.Errorf("default changed by StoreToken: %q", got)
	}
	dev, _ := LoadTokenFor(AuthDev)
	prod, _ := LoadTokenFor(AuthProd)
	if dev.OAuth2Token.AccessToken != "dev-access" || prod.OAuth2Token.AccessToken != "prod-access" {
		t.Fatalf("dev=%+v prod=%+v", dev, prod)
	}
	if legacy, _ := loadLegacyToken(viper.GetViper()); legacy.OAuth2Token.AccessToken != "dev-access" {
		t.Errorf("legacy key must keep the default environment's login, got %+v", legacy)
	}

	// an explicit login makes prod the default and mirrors it for older CLI versions
	if err := SetDefaultEnvironment(AuthProd); err != nil {
		t.Fatal(err)
	}
	reread(t, path)
	if got := DefaultEnvironment(); got != AuthProd {
		t.Errorf("default = %q, want prod", got)
	}
	if legacy, _ := loadLegacyToken(viper.GetViper()); legacy.OAuth2Token.AccessToken != "prod-access" || legacy.AuthEnvironment != AuthProd {
		t.Errorf("legacy mirror = %+v", legacy)
	}
	if dev, _ := LoadTokenFor(AuthDev); dev.OAuth2Token.AccessToken != "dev-access" {
		t.Errorf("dev login lost: %+v", dev)
	}

	SetActiveEnvironment(AuthDev)
	if tok, _ := LoadToken(); tok.OAuth2Token.AccessToken != "dev-access" {
		t.Errorf("LoadToken must use the active environment, got %+v", tok)
	}
}

func TestSetAuthForRequestOnlyToCloudHosts(t *testing.T) {
	withConfig(t, `default-env: prod
logins:
  prod:
    oauth2token:
      accesstoken: prod-access
  dev:
    oauth2token:
      accesstoken: dev-access
`)
	auth := func(url string) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		_ = SetAuthForRequest(req)
		return req.Header.Get("Authorization")
	}

	if got := auth("https://abc.instance.wandelbots.io/api/v2/cells"); got != "Bearer prod-access" {
		t.Errorf("cloud host: %q", got)
	}
	SetActiveEnvironment(AuthDev)
	if got := auth("https://api.portal.dev.wandelbots.io/instances"); got != "Bearer dev-access" {
		t.Errorf("dev portal: %q", got)
	}
	for _, url := range []string{
		"http://192.168.1.10/api/v2/cells",
		"https://ipc.example.com/api",
		"https://wandelbots.io.example.com/api",
		"https://notwandelbots.io/api",
	} {
		if got := auth(url); got != "" {
			t.Errorf("%s must not get the portal login, got %q", url, got)
		}
	}
}

func TestSendsTokenTo(t *testing.T) {
	for host, want := range map[string]bool{
		"abc.instance.wandelbots.io":          true,
		"https://abc.instance.wandelbots.io/": true,
		"api.portal.dev.wandelbots.io:443":    true,
		"wandelbots.io":                       true,
		"192.168.1.10":                        false,
		"http://10.0.0.1:8080":                false,
		"wandelbots.io.example.com":           false,
		"evilwandelbots.io":                   false,
	} {
		if got := SendsTokenTo(host); got != want {
			t.Errorf("SendsTokenTo(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestNewerLegacyLoginWins(t *testing.T) {
	withConfig(t, `default-env: dev
logins:
  dev:
    oauth2token:
      accesstoken: old
      expiry: 2026-01-01T00:00:00Z
token:
  authenvironment: dev
  oauth2token:
    accesstoken: refreshed-by-older-cli
    expiry: 2026-06-01T00:00:00Z
`)
	if tok, _ := LoadTokenFor(AuthDev); tok.OAuth2Token.AccessToken != "refreshed-by-older-cli" {
		t.Errorf("got %q, want the newer legacy login", tok.OAuth2Token.AccessToken)
	}
}
