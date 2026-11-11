package mcp_test

import (
	"context"

	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
)

// fakeAPI implements novaapi.API; it records calls so tests can assert
// exactly what the tools construct, mirroring the fake used by the original
// backend-internal mcp package's tests.
type fakeAPI struct {
	version        string
	cells          []string
	cell           novaapi.CellInfo
	controllers    []string
	controller     novaapi.ControllerInfo
	configurations []string
	deployResult   novaapi.DeployResult
	err            *novaapi.Error

	createdSpecs []novaapi.VirtualControllerSpec
	deletedNames []string
}

func (f *fakeAPI) GetSystemVersion(context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.version, nil
}

func (f *fakeAPI) ListCells(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.cells, nil
}

func (f *fakeAPI) DescribeCell(context.Context, string) (novaapi.CellInfo, error) {
	if f.err != nil {
		return novaapi.CellInfo{}, f.err
	}
	return f.cell, nil
}

func (f *fakeAPI) ListControllers(context.Context, string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.controllers, nil
}

func (f *fakeAPI) DescribeController(context.Context, string, string) (novaapi.ControllerInfo, error) {
	if f.err != nil {
		return novaapi.ControllerInfo{}, f.err
	}
	return f.controller, nil
}

func (f *fakeAPI) ListRobotConfigurations(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.configurations, nil
}

func (f *fakeAPI) CreateVirtualController(_ context.Context, _ string, spec novaapi.VirtualControllerSpec) (novaapi.DeployResult, error) {
	if f.err != nil {
		return novaapi.DeployResult{}, f.err
	}
	f.createdSpecs = append(f.createdSpecs, spec)
	return f.deployResult, nil
}

func (f *fakeAPI) DeleteController(_ context.Context, _, controllerID string) (novaapi.DeployResult, error) {
	if f.err != nil {
		return novaapi.DeployResult{}, f.err
	}
	f.deletedNames = append(f.deletedNames, controllerID)
	return f.deployResult, nil
}
