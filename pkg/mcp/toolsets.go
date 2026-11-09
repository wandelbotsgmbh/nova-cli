package mcp

import (
	"slices"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/apps"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/cells"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/controllers"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/datasets"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/diagnosis"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/guides"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/kinematics"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/maintenance"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/models"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/motion"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/planning"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/programs"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/stores"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/system"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/toolsets/virtualcontrollers"
)

// Toolset names, re-exported for callers of this package.
const (
	ToolsetContext           = tool.Context
	ToolsetCells             = tool.Cells
	ToolsetControllers       = tool.Controllers
	ToolsetVirtualController = tool.VirtualControllers
)

// AllToolsets is every toolset in a fixed order.
func AllToolsets() []string { return tool.AllToolsets() }

// DefaultToolsets are served without --toolsets (mostly reads); the rest is opt-in.
func DefaultToolsets() []string { return tool.DefaultToolsets() }

// toolsetPackages build the tools beyond the ones ported from the instance service.
var toolsetPackages = []func(tool.Env) []tool.Registration{
	cells.Tools, system.Tools, apps.Tools, diagnosis.Tools,
	controllers.Tools, models.Tools, kinematics.Tools,
	virtualcontrollers.Tools,
	datasets.Tools, stores.Tools,
	planning.Tools, motion.Tools, programs.Tools,
	guides.Tools, maintenance.Tools,
}

// Register adds every tool of the given toolsets to s. With readOnly, tools that
// change anything are skipped server-side, a guard rail rather than a hint (ADR-0009).
// env.Client nil registers only the ported tools.
func Register(s *sdk.Server, api novaapi.API, env tool.Env, toolsets []string, readOnly bool) {
	regs := tools(api)
	if env.Client != nil {
		for _, build := range toolsetPackages {
			regs = append(regs, build(env)...)
		}
	}
	for _, reg := range regs {
		if !slices.Contains(toolsets, reg.Toolset) || readOnly && !reg.ReadOnly {
			continue
		}
		reg.Add(s)
	}
}
