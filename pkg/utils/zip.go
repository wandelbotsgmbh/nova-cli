package utils

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	zipFileExtension = ".zip"
)

func ZipFolder(dataFolder string, targetFolder string, zipFilename string) (string, error) {
	zipFilename, _ = strings.CutSuffix(zipFilename, zipFileExtension)
	filename := targetFolder + "/" + zipFilename + zipFileExtension
	archive, err := os.Create(filename)
	if err != nil {
		return "", err
	}
	defer archive.Close()

	zipWriter := zip.NewWriter(archive)
	defer zipWriter.Close()

	err = filepath.Walk(dataFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		zipPath, err := filepath.Rel(dataFolder, path)
		if err != nil {
			return err
		}

		zipFile, err := zipWriter.Create(zipPath)
		if err != nil {
			return err
		}

		_, err = io.Copy(zipFile, file)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	return filename, nil
}

// Reads a specific file from a tar.gz archive
func ReadFileFromTarGz(tarGzPath, targetFile string) ([]byte, error) {
	file, err := os.Open(tarGzPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("file %s not found in archive", targetFile)
		}
		if err != nil {
			return nil, err
		}

		if header.Name == targetFile && header.Typeflag == tar.TypeReg {
			return io.ReadAll(tarReader)
		}
	}
}
