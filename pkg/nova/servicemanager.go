package nova

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	apiv1 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova"

	"gopkg.in/yaml.v3"
)

const (
	contentTypeJson = "application/json"
)

func listAppsV1Impl(host string, cell string) ([]string, error) {
	client, err := NewClientWithToken(host)
	if err != nil {
		return []string{}, err
	}

	resp, err := client.ListAppsWithResponse(context.TODO(), cell)
	if err != nil {
		return []string{}, err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return []string{}, fmt.Errorf("failed to list apps: %s", resp.Status())
	}

	return *resp.JSON200, nil
}

func updateAppConfigurationFromJsonV1Impl(host string, cell string, app string, config []byte) error {
	client, err := NewClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv1.UpdateAppParams{CompletionTimeout: GetDefaultCompletionTimeout()}
	resp, err := client.UpdateAppWithBodyWithResponse(context.TODO(), cell, app, params, contentTypeJson, bytes.NewReader(config))
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to update app configuration: %s %s", resp.Status(), string(resp.Body))
	}

	return nil
}

func updateAppConfigurationFromYamlV1Impl(host string, cell string, app string, config []byte) error {
	jsonStr, err := ConvertYamlToJsonString(config)
	if err != nil {
		return err
	}
	return updateAppConfigurationFromJsonV1Impl(host, cell, app, jsonStr)
}

func updateAppConfigurationV1Impl(host string, cell string, app string, config apiv1.App) error {
	client, err := NewClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv1.UpdateAppParams{CompletionTimeout: GetDefaultCompletionTimeout()}
	resp, err := client.UpdateAppWithResponse(context.TODO(), cell, app, params, config)
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to update app configuration: %s %s", resp.Status(), string(resp.Body))
	}

	return nil
}

func deleteAppV1Impl(host string, cell string, app string) error {
	client, err := NewClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv1.DeleteAppParams{CompletionTimeout: GetDefaultCompletionTimeout()}
	resp, err := client.DeleteAppWithResponse(context.TODO(), cell, app, params)
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to delete app: %s %s", resp.Status(), string(resp.Body))
	}
	return nil
}

func addAppV1Impl(host string, cell string, app string, config apiv1.App) error {
	client, err := NewClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv1.AddAppParams{CompletionTimeout: GetDefaultCompletionTimeout()}
	resp, err := client.AddAppWithResponse(context.TODO(), cell, params, config)
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to create new app: %s", resp.Status())
	}
	return nil
}

func addAppFromJsonV1Impl(host, cell, app string, config []byte) error {
	client, err := NewClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv1.AddAppParams{CompletionTimeout: GetDefaultCompletionTimeout()}
	resp, err := client.AddAppWithBodyWithResponse(context.TODO(), cell, params, contentTypeJson, bytes.NewReader(config))
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to create new app: %s %s", resp.Status(), string(resp.Body))
	}
	return nil
}

func addAppFromYamlV1Impl(host, cell, app string, config []byte) error {
	jsonStr, err := ConvertYamlToJsonString(config)
	if err != nil {
		return err
	}
	return addAppFromJsonV1Impl(host, cell, app, jsonStr)
}

func getAppV1Impl(host, cellName, appName string) (*apiv1.GetAppResp, error) {
	client, err := NewClientWithToken(host)
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
func getAppJsonV1Impl(host, cellName, appName string) ([]byte, error) {
	c, err := getAppV1Impl(host, cellName, appName)
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
func getAppYamlV1Impl(host, cellName, appName string) ([]byte, error) {
	app, err := getAppJsonV1Impl(host, cellName, appName)
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

func listCellsV1Impl(host string) ([]string, error) {
	client, err := NewClientWithToken(host)
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

func createCellV1Impl(host string, cell string) error {
	client, err := NewClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv1.DeployCellParams{CompletionTimeout: GetDefaultCompletionTimeoutSeconds()}
	resp, err := client.DeployCell(context.TODO(), params, apiv1.Cell{Name: cell})
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

func deleteCellV1Impl(host string, cell string) error {
	client, err := NewClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv1.DeleteCellParams{CompletionTimeout: GetDefaultCompletionTimeoutSeconds()}
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
func getCellJsonV1Impl(host string, cell string) ([]byte, error) {
	c, err := getCellV1Impl(host, cell)
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
func getCellYamlV1Impl(host string, cell string) ([]byte, error) {
	c, err := getCellV1Impl(host, cell)
	if err != nil {
		return nil, err
	}
	if c.Body == nil {
		return nil, fmt.Errorf("cell body is empty")
	}

	return ConvertJsonToYamlString(c.Body)
}

// Gets the cell as response including raw response and struct
func getCellV1Impl(host string, cell string) (*apiv1.GetCellResp, error) {
	client, err := NewClientWithToken(host)
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
func updateConfigurationFromJsonV1Impl(host string, cell string, config []byte) error {
	client, err := NewClientWithToken(host)
	if err != nil {
		return err
	}

	params := &apiv1.UpdateCellParams{CompletionTimeout: GetDefaultCompletionTimeoutSeconds()}
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
func updateConfigurationFromYamlV1Impl(host string, cell string, config []byte) error {
	jsonString, err := ConvertYamlToJsonString(config)
	if err != nil {
		return err
	}
	return updateConfigurationFromJsonV1Impl(host, cell, []byte(jsonString))
}

func getNovaVersionV1Impl(host string) (string, error) {
	client, err := NewClientWithToken(host)
	if err != nil {
		return "", err
	}

	resp, err := client.GetSystemVersionWithResponse(context.TODO())
	if err != nil {
		return "", err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return "", fmt.Errorf("failed to get nova version: %s", resp.Status())
	}

	return string(resp.Body), nil
}

func setNovaVersionV1Impl(host string, version string) error {
	client, err := NewClientWithToken(host)
	if err != nil {
		return err
	}

	targetVersion := version
	if version == "next" || version == "stable" {
		tmpVersion, err := checkNovaVersionUpdateV1Impl(host, version)
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

	body := apiv1.UpdateNovaVersionJSONRequestBody{Channel: apiv1.ReleaseChannel(version)}
	resp, err := client.UpdateNovaVersionWithResponse(context.TODO(), body)
	if err != nil {
		return err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return fmt.Errorf("failed to set nova version: %s %s", resp.Status(), string(resp.Body))
	}

	return waitForNovaVersionV1Impl(host, targetVersion)
}

func checkNovaVersionUpdateV1Impl(host string, version string) (string, error) {
	client, err := NewClientWithToken(host)
	if err != nil {
		return "", err
	}

	params := &apiv1.CheckNovaVersionUpdateParams{
		Channel: apiv1.ReleaseChannel(version),
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

func getSystemStatusV1Impl(host string) (*apiv1.ServiceStatusList, error) {
	client, err := NewClientWithToken(host)
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

func PrettyPrintStatus(response *apiv1.GetSystemStatusResp) (string, error) {
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

func waitForNovaVersionV1Impl(host string, expectedVersion string) error {
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

func GetDefaultCompletionTimeout() *apiv1.CompletionTimeout {
	timeout := 300
	return &timeout
}
