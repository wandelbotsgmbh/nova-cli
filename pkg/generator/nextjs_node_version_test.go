package generator

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// The Next.js template names its Node version in four places; Dependabot only
// updates some of them (npm: @types/node; the template Dockerfile not at all),
// so this keeps them in step.
func TestNextJsTemplateNodeVersionInSync(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		b, err := nextJsAppProject.ReadFile("NextJsApp/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	nvmrc := strings.TrimSpace(read(".nvmrc"))

	docker := regexp.MustCompile(`(?m)^FROM node:([0-9.]+)-`).FindStringSubmatch(read("Dockerfile"))
	if docker == nil {
		t.Fatal("Dockerfile: no 'FROM node:<version>-' line")
	}
	pnpm := regexp.MustCompile(`(?m)^useNodeVersion:\s*([0-9.]+)`).FindStringSubmatch(read("pnpm-workspace.yaml"))
	if pnpm == nil {
		t.Fatal("pnpm-workspace.yaml: no useNodeVersion")
	}
	if docker[1] != nvmrc || pnpm[1] != nvmrc {
		t.Errorf("Node versions differ: .nvmrc %s, Dockerfile %s, pnpm-workspace.yaml useNodeVersion %s", nvmrc, docker[1], pnpm[1])
	}

	var pkg struct {
		DevDependencies map[string]string `json:"devDependencies"`
		Dependencies    map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(read("package.json")), &pkg); err != nil {
		t.Fatal(err)
	}
	types := pkg.DevDependencies["@types/node"]
	if types == "" {
		types = pkg.Dependencies["@types/node"]
	}
	major := strings.SplitN(nvmrc, ".", 2)[0]
	if got := strings.SplitN(strings.TrimLeft(types, "^~>= "), ".", 2)[0]; got != major {
		t.Errorf("@types/node %q doesn't match Node %s (major %s)", types, nvmrc, major)
	}
}
