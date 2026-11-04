package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/version"
	"golang.org/x/oauth2"
)

// Token refreshes and the device flow go through the oauth2 library, which uses
// the client from the context.
func TestOAuthRequestsSendUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"a","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	config := &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{TokenURL: srv.URL}}
	expired := &oauth2.Token{RefreshToken: "r", Expiry: time.Now().Add(-time.Hour)}
	if _, err := config.TokenSource(withHTTPClient(context.Background()), expired).Token(); err != nil {
		t.Fatal(err)
	}
	if got != version.UserAgent() {
		t.Errorf("User-Agent = %q, want %q", got, version.UserAgent())
	}
}
