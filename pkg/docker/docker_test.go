package docker

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestCreateLocalImageName(t *testing.T) {
	now := time.Unix(1757000000, 0)

	tests := []struct {
		name     string
		registry string
		image    string
		expected string
	}{
		{"Plain registry", "registry.example.com/team", "My App", "registry.example.com/team/my_app:local-1757000000"},
		{"Scheme and trailing slash stripped", "https://registry.example.com/", "app", "registry.example.com/app:local-1757000000"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := CreateLocalImageName(tc.registry, tc.image, now)
			if actual != tc.expected {
				t.Errorf("CreateLocalImageName(%q, %q) = %q, expected %q", tc.registry, tc.image, actual, tc.expected)
			}
		})
	}
}

func TestNormalizeImageName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"All lowercase", "valid_name", "valid_name"},
		{"Uppercase letters", "Invalid_Name", "invalid_name"},
		{"Unallowed characters", "invalid!name$", "invalid_name_"},
		{"Starts with unallowed characters", "!invalidname", "nova_invalidname"},
		{"Mixed unallowed characters", "Inva!id_Na$me", "inva_id_na_me"},
		{"Space characters", " invalid name !ß", "invalid_name_"},
		{"Numbers and points stay", "!N7.-_", "nova_n7.-_"},
		{"Only invalid chars", "!!!Öß&", "nova_"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := normalizeImageName(tc.input)
			if actual != tc.expected {
				t.Errorf("normalizeImageName(%q) = %q, expected %q", tc.input, actual, tc.expected)
			}
		})
	}
}

func TestGetDigestName(t *testing.T) {

	expected := "sha256:014b4d4346af32c34765fc9d07d8d1585da6de53b0391d8f8c1f203961d492ec"

	out := `
	b7f4862e8a23: Waiting
	81dcc0325be2: Waiting
	4dd0418cbdf9: Waiting
	02f2bcb26af5: Waiting
	6d58f8cdc8cb: Waiting
	0007fda5601a: Layer already exists
	b7f4862e8a23: Layer already exists
	e979fe72cc13: Pushed
	f6c553779e0b: Pushed
	a976972fe1f5: Layer already exists
	6d58f8cdc8cb: Layer already exists
	81dcc0325be2: Layer already exists
	4dd0418cbdf9: Layer already exists
	90f8ec5c81bd: Pushed
	02f2bcb26af5: Layer already exists
	ef4b7dfaac80: Pushed
	latest: digest: sha256:014b4d4346af32c34765fc9d07d8d1585da6de53b0391d8f8c1f203961d492ec size: 2617
	`

	digest, err := getImageDigest(out)

	if err != nil {
		t.Errorf("getImageDigest() failed: %v", err)
	}

	if digest != expected {
		t.Errorf("getImageDigest() = %q, expected %q", digest, expected)
	}
}

func TestGetRegistryCredentials(t *testing.T) {
	t.Skip("needs to be executed manually for debugging/dev purposes")

	//creds, err := dockerRuntime{}.GetRegistryCredentials("wandelbots.azurecr.io/development-nova-apps")
	// creds, err := dockerRuntime{}.GetRegistryCredentials("registry.github.com/wandelbotsgmbh/nova-cli")
	creds, err := dockerRuntime{}.GetRegistryCredentials("registry-1.docker.io/andiikaa/bla")

	if err != nil {
		t.Errorf("GetRegistryCredentials() failed: %v", err)
	}
	if creds.Username == "" || creds.Password == "" {
		t.Errorf("GetRegistryCredentials() returned empty credentials")
	}
	fmt.Println(creds)
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name      string
		override  string
		installed []string
		expected  string
		wantErr   bool
	}{
		{"auto prefers docker", "", []string{"docker", "podman"}, RuntimeNameDocker, false},
		{"auto docker only", "", []string{"docker"}, RuntimeNameDocker, false},
		{"auto falls back to podman", "", []string{"podman"}, RuntimeNamePodman, false},
		{"auto neither installed", "", nil, "", true},
		{"override podman", "podman", []string{"docker", "podman"}, RuntimeNamePodman, false},
		{"override docker", "docker", []string{"docker", "podman"}, RuntimeNameDocker, false},
		{"override not installed", "podman", []string{"docker"}, "", true},
		{"invalid override", "containerd", []string{"docker", "podman"}, "", true},
	}

	original := lookPath
	defer func() { lookPath = original }()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lookPath = func(file string) (string, error) {
				if slices.Contains(tc.installed, file) {
					return "/usr/bin/" + file, nil
				}
				return "", errors.New("not found")
			}

			rt, err := Detect(tc.override)
			if tc.wantErr {
				if err == nil {
					t.Errorf("Detect(%q) expected error, got runtime %q", tc.override, rt.Name())
				}
				return
			}
			if err != nil {
				t.Errorf("Detect(%q) failed: %v", tc.override, err)
			}
			if rt.Name() != tc.expected {
				t.Errorf("Detect(%q) = %q, expected %q", tc.override, rt.Name(), tc.expected)
			}
		})
	}
}

func TestIsSupportedPodmanVersion(t *testing.T) {
	tests := []struct {
		version   string
		supported bool
	}{
		{"6.1.1", true},
		{"5.8.4", true},
		{"5.4.2", true},
		{"5.4", true},
		{"5.3.9", false},
		{"4.9.4-rhel", false},
		{"3.4.4", false},
		// unparseable versions must not trigger a false warning
		{"unknown", true},
		{"", true},
		{"x.y.z", true},
	}

	for _, tc := range tests {
		t.Run(tc.version, func(t *testing.T) {
			if got := isSupportedPodmanVersion(tc.version); got != tc.supported {
				t.Errorf("isSupportedPodmanVersion(%q) = %v, expected %v", tc.version, got, tc.supported)
			}
		})
	}
}

func TestParseDigestFile(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
		wantErr  bool
	}{
		{"plain digest", "sha256:014b4d4346af32c34765fc9d07d8d1585da6de53b0391d8f8c1f203961d492ec", "sha256:014b4d4346af32c34765fc9d07d8d1585da6de53b0391d8f8c1f203961d492ec", false},
		{"trailing newline", "sha256:014b4d4346af32c34765fc9d07d8d1585da6de53b0391d8f8c1f203961d492ec\n", "sha256:014b4d4346af32c34765fc9d07d8d1585da6de53b0391d8f8c1f203961d492ec", false},
		{"empty file", "", "", true},
		{"garbage", "not-a-digest", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			digest, err := parseDigestFile(tc.content)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseDigestFile(%q) expected error, got %q", tc.content, digest)
				}
				return
			}
			if err != nil {
				t.Errorf("parseDigestFile(%q) failed: %v", tc.content, err)
			}
			if digest != tc.expected {
				t.Errorf("parseDigestFile(%q) = %q, expected %q", tc.content, digest, tc.expected)
			}
		})
	}
}
