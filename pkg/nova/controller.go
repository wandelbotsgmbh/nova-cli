package nova

import (
	"context"
	"fmt"

	apiv1 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova"
)

func listControllersV1Impl(host string, cell string) ([]apiv1.ControllerInstance, error) {
	client, err := NewClientWithToken(host)
	if err != nil {
		return []apiv1.ControllerInstance{}, err
	}

	resp, err := client.ListControllersWithResponse(context.TODO(), cell)
	if err != nil {
		return []apiv1.ControllerInstance{}, err
	}

	if !StatusSuccessfull(resp.StatusCode()) {
		return []apiv1.ControllerInstance{}, fmt.Errorf("failed to list controllers %s", resp.Status())
	}

	return resp.JSON200.Instances, nil
}

func listControllerNamesV1Impl(host string, cell string) ([]string, error) {
	instances, err := listControllersV1Impl(host, cell)
	if err != nil {
		return []string{}, err
	}

	names := make([]string, len(instances))
	for i, instance := range instances {
		names[i] = instance.Controller
	}
	return names, nil
}
