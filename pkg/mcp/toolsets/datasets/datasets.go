// Package datasets serves the `datasets` toolset: taught poses, frames and command
// routines stored per cell (experimental NOVA API, /experimental/cells/{cell}/datasets).
package datasets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/tool"
)

// Tools returns the tools of this package.
func Tools(env tool.Env) []tool.Registration {
	c := env.Client
	return []tool.Registration{
		listDatasets(c), describeDataset(c), listDatasetPoses(c), describeCommandRoutine(c),
		resolvePoses(c), localizePoses(c), createDataset(c), deleteDataset(c),
	}
}

// maxPoses bounds the poses of one resolve/localize/create call.
const maxPoses = 200

var idPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

func datasetsPath(cell string) string {
	return "/experimental/cells/" + url.PathEscape(cell) + "/datasets"
}

func datasetPath(cell, dataset string) string {
	return datasetsPath(cell) + "/" + url.PathEscape(dataset)
}

func revisionQuery(revision int) url.Values {
	if revision <= 0 {
		return nil
	}
	return url.Values{"revision": {strconv.Itoa(revision)}}
}

func notFound(cell, dataset string, revision int) string {
	if revision > 0 {
		return fmt.Sprintf("revision %d of dataset '%s' not found in cell '%s'", revision, dataset, cell)
	}
	return fmt.Sprintf("dataset '%s' not found in cell '%s'", dataset, cell)
}

func shorten(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// Pose is a position [x, y, z] in mm and a rotation vector [rx, ry, rz] in rad.
type Pose struct {
	Position    []float64 `json:"position" jsonschema:"[x, y, z] in mm"`
	Orientation []float64 `json:"orientation" jsonschema:"rotation vector [rx, ry, rz] in rad (axis times angle)"`
}

func validatePoses(toolName string, poses []Pose) *novaapi.Error {
	if len(poses) == 0 {
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: toolName + ": 'poses' must contain at least one pose"}
	}
	if len(poses) > maxPoses {
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: fmt.Sprintf("%s: at most %d poses per call", toolName, maxPoses)}
	}
	for i, p := range poses {
		if err := validatePose(toolName, fmt.Sprintf("poses[%d]", i), p); err != nil {
			return err
		}
	}
	return nil
}

func validatePose(toolName, field string, p Pose) *novaapi.Error {
	if len(p.Position) != 3 || len(p.Orientation) != 3 {
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: fmt.Sprintf(
			"%s: %s needs 'position' [x, y, z] and 'orientation' [rx, ry, rz], 3 numbers each", toolName, field)}
	}
	return nil
}

// --- API structs: only the fields handed on ---

type apiDataset struct {
	Dataset     string `json:"dataset"`
	Name        string `json:"name"`
	Revision    int    `json:"revision"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type apiKinematicConfiguration struct {
	KinematicBranch struct {
		Shoulder string `json:"shoulder_branch"`
		Elbow    string `json:"elbow_branch"`
		Wrist    string `json:"wrist_branch"`
	} `json:"kinematic_branch"`
}

type apiPose struct {
	DatasetPose            string                     `json:"dataset_pose"`
	Name                   string                     `json:"name"`
	Description            string                     `json:"description"`
	Frame                  *string                    `json:"frame"`
	Pose                   Pose                       `json:"pose"`
	KinematicConfiguration *apiKinematicConfiguration `json:"kinematic_configuration"`
}

type apiFrame struct {
	Frame          string  `json:"frame"`
	Name           string  `json:"name"`
	ReferenceFrame *string `json:"reference_frame"`
	Pose           Pose    `json:"pose"`
}

type apiCommandRoutine struct {
	CommandRoutine string `json:"command_routine"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	MotionGroup    *struct {
		ID string `json:"id"`
	} `json:"motion_group"`
	MotionGroupSetup *struct {
		MotionGroupModel string `json:"motion_group_model"`
		CycleTime        int    `json:"cycle_time"`
	} `json:"motion_group_setup"`
	TCP                string            `json:"tcp"`
	StartJointPosition []float64         `json:"start_joint_position"`
	Commands           []json.RawMessage `json:"commands"`
}

type apiDatasetDetails struct {
	apiDataset
	Poses           []apiPose           `json:"poses"`
	Frames          []apiFrame          `json:"frames"`
	CommandRoutines []apiCommandRoutine `json:"command_routines"`
}

func getDataset(ctx context.Context, c *novaapi.Client, toolName, cell, dataset string, revision int) (apiDatasetDetails, *novaapi.Error) {
	var details apiDatasetDetails
	_, err := c.JSON(ctx, novaapi.Call{
		Tool: toolName, Method: http.MethodGet, Path: datasetPath(cell, dataset),
		Query: revisionQuery(revision), NotFound: notFound(cell, dataset, revision),
	}, &details)
	return details, err
}

// listRevisions returns the revision numbers of a dataset in ascending order, none if
// it doesn't exist.
func listRevisions(ctx context.Context, c *novaapi.Client, toolName, cell, dataset string) ([]int, *novaapi.Error) {
	var all []apiDataset
	if _, err := c.JSON(ctx, novaapi.Call{
		Tool: toolName, Method: http.MethodGet, Path: datasetsPath(cell), Query: url.Values{"dataset": {dataset}},
		NotFound: fmt.Sprintf("cell '%s' not found", cell),
	}, &all); err != nil {
		return nil, err
	}
	revisions := []int{}
	for _, d := range all {
		if d.Dataset == dataset {
			revisions = append(revisions, d.Revision)
		}
	}
	slices.Sort(revisions)
	return revisions, nil
}

func frameName(f *string) string {
	if f == nil {
		return ""
	}
	return *f
}

// --- list_datasets ---

type listDatasetsInput struct {
	Cell         string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Dataset      string `json:"dataset,omitempty" jsonschema:"only the revisions of this dataset"`
	AllRevisions bool   `json:"all_revisions,omitempty" jsonschema:"list every revision instead of only the latest one of each dataset"`
	tool.PageInput
}

// DatasetSummary is one dataset revision.
type DatasetSummary struct {
	Dataset     string `json:"dataset"`
	Name        string `json:"name,omitempty"`
	Revision    int    `json:"revision"`
	Description string `json:"description,omitempty" jsonschema:"shortened to 200 characters"`
	UpdatedAt   string `json:"updated_at"`
}

type listDatasetsOutput struct {
	Datasets []DatasetSummary `json:"datasets"`
	tool.PageInfo
}

func listDatasets(c *novaapi.Client) tool.Registration {
	const name = "list_datasets"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "List the datasets of a cell: named collections of taught poses, frames and command routines. " +
				"Latest revision of each dataset by default.",
			Annotations: tool.Read("List datasets"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in listDatasetsInput) (*sdk.CallToolResult, listDatasetsOutput, error) {
			if err := tool.Required(name, "cell", in.Cell); err != nil {
				return tool.Fail[listDatasetsOutput](err)
			}
			query := url.Values{}
			if in.Dataset != "" {
				query.Set("dataset", in.Dataset)
			}
			if !in.AllRevisions && in.Dataset == "" {
				query.Set("latest_only", "true")
			}
			var all []apiDataset
			if _, err := c.JSON(ctx, novaapi.Call{
				Tool: name, Method: http.MethodGet, Path: datasetsPath(in.Cell), Query: query,
				NotFound: fmt.Sprintf("cell '%s' not found", in.Cell),
			}, &all); err != nil {
				return tool.Fail[listDatasetsOutput](err)
			}
			if in.Dataset != "" && !in.AllRevisions && len(all) > 0 {
				latest := all[0]
				for _, d := range all {
					if d.Revision > latest.Revision {
						latest = d
					}
				}
				all = []apiDataset{latest}
			}
			summaries := make([]DatasetSummary, len(all))
			for i, d := range all {
				summaries[i] = DatasetSummary{Dataset: d.Dataset, Name: d.Name, Revision: d.Revision,
					Description: shorten(d.Description, 200), UpdatedAt: d.UpdatedAt}
			}
			window, page := tool.Page(summaries, in.PageInput)
			return tool.OK("datasets listed", page.Fields(map[string]any{"cell": in.Cell}),
				listDatasetsOutput{Datasets: window, PageInfo: page})
		})
	}
	return tool.Registration{Toolset: tool.Datasets, ReadOnly: true, Add: add}
}

// --- describe_dataset ---

type datasetInput struct {
	Cell     string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Dataset  string `json:"dataset" jsonschema:"dataset id (see list_datasets)"`
	Revision int    `json:"revision,omitempty" jsonschema:"dataset revision (default: latest)"`
}

// FrameSummary is a frame of a dataset; its pose is relative to reference_frame (world
// if empty).
type FrameSummary struct {
	Frame          string `json:"frame"`
	Name           string `json:"name,omitempty"`
	ReferenceFrame string `json:"reference_frame,omitempty"`
	Pose           Pose   `json:"pose"`
}

// RoutineSummary names a command routine.
type RoutineSummary struct {
	CommandRoutine string `json:"command_routine"`
	Name           string `json:"name,omitempty"`
	Commands       int    `json:"commands"`
}

type describeDatasetOutput struct {
	Dataset         string           `json:"dataset"`
	Name            string           `json:"name,omitempty"`
	Revision        int              `json:"revision"`
	Description     string           `json:"description,omitempty"`
	CreatedAt       string           `json:"created_at"`
	UpdatedAt       string           `json:"updated_at"`
	PoseCount       int              `json:"pose_count"`
	PoseIDs         []string         `json:"pose_ids" jsonschema:"the first 100 pose ids; list_dataset_poses returns the poses"`
	Frames          []FrameSummary   `json:"frames" jsonschema:"the first 100 frames"`
	FrameCount      int              `json:"frame_count"`
	CommandRoutines []RoutineSummary `json:"command_routines" jsonschema:"the first 100 command routines; describe_command_routine returns one"`
	RoutineCount    int              `json:"command_routine_count"`
}

func describeDataset(c *novaapi.Client) tool.Registration {
	const name = "describe_dataset"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name:        name,
			Description: "A dataset's revision, description, frames, and the ids of its poses and command routines.",
			Annotations: tool.Read("Describe dataset"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in datasetInput) (*sdk.CallToolResult, describeDatasetOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "dataset", in.Dataset); err != nil {
				return tool.Fail[describeDatasetOutput](err)
			}
			d, err := getDataset(ctx, c, name, in.Cell, in.Dataset, in.Revision)
			if err != nil {
				return tool.Fail[describeDatasetOutput](err)
			}
			out := describeDatasetOutput{
				Dataset: d.Dataset, Name: d.Name, Revision: d.Revision, Description: shorten(d.Description, 1000),
				CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
				PoseCount: len(d.Poses), FrameCount: len(d.Frames), RoutineCount: len(d.CommandRoutines),
				PoseIDs: []string{}, Frames: []FrameSummary{}, CommandRoutines: []RoutineSummary{},
			}
			for i, p := range d.Poses {
				if i == 100 {
					break
				}
				out.PoseIDs = append(out.PoseIDs, p.DatasetPose)
			}
			for i, f := range d.Frames {
				if i == 100 {
					break
				}
				out.Frames = append(out.Frames, FrameSummary{Frame: f.Frame, Name: f.Name, ReferenceFrame: frameName(f.ReferenceFrame), Pose: f.Pose})
			}
			for i, r := range d.CommandRoutines {
				if i == 100 {
					break
				}
				out.CommandRoutines = append(out.CommandRoutines, RoutineSummary{CommandRoutine: r.CommandRoutine, Name: r.Name, Commands: len(r.Commands)})
			}
			return tool.OK("dataset described", map[string]any{
				"cell": in.Cell, "dataset": d.Dataset, "revision": d.Revision,
				"poses": len(d.Poses), "frames": len(d.Frames), "command_routines": len(d.CommandRoutines),
			}, out)
		})
	}
	return tool.Registration{Toolset: tool.Datasets, ReadOnly: true, Add: add}
}

// --- list_dataset_poses ---

type listPosesInput struct {
	datasetInput
	Frame string `json:"frame,omitempty" jsonschema:"only poses expressed in this frame"`
	tool.PageInput
}

// PoseSummary is a taught pose. Its pose is relative to frame (world if empty).
type PoseSummary struct {
	DatasetPose string `json:"dataset_pose"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty" jsonschema:"shortened to 200 characters"`
	Frame       string `json:"frame,omitempty"`
	Pose        Pose   `json:"pose"`
	Branch      string `json:"kinematic_branch,omitempty" jsonschema:"shoulder/elbow/wrist branch, if the pose has a kinematic configuration"`
}

type listPosesOutput struct {
	Poses []PoseSummary `json:"poses"`
	tool.PageInfo
}

func listDatasetPoses(c *novaapi.Client) tool.Registration {
	const name = "list_dataset_poses"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "List the taught poses of a dataset with position (mm), rotation vector (rad), frame and " +
				"kinematic branch. Poses are relative to their frame; resolve_poses_to_world converts them.",
			Annotations: tool.Read("List dataset poses"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in listPosesInput) (*sdk.CallToolResult, listPosesOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "dataset", in.Dataset); err != nil {
				return tool.Fail[listPosesOutput](err)
			}
			d, err := getDataset(ctx, c, name, in.Cell, in.Dataset, in.Revision)
			if err != nil {
				return tool.Fail[listPosesOutput](err)
			}
			poses := []PoseSummary{}
			for _, p := range d.Poses {
				if in.Frame != "" && frameName(p.Frame) != in.Frame {
					continue
				}
				summary := PoseSummary{DatasetPose: p.DatasetPose, Name: p.Name, Description: shorten(p.Description, 200),
					Frame: frameName(p.Frame), Pose: p.Pose}
				if k := p.KinematicConfiguration; k != nil {
					summary.Branch = k.KinematicBranch.Shoulder + "/" + k.KinematicBranch.Elbow + "/" + k.KinematicBranch.Wrist
				}
				poses = append(poses, summary)
			}
			window, page := tool.Page(poses, in.PageInput)
			return tool.OK("dataset poses listed", page.Fields(map[string]any{"dataset": d.Dataset, "revision": d.Revision}),
				listPosesOutput{Poses: window, PageInfo: page})
		})
	}
	return tool.Registration{Toolset: tool.Datasets, ReadOnly: true, Add: add}
}

// --- describe_command_routine ---

type routineInput struct {
	datasetInput
	CommandRoutine string `json:"command_routine" jsonschema:"command routine id (see describe_dataset)"`
}

// CommandSummary is one command of a routine.
type CommandSummary struct {
	Index   int    `json:"index"`
	Type    string `json:"type"`
	Intent  string `json:"intent,omitempty"`
	Details string `json:"details,omitempty" jsonschema:"the command without type and metadata, compact JSON shortened to 300 characters"`
}

type describeRoutineOutput struct {
	CommandRoutine     string           `json:"command_routine"`
	Name               string           `json:"name,omitempty"`
	Description        string           `json:"description,omitempty"`
	MotionGroup        string           `json:"motion_group,omitempty"`
	MotionGroupModel   string           `json:"motion_group_model,omitempty" jsonschema:"empty for an I/O-only routine"`
	CycleTimeMs        int              `json:"cycle_time_ms,omitempty"`
	TCP                string           `json:"tcp,omitempty"`
	StartJointPosition []float64        `json:"start_joint_position,omitempty"`
	CommandCount       int              `json:"command_count"`
	Commands           []CommandSummary `json:"commands" jsonschema:"the first 100 commands"`
}

func summarizeCommand(i int, raw json.RawMessage) CommandSummary {
	var fields map[string]json.RawMessage
	summary := CommandSummary{Index: i}
	if err := json.Unmarshal(raw, &fields); err != nil {
		summary.Type = "unknown"
		return summary
	}
	_ = json.Unmarshal(fields["type"], &summary.Type)
	_ = json.Unmarshal(fields["intent"], &summary.Intent)
	delete(fields, "type")
	delete(fields, "intent")
	delete(fields, "metadata")
	if details, err := json.Marshal(fields); err == nil && len(fields) > 0 {
		summary.Details = shorten(string(details), 300)
	}
	return summary
}

func describeCommandRoutine(c *novaapi.Client) tool.Registration {
	const name = "describe_command_routine"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "A command routine of a dataset: motion group, model, TCP, start joints, and its ordered " +
				"commands (motions, I/O, waits, markers) in short form.",
			Annotations: tool.Read("Describe command routine"),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in routineInput) (*sdk.CallToolResult, describeRoutineOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "dataset", in.Dataset, "command_routine", in.CommandRoutine); err != nil {
				return tool.Fail[describeRoutineOutput](err)
			}
			d, err := getDataset(ctx, c, name, in.Cell, in.Dataset, in.Revision)
			if err != nil {
				return tool.Fail[describeRoutineOutput](err)
			}
			for _, r := range d.CommandRoutines {
				if r.CommandRoutine != in.CommandRoutine {
					continue
				}
				out := describeRoutineOutput{
					CommandRoutine: r.CommandRoutine, Name: r.Name, Description: shorten(r.Description, 1000),
					TCP: r.TCP, StartJointPosition: r.StartJointPosition, CommandCount: len(r.Commands), Commands: []CommandSummary{},
				}
				if r.MotionGroup != nil {
					out.MotionGroup = r.MotionGroup.ID
				}
				if r.MotionGroupSetup != nil {
					out.MotionGroupModel = r.MotionGroupSetup.MotionGroupModel
					out.CycleTimeMs = r.MotionGroupSetup.CycleTime
				}
				for i, raw := range r.Commands {
					if i == 100 {
						break
					}
					out.Commands = append(out.Commands, summarizeCommand(i, raw))
				}
				return tool.OK("command routine described", map[string]any{
					"dataset": d.Dataset, "command_routine": r.CommandRoutine, "commands": len(r.Commands),
				}, out)
			}
			return tool.Fail[describeRoutineOutput](&novaapi.Error{Code: novaapi.CodeNotFound, Message: fmt.Sprintf(
				"%s: command routine '%s' not found in dataset '%s'", name, in.CommandRoutine, in.Dataset)})
		})
	}
	return tool.Registration{Toolset: tool.Datasets, ReadOnly: true, Add: add}
}

// --- resolve_poses_to_world / localize_poses_from_world ---

type transformInput struct {
	Cell     string `json:"cell" jsonschema:"cell name (see list_cells)"`
	Dataset  string `json:"dataset" jsonschema:"dataset id (see list_datasets)"`
	Frame    string `json:"frame" jsonschema:"frame id of the dataset (see describe_dataset)"`
	Revision int    `json:"revision,omitempty" jsonschema:"dataset revision (default: latest)"`
	Poses    []Pose `json:"poses" jsonschema:"1 to 200 poses"`
}

type transformOutput struct {
	Poses []Pose `json:"poses"`
}

func transformTool(c *novaapi.Client, name, op, title, description, sentence string) tool.Registration {
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{Name: name, Description: description, Annotations: tool.Read(title)},
			func(ctx context.Context, _ *sdk.CallToolRequest, in transformInput) (*sdk.CallToolResult, transformOutput, error) {
				if err := tool.Required(name, "cell", in.Cell, "dataset", in.Dataset, "frame", in.Frame); err != nil {
					return tool.Fail[transformOutput](err)
				}
				if err := validatePoses(name, in.Poses); err != nil {
					return tool.Fail[transformOutput](err)
				}
				var poses []Pose
				if _, err := c.JSON(ctx, novaapi.Call{
					Tool: name, Method: http.MethodPost,
					Path:  datasetPath(in.Cell, in.Dataset) + "/frames/" + url.PathEscape(in.Frame) + "/" + op,
					Query: revisionQuery(in.Revision), Body: in.Poses,
					NotFound: fmt.Sprintf("dataset '%s' or its frame '%s' not found in cell '%s'", in.Dataset, in.Frame, in.Cell),
				}, &poses); err != nil {
					return tool.Fail[transformOutput](err)
				}
				return tool.OK(sentence, map[string]any{"dataset": in.Dataset, "frame": in.Frame, "count": len(poses)},
					transformOutput{Poses: poses})
			})
	}
	return tool.Registration{Toolset: tool.Datasets, ReadOnly: true, Add: add}
}

func resolvePoses(c *novaapi.Client) tool.Registration {
	return transformTool(c, "resolve_poses_to_world", "resolve", "Resolve poses to world",
		"Convert poses expressed in a dataset frame into the world frame, through the dataset's chain of reference frames. "+
			"Pure computation, changes nothing.", "poses resolved to world")
}

func localizePoses(c *novaapi.Client) tool.Registration {
	return transformTool(c, "localize_poses_from_world", "localize", "Localize poses from world",
		"Convert world-frame poses into a dataset frame (the inverse of resolve_poses_to_world). Pure computation, changes nothing.",
		"poses localized in frame")
}

// --- create_dataset ---

// NewPose is a pose to create with a dataset.
type NewPose struct {
	DatasetPose string            `json:"dataset_pose" jsonschema:"pose id: starts with a letter, then letters, digits, hyphens"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Frame       string            `json:"frame,omitempty" jsonschema:"frame id the pose is relative to (default: world)"`
	Pose        Pose              `json:"pose"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// NewFrame is a frame to create with a dataset.
type NewFrame struct {
	Frame          string            `json:"frame" jsonschema:"frame id"`
	Name           string            `json:"name,omitempty"`
	ReferenceFrame string            `json:"reference_frame,omitempty" jsonschema:"parent frame id (default: world)"`
	Pose           Pose              `json:"pose" jsonschema:"pose relative to the reference frame"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type createDatasetInput struct {
	Cell        string     `json:"cell" jsonschema:"cell name (see list_cells)"`
	Dataset     string     `json:"dataset" jsonschema:"new dataset id: starts with a letter, then letters, digits, hyphens"`
	Name        string     `json:"name,omitempty" jsonschema:"display name"`
	Description string     `json:"description,omitempty"`
	Frames      []NewFrame `json:"frames,omitempty"`
	Poses       []NewPose  `json:"poses,omitempty" jsonschema:"up to 200 poses"`
	NewRevision bool       `json:"new_revision,omitempty" jsonschema:"if the dataset exists, save this content as its new latest revision (older revisions stay); without it an existing id is ALREADY_EXISTS"`
}

type createDatasetOutput struct {
	Dataset  string `json:"dataset"`
	Revision int    `json:"revision"`
	Poses    int    `json:"poses"`
	Frames   int    `json:"frames"`
}

func validateCreate(name string, in createDatasetInput) *novaapi.Error {
	if err := tool.Required(name, "cell", in.Cell, "dataset", in.Dataset); err != nil {
		return err
	}
	invalid := func(format string, args ...any) *novaapi.Error {
		return &novaapi.Error{Code: novaapi.CodeInvalidArgument, Message: name + ": " + fmt.Sprintf(format, args...)}
	}
	if !idPattern.MatchString(in.Dataset) {
		return invalid("dataset id '%s' must start with a letter and contain only letters, digits and hyphens", in.Dataset)
	}
	if len(in.Poses) > maxPoses {
		return invalid("at most %d poses per dataset in one call", maxPoses)
	}
	frames := map[string]bool{}
	for i, f := range in.Frames {
		if f.Frame == "" {
			return invalid("frames[%d] needs a 'frame' id", i)
		}
		if frames[f.Frame] {
			return invalid("frame '%s' is defined twice", f.Frame)
		}
		frames[f.Frame] = true
		if err := validatePose(name, fmt.Sprintf("frames[%d].pose", i), f.Pose); err != nil {
			return err
		}
	}
	for i, f := range in.Frames {
		if f.ReferenceFrame != "" && !frames[f.ReferenceFrame] {
			return invalid("frames[%d] references unknown frame '%s'", i, f.ReferenceFrame)
		}
	}
	ids := map[string]bool{}
	for i, p := range in.Poses {
		if !idPattern.MatchString(p.DatasetPose) {
			return invalid("poses[%d] needs a 'dataset_pose' id that starts with a letter and contains only letters, digits and hyphens", i)
		}
		if ids[p.DatasetPose] {
			return invalid("pose id '%s' is used twice", p.DatasetPose)
		}
		ids[p.DatasetPose] = true
		if p.Frame != "" && !frames[p.Frame] {
			return invalid("poses[%d] references unknown frame '%s'", i, p.Frame)
		}
		if err := validatePose(name, fmt.Sprintf("poses[%d].pose", i), p.Pose); err != nil {
			return err
		}
	}
	return nil
}

func createDataset(c *novaapi.Client) tool.Registration {
	const name = "create_dataset"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Create a dataset in a cell with frames and taught poses (no command routines). An existing id " +
				"is ALREADY_EXISTS unless new_revision is set: then the content becomes the dataset's new latest revision " +
				"and replaces the poses and frames of the latest one (command routines are not carried over).",
			Annotations: tool.Write("Create dataset", false),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in createDatasetInput) (*sdk.CallToolResult, createDatasetOutput, error) {
			if err := validateCreate(name, in); err != nil {
				return tool.Fail[createDatasetOutput](err)
			}
			revisions, err := listRevisions(ctx, c, name, in.Cell, in.Dataset)
			if err != nil {
				return tool.Fail[createDatasetOutput](err)
			}
			if len(revisions) > 0 && !in.NewRevision {
				return tool.Fail[createDatasetOutput](&novaapi.Error{Code: novaapi.CodeAlreadyExists, Message: fmt.Sprintf(
					"%s: dataset '%s' already exists in cell '%s' (latest revision %d); pick another id, or set new_revision to replace its content in a new revision",
					name, in.Dataset, in.Cell, slices.Max(revisions))})
			}
			type pose struct {
				NewPose
				Dataset string  `json:"dataset"`
				Frame   *string `json:"frame,omitempty"`
			}
			type frame struct {
				NewFrame
				Dataset        string  `json:"dataset"`
				ReferenceFrame *string `json:"reference_frame,omitempty"`
			}
			body := struct {
				Dataset     string  `json:"dataset"`
				Name        string  `json:"name,omitempty"`
				Description string  `json:"description,omitempty"`
				Poses       []pose  `json:"poses"`
				Frames      []frame `json:"frames"`
			}{Dataset: in.Dataset, Name: in.Name, Description: in.Description, Poses: []pose{}, Frames: []frame{}}
			for _, p := range in.Poses {
				entry := pose{NewPose: p, Dataset: in.Dataset}
				if p.Frame != "" {
					entry.Frame = &p.Frame
				}
				body.Poses = append(body.Poses, entry)
			}
			for _, f := range in.Frames {
				entry := frame{NewFrame: f, Dataset: in.Dataset}
				if f.ReferenceFrame != "" {
					entry.ReferenceFrame = &f.ReferenceFrame
				}
				body.Frames = append(body.Frames, entry)
			}
			var created apiDatasetDetails
			if _, err := c.JSON(ctx, novaapi.Call{
				Tool: name, Method: http.MethodPost, Path: datasetsPath(in.Cell), Body: body,
				NotFound: fmt.Sprintf("cell '%s' not found", in.Cell),
			}, &created); err != nil {
				return tool.Fail[createDatasetOutput](err)
			}
			out := createDatasetOutput{Dataset: in.Dataset, Revision: created.Revision, Poses: len(created.Poses), Frames: len(created.Frames)}
			return tool.OK("dataset created", map[string]any{"cell": in.Cell, "dataset": in.Dataset, "revision": out.Revision}, out)
		})
	}
	return tool.Registration{Toolset: tool.Datasets, ReadOnly: false, Add: add}
}

// --- delete_dataset ---

type deleteDatasetOutput struct {
	Dataset            string `json:"dataset"`
	Revision           int    `json:"revision,omitempty" jsonschema:"the revision deleted; 0 means the latest"`
	RemainingRevisions []int  `json:"remaining_revisions" jsonschema:"revisions of the dataset that still exist"`
}

func deleteDataset(c *novaapi.Client) tool.Registration {
	const name = "delete_dataset"
	add := func(s *sdk.Server) {
		sdk.AddTool(s, &sdk.Tool{
			Name: name,
			Description: "Delete one revision of a dataset (the latest unless 'revision' is set) with its poses and command " +
				"routines; older revisions stay and are listed in the result. DESTRUCTIVE and not undoable: confirm with the " +
				"user first, naming cell, dataset and revision.",
			// not idempotent: without 'revision' every call deletes the then-latest one
			Annotations: tool.Annotations("Delete dataset", false, false, true, false),
		}, func(ctx context.Context, _ *sdk.CallToolRequest, in datasetInput) (*sdk.CallToolResult, deleteDatasetOutput, error) {
			if err := tool.Required(name, "cell", in.Cell, "dataset", in.Dataset); err != nil {
				return tool.Fail[deleteDatasetOutput](err)
			}
			if _, err := c.JSON(ctx, novaapi.Call{
				Tool: name, Method: http.MethodDelete, Path: datasetPath(in.Cell, in.Dataset),
				Query: revisionQuery(in.Revision), NotFound: notFound(in.Cell, in.Dataset, in.Revision),
			}, nil); err != nil {
				return tool.Fail[deleteDatasetOutput](err)
			}
			// the delete is done: a failing listing must not make it look failed (a
			// repeated call would delete the next revision)
			fields := map[string]any{"cell": in.Cell, "dataset": in.Dataset}
			remaining, err := listRevisions(ctx, c, name, in.Cell, in.Dataset)
			if err != nil {
				remaining = []int{}
				fields["remaining_revisions"] = "unknown (listing failed, see list_datasets)"
			} else {
				fields["remaining_revisions"] = len(remaining)
			}
			if in.Revision > 0 {
				fields["revision"] = in.Revision
			} else {
				fields["revision"] = "latest"
			}
			return tool.OK("dataset revision deleted", fields,
				deleteDatasetOutput{Dataset: in.Dataset, Revision: in.Revision, RemainingRevisions: remaining})
		})
	}
	return tool.Registration{Toolset: tool.Datasets, ReadOnly: false, Add: add}
}
