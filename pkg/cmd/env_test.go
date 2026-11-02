package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
)

const multiEnvConfig = `default-env: prod
current-instance: devcloud
instances:
  - name: devcloud
    kind: cloud
    env: dev
    host: abc.instance.dev.example.wandelbots.io
    id: abc
  - name: lab
    kind: host
    host: 192.168.1.10
`

func viewEnv(t *testing.T, args ...string) string {
	t.Helper()
	out := mustRun(t, append(args, "config", "view")...)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "env: ") {
			return strings.TrimPrefix(line, "env: ")
		}
	}
	t.Fatalf("no env line in:\n%s", out)
	return ""
}

func TestEnvResolution(t *testing.T) {
	withConfig(t, multiEnvConfig)

	if got := viewEnv(t); got != "dev (from instance)" {
		t.Errorf("cloud target: env = %q", got)
	}
	if got := viewEnv(t, "--instance", "lab"); got != "prod (from default)" {
		t.Errorf("host target: env = %q", got)
	}
	if got := viewEnv(t, "--instance", "lab", "--env", "stg"); got != "stg (from flag)" {
		t.Errorf("--env: env = %q", got)
	}
	t.Setenv(envVarName, "stg")
	if got := viewEnv(t, "--instance", "lab"); got != "stg (from env)" {
		t.Errorf("NOVA_ENV: env = %q", got)
	}

	// an explicit environment that contradicts the cloud target is refused
	if _, _, err := runWithConfig(t, "config", "view"); err == nil || !strings.Contains(err.Error(), `in "dev"`) {
		t.Errorf("NOVA_ENV=stg with a dev instance: err = %v", err)
	}
	t.Setenv(envVarName, "")
	if _, _, err := runWithConfig(t, "--env", "prod", "config", "view"); err == nil {
		t.Error("--env prod with a dev instance must fail")
	}
	if _, _, err := runWithConfig(t, "--env", "qa", "--instance", "lab", "config", "view"); err == nil || !strings.Contains(err.Error(), "invalid environment") {
		t.Errorf("unknown env: err = %v", err)
	}
}

func TestEnvForCloudOnlyCommandsOnOtherEntries(t *testing.T) {
	withConfig(t, multiEnvConfig)
	// stop <host entry> still fails on the kind, before any portal call
	if _, _, err := runWithConfig(t, "instance", "stop", "lab"); err == nil || !strings.Contains(err.Error(), "only works for cloud instances") {
		t.Errorf("err = %v", err)
	}
	// --env that contradicts a named cloud entry is refused before contacting the portal
	if _, _, err := runWithConfig(t, "--instance", "lab", "--env", "prod", "instance", "stop", "devcloud"); err == nil || !strings.Contains(err.Error(), `in "dev"`) {
		t.Errorf("err = %v", err)
	}
}

func TestInstanceListShowsEnv(t *testing.T) {
	withConfig(t, multiEnvConfig)
	out := mustRun(t, "instance", "list")
	if !strings.Contains(out, "ENV") || !strings.Contains(out, "dev") {
		t.Errorf("list must show the environment of cloud entries:\n%s", out)
	}
	r, _ := instances.Load(viper.GetViper())
	if c, _ := r.Get("devcloud"); c.Env != "dev" {
		t.Errorf("env not kept: %+v", c)
	}
}
