package nova

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	apiv2 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova/v2"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"

	"gopkg.in/yaml.v3"
)

func ListAppsV2Impl(host string, cell string) ([]string, error) {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return []string{}, err
	}

	resp, err := client.ListAppsWithResponse(context.TODO(), cell)
	if err != nil {
		return []string{}, err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return []string{}, fmt.Errorf("failed to list apps: %s %s", resp.Status(), string(resp.Body))
	}

	return *resp.JSON200, nil
}

func UpdateAppConfigurationFromJsonV2Impl(host string, cell string, app string, config []byte) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv2.UpdateAppParams{CompletionTimeout: GetDefaultCompletionTimeout()}
	resp, err := client.UpdateAppWithBodyWithResponse(context.TODO(), cell, app, params, contentTypeJson, bytes.NewReader(config))
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to update app configuration: %s %s", resp.Status(), string(resp.Body))
	}

	return nil
}

func UpdateAppConfigurationFromYamlV2Impl(host string, cell string, app string, config []byte) error {
	jsonStr, err := ConvertYamlToJsonString(config)
	if err != nil {
		return err
	}
	return UpdateAppConfigurationFromJsonV2Impl(host, cell, app, jsonStr)
}

func UpdateAppConfigurationV2Impl(host string, cell string, app string, config apiv2.App) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv2.UpdateAppParams{CompletionTimeout: GetDefaultCompletionTimeout()}
	resp, err := client.UpdateAppWithResponse(context.TODO(), cell, app, params, config)
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to update app configuration: %s %s", resp.Status(), string(resp.Body))
	}

	return nil
}

func DeleteAppV2Impl(host string, cell string, app string) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv2.DeleteAppParams{CompletionTimeout: GetDefaultCompletionTimeout()}
	resp, err := client.DeleteAppWithResponse(context.TODO(), cell, app, params)
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to delete app: %s %s", resp.Status(), string(resp.Body))
	}
	return nil
}

func AddAppV2Impl(host string, cell string, app string, config apiv2.App) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv2.AddAppParams{CompletionTimeout: GetDefaultCompletionTimeout()}
	resp, err := client.AddAppWithResponse(context.TODO(), cell, params, config)
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to create new app: %s %s", resp.Status(), string(resp.Body))
	}
	return nil
}

func AddAppFromJsonV2Impl(host, cell, app string, config []byte) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv2.AddAppParams{CompletionTimeout: GetDefaultCompletionTimeout()}
	resp, err := client.AddAppWithBodyWithResponse(context.TODO(), cell, params, contentTypeJson, bytes.NewReader(config))
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to create new app: %s %s", resp.Status(), string(resp.Body))
	}
	return nil
}

func AddAppFromYamlV2Impl(host, cell, app string, config []byte) error {
	jsonStr, err := ConvertYamlToJsonString(config)
	if err != nil {
		return err
	}
	return AddAppFromJsonV2Impl(host, cell, app, jsonStr)
}

func GetAppV2Impl(host, cellName, appName string) (*apiv2.GetAppResp, error) {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return nil, err
	}

	resp, err := client.GetAppWithResponse(context.TODO(), cellName, appName)
	if err != nil {
		return nil, err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return nil, fmt.Errorf("failed to get app: %s %s", resp.Status(), string(resp.Body))
	}

	return resp, nil
}

// Gets the app as raw pretty printed json
func GetAppJsonV2Impl(host, cellName, appName string) ([]byte, error) {
	c, err := GetAppV2Impl(host, cellName, appName)
	if err != nil {
		return nil, err
	}

	if c.Body == nil {
		return nil, fmt.Errorf("cell body is empty")
	}

	var tmp map[string]any
	err = json.Unmarshal(c.Body, &tmp)
	if err != nil {
		return nil, err
	}

	// pretty print json
	bytes, err := json.MarshalIndent(tmp, "", "  ")
	if err != nil {
		return nil, err
	}
	return bytes, nil
}

// Gets the app as raw yaml
func GetAppYamlV2Impl(host, cellName, appName string) ([]byte, error) {
	app, err := GetAppJsonV2Impl(host, cellName, appName)
	if err != nil {
		return nil, err
	}

	// Json unmarshal to yaml does not work directly from the generated structs.
	// It might happen that e.g. fields get lost
	var tmp map[string]any
	err = yaml.Unmarshal(app, &tmp)
	if err != nil {
		return nil, err
	}

	return yaml.Marshal(tmp)
}

func ListCellsV2Impl(host string) ([]string, error) {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return []string{}, err
	}

	cells, err := client.ListCellsWithResponse(context.TODO())
	if err != nil {
		return []string{}, err
	}

	if !StatusSuccessfull(cells.StatusCode()) {
		return []string{}, fmt.Errorf("failed to list cells: %s %s", cells.Status(), string(cells.Body))
	}

	return *cells.JSON200, nil
}

func CreateCellV2Impl(host string, cell string) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv2.DeployCellParams{CompletionTimeout: GetDefaultCompletionTimeoutSeconds()}
	resp, err := client.DeployCell(context.TODO(), params, apiv2.Cell{Name: cell})
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if !StatusSuccessfull(resp.StatusCode) {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create cell: %s %s", resp.Status, string(body))
	}

	return nil
}

func DeleteCellV2Impl(host string, cell string) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv2.DeleteCellParams{CompletionTimeout: GetDefaultCompletionTimeoutSeconds()}
	resp, err := client.DeleteCell(context.TODO(), cell, params)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if !StatusSuccessfull(resp.StatusCode) {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete cell: %s %s", resp.Status, string(body))
	}

	return nil
}

// Gets the cell as raw pretty printed json
func GetCellJsonV2Impl(host string, cell string) ([]byte, error) {
	c, err := GetCellV2Impl(host, cell)
	if err != nil {
		return nil, err
	}

	if c.Body == nil {
		return nil, fmt.Errorf("cell body is empty")
	}

	var tmp map[string]any
	err = json.Unmarshal(c.Body, &tmp)
	if err != nil {
		return nil, err
	}

	// pretty print json
	bytes, err := json.MarshalIndent(tmp, "", "  ")
	if err != nil {
		return nil, err
	}
	return bytes, nil
}

// Gets the cell as raw yaml
func GetCellYamlV2Impl(host string, cell string) ([]byte, error) {
	c, err := GetCellV2Impl(host, cell)
	if err != nil {
		return nil, err
	}
	if c.Body == nil {
		return nil, fmt.Errorf("cell body is empty")
	}

	return ConvertJsonToYamlString(c.Body)
}

// Gets the cell as response including raw response and struct
func GetCellV2Impl(host string, cell string) (*apiv2.GetCellResp, error) {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return nil, err
	}

	resp, err := client.GetCellWithResponse(context.TODO(), cell)
	if err != nil {
		return nil, err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return nil, fmt.Errorf("failed to get cell: %s %s", resp.Status(), string(resp.Body))
	}

	return resp, nil
}

// Takes the json and updates the configuration with it.
// There is no validation of the json. It will use the plain json as it is, without unmarshalling it to actual structs.
func UpdateConfigurationFromJsonV2Impl(host string, cell string, config []byte) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv2.UpdateCellParams{CompletionTimeout: GetDefaultCompletionTimeoutSeconds()}
	resp, err := client.UpdateCellWithBodyWithResponse(context.TODO(), cell, params, contentTypeJson, bytes.NewReader(config))
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to update configuration: %s %s", resp.Status(), string(resp.Body))
	}

	return nil
}

// Takes the yaml and updates the configuration with it.
// There is no validation of the yaml. It will use the plain yaml as it is, without unmarshalling it to actual structs.
func UpdateConfigurationFromYamlV2Impl(host string, cell string, config []byte) error {
	jsonString, err := ConvertYamlToJsonString(config)
	if err != nil {
		return err
	}
	return UpdateConfigurationFromJsonV2Impl(host, cell, []byte(jsonString))
}

func GetNovaVersionV2Impl(host string) (string, error) {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return "", err
	}

	resp, err := client.GetSystemVersionWithResponse(context.TODO())
	if err != nil {
		return "", err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return "", fmt.Errorf("failed to get nova version: %s %s", resp.Status(), string(resp.Body))
	}

	return string(resp.Body), nil
}

func SetNovaVersionV2Impl(host string, version string) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	targetVersion := version
	if version == "next" || version == "stable" {
		tmpVersion, err := CheckNovaVersionUpdateV2Impl(host, version)
		if err != nil {
			return err
		}
		if tmpVersion == "" {
			fmt.Printf("latest version from channel '%s' already installed\n", version)
			return nil
		}
		targetVersion = tmpVersion
		fmt.Printf("setting nova to target version %s...\n", targetVersion)
	}

	body := apiv2.UpdateNovaVersionJSONRequestBody{Channel: apiv2.ReleaseChannel(version)}
	resp, err := client.UpdateNovaVersionWithResponse(context.TODO(), body)
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to set nova version: %s %s", resp.Status(), string(resp.Body))
	}

	return waitForNovaVersionV2Impl(host, targetVersion)
}

func CheckNovaVersionUpdateV2Impl(host string, version string) (string, error) {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return "", err
	}

	params := &apiv2.CheckNovaVersionUpdateParams{
		Channel: apiv2.ReleaseChannel(version),
	}

	resp, err := client.CheckNovaVersionUpdateWithResponse(context.TODO(), params)
	if err != nil {
		return "", err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return "", fmt.Errorf("failed to get nova version: %s %s", resp.Status(), string(resp.Body))
	}

	return string(resp.Body), nil
}

func GetSystemStatusV2Impl(host string) (*apiv2.ServiceStatusList, error) {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return nil, err
	}

	resp, err := client.GetSystemStatusWithResponse(context.TODO())
	if err != nil {
		return nil, err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return nil, fmt.Errorf("failed to get system status: %s %s", resp.Status(), string(resp.Body))
	}

	return resp.JSON200, nil
}

func PrettyPrintStatusV2(response *apiv2.GetSystemStatusResp) (string, error) {
	var data []map[string]any
	err := json.Unmarshal(response.Body, &data)
	if err != nil {
		return "", fmt.Errorf("failed to parse system status: %w", err)
	}

	yaml, err := yaml.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to parse system status: %w", err)
	}

	return string(yaml), nil
}

func waitForNovaVersionV2Impl(host string, expectedVersion string) error {
	timeout := time.Duration(*GetDefaultCompletionTimeoutSeconds()) * time.Second
	waitTime := 200 * time.Millisecond
	start := time.Now()

	for {
		time.Sleep(waitTime)
		currentVersion, err := getNovaVersionV1Impl(host)
		if err == nil && currentVersion == expectedVersion {
			return nil
		}
		if time.Since(start) > timeout {
			return fmt.Errorf("timeout while waiting for nova version to be updated")
		}
	}
}

func ActivateLicenseV2Impl(host string) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	licenseNotSupported, err := IsCurrentVersionLessThan(host, licenseSinceVersion)
	if err != nil {
		return err
	}

	if licenseNotSupported {
		return errLicenseFeatureVersion
	}

	token, err := oauth.LoadToken()
	if err != nil {
		return errNoTokenFound
	}

	if token.OAuth2Token.RefreshToken == "" {
		return errNoTokenFound
	}

	body := apiv2.ActivateLicenseJSONRequestBody{
		OwnerRefreshToken: token.OAuth2Token.RefreshToken,
	}

	resp, err := client.ActivateLicense(context.TODO(), body)
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode) {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to activate license: %s %s", resp.Status, string(body))
	}

	return nil
}

func GetLicenseV2Impl(host string) ([]byte, error) {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return nil, err
	}

	licenseNotSupported, err := IsCurrentVersionLessThan(host, licenseSinceVersion)
	if err != nil {
		return nil, err
	}

	if licenseNotSupported {
		return nil, errLicenseFeatureVersion
	}

	resp, err := client.GetLicense(context.TODO())
	if err != nil {
		return nil, err
	}

	if !StatusSuccessfull(resp.StatusCode) {
		return nil, fmt.Errorf("failed to get license: %s", resp.Status)
	}

	return io.ReadAll(resp.Body)
}

func DownloadDiagnosisPackageV2Impl(host string) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}

	resp, err := client.GetDiagnosePackage(context.TODO())
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode) {
		return fmt.Errorf("failed to download diagnosis package: %s", resp.Status)
	}

	os.Remove("diagnosis.zip")
	out, err := os.Create("diagnosis.zip")
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func ListRobotControllersV2Impl(host string, cell string) ([]string, error) {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return []string{}, err
	}

	resp, err := client.ListRobotControllersWithResponse(context.TODO(), apiv2.CellID(cell))
	if err != nil {
		return []string{}, err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return []string{}, fmt.Errorf("failed to list robot controllers: %s %s", resp.Status(), string(resp.Body))
	}

	if resp.JSON200 == nil {
		return []string{}, nil
	}

	return *resp.JSON200, nil
}

func GetDefaultCompletionTimeoutV2() *apiv2.CompletionTimeout {
	timeout := 300
	return &timeout
}
