package docker

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/wandelbotsgmbh/nova-cli/pkg/utils"
)

// ImageBlobs describes the content-addressable blobs that make up a single
// platform image inside an OCI layout: the manifest, its config and its layers.
// Indexes holds any intermediate index blobs on the path from index.json down to
// the manifest (e.g. buildx wrapping the manifest in an inner index); they must
// be shipped alongside the manifest so a partial layout stays valid.
type ImageBlobs struct {
	Manifest ocispec.Descriptor
	Config   ocispec.Descriptor
	Layers   []ocispec.Descriptor
	Indexes  []ocispec.Descriptor
}

// Reads an OCI layout directory and resolves the blobs of its single
// linux/amd64 image (config + layers). An index that wraps platform-specific
// manifests is descended into; a layout that points straight at a manifest is
// used directly.
func ReadImageBlobs(ociDir string) (ImageBlobs, error) {
	var index ocispec.Index
	if err := readJSONBlob(filepath.Join(ociDir, "index.json"), &index); err != nil {
		return ImageBlobs{}, err
	}

	var indexes []ocispec.Descriptor
	manifestDesc, err := selectManifest(ociDir, index.Manifests, &indexes)
	if err != nil {
		return ImageBlobs{}, err
	}

	var manifest ocispec.Manifest
	if err := readJSONBlob(blobPath(ociDir, manifestDesc.Digest.String()), &manifest); err != nil {
		return ImageBlobs{}, err
	}

	return ImageBlobs{
		Manifest: manifestDesc,
		Config:   manifest.Config,
		Layers:   manifest.Layers,
		Indexes:  indexes,
	}, nil
}

// selectManifest resolves a list of descriptors (from index.json) to the single
// image manifest we want. It descends nested indexes and prefers a linux/amd64
// platform when descriptors carry platform info. Each intermediate index blob it
// descends through is appended to *indexes so callers can ship the full graph.
func selectManifest(ociDir string, descriptors []ocispec.Descriptor, indexes *[]ocispec.Descriptor) (ocispec.Descriptor, error) {
	var fallback *ocispec.Descriptor

	for i := range descriptors {
		d := descriptors[i]
		switch d.MediaType {
		case ocispec.MediaTypeImageManifest, "application/vnd.docker.distribution.manifest.v2+json":
			if d.Platform != nil && d.Platform.OS == "linux" && d.Platform.Architecture == "amd64" {
				return d, nil
			}
			if fallback == nil {
				dd := d
				fallback = &dd
			}
		case ocispec.MediaTypeImageIndex, "application/vnd.docker.distribution.manifest.list.v2+json":
			var nested ocispec.Index
			if err := readJSONBlob(blobPath(ociDir, d.Digest.String()), &nested); err != nil {
				return ocispec.Descriptor{}, err
			}
			*indexes = append(*indexes, d)
			return selectManifest(ociDir, nested.Manifests, indexes)
		}
	}

	if fallback != nil {
		return *fallback, nil
	}
	return ocispec.Descriptor{}, fmt.Errorf("no image manifest found in OCI layout")
}

// WritePartialOCI writes a gzipped tar of the OCI layout containing only the
// metadata (oci-layout, index.json) and the blobs whose digest is in include.
// The result is a valid partial layout: blobs omitted here must already be
// present in the destination's content store to reassemble the full image.
func WritePartialOCI(ociDir string, dstTarGz string, include map[string]bool) (err error) {
	dest, err := os.Create(dstTarGz)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(dstTarGz)
		}
	}()

	gz := gzip.NewWriter(dest)
	tw := tar.NewWriter(gz)

	writeErr := func() error {
		for _, meta := range []string{"oci-layout", "index.json"} {
			if err := addFileToTar(tw, ociDir, meta); err != nil {
				return err
			}
		}
		for digestStr := range include {
			rel := filepath.Join("blobs", "sha256", utils.DigestHex(digestStr))
			if err := addFileToTar(tw, ociDir, rel); err != nil {
				return err
			}
		}
		return nil
	}()
	if writeErr != nil {
		tw.Close()
		gz.Close()
		dest.Close()
		err = writeErr
		return err
	}

	if err = tw.Close(); err != nil {
		gz.Close()
		dest.Close()
		return err
	}
	if err = gz.Close(); err != nil {
		dest.Close()
		return err
	}
	return dest.Close()
}

func addFileToTar(tw *tar.Writer, baseDir string, relPath string) error {
	full := filepath.Join(baseDir, relPath)
	info, err := os.Stat(full)
	if err != nil {
		return err
	}

	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	// tar entries use forward slashes regardless of the host OS
	hdr.Name = filepath.ToSlash(relPath)

	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}

	f, err := os.Open(full)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func readJSONBlob(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// blobPath maps a digest ("sha256:<hex>") to its file inside an OCI layout.
func blobPath(ociDir string, digest string) string {
	return filepath.Join(ociDir, "blobs", "sha256", utils.DigestHex(digest))
}
