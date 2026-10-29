package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"text/template"
)

const (
	// path to the homebrew template
	homebrewTemplate = "nova.rb.tpl"
	// path for the templated file
	homebrewFile = "../../bin/homebrew/Formula/n/nova.rb"
	// folder where the binaries are expected
	binFolder = "../../bin"
	// format with filename and version
	macArm64   = binFolder + "/%s_macos_arm64-%s.tar.gz"
	macAmd64   = binFolder + "/%s_macos_amd64-%s.tar.gz"
	linuxAmd64 = binFolder + "/%s_linux_amd64-%s.tar.gz"
	linuxArm64 = binFolder + "/%s_linux_arm64-%s.tar.gz"
)

type Homebrew struct {
	Version          string
	MacArm64Sha256   string
	MacIntelSha256   string
	LinuxIntelSha256 string
	LinuxArm64Sha256 string
	// the filename under github releases
	ReleaseFilename string
}

func main() {
	if len(os.Args) < 4 {
		log.Fatalf("Usage: %s <version> <filename> <release_filename>", os.Args[0])
	}

	homebrew, err := collectSha256(os.Args[1], os.Args[2], os.Args[3])
	if err != nil {
		log.Fatalf("Failed to collect sha256: %v\n", err)
	}

	content, err := os.ReadFile(homebrewTemplate)
	if err != nil {
		log.Fatalf("Failed to read file %s: %v\n", homebrewTemplate, err)
	}

	err = templateFile(content, homebrew)
	if err != nil {
		log.Fatalf("failed to create homebrew file: %v\n", err)
	}
}

func collectSha256(version, filename, releaseFilename string) (Homebrew, error) {
	homebrew := Homebrew{
		Version:         version,
		ReleaseFilename: releaseFilename,
	}

	macArm64Sha, err := sha256sum(fmt.Sprintf(macArm64, filename, version))
	if err != nil {
		return Homebrew{}, err
	}
	macAmd64Sha, err := sha256sum(fmt.Sprintf(macAmd64, filename, version))
	if err != nil {
		return Homebrew{}, err
	}
	linuxAmd64Sha, err := sha256sum(fmt.Sprintf(linuxAmd64, filename, version))
	if err != nil {
		return Homebrew{}, err
	}
	linuxArm64Sha, err := sha256sum(fmt.Sprintf(linuxArm64, filename, version))
	if err != nil {
		return Homebrew{}, err
	}

	homebrew.MacArm64Sha256 = macArm64Sha
	homebrew.MacIntelSha256 = macAmd64Sha
	homebrew.LinuxIntelSha256 = linuxAmd64Sha
	homebrew.LinuxArm64Sha256 = linuxArm64Sha

	return homebrew, nil
}

func templateFile(tpl []byte, homebrew Homebrew) error {
	tmpl, err := template.New("").Parse(string(tpl))
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Dir(homebrewFile), os.ModePerm)
	if err != nil {
		return err
	}

	outputFile, err := os.Create(homebrewFile)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	err = tmpl.Execute(outputFile, homebrew)
	if err != nil {
		return err
	}

	return nil
}

func sha256sum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
