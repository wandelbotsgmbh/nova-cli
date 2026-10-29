package portal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"github.com/wandelbotsgmbh/nova-cli/pkg/version"
)

const (
	portalProd = "https://api.portal.wandelbots.io/"
	portalStg  = "https://api.portal.stg.wandelbots.io/"
	portalDev  = "https://api.portal.dev.wandelbots.io/"
)

type Portal struct {
	client *ClientWithResponses
}

type Instance struct {
	InstanceId string
	Host       string
}

func NewPortal(environment oauth.AuthEnvironment) (*Portal, error) {
	host := getPortalHost(environment)

	if err := oauth.RefreshTokenOrLogin(host); err != nil {
		return nil, err
	}

	client, error := NewClientWithResponses(host, WithHTTPClient(httpx.Client()), WithRequestEditorFn(oauth.SetAuthForClient), WithRequestEditorFn(version.SetUserAgent))
	if error != nil {
		return nil, error
	}
	return &Portal{
		client: client,
	}, nil
}

// ListRegistryCredentials retrieves the image-registry credentials for the
// authenticated user from the developer portal.
func (p *Portal) ListRegistryCredentials() (RegistryCredentialsList, error) {
	resp, err := p.client.ListMyRegistryCredentialsWithResponse(context.Background())
	if err != nil {
		return nil, err
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("failed to list registry credentials: %d %s", resp.StatusCode(), string(resp.Body))
	}

	return *resp.JSON200, nil
}

// GetLatestVersion returns the latest Wandelbots NOVA version for the given
// release channel. The endpoint responds with the version as a plain-text
// string. currentVersion may be empty for a fresh install.
func (p *Portal) GetLatestVersion(channel ReleaseChannel, currentVersion string) (string, error) {
	resp, err := p.client.GetLatestVersionWithResponse(
		context.Background(), channel, &GetLatestVersionParams{CurrentVersion: currentVersion},
	)
	if err != nil {
		return "", err
	}

	if resp.StatusCode() != 200 {
		return "", fmt.Errorf("failed to get latest version: %d %s", resp.StatusCode(), string(resp.Body))
	}

	// Body is text/plain; trim whitespace and any surrounding quotes.
	version := strings.TrimSpace(string(resp.Body))
	version = strings.Trim(version, `"`)
	return version, nil
}

func (p *Portal) DeleteInstance(instanceId string) error {
	resp, err := p.client.DeleteInstanceWithResponse(context.Background(), instanceId)
	if err != nil {
		return err
	}

	if resp.JSON200 != nil {
		return nil
	}

	return fmt.Errorf("failed to delete instance: %d %s", resp.StatusCode(), string(resp.Body))
}

func (p *Portal) CreateInstance(name string) (Instance, error) {
	body := CreateInstanceJSONRequestBody{
		SandboxName: name,
	}
	resp, err := p.client.CreateInstanceWithResponse(context.Background(), body)
	if err != nil {
		return Instance{}, err
	}

	if resp.JSON200 == nil {
		return Instance{}, fmt.Errorf("failed to create instance: %d %s", resp.StatusCode(), string(resp.Body))
	}

	return Instance{
		InstanceId: resp.JSON200.InstanceId,
		Host:       resp.JSON200.Host,
	}, nil
}

func (p *Portal) CreateInstanceDev(name string, chart, version *string, ttlMinutes *int) (Instance, error) {
	body := CreateCustomInstanceJSONRequestBody{
		SandboxName: name,
		TtlMinutes:  ttlMinutes,
		Version:     version,
	}

	if chart != nil {
		c := CustomInstanceRequestChart(*chart)
		body.Chart = &c
	}

	resp, err := p.client.CreateCustomInstanceWithResponse(context.Background(), body)
	if err != nil {
		return Instance{}, err
	}

	if resp.JSON200 == nil {
		return Instance{}, fmt.Errorf("failed to create instance: %d %s", resp.StatusCode(), string(resp.Body))
	}

	return Instance{
		InstanceId: resp.JSON200.InstanceId,
		Host:       resp.JSON200.Host,
	}, nil
}

func (p *Portal) ListInstances() ([]UserInstance, error) {
	resp, err := p.client.GetUserInstancesWithResponse(context.Background())
	if err != nil {
		return nil, err
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("failed to list instances: %d %s", resp.StatusCode(), string(resp.Body))
	}
	return resp.JSON200.Instances, nil
}

func (p *Portal) GetInstance(instanceId string) (UserInstance, error) {
	instances, err := p.ListInstances()
	if err != nil {
		return UserInstance{}, err
	}

	for _, instance := range instances {
		if instance.InstanceId == strings.TrimSpace(instanceId) {
			return instance, nil
		}
	}

	return UserInstance{}, errors.New("the given instance was not found")
}

func (p *Portal) UpdateInstanceState(instanceId string, state StateUpdateState) error {
	body := UpdateInstanceStateJSONRequestBody{
		State: state,
	}

	resp, err := p.client.UpdateInstanceStateWithResponse(context.Background(), instanceId, body)
	if err != nil {
		return err
	}

	if resp.JSON200 == nil {
		return fmt.Errorf("failed to update instance state: %d %s", resp.StatusCode(), string(resp.Body))
	}

	return nil
}

// Default to production if unknown environment
func getPortalHost(env oauth.AuthEnvironment) string {
	switch env {
	case oauth.AuthProd:
		return portalProd
	case oauth.AuthStg:
		return portalStg
	case oauth.AuthDev:
		return portalDev
	default:
		return portalProd
	}
}
