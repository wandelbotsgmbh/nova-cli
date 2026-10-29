package docker

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/wandelbotsgmbh/nova-cli/pkg/utils"
)

func sha256Descriptor(data []byte) ocispec.Descriptor {
	return ocispec.Descriptor{
		Digest: digest.FromBytes(data),
		Size:   int64(len(data)),
	}
}

// writeLayout builds a minimal OCI layout on disk and returns its dir.
// If wrapInIndex is true, index.json points to an image index that in turn
// references the manifest (the docker buildx multi-descriptor shape).
func writeLayout(t *testing.T, wrapInIndex bool) (string, ImageBlobs) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	writeBlob := func(v any) ocispec.Descriptor {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		d := sha256Descriptor(data)
		if err := os.WriteFile(blobPath(dir, d.Digest.String()), data, 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}
	writeRawBlob := func(content string) ocispec.Descriptor {
		data := []byte(content)
		d := sha256Descriptor(data)
		if err := os.WriteFile(blobPath(dir, d.Digest.String()), data, 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}

	config := writeRawBlob("config-json")
	layer1 := writeRawBlob("layer-one-contents")
	layer2 := writeRawBlob("layer-two-contents")

	manifest := ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    config,
		Layers:    []ocispec.Descriptor{layer1, layer2},
	}
	manifestDesc := writeBlob(manifest)
	manifestDesc.MediaType = ocispec.MediaTypeImageManifest
	manifestDesc.Platform = &ocispec.Platform{OS: "linux", Architecture: "amd64"}

	var index ocispec.Index
	var wantIndexes []ocispec.Descriptor
	if wrapInIndex {
		inner := ocispec.Index{MediaType: ocispec.MediaTypeImageIndex, Manifests: []ocispec.Descriptor{manifestDesc}}
		innerDesc := writeBlob(inner)
		innerDesc.MediaType = ocispec.MediaTypeImageIndex
		index = ocispec.Index{Manifests: []ocispec.Descriptor{innerDesc}}
		wantIndexes = []ocispec.Descriptor{innerDesc}
	} else {
		index = ocispec.Index{Manifests: []ocispec.Descriptor{manifestDesc}}
	}
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	return dir, ImageBlobs{Manifest: manifestDesc, Config: config, Layers: []ocispec.Descriptor{layer1, layer2}, Indexes: wantIndexes}
}

func TestReadImageBlobs(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		name := "direct-manifest"
		if wrap {
			name = "index-wrapped"
		}
		t.Run(name, func(t *testing.T) {
			dir, want := writeLayout(t, wrap)
			got, err := ReadImageBlobs(dir)
			if err != nil {
				t.Fatalf("ReadImageBlobs: %v", err)
			}
			if got.Config.Digest != want.Config.Digest {
				t.Errorf("config digest = %s, want %s", got.Config.Digest, want.Config.Digest)
			}
			if len(got.Layers) != 2 {
				t.Fatalf("got %d layers, want 2", len(got.Layers))
			}
			for i := range want.Layers {
				if got.Layers[i].Digest != want.Layers[i].Digest {
					t.Errorf("layer %d digest = %s, want %s", i, got.Layers[i].Digest, want.Layers[i].Digest)
				}
			}
			// intermediate index blobs must be surfaced so the partial stays valid
			if len(got.Indexes) != len(want.Indexes) {
				t.Fatalf("got %d indexes, want %d", len(got.Indexes), len(want.Indexes))
			}
			for i := range want.Indexes {
				if got.Indexes[i].Digest != want.Indexes[i].Digest {
					t.Errorf("index %d digest = %s, want %s", i, got.Indexes[i].Digest, want.Indexes[i].Digest)
				}
			}
		})
	}
}

// podman's `save --format oci-dir` writes an index whose manifest descriptor
// carries no platform field; ReadImageBlobs must still resolve it.
func TestReadImageBlobsNilPlatform(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	writeRaw := func(content string) ocispec.Descriptor {
		data := []byte(content)
		d := sha256Descriptor(data)
		if err := os.WriteFile(blobPath(dir, d.Digest.String()), data, 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}
	config := writeRaw("cfg")
	layer := writeRaw("layer")

	manifest := ocispec.Manifest{MediaType: ocispec.MediaTypeImageManifest, Config: config, Layers: []ocispec.Descriptor{layer}}
	mdata, _ := json.Marshal(manifest)
	mdesc := sha256Descriptor(mdata)
	mdesc.MediaType = ocispec.MediaTypeImageManifest // note: no Platform set
	if err := os.WriteFile(blobPath(dir, mdesc.Digest.String()), mdata, 0o644); err != nil {
		t.Fatal(err)
	}

	index := ocispec.Index{MediaType: ocispec.MediaTypeImageIndex, Manifests: []ocispec.Descriptor{mdesc}}
	idata, _ := json.Marshal(index)
	if err := os.WriteFile(filepath.Join(dir, "index.json"), idata, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadImageBlobs(dir)
	if err != nil {
		t.Fatalf("ReadImageBlobs: %v", err)
	}
	if got.Config.Digest != config.Digest {
		t.Errorf("config digest = %s, want %s", got.Config.Digest, config.Digest)
	}
	if len(got.Layers) != 1 || got.Layers[0].Digest != layer.Digest {
		t.Errorf("layers = %+v, want single %s", got.Layers, layer.Digest)
	}
}

func TestWritePartialOCI(t *testing.T) {
	dir, blobs := writeLayout(t, false)

	// include manifest + config + only the first layer (second is "already present")
	include := map[string]bool{
		blobs.Manifest.Digest.String():  true,
		blobs.Config.Digest.String():    true,
		blobs.Layers[0].Digest.String(): true,
	}
	out := filepath.Join(t.TempDir(), "partial.tar.gz")
	if err := WritePartialOCI(dir, out, include); err != nil {
		t.Fatalf("WritePartialOCI: %v", err)
	}

	entries := tarEntries(t, out)

	mustHave := []string{
		"oci-layout",
		"index.json",
		"blobs/sha256/" + utils.DigestHex(blobs.Manifest.Digest.String()),
		"blobs/sha256/" + utils.DigestHex(blobs.Config.Digest.String()),
		"blobs/sha256/" + utils.DigestHex(blobs.Layers[0].Digest.String()),
	}
	for _, m := range mustHave {
		if !entries[m] {
			t.Errorf("partial archive missing %q", m)
		}
	}

	excluded := "blobs/sha256/" + utils.DigestHex(blobs.Layers[1].Digest.String())
	if entries[excluded] {
		t.Errorf("partial archive should not contain excluded layer %q", excluded)
	}
}

func tarEntries(t *testing.T, tarGzPath string) map[string]bool {
	t.Helper()
	f, err := os.Open(tarGzPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	entries := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		entries[hdr.Name] = true
	}
	return entries
}
