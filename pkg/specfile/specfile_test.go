package specfile

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestUnmarshalSpec(t *testing.T) {
	s1 := "testdata/app.json"
	s2 := "testdata/app.yaml"
	s3 := "https://catalog.wandelbots.io/_preview/doom/manifest.yaml"
	s3T := "testdata/manifest.yaml"

	expectedSpec := map[string]any{}
	f, _ := os.ReadFile(s1)
	err := json.Unmarshal(f, &expectedSpec)
	if err != nil {
		t.Errorf("yaml.Unmarshal(%s) expected data failed: %v", s1, err)
	}

	expectedManifestSpec := map[string]any{}
	f2, _ := os.ReadFile(s3T)
	err = yaml.Unmarshal(f2, &expectedManifestSpec)
	if err != nil {
		t.Errorf("yaml.Unmarshal(%s) expected data failed: %v", s3T, err)
	}

	us1, err := UnmarshalSpec(s1)
	if err != nil {
		t.Errorf("UnmarshalSpec(%s) failed: %v", s1, err)
	}

	us2, err := UnmarshalSpec(s2)
	if err != nil {
		t.Errorf("UnmarshalSpec(%s) failed: %v", s2, err)
	}

	// TODO: this is a hack, because json does not know about integer.
	// yaml will be parsed to int
	// json to float64
	// thus validation would fail
	us2["port"] = float64(8080)

	us3, err := UnmarshalSpec(s3)
	if err != nil {
		t.Errorf("UnmarshalSpec(%s) failed: %v", s3, err)
	}

	if !reflect.DeepEqual(expectedSpec, us1) {
		t.Errorf("specs do not equal =\n%v\n\n want\n%v", us1, expectedSpec)
	}

	if !reflect.DeepEqual(expectedSpec, us2) {
		t.Errorf("specs do not equal =\n%v\n\n want\n%v", us2, expectedSpec)
	}

	if expectedManifestSpec["app"].(map[string]any)["name"] != us3["app"].(map[string]any)["name"] {
		t.Errorf("specs do not equal =\n%v\n\n want\n%v", us3, expectedManifestSpec)
	}

	// compare via reflect.DeepEqual does not work
	expectedSecret := expectedManifestSpec["app"].(map[string]any)["containerImage"].(map[string]any)["secrets"].([]any)[0]
	actualSecret := us3["app"].(map[string]any)["containerImage"].(map[string]any)["secrets"].([]any)[0]

	if !reflect.DeepEqual(expectedSecret, actualSecret) {
		t.Errorf("specs do not equal =\n%v\n\n want\n%v", actualSecret, expectedSecret)
	}
}

func TestIsSpec(t *testing.T) {
	s1 := "testdata/app.json"
	s2 := "testdata/app.yaml"
	s3 := "https://catalog.wandelbots.io/_preview/doom/manifest.yaml"
	s4 := "testdata/cell.cell"
	s5 := "testdata/test.exe"
	s6 := "testdata/test"
	s7 := "testdata/app.yml"

	if !IsSpec(s1) {
		t.Errorf("expected to be a supported spec file: %s", s1)
	}

	if !IsSpec(s2) {
		t.Errorf("expected to be a supported spec file: %s", s2)
	}

	if !IsSpec(s3) {
		t.Errorf("expected to be a supported spec file: %s", s3)
	}

	if !IsSpec(s4) {
		t.Errorf("expected to be a supported spec file: %s", s4)
	}

	if IsSpec(s5) {
		t.Errorf("expected not to be a supported spec file: %s", s5)
	}

	if IsSpec(s6) {
		t.Errorf("expected not to be a supported spec file: %s", s6)
	}

	if !IsSpec(s7) {
		t.Errorf("expected to be a supported spec file: %s", s7)
	}
}
