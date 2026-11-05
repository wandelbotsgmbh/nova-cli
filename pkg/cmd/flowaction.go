package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/utils"
	"golang.org/x/term"
)

// NovaFlow action services come from the Copier template the NovaFlow team
// maintains, run with uv: projects keep a link to it, so 'copier update'
// brings later template versions into them.
const (
	novaflowActionTemplate = "gh:wandelbotsgmbh/novaflow-action-template"
	novaflowActionRepo     = "https://github.com/wandelbotsgmbh/novaflow-action-template.git"
	// the template supports up to 5 actions per copier run
	novaflowMaxActions = 5
)

var novaflowActionKinds = []string{"action", "condition", "state_provider"}

// --action for 'nova flow action create'
var flowActionSpecs []string

var flowActionCmd = &cobra.Command{
	Use:   "action",
	Short: "Develop custom actions for flows",
}

var flowActionCreateCmd = &cobra.Command{
	Use:     "create <name>",
	Short:   "Create an action service project",
	Long:    flowActionCreateLong,
	Example: flowActionCreateExample,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return createNovaflowActionProject(cmd, strings.TrimSpace(args[0]))
	},
}

func init() {
	flowActionCreateCmd.Flags().StringArrayVar(&flowActionSpecs, "action", nil,
		"An action to create, as Name or Name:kind (kind: action, condition, state_provider). Repeat for more, up to 5. Makes the creation non-interactive.")
	flowActionCmd.AddCommand(flowActionCreateCmd)
	flowCmd.AddCommand(flowActionCmd)
}

type novaflowAction struct {
	Name string
	Kind string
}

func parseNovaflowActions(specs []string) ([]novaflowAction, error) {
	if len(specs) > novaflowMaxActions {
		return nil, fmt.Errorf("at most %d actions can be created at once, add more later with 'uvx copier update --trust'", novaflowMaxActions)
	}
	var actions []novaflowAction
	for _, spec := range specs {
		name, kind, _ := strings.Cut(spec, ":")
		if kind == "" {
			kind = "action"
		}
		if !isPascalCase(name) {
			return nil, fmt.Errorf("action name %q must be PascalCase, e.g. OpenGripper", name)
		}
		if !slices.Contains(novaflowActionKinds, kind) {
			return nil, fmt.Errorf("action %s: kind %q must be one of %s", name, kind, strings.Join(novaflowActionKinds, ", "))
		}
		actions = append(actions, novaflowAction{Name: name, Kind: kind})
	}
	return actions, nil
}

func isPascalCase(s string) bool {
	if s == "" || !unicode.IsUpper(rune(s[0])) {
		return false
	}
	for _, r := range s {
		if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

// serviceName turns a project name like "gripper-actions" into the PascalCase
// service name the template expects ("GripperActions"), keeping existing
// capitals ("GripperActions" stays as it is).
func serviceName(appName string) string {
	var b strings.Builder
	upper := true
	for _, r := range appName {
		switch {
		case r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r)):
			upper = true
		case upper:
			b.WriteRune(unicode.ToUpper(r))
			upper = false
		default:
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" || !unicode.IsLetter(rune(s[0])) {
		s = "Service" + s
	}
	return s
}

// copierArgs builds the uvx command line that renders the template.
func copierArgs(outpath, service, appName, registry string, actions []novaflowAction, interactive bool) []string {
	// --quiet: the template's own next steps would contradict ours (.env is written below)
	args := []string{"copier", "copy", "--trust", "--quiet"}
	if !interactive {
		args = append(args, "--defaults")
	}
	data := func(k, v string) { args = append(args, "--data", k+"="+v) }
	data("service_name", service)
	data("app_name", appName)
	if registry != "" {
		data("image_registry", registry)
	}
	if len(actions) > 0 {
		data("action_count", strconv.Itoa(len(actions)))
		for i, a := range actions {
			n := strconv.Itoa(i + 1)
			data("action_"+n+"_name", a.Name)
			data("action_"+n+"_kind", a.Kind)
		}
	}
	return append(args, novaflowActionTemplate, outpath)
}

func createNovaflowActionProject(cmd *cobra.Command, name string) error {
	if err := utils.VerifyProjectName(name); err != nil {
		return err
	}
	appName, err := utils.ConvertToValidSubdomainName(name)
	if err != nil {
		return err
	}
	actions, err := parseNovaflowActions(flowActionSpecs)
	if err != nil {
		return err
	}
	outpath := "./" + name
	if _, err := os.Stat(outpath); err == nil {
		return fmt.Errorf("directory %s already exists, to update a generated project run 'uvx copier update --trust' in it", outpath)
	}
	uvx, err := exec.LookPath("uvx")
	if err != nil {
		return errors.New("creating action services needs uv, see https://docs.astral.sh/uv/getting-started/installation/")
	}

	// copier answers a failed clone with a long traceback: check access first,
	// without letting git prompt for credentials
	git, err := exec.LookPath("git")
	if err != nil {
		return errors.New("creating action services needs git")
	}
	check := exec.Command(git, "ls-remote", "--exit-code", "-q", novaflowActionRepo, "HEAD")
	check.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := check.CombinedOutput(); err != nil {
		return fmt.Errorf("can't read the NovaFlow action template %s: %s\n"+
			"The template is not public yet: you need read access to it with your git credentials", novaflowActionRepo, strings.TrimSpace(string(out)))
	}

	// copier asks for everything not given as flag, unless there is no
	// terminal (agents, CI) or actions were given: then defaults apply
	interactive := len(actions) == 0 && term.IsTerminal(int(os.Stdin.Fd()))
	c := exec.Command(uvx, copierArgs(outpath, serviceName(name), appName, viper.GetString("image-registry"), actions, interactive)...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("creating the project from %s failed: %w", novaflowActionTemplate, err)
	}

	if err := writeAppEnv(cmd, outpath, true); err != nil {
		cmd.Printf("Could not write the local development settings: %v\nRun 'nova app update-env' in %s once that is fixed.\n", err, outpath)
	}

	cmd.Printf("\nNext steps:\n  cd %s\n  uv sync\n  uv run pytest\n", name)
	if currentTarget().Kind == instances.KindCloud {
		cmd.Println("  nova app install .   # cloud instances don't accept NATS connections from outside, so deploy to run it")
	} else {
		cmd.Println("  uv run run.py        # registers the actions on the instance, they show up in 'nova flow open'")
		cmd.Println("  nova app install .   # deploy when done")
	}
	return nil
}
