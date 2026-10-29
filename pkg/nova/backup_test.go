package nova

import (
	"testing"
)

func TestReadManifest(t *testing.T) {
	manifest, err := ReadBackupManifest("testdata/backup.tar.gz")
	if err != nil {
		t.Fatalf("failed to read manifest: %v", err)
	}

	if len(manifest.Resources) == 0 {
		t.Error("expected resources in manifest, got empty")
	}

	if manifest.Resources[0] != "v2/cells/cell.nova" {
		t.Error("expected resources 'v2/cells/cell.nova' not in manifest")
	}

	if manifest.NovaVersion != "25.6.1" {
		t.Error("expected NovaVersion '25.6.1' manifest, got empty string")
	}
}
