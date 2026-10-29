package examples

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/wandelbotsgmbh/nova-cli/pkg/utils"
)

type BasicAuth struct {
	Username string
	Password string
}

type InstanceInformation struct {
	// The host under which the nova api is reachable
	NovaApiHost string
	// Basic Auth credentials for accessing virtual nova instances
	BasicAuth BasicAuth
	// The name of the cell, the plugin should connect with/live in
	CellName string
	// Access token received via oauth
	AccessToken string
}

type ExampleSpec struct {
	// Informations about remote instances
	InstanceInformation InstanceInformation
}

func Template(data io.ReadCloser, spec ExampleSpec) (io.ReadCloser, error) {
	buf := bytes.NewBuffer([]byte{})
	if read, err := io.Copy(buf, data); err != nil {
		return nil, err
	} else if read == 0 {
		return data, nil
	}

	t, err := template.New("ExampleSpec").Parse(string(buf.String()))
	if err != nil {
		return nil, err
	}

	buf.Reset()

	if err := t.Execute(buf, spec); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

func UnzipGitZip(zipData []byte, spec ExampleSpec) error {
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("failed to create zip reader: %w", err)
	}

	for _, file := range zipReader.File {
		err := extractFile(file, spec)
		if err != nil {
			return fmt.Errorf("failed to extract file: %w", err)
		}
	}

	return nil
}

func simplifyGitPath(name string) string {
	path, sub, found := strings.Cut(name, "/")
	if !found {
		return path
	}
	path = utils.TrimGithubArchiveName(path)
	return path + "/" + sub
}

func extractFile(zipFile *zip.File, spec ExampleSpec) error {
	fileName := simplifyGitPath(zipFile.Name)
	isTemplate := strings.HasSuffix(fileName, ".tpl")
	fileName = strings.TrimSuffix(fileName, ".tpl")

	if zipFile.FileInfo().IsDir() {
		os.MkdirAll(fileName, os.ModePerm)
		return nil
	}

	rc, err := zipFile.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	outFile, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer outFile.Close()

	if isTemplate {
		if templated, err := Template(rc, spec); err != nil {
			fmt.Println("failed to template file:", err.Error())
		} else {
			rc = templated
		}
	}

	_, err = io.Copy(outFile, rc)
	return err
}
