package editor

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/spf13/viper"
)

type Editor struct {
}

func NewDefaultEditor() *Editor {
	return &Editor{}
}

// Content is passed to a newly created tempfile. The tempfile will be deleted afterwards.
// Tempfile content will be returned, if there are no errors.
func (e *Editor) EditInTempfile(prefix, suffix string, content []byte) ([]byte, error) {
	f, err := os.CreateTemp("", prefix+"*"+suffix)
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	defer f.Close()

	tempFile := f.Name()

	if _, err := io.Copy(f, bytes.NewReader(content)); err != nil {
		return nil, err
	}

	// we close the file so that the editor has full access
	f.Close()

	shell := defaultShell
	editor := GetDefaultEditor()

	args := []string{shell, shellCommandFlag, fmt.Sprintf("%s %s", editor, tempFile)}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	err = cmd.Run()
	if err != nil {
		return nil, err
	}

	f, err = os.Open(tempFile)
	if err != nil {
		return nil, err
	}

	return io.ReadAll(f)
}

func GetDefaultEditor() string {
	e := viper.GetString("editor")
	if e == "" {
		e = defaultEditor
	}
	return e
}
