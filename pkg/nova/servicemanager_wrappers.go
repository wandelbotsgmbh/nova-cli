package nova

import (
	"fmt"

	apiv1 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova"
	apiv2 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova/v2"
)

type ServiceRunningStatus struct {
	Service string
	Running bool
}

func shouldRetryAfterCapabilityRefresh(host string, previous apiCapability) (bool, error) {
	_, changed, err := refreshHostCapabilityOnFailure(host, previous)
	if err != nil {
		return false, err
	}
	return changed, nil
}

func requireHostCapability(host string) (apiCapability, error) {
	cap := getHostCapability(host)
	if cap == capUnknown {
		return capUnknown, fmt.Errorf("failed to detect API capability for host %s", host)
	}
	return cap, nil
}

// ============================================================================
// Public Wrapper Functions - Auto-fallback from V2 to V1
// ============================================================================

// ListApps lists all apps in a cell, automatically trying V2 first, then V1
func ListApps(host string, cell string) ([]string, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return []string{}, err
	}

	result := []string{}
	switch cap {
	case capV1Only:
		result, err = listAppsV1Impl(host, cell)
	case capV2Only:
		result, err = ListAppsV2Impl(host, cell)
	default:
		return []string{}, fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capp_err := shouldRetryAfterCapabilityRefresh(host, cap)
		if capp_err != nil {
			return []string{}, fmt.Errorf("failed to detect API capability for host %s: %w", host, capp_err)
		}
		if changed {
			return ListApps(host, cell)
		}
	}
	return result, err
}

// GetApp gets an app, automatically trying V2 first, then V1
func GetApp(host, cellName, appName string) (*apiv1.GetAppResp, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return nil, err
	}

	var result *apiv1.GetAppResp
	switch cap {
	case capV1Only:
		result, err = getAppV1Impl(host, cellName, appName)
	case capV2Only:
		_, err = GetAppV2Impl(host, cellName, appName)
		if err == nil {
			result, err = getAppV1Impl(host, cellName, appName)
		}
	default:
		return nil, fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return nil, fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return GetApp(host, cellName, appName)
		}
	}
	return result, err
}

// GetAppJson gets an app as JSON, automatically trying V2 first, then V1
func GetAppJson(host, cellName, appName string) ([]byte, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return []byte{}, err
	}

	result := []byte{}
	switch cap {
	case capV1Only:
		result, err = getAppJsonV1Impl(host, cellName, appName)
	case capV2Only:
		result, err = GetAppJsonV2Impl(host, cellName, appName)
	default:
		return []byte{}, fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return []byte{}, fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return GetAppJson(host, cellName, appName)
		}
	}
	return result, err
}

// GetAppYaml gets an app as YAML, automatically trying V2 first, then V1
func GetAppYaml(host, cellName, appName string) ([]byte, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return []byte{}, err
	}

	result := []byte{}
	switch cap {
	case capV1Only:
		result, err = getAppYamlV1Impl(host, cellName, appName)
	case capV2Only:
		result, err = GetAppYamlV2Impl(host, cellName, appName)
	default:
		return []byte{}, fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return []byte{}, fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return GetAppYaml(host, cellName, appName)
		}
	}
	return result, err
}

// AddAppFromJson adds an app from JSON, automatically trying V2 first, then V1
func AddAppFromJson(host, cell, app string, config []byte) error {
	cap, err := requireHostCapability(host)
	if err != nil {
		return err
	}

	switch cap {
	case capV1Only:
		err = addAppFromJsonV1Impl(host, cell, app, config)
	case capV2Only:
		err = AddAppFromJsonV2Impl(host, cell, app, config)
	default:
		return fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return AddAppFromJson(host, cell, app, config)
		}
	}
	return err
}

// AddAppFromYaml adds an app from YAML, automatically trying V2 first, then V1
func AddAppFromYaml(host, cell, app string, config []byte) error {
	jsonStr, err := ConvertYamlToJsonString(config)
	if err != nil {
		return err
	}
	return AddAppFromJson(host, cell, app, jsonStr)
}

// UpdateAppConfigurationFromJson updates app configuration from JSON, automatically trying V2 first, then V1
func UpdateAppConfigurationFromJson(host string, cell string, app string, config []byte) error {
	cap, err := requireHostCapability(host)
	if err != nil {
		return err
	}

	switch cap {
	case capV1Only:
		err = updateAppConfigurationFromJsonV1Impl(host, cell, app, config)
	case capV2Only:
		err = UpdateAppConfigurationFromJsonV2Impl(host, cell, app, config)
	default:
		return fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return UpdateAppConfigurationFromJson(host, cell, app, config)
		}
	}
	return err
}

// UpdateAppConfigurationFromYaml updates app configuration from YAML, automatically trying V2 first, then V1
func UpdateAppConfigurationFromYaml(host string, cell string, app string, config []byte) error {
	jsonStr, err := ConvertYamlToJsonString(config)
	if err != nil {
		return err
	}
	return UpdateAppConfigurationFromJson(host, cell, app, jsonStr)
}

// DeleteApp deletes an app, automatically trying V2 first, then V1
func DeleteApp(host string, cell string, app string) error {
	cap, err := requireHostCapability(host)
	if err != nil {
		return err
	}

	switch cap {
	case capV1Only:
		err = deleteAppV1Impl(host, cell, app)
	case capV2Only:
		err = DeleteAppV2Impl(host, cell, app)
	default:
		return fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return DeleteApp(host, cell, app)
		}
	}
	return err
}

// ListCells lists all cells, automatically trying V2 first, then V1
func ListCells(host string) ([]string, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return []string{}, err
	}

	result := []string{}
	switch cap {
	case capV1Only:
		result, err = listCellsV1Impl(host)
	case capV2Only:
		result, err = ListCellsV2Impl(host)
	default:
		return []string{}, fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return []string{}, fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return ListCells(host)
		}
	}
	return result, err
}

// CreateCell creates a cell, automatically trying V2 first, then V1
func CreateCell(host string, cell string) error {
	cap, err := requireHostCapability(host)
	if err != nil {
		return err
	}

	switch cap {
	case capV1Only:
		err = createCellV1Impl(host, cell)
	case capV2Only:
		err = CreateCellV2Impl(host, cell)
	default:
		return fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return CreateCell(host, cell)
		}
	}
	return err
}

// DeleteCell deletes a cell, automatically trying V2 first, then V1
func DeleteCell(host string, cell string) error {
	cap, err := requireHostCapability(host)
	if err != nil {
		return err
	}

	switch cap {
	case capV1Only:
		err = deleteCellV1Impl(host, cell)
	case capV2Only:
		err = DeleteCellV2Impl(host, cell)
	default:
		return fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return DeleteCell(host, cell)
		}
	}
	return err
}

// GetCellJson gets a cell as JSON, automatically trying V2 first, then V1
func GetCellJson(host string, cell string) ([]byte, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return []byte{}, err
	}

	result := []byte{}
	switch cap {
	case capV1Only:
		result, err = getCellJsonV1Impl(host, cell)
	case capV2Only:
		result, err = GetCellJsonV2Impl(host, cell)
	default:
		return []byte{}, fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return []byte{}, fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return GetCellJson(host, cell)
		}
	}
	return result, err
}

// GetCellYaml gets a cell as YAML, automatically trying V2 first, then V1
func GetCellYaml(host string, cell string) ([]byte, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return []byte{}, err
	}

	result := []byte{}
	switch cap {
	case capV1Only:
		result, err = getCellYamlV1Impl(host, cell)
	case capV2Only:
		result, err = GetCellYamlV2Impl(host, cell)
	default:
		return []byte{}, fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return []byte{}, fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return GetCellYaml(host, cell)
		}
	}
	return result, err
}

// UpdateConfigurationFromJson updates cell configuration from JSON, automatically trying V2 first, then V1
func UpdateConfigurationFromJson(host string, cell string, config []byte) error {
	cap, err := requireHostCapability(host)
	if err != nil {
		return err
	}

	switch cap {
	case capV1Only:
		err = updateConfigurationFromJsonV1Impl(host, cell, config)
	case capV2Only:
		err = UpdateConfigurationFromJsonV2Impl(host, cell, config)
	default:
		return fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return UpdateConfigurationFromJson(host, cell, config)
		}
	}
	return err
}

// UpdateConfigurationFromYaml updates cell configuration from YAML, automatically trying V2 first, then V1
func UpdateConfigurationFromYaml(host string, cell string, config []byte) error {
	jsonString, err := ConvertYamlToJsonString(config)
	if err != nil {
		return err
	}
	return UpdateConfigurationFromJson(host, cell, jsonString)
}

// GetNovaVersion gets the nova version, automatically trying V2 first, then V1
func GetNovaVersion(host string) (string, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return "", err
	}

	result := ""
	switch cap {
	case capV1Only:
		result, err = getNovaVersionV1Impl(host)
	case capV2Only:
		result, err = GetNovaVersionV2Impl(host)
	default:
		return "", fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return "", fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return GetNovaVersion(host)
		}
	}
	return result, err
}

// SetNovaVersion sets the nova version, automatically trying V2 first, then V1
func SetNovaVersion(host string, version string) error {
	cap, err := requireHostCapability(host)
	if err != nil {
		return err
	}

	switch cap {
	case capV1Only:
		err = setNovaVersionV1Impl(host, version)
	case capV2Only:
		err = SetNovaVersionV2Impl(host, version)
	default:
		return fmt.Errorf("host %s has no known API capability", host)
	}

	if err == nil {
		updateHostCapabilityFromTargetVersion(host, version)
		return nil
	}

	changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
	if capErr != nil {
		return fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
	}
	if changed {
		return SetNovaVersion(host, version)
	}
	return err
}

// CheckNovaVersionUpdate checks for nova version updates, automatically trying V2 first, then V1
func CheckNovaVersionUpdate(host string, version string) (string, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return "", err
	}

	result := ""
	switch cap {
	case capV1Only:
		result, err = checkNovaVersionUpdateV1Impl(host, version)
	case capV2Only:
		result, err = CheckNovaVersionUpdateV2Impl(host, version)
	default:
		return "", fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return "", fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return CheckNovaVersionUpdate(host, version)
		}
	}
	return result, err
}

// GetSystemStatus gets system status in a compact API-agnostic format.
func GetSystemStatus(host string) ([]ServiceRunningStatus, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return []ServiceRunningStatus{}, err
	}

	statusList := []ServiceRunningStatus{}
	switch cap {
	case capV1Only:
		v1Result, implErr := getSystemStatusV1Impl(host)
		err = implErr
		if err == nil {
			statusList = make([]ServiceRunningStatus, 0, len(*v1Result))
			for _, service := range *v1Result {
				statusList = append(statusList, ServiceRunningStatus{
					Service: service.Service,
					Running: service.Status.Code == apiv1.ServiceStatusPhaseRunning,
				})
			}
		}
	case capV2Only:
		v2Result, implErr := GetSystemStatusV2Impl(host)
		err = implErr
		if err == nil {
			statusList = make([]ServiceRunningStatus, 0, len(*v2Result))
			for _, service := range *v2Result {
				statusList = append(statusList, ServiceRunningStatus{
					Service: service.Service,
					Running: service.Status.Code == apiv2.ServiceStatusPhaseRunning,
				})
			}
		}
	default:
		return []ServiceRunningStatus{}, fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return []ServiceRunningStatus{}, fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return GetSystemStatus(host)
		}
	}

	return statusList, err
}

// ListControllers lists controllers, automatically trying V2 first, then V1
func ListControllers(host string, cell string) ([]string, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return []string{}, err
	}

	result := []string{}
	switch cap {
	case capV1Only:
		result, err = listControllerNamesV1Impl(host, cell)
	case capV2Only:
		result, err = ListRobotControllersV2Impl(host, cell)
	default:
		return []string{}, fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return []string{}, fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return ListControllers(host, cell)
		}
	}
	return result, err
}

// ActivateLicense activates a license, automatically trying V2 first, then V1
func ActivateLicense(host string) error {
	cap, err := requireHostCapability(host)
	if err != nil {
		return err
	}

	switch cap {
	case capV1Only:
		err = activateLicenseV1Impl(host)
	case capV2Only:
		err = ActivateLicenseV2Impl(host)
	default:
		return fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return ActivateLicense(host)
		}
	}
	return err
}

// GetLicense gets the license info, automatically trying V2 first, then V1
func GetLicense(host string) ([]byte, error) {
	cap, err := requireHostCapability(host)
	if err != nil {
		return []byte{}, err
	}

	result := []byte{}
	switch cap {
	case capV1Only:
		result, err = getLicenseV1Impl(host)
	case capV2Only:
		result, err = GetLicenseV2Impl(host)
	default:
		return []byte{}, fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return []byte{}, fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return GetLicense(host)
		}
	}
	return result, err
}

// DownloadDiagnosisPackage downloads the diagnosis package, automatically trying V2 first, then V1
func DownloadDiagnosisPackage(host string) error {
	cap, err := requireHostCapability(host)
	if err != nil {
		return err
	}

	switch cap {
	case capV1Only:
		err = downloadDiagnosisPackageV1Impl(host)
	case capV2Only:
		err = DownloadDiagnosisPackageV2Impl(host)
	default:
		return fmt.Errorf("host %s has no known API capability", host)
	}

	if err != nil {
		changed, capErr := shouldRetryAfterCapabilityRefresh(host, cap)
		if capErr != nil {
			return fmt.Errorf("failed to detect API capability for host %s: %w", host, capErr)
		}
		if changed {
			return DownloadDiagnosisPackage(host)
		}
	}
	return err
}
