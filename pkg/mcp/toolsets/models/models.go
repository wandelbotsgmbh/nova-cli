// Package models serves the motion-group-models toolset: the robot models an instance
// knows, with limits, home joints and a kinematic summary. Model files (GLB, USD,
// collision) are never returned.
package models

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// Tools returns the tools of this package.
func Tools(env tool.Env) []tool.Registration {
	return []tool.Registration{listMotionGroupModels(env.Client), describeMotionGroupModel(env.Client)}
}

func round(v float64, n int) float64 {
	p := math.Pow(10, float64(n))
	r := math.Round(v*p) / p
	if r == 0 {
		return 0
	}
	return r
}

func roundPtr(v *float64, n int) *float64 {
	if v == nil {
		return nil
	}
	r := round(*v, n)
	return &r
}

// --- list_motion_group_models ---

type listInput struct {
	Query string `json:"query,omitempty" jsonschema:"case-insensitive filter on the model id or readable name, e.g. 'ur5' or 'kuka'"`
	tool.PageInput
}

type modelEntry struct {
	Model string `json:"model" jsonschema:"model id, as used by kinematics and describe_motion_group_model"`
	Name  string `json:"name,omitempty" jsonschema:"readable product name"`
}

type listOutput struct {
	Models []modelEntry `json:"models"`
	tool.PageInfo
}

func listMotionGroupModels(c *novaapi.Client) tool.Registration {
	const name = "list_motion_group_models"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "List the motion group (robot) models this instance knows, optionally filtered by a search text.",
			Annotations: tool.Read("List motion group models"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in listInput) (*sdk.CallToolResult, listOutput, error) {
			var ids []string
			if err := c.Get(ctx, name, "/motion-group-models", "", &ids); err != nil {
				return tool.Fail[listOutput](err)
			}
			// readable names come from the experimental catalog; without it, ids only
			names := map[string]string{}
			var catalog []struct {
				Model string `json:"motion_group_name"`
				Name  string `json:"readable_name"`
			}
			if err := c.Get(ctx, name, "/experimental/motion-group-models/catalog", "", &catalog); err == nil {
				for _, entry := range catalog {
					names[entry.Model] = entry.Name
				}
			}
			slices.Sort(ids)
			query := strings.ToLower(strings.TrimSpace(in.Query))
			all := []modelEntry{}
			for _, id := range ids {
				entry := modelEntry{Model: id, Name: names[id]}
				if query != "" && !strings.Contains(strings.ToLower(id), query) && !strings.Contains(strings.ToLower(entry.Name), query) {
					continue
				}
				all = append(all, entry)
			}
			page, info := tool.Page(all, in.PageInput)
			extra := map[string]any{}
			if query != "" {
				extra["query"] = in.Query
			}
			return tool.OK("motion group models listed", info.Fields(extra), listOutput{Models: page, PageInfo: info})
		})
	}
	return tool.Registration{Toolset: tool.MotionGroupModels, ReadOnly: true, Add: add}
}

// --- describe_motion_group_model ---

type describeInput struct {
	Model string `json:"model" jsonschema:"model id (see list_motion_group_models), e.g. UniversalRobots_UR5e"`
}

type jointLimit struct {
	Lower    *float64 `json:"lower,omitempty" jsonschema:"lower position limit in rad (mm for linear joints)"`
	Upper    *float64 `json:"upper,omitempty" jsonschema:"upper position limit in rad (mm for linear joints)"`
	Velocity *float64 `json:"velocity,omitempty" jsonschema:"max velocity in rad/s"`
}

type describeOutput struct {
	Model            string       `json:"model"`
	JointCount       int          `json:"joint_count"`
	JointTypes       []string     `json:"joint_types,omitempty" jsonschema:"revolute or prismatic, per joint"`
	InverseSolver    string       `json:"inverse_solver,omitempty"`
	HomeJoints       []float64    `json:"home_joints,omitempty" jsonschema:"home joint position in rad"`
	JointLimits      []jointLimit `json:"joint_limits,omitempty" jsonschema:"joint limits in automatic mode"`
	TCPVelocityLimit *float64     `json:"tcp_velocity_limit,omitempty" jsonschema:"max TCP velocity in automatic mode, mm/s"`
}

func describeMotionGroupModel(c *novaapi.Client) tool.Registration {
	const name = "describe_motion_group_model"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "A robot model's kinematic summary (joint count and types, inverse solver), home joints and " +
				"joint/TCP limits. No geometry or model files.",
			Annotations: tool.Read("Describe motion group model"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in describeInput) (*sdk.CallToolResult, describeOutput, error) {
			if err := tool.Required(name, "model", in.Model); err != nil {
				return tool.Fail[describeOutput](err)
			}
			base := "/motion-group-models/" + url.PathEscape(in.Model)
			notFound := fmt.Sprintf("motion group model '%s' not found (see list_motion_group_models)", in.Model)
			var kinematic struct {
				DH []struct {
					Type string `json:"type"`
				} `json:"dh_parameters"`
				InverseSolver string `json:"inverse_solver"`
			}
			if err := c.Get(ctx, name, base+"/kinematic", notFound, &kinematic); err != nil {
				return tool.Fail[describeOutput](err)
			}
			out := describeOutput{Model: in.Model, JointCount: len(kinematic.DH), InverseSolver: kinematic.InverseSolver}
			for _, dh := range kinematic.DH {
				out.JointTypes = append(out.JointTypes, jointType(dh.Type))
			}

			// home joints and limits are experimental endpoints: optional
			experimental := "/experimental" + base
			var home []float64
			if err := c.Get(ctx, name, experimental+"/homejoints", notFound, &home); err == nil {
				for _, j := range home {
					out.HomeJoints = append(out.HomeJoints, round(j, 4))
				}
			}
			var limits struct {
				Auto struct {
					Joints []struct {
						Position *struct {
							Lower *float64 `json:"lower_limit"`
							Upper *float64 `json:"upper_limit"`
						} `json:"position"`
						Velocity *float64 `json:"velocity"`
					} `json:"joints"`
					TCP *struct {
						Velocity *float64 `json:"velocity"`
					} `json:"tcp"`
				} `json:"auto_limits"`
			}
			if err := c.Get(ctx, name, experimental+"/limit", notFound, &limits); err == nil {
				for _, j := range limits.Auto.Joints {
					limit := jointLimit{Velocity: roundPtr(j.Velocity, 4)}
					if j.Position != nil {
						limit.Lower, limit.Upper = roundPtr(j.Position.Lower, 4), roundPtr(j.Position.Upper, 4)
					}
					out.JointLimits = append(out.JointLimits, limit)
				}
				if limits.Auto.TCP != nil {
					out.TCPVelocityLimit = roundPtr(limits.Auto.TCP.Velocity, 2)
				}
			}
			return tool.OK("motion group model described", map[string]any{"model": in.Model, "joints": out.JointCount}, out)
		})
	}
	return tool.Registration{Toolset: tool.MotionGroupModels, ReadOnly: true, Add: add}
}

func jointType(t string) string {
	switch t {
	case "REVOLUTE_JOINT":
		return "revolute"
	case "PRISMATIC_JOINT":
		return "prismatic"
	}
	return strings.ToLower(t)
}
