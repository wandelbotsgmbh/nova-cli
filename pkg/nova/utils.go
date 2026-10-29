package nova

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	apiv1 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova"
	apiv2 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova/v2"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"github.com/wandelbotsgmbh/nova-cli/pkg/version"

	gv "github.com/hashicorp/go-version"
	"gopkg.in/yaml.v3"
)

const (
	API_BASE_V1     = "/api/v1"
	API_BASE_V2     = "/api/v2"
	WABO_CLOUD_HOST = "wandelbots.io"
)

func CreateCellIfNotExists(host string, cell string) (bool, error) {
	exists, err := CellExists(host, cell)
	if err != nil {
		return false, err
	}

	if exists {
		return false, nil
	}

	err = CreateCell(host, cell)
	return err == nil, err
}

func CellExists(host string, cell string) (bool, error) {
	cells, err := ListCells(host)
	if err != nil {
		return false, err
	}

	for _, c := range cells {
		if c == cell {
			return true, nil
		}
	}

	return false, nil
}

// This creates a new client with already available token and/or basic auth.
// If the host is some wabo cloud instance and no auth is available, it will return an error.
func NewClientWithToken(host string) (*apiv1.ClientWithResponses, error) {
	// Ensure the user is logged in
	if err := oauth.RefreshTokenOrLogin(host); err != nil {
		return nil, err
	}

	host = HostWithProtocol(host) + API_BASE_V1

	client, err := apiv1.NewClientWithResponses(host, apiv1.WithRequestEditorFn(oauth.SetAuthForClient), apiv1.WithRequestEditorFn(version.SetUserAgent))
	if err != nil {
		return nil, err
	}
	return client, nil
}

// This creates a new client with already available token and/or basic auth.
// If the host is some wabo cloud instance and no auth is available, it will return an error.
func NewV2ClientWithToken(host string) (*apiv2.ClientWithResponses, error) {
	// Ensure the user is logged in
	if err := oauth.RefreshTokenOrLogin(host); err != nil {
		return nil, err
	}

	host = HostWithProtocol(host) + API_BASE_V2

	client, err := apiv2.NewClientWithResponses(host, apiv2.WithRequestEditorFn(oauth.SetAuthForClient), apiv2.WithRequestEditorFn(version.SetUserAgent))
	if err != nil {
		return nil, err
	}
	return client, nil
}

func GetDefaultCompletionTimeoutSeconds() *apiv1.CompletionTimeout {
	timeout := 300
	return &timeout
}

func StatusSuccessfull(status int) bool {
	return status >= 200 && status < 300
}

// HostWithProtocol adds "http://" to the host if it does not have a protocol as prefix.
// If the host is a wabo cloud instance and has no protocol, it will add "https://".
func HostWithProtocol(host string) string {
	hasPrefix := strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://")
	if !hasPrefix && strings.Contains(host, WABO_CLOUD_HOST) {
		return "https://" + host
	}
	if !hasPrefix {
		return "http://" + host
	}
	return host
}

// Take a json string and converts it to yaml.
func ConvertJsonToYamlString(in []byte) ([]byte, error) {
	var tmp map[string]interface{}
	err := json.Unmarshal(in, &tmp)
	if err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(tmp)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Take a yaml string and converts it to yaml.
func ConvertYamlToJsonString(in []byte) ([]byte, error) {
	var tmp map[string]any
	err := yaml.Unmarshal(in, &tmp)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(tmp)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Returns true if the current version is less than the given version.
func IsCurrentVersionLessThan(host, version string) (bool, error) {
	nv, err := GetNovaVersion(host)
	if err != nil {
		return false, err
	}

	currVersion, err := gv.NewVersion(nv)
	if err != nil {
		return false, err
	}

	minVersion, err := gv.NewVersion(version)
	if err != nil {
		return false, err
	}

	return currVersion.LessThan(minVersion), nil
}

// Returns true if the current version is equal to the given version.
func IsCurrentVersionEqual(host, version string) (bool, error) {
	nv, err := GetNovaVersion(host)
	if err != nil {
		return false, err
	}

	currVersion, err := gv.NewVersion(nv)
	if err != nil {
		return false, err
	}

	givenVersion, err := gv.NewVersion(version)
	if err != nil {
		return false, err
	}

	return currVersion.Equal(givenVersion), nil
}

// Wait for all services to be in running state.
//
// host:		The host of the nova instance.
// started:		The time when the waiting started.
// timeout: 	The maximum time to wait for all services to be running.
// interval:	The interval to check the status of the services.
func WaitForAllRunning(host string, started time.Time, timeout time.Duration, interval time.Duration) error {
	var lastError error
	for {
		allRunning := true
		if s, err := GetSystemStatus(host); err == nil {
			for _, service := range s {
				if !service.Running {
					allRunning = false
					break
				}
			}
			if allRunning {
				return nil
			}
		} else {
			lastError = err
		}

		if time.Since(started) > timeout {
			if lastError != nil {
				return fmt.Errorf("timeout reached while waiting for nova version of the instance. Last seen error %w", lastError)
			}
			return errors.New("timeout reached while waiting for nova version of the instance")
		}

		time.Sleep(interval)
	}
}
