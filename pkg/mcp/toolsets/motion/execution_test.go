package motion

import (
	"context"
	"strings"
	"testing"
)

// stop_execution can arrive while execute_trajectory is still connecting: the stop is
// recorded without a connection, and the start sequence sends nothing afterwards.
func TestExecution_StopBeforeTheConnectionIsUp(t *testing.T) {
	exec := &execution{trajectory: "t"}
	if err := exec.pause(context.Background()); err != nil {
		t.Fatalf("pause without a connection: %v", err)
	}
	if !exec.wasStopped() {
		t.Fatal("the stop must be recorded")
	}
	if apiErr := start(context.Background(), context.Background(), exec, "execute_trajectory", "t", 100); apiErr == nil || !strings.Contains(apiErr.Message, "stopped") {
		t.Fatalf("start must not move a stopped execution, got %v", apiErr)
	}
}

// After use_instance the stop call is pinned to another instance than the movement:
// it must still find the execution on that cell and controller.
func TestRunning_FindsExecutionOfAnotherInstance(t *testing.T) {
	exec := &execution{trajectory: "t"}
	if !register("https://a/api/v2#cell", "sim", exec) {
		t.Fatal("register failed")
	}
	t.Cleanup(func() { unregister("https://a/api/v2#cell", "sim", exec) })
	if got := running("https://b/api/v2#cell", "cell", "sim"); len(got) != 1 || got[0] != exec {
		t.Errorf("expected the execution started on instance a, got %v", got)
	}
	if got := running("https://b/api/v2#cell", "cell", "other"); len(got) != 0 {
		t.Errorf("another controller must not match, got %v", got)
	}
	// the same cell and controller name on a second instance: a stop from a third
	// instance reaches both
	other := &execution{trajectory: "t2"}
	register("https://c/api/v2#cell", "sim", other)
	t.Cleanup(func() { unregister("https://c/api/v2#cell", "sim", other) })
	if got := running("https://b/api/v2#cell", "cell", "sim"); len(got) != 2 {
		t.Errorf("expected both executions, got %v", got)
	}
	if got := running("https://c/api/v2#cell", "cell", "sim"); len(got) != 1 || got[0] != other {
		t.Errorf("the execution of the current instance comes first and alone, got %v", got)
	}
}
