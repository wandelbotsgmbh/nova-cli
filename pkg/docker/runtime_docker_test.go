//go:build !windows

package docker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDocker puts a docker stub on PATH that logs its arguments and fails
// "docker buildx ..." the way the real CLI does without the buildx plugin.
func fakeDocker(t *testing.T, withBuildx bool) (logFile string) {
	t.Helper()
	dir := t.TempDir()
	logFile = filepath.Join(dir, "calls")
	buildx := "exit 0"
	if !withBuildx {
		buildx = `echo "unknown shorthand flag: 't' in -t" >&2; exit 125`
	}
	script := "#!/bin/sh\necho \"$*\" >> " + logFile + "\n" +
		"if [ \"$1\" = buildx ]; then " + buildx + "; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile
}

func lastCall(t *testing.T, logFile string) string {
	t.Helper()
	b, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return lines[len(lines)-1]
}

func TestDockerBuildImage(t *testing.T) {
	log := fakeDocker(t, true)
	if err := (dockerRuntime{}).BuildImage("ctx", "img"); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	want := "buildx build --load -t img --platform linux/amd64 ctx"
	if got := lastCall(t, log); got != want {
		t.Errorf("docker called with %q, want %q", got, want)
	}
}

func TestDockerBuildsWithoutBuildx(t *testing.T) {
	log := fakeDocker(t, false)
	rt := dockerRuntime{}
	builds := map[string]func() error{
		"BuildImage":                func() error { return rt.BuildImage("ctx", "img") },
		"BuildImageOCI":             func() error { return rt.BuildImageOCI("ctx", "img", "oci") },
		"BuildAndPushMultiPlatform": func() error { return rt.BuildAndPushMultiPlatform("Dockerfile", "ctx", "img") },
	}
	for name, build := range builds {
		if err := build(); !errors.Is(err, errBuildxMissing) {
			t.Errorf("%s error = %v, want errBuildxMissing", name, err)
		}
		// only the "docker buildx version" probe may run, never a build
		if got := lastCall(t, log); got != "buildx version" {
			t.Errorf("%s: last docker call %q, want only the buildx probe", name, got)
		}
	}
}
