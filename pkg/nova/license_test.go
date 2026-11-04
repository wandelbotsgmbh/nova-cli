package nova

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"golang.org/x/oauth2"
)

// unlicensedHost answers like the auth-gateway of a host without a license: 403 for
// everything except the license management endpoints.
func unlicensedHost(t *testing.T, activated *bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v2/license" {
			var body struct {
				OwnerRefreshToken string `json:"owner_refresh_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			*activated = body.OwnerRefreshToken == "refresh"
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":403,"message":"license not installed"}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(clearCapabilityCache)
	return srv
}

func TestActivateLicenseOnUnlicensedHost(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("token", oauth.WaboAuthToken{
		OAuth2Token:     oauth2.Token{AccessToken: "access", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)},
		AuthEnvironment: oauth.AuthProd,
	})
	var activated bool
	srv := unlicensedHost(t, &activated)

	if err := ActivateLicense(srv.URL); err != nil {
		t.Fatalf("ActivateLicense: %v", err)
	}
	if !activated {
		t.Error("expected the activation request with the owner refresh token")
	}
}

func TestCapabilityErrorKeepsTheCause(t *testing.T) {
	var activated bool
	srv := unlicensedHost(t, &activated)

	_, err := ListCells(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "license not installed") {
		t.Errorf("expected the host's answer in the error, got %v", err)
	}
}

func TestActivateLicenseWithoutLoginNamesTheEnvironment(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	oauth.SetActiveEnvironment(oauth.AuthStg)
	t.Cleanup(func() { oauth.SetActiveEnvironment("") })
	var activated bool
	srv := unlicensedHost(t, &activated)

	err := ActivateLicense(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "nova login --env stg") {
		t.Errorf("expected a hint for the stg login, got %v", err)
	}
	if activated {
		t.Error("no activation without a login")
	}
}
