package cmd

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"github.com/wandelbotsgmbh/nova-cli/pkg/portal"
)

// fakePortalListings answers listPortalInstancesIn per environment; an environment
// without an entry fails like a missing login.
func fakePortalListings(t *testing.T, listings map[oauth.AuthEnvironment][]string) *[]oauth.AuthEnvironment {
	t.Helper()
	var asked []oauth.AuthEnvironment
	orig := listPortalInstancesIn
	listPortalInstancesIn = func(env oauth.AuthEnvironment) ([]portal.UserInstance, error) {
		asked = append(asked, env)
		if oauth.ActiveEnvironment() != env {
			t.Errorf("listing %s while the active environment is %s: the login of another environment would be sent", env, oauth.ActiveEnvironment())
		}
		ids, ok := listings[env]
		if !ok {
			return nil, errors.New("not logged in")
		}
		var list []portal.UserInstance
		for _, id := range ids {
			list = append(list, portal.UserInstance{InstanceId: id})
		}
		return list, nil
	}
	t.Cleanup(func() { listPortalInstancesIn = orig })
	return &asked
}

func TestInstancePrune(t *testing.T) {
	withConfig(t, `current-instance: gone-prod
instances:
  - {name: alive-prod, kind: cloud, id: aaa, host: aaa.instance.wandelbots.io, env: prod}
  - {name: gone-prod, kind: cloud, id: bbb, host: bbb.instance.wandelbots.io, env: prod}
  - {name: gone-dev, kind: cloud, id: ccc, host: ccc.instance.dev.wandelbots.io, env: dev}
  - {name: stg-nologin, kind: cloud, id: ddd, host: ddd.instance.stg.wandelbots.io, env: stg}
  - {name: lab, kind: host, host: 192.168.1.10}
`)
	fake := newFakeK8sRunService(t)
	mustRun(t, "k8srun", "create", "--name", "cluster-alive") // id-1
	mustRun(t, "k8srun", "create", "--name", "cluster-gone")  // id-2
	fake.gone["id-2"] = true
	mustRun(t, "instance", "select", "gone-prod")
	asked := fakePortalListings(t, map[oauth.AuthEnvironment][]string{
		oauth.AuthProd: {"aaa", "zzz"},
		oauth.AuthDev:  {},
	})

	out, _, err := runWithConfig(t, "instance", "prune", "--dry-run")
	if err == nil {
		t.Error("an unchecked entry must fail the command")
	}
	if r, _ := instances.Load(viper.GetViper()); len(r.Instances) != 7 {
		t.Fatalf("--dry-run must not change the list: %+v", r.Instances)
	}
	if !strings.Contains(out, "would remove gone-prod") {
		t.Errorf("dry run: %q", out)
	}

	out, errOut, err := runWithConfig(t, "instance", "prune")
	if err == nil || !strings.Contains(err.Error(), "1 instance(s) could not be checked") {
		t.Errorf("err = %v", err)
	}
	removed := strings.Fields(strings.ReplaceAll(out, "removed ", ""))
	sort.Strings(removed)
	if strings.Join(removed, ",") != "cluster-gone,gone-dev,gone-prod" {
		t.Errorf("removed %v, want cluster-gone, gone-dev, gone-prod", removed)
	}
	if !strings.Contains(errOut, `keeping "stg-nologin"`) || !strings.Contains(errOut, `selected instance "gone-prod" was removed`) {
		t.Errorf("errOut = %q", errOut)
	}

	r, _ := instances.Load(viper.GetViper())
	var kept []string
	for _, i := range r.Instances {
		kept = append(kept, i.Name)
	}
	sort.Strings(kept)
	if strings.Join(kept, ",") != "alive-prod,cluster-alive,lab,stg-nologin" {
		t.Errorf("kept %v", kept)
	}
	if r.Current != "" {
		t.Errorf("the removed selection must be cleared, current %q", r.Current)
	}

	// one listing per environment and run, in that environment
	envs := map[oauth.AuthEnvironment]int{}
	for _, e := range *asked {
		envs[e]++
	}
	if envs[oauth.AuthProd] != 2 || envs[oauth.AuthDev] != 2 || envs[oauth.AuthStg] != 2 {
		t.Errorf("listings per environment: %v", envs)
	}
}

// An entry without env is checked in the default environment, as every other command
// resolves it, not in prod.
func TestInstancePruneEntryWithoutEnv(t *testing.T) {
	withConfig(t, `default-env: dev
instances:
  - {name: default, kind: cloud, id: ccc, host: ccc.instance.dev.wandelbots.io}
`)
	fakePortalListings(t, map[oauth.AuthEnvironment][]string{
		oauth.AuthProd: {},
		oauth.AuthDev:  {"ccc"},
	})
	if out, errOut, err := runWithConfig(t, "instance", "prune"); err != nil || out != "" {
		t.Fatalf("prune: %v %q %q", err, out, errOut)
	}
	if r, _ := instances.Load(viper.GetViper()); len(r.Instances) != 1 {
		t.Errorf("the dev instance must be kept: %+v", r.Instances)
	}
}

// Nucleus servers migrated from instance entries survive removing those entries.
func TestPruneKeepsMigratedNucleusServers(t *testing.T) {
	path := withConfig(t, `current-instance: alive
instances:
  - {name: alive, kind: cloud, id: aaa, host: aaa.instance.wandelbots.io, env: prod}
  - {name: gone, kind: cloud, id: bbb, host: bbb.instance.wandelbots.io, env: prod, nucleus-host: nucleus.wb.wandelbots.io, nucleus-token: wbtok}
`)
	fakePortalListings(t, map[oauth.AuthEnvironment][]string{oauth.AuthProd: {"aaa"}})
	mustRun(t, "instance", "prune")
	if !strings.Contains(readConfig(t, path), "nucleus-servers") {
		t.Errorf("the migrated servers must be written:\n%s", readConfig(t, path))
	}
	if wb, ok := loadServers(t).Get("wb"); !ok || wb.Token != "wbtok" {
		t.Errorf("server of the removed entry lost: %+v", loadServers(t))
	}
}
