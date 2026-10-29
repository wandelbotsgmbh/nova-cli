package docker

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Exports the image via `<runtime> save` (docker or podman) and writes it
// gzip-compressed to destPath. Compression happens in-process (stdlib gzip), so
// no local gzip binary is required on any platform. `save` emits uncompressed
// layers, so compressing here typically shrinks the archive by more than half
// before it goes over the wire.
func SaveImageGzipped(runtime string, image string, destPath string) (err error) {
	cmd := exec.Command(runtime, "save", image)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	dest, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(destPath)
		}
	}()

	if err = cmd.Start(); err != nil {
		dest.Close()
		return err
	}

	gz := gzip.NewWriter(dest)
	if _, err = io.Copy(gz, stdout); err != nil {
		cmd.Wait()
		dest.Close()
		return fmt.Errorf("failed to compress image archive: %w", err)
	}

	if err = cmd.Wait(); err != nil {
		dest.Close()
		return fmt.Errorf("docker save failed: %w", err)
	}

	if err = gz.Close(); err != nil {
		dest.Close()
		return err
	}
	return dest.Close()
}
