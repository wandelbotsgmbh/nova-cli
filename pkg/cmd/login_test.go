package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"golang.org/x/oauth2"
)

// fakePortalLogin replaces the network login and records the environment.
func fakePortalLogin(t *testing.T) *[]oauth.AuthEnvironment {
	t.Helper()
	var calls []oauth.AuthEnvironment
	orig := portalLogin
	portalLogin = func(_ context.Context, env oauth.AuthEnvironment, _ string) error {
		calls = append(calls, env)
		return oauth.StoreToken(&oauth2.Token{AccessToken: "a-" + string(env), Expiry: time.Now().Add(time.Hour)}, env)
	}
	t.Cleanup(func() { portalLogin = orig })
	return &calls
}

const stgCloudInstance = `current-instance: c
instances:
  - name: c
    kind: cloud
    host: c.instance.stg.wandelbots.io
    id: c
    env: stg
`

// A plain 'nova login' keeps the current environment instead of switching the
// default to prod.
func TestLoginKeepsCurrentEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, config, novaEnv string
		args                  []string
		want                  oauth.AuthEnvironment
	}{
		{"fresh config", "", "", nil, oauth.AuthProd},
		{"default kept", "default-env: dev\n", "", nil, oauth.AuthDev},
		{"cloud target", "default-env: dev\n" + stgCloudInstance, "", nil, oauth.AuthStg},
		{"NOVA_ENV", "default-env: dev\n", "stg", nil, oauth.AuthStg},
		{"--env", "default-env: dev\n", "", []string{"--env", "prod"}, oauth.AuthProd},
		{"--env beats cloud target", "default-env: dev\n" + stgCloudInstance, "", []string{"--env", "dev"}, oauth.AuthDev},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := withConfig(t, tc.config)
			t.Setenv(envVarName, tc.novaEnv)
			calls := fakePortalLogin(t)

			_, stderr, err := runWithConfig(t, append([]string{"login"}, tc.args...)...)
			if err != nil {
				t.Fatalf("login: %v\n%s", err, stderr)
			}
			if len(*calls) != 1 || (*calls)[0] != tc.want {
				t.Errorf("logged in to %v, want [%s]", *calls, tc.want)
			}
			if cfg := readConfig(t, path); !strings.Contains(cfg, "default-env: "+string(tc.want)) {
				t.Errorf("default environment not %s:\n%s", tc.want, cfg)
			}
		})
	}
}
