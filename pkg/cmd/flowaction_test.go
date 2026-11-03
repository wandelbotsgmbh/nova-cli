package cmd

import (
	"slices"
	"strings"
	"testing"
)

func TestServiceName(t *testing.T) {
	for in, want := range map[string]string{
		"gripper-actions": "GripperActions",
		"GripperActions":  "GripperActions",
		"my_cool app":     "MyCoolApp",
		"2nd-robot":       "Service2ndRobot",
	} {
		if got := serviceName(in); got != want {
			t.Errorf("serviceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseNovaflowActions(t *testing.T) {
	got, err := parseNovaflowActions([]string{"OpenGripper", "IsOpen:condition"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (novaflowAction{"OpenGripper", "action"}) || got[1] != (novaflowAction{"IsOpen", "condition"}) {
		t.Errorf("unexpected actions %v", got)
	}
	for _, bad := range [][]string{{"open_gripper"}, {"Open:sensor"}, {"A", "B", "C", "D", "E", "F"}} {
		if _, err := parseNovaflowActions(bad); err == nil {
			t.Errorf("%v: expected an error", bad)
		}
	}
}

func TestCopierArgs(t *testing.T) {
	args := copierArgs("./gripper-actions", "GripperActions", "gripper-actions", "my.registry", []novaflowAction{{"OpenGripper", "action"}}, false)
	line := strings.Join(args, " ")
	for _, want := range []string{
		"copier copy --trust --quiet --defaults",
		"--data service_name=GripperActions", "--data app_name=gripper-actions", "--data image_registry=my.registry",
		"--data action_count=1", "--data action_1_name=OpenGripper", "--data action_1_kind=action",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %s", want, line)
		}
	}
	if args[len(args)-2] != novaflowActionTemplate || args[len(args)-1] != "./gripper-actions" {
		t.Errorf("template and target must come last: %v", args)
	}
	if slices.Contains(copierArgs("./x", "X", "x", "", nil, true), "--defaults") {
		t.Error("interactive runs must not pass --defaults")
	}
}
