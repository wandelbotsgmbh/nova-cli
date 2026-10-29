package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var virtualNovaVersion string

type virtualInstance struct {
	// Base64 encoded kubeconfig
	Kubeconfig string `json:"kubeconfig"`
	Id         string `json:"id"`
	Ip         string `json:"ip"`
}

// TODO decide if these commands will be available for customers
var virtualCmd = &cobra.Command{
	Use:   "virtual",
	Short: "Create or destroy virtual Wandelbots NOVA OS instances",
	Long:  `Create or destroy virtual Wandelbots NOVA OS instances`,
}

var createVirtualCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a virtual Wandelbots NOVA OS instance",
	Long: `Create a virtual Wandelbots NOVA OS instance
	If a previous instance exists, it will be destroyed first. Any following interaction with the CLI will point to that newly created instance.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceId := viper.GetString("virtualid")

		// destroy previous instance
		if instanceId != "" {
			err := destroyVirtual(instanceId)
			if err != nil {
				cmd.Printf("destroying previous instance failed: %v\n", err)
			}
		}

		baseUrl, err := url.Parse("http://k8s.wabo.run/instance")
		if err != nil {
			return err
		}

		params := url.Values{}
		// duration in minutes
		params.Add("duration", "720")
		params.Add("comment", "created by nova-cli")
		// TODO in future we need some possibility to configure the version of the SM
		params.Add("install_service_manager", "true")
		params.Add("service_manager_version", virtualNovaVersion)
		baseUrl.RawQuery = params.Encode()

		// create new instance
		resp, err := http.Get(baseUrl.String())
		if err != nil {
			return err
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		var instance virtualInstance
		err = json.Unmarshal(bodyBytes, &instance)
		if err != nil {
			return err
		}

		if instance.Id == "" {
			return fmt.Errorf("no instance id returned")
		}

		if instance.Ip == "" {
			return fmt.Errorf("no instance ip returned")
		}

		if instance.Kubeconfig == "" {
			return fmt.Errorf("no instance kubeconfig returned")
		}

		viper.Set("virtualid", instance.Id)
		viper.Set("host", instance.Ip)
		viper.WriteConfig()

		kubeBytes, err := base64.StdEncoding.DecodeString(instance.Kubeconfig)
		if err != nil {
			return err
		}
		os.Remove("kubeconfig")

		out, err := os.Create("kubeconfig")
		if err != nil {
			return err
		}
		defer out.Close()

		// Write the body to file
		_, err = io.Copy(out, bytes.NewBuffer(kubeBytes))

		cmd.Printf("created virtual instance:\n ID: %s\n IP %s\n", instance.Id, instance.Ip)

		return err
	},
}

var destroyVirtualCmd = &cobra.Command{
	Use:   "destroy",
	Short: "destroy a virtual Wandelbots NOVA OS instance",
	Long:  `destroy a virtual Wandelbots NOVA OSinstance`,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceId := viper.GetString("virtualid")
		return destroyVirtual(instanceId)
	},
}

func destroyVirtual(instanceId string) error {
	content := fmt.Sprintf(`{"instance_id": "%s", "duration": %d}`, instanceId, 0)

	baseUrl, err := url.Parse("http://k8s.wabo.run/instance")
	if err != nil {
		return err
	}

	params := url.Values{}
	// duration in minutes
	params.Add("duration", "0")
	params.Add("instance_id", instanceId)
	baseUrl.RawQuery = params.Encode()

	req, err := http.NewRequest(http.MethodPut, baseUrl.String(), bytes.NewBuffer([]byte(content)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("destroying virtual instance failed with status code %d", resp.StatusCode)
	}

	return nil
}

func init() {
	virtualCmd.AddCommand(createVirtualCmd)
	virtualCmd.AddCommand(destroyVirtualCmd)
	rootCmd.AddCommand(virtualCmd)

	createVirtualCmd.Flags().StringVarP(&virtualNovaVersion, "version", "v", "x.x.x", "Version of the service manager to install.")
}
