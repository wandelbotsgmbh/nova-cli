package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

const (
	binFolder = "../../bin"
	win64Zip  = binFolder + "/%s_win64-%s.zip"
)

func main() {
	if len(os.Args) < 4 {
		log.Fatalf("Usage: %s <version> <filename> <github_token>", os.Args[0])
	}

	version := os.Args[1]
	filename := os.Args[2]
	token := os.Args[3]
	binFile := fmt.Sprintf(win64Zip, filename, version)
	hash, err := sha256sum(binFile)
	if err != nil {
		log.Fatalf("Failed to collect sha256: %v\n", err)
	}

	err = triggerUpdate(os.Args[1], hash, token)
	if err != nil {
		log.Fatalf("Failed to update wandelbots winget repo: %v\n", err)
	}
}

func triggerUpdate(version, fileHash, token string) error {
	url := "https://api.github.com/repos/wandelbotsgmbh/winget-pkgs/dispatches"
	payload := map[string]interface{}{
		"event_type": "add-nova-cli-entry",
		"client_payload": map[string]string{
			"hash":    fileHash,
			"version": version,
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("request failed with status: " + resp.Status)
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
