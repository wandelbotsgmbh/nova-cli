package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

// tokenServer answers refresh requests with a new access token and counts them.
func tokenServer(t *testing.T) *int {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"refreshed","refresh_token":"rotated","token_type":"Bearer","expires_in":86400}`))
	}))
	t.Cleanup(srv.Close)
	old := refreshConfig
	refreshConfig = func(AuthEnvironment) *oauth2.Config {
		return &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{TokenURL: srv.URL}}
	}
	t.Cleanup(func() { refreshConfig = old })
	return &calls
}

func loginYAML(access string, expiry time.Time) string {
	return "    oauth2token:\n      accesstoken: " + access + "\n      tokentype: Bearer\n      refreshtoken: r-" + access +
		"\n      expiry: " + expiry.UTC().Format(time.RFC3339) + "\n    authenvironment: prod\n"
}

const configHead = "default-env: prod\ncurrent-instance: a\ninstances:\n  - name: a\n    kind: cloud\n    host: a.instance.wandelbots.io\n"

// Another process changed the config after this one read it: a refresh must neither
// revert those changes nor ignore a login the other process refreshed.
func TestRefreshToken_KeepsChangesOfOtherProcesses(t *testing.T) {
	calls := tokenServer(t)
	path := withConfig(t, configHead+"logins:\n  prod:\n"+loginYAML("stale", time.Now().Add(-time.Hour)))

	// meanwhile in another terminal: new instance selected, login still expired
	other := strings.Replace(configHead, "current-instance: a", "current-instance: b", 1) +
		"  - name: b\n    kind: host\n    host: 10.0.0.1\n" +
		"logins:\n  prod:\n" + loginYAML("stale", time.Now().Add(-time.Hour))
	if err := os.WriteFile(path, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RefreshToken(context.Background(), AuthProd); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("expected one refresh, got %d", *calls)
	}
	written, _ := os.ReadFile(path)
	for _, want := range []string{"current-instance: b", "name: b", "accesstoken: refreshed"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("config lost %q:\n%s", want, written)
		}
	}
	if token, _ := LoadTokenFor(AuthProd); token.OAuth2Token.AccessToken != "refreshed" {
		t.Errorf("this process must use the new login, got %q", token.OAuth2Token.AccessToken)
	}
}

func TestRefreshToken_AdoptsLoginRefreshedElsewhere(t *testing.T) {
	calls := tokenServer(t)
	path := withConfig(t, configHead+"logins:\n  prod:\n"+loginYAML("stale", time.Now().Add(-time.Hour)))
	fresh := configHead + "logins:\n  prod:\n" + loginYAML("fresh", time.Now().Add(time.Hour))
	if err := os.WriteFile(path, []byte(fresh), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	if err := RefreshToken(context.Background(), AuthProd); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if *calls != 0 {
		t.Errorf("a valid login on disk must not be refreshed again (refresh tokens rotate), got %d calls", *calls)
	}
	if token, _ := LoadTokenFor(AuthProd); token.OAuth2Token.AccessToken != "fresh" {
		t.Errorf("expected the login from disk, got %q", token.OAuth2Token.AccessToken)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("adopting a login must not write the config")
	}
}

func TestRefreshToken_WithoutConfigFile(t *testing.T) {
	tokenServer(t)
	viper.Reset()
	t.Cleanup(viper.Reset)
	if err := RefreshToken(context.Background(), AuthProd); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("expected 'not logged in', got %v", err)
	}
}

// The file was overwritten with an older copy (another process saved its snapshot):
// the newer, still valid login of this process counts and is written back, without a
// refresh with the stale, possibly rotated refresh token.
func TestRefreshToken_NewerLoginInMemoryWins(t *testing.T) {
	calls := tokenServer(t)
	path := withConfig(t, configHead+"logins:\n  prod:\n"+loginYAML("fresh", time.Now().Add(time.Hour)))
	older := strings.Replace(configHead, "current-instance: a", "current-instance: b", 1) +
		"logins:\n  prod:\n" + loginYAML("stale", time.Now().Add(-time.Hour))
	if err := os.WriteFile(path, []byte(older), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RefreshToken(context.Background(), AuthProd); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if *calls != 0 {
		t.Errorf("expected no refresh, got %d", *calls)
	}
	if token, _ := LoadTokenFor(AuthProd); token.OAuth2Token.AccessToken != "fresh" {
		t.Errorf("expected the login of this process, got %q", token.OAuth2Token.AccessToken)
	}
	written, _ := os.ReadFile(path)
	for _, want := range []string{"current-instance: b", "accesstoken: fresh"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("config lacks %q:\n%s", want, written)
		}
	}
}

// Another process is writing the config right now (viper truncates the file first):
// the empty file is neither "not logged in" nor a copy to write back.
func TestRefreshToken_IgnoresFileBeingWritten(t *testing.T) {
	calls := tokenServer(t)
	path := withConfig(t, configHead+"logins:\n  prod:\n"+loginYAML("fresh", time.Now().Add(time.Hour)))
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RefreshToken(context.Background(), AuthProd); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if *calls != 0 {
		t.Errorf("expected no refresh, got %d", *calls)
	}
	if after, _ := os.ReadFile(path); len(after) != 0 {
		t.Errorf("the file must be left to its writer, got:\n%s", after)
	}
}

// A login of another environment that only exists in the legacy key (older CLI
// versions) survives when a login from the file replaces that key.
func TestRefreshToken_AdoptingKeepsLegacyLogin(t *testing.T) {
	tokenServer(t)
	legacyStg := "token:\n" + strings.Replace(loginYAML("stg-login", time.Now().Add(time.Hour)), "authenvironment: prod", "authenvironment: stg", 1)
	path := withConfig(t, configHead+legacyStg+"logins:\n  prod:\n"+loginYAML("stale", time.Now().Add(-time.Hour)))
	fresh := configHead + legacyStg + "logins:\n  prod:\n" + loginYAML("fresh", time.Now().Add(time.Hour))
	if err := os.WriteFile(path, []byte(fresh), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RefreshToken(context.Background(), AuthProd); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if token, _ := LoadTokenFor(AuthStg); token.OAuth2Token.AccessToken != "stg-login" {
		t.Errorf("the stg login is gone, got %q", token.OAuth2Token.AccessToken)
	}
}
