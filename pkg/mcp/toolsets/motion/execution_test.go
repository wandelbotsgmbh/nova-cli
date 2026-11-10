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
	if apiErr := start(context.Background(), exec, "execute_trajectory", "t", 100); apiErr == nil || !strings.Contains(apiErr.Message, "stopped") {
		t.Fatalf("start must not move a stopped execution, got %v", apiErr)
	}
}
