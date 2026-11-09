package virtualcontrollers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/internal/livetest"
	"github.com/wandelbotsgmbh/nova-cli/pkg/mcp/novaapi"
)

const liveController = "mcp-test-vc"

// TestLiveVirtualController runs every tool against a virtual controller it creates and
// deletes again.
func TestLiveVirtualController(t *testing.T) {
	c := livetest.Client(t)
	ctx := context.Background()
	cell := livetest.Cell()
	controllers := "/cells/" + cell + "/controllers"

	if _, err := c.JSON(ctx, novaapi.Call{Tool: "setup", Method: http.MethodPost, Path: controllers, Body: map[string]any{
		"name": liveController, "configuration": map[string]any{"kind": "VirtualController", "manufacturer": "universalrobots", "type": "universalrobots-ur5e"},
	}}, nil); err != nil && err.Code != novaapi.CodeAlreadyExists {
		t.Fatalf("create %s: %v", liveController, err)
	}
	t.Cleanup(func() {
		if _, err := c.JSON(ctx, novaapi.Call{Tool: "cleanup", Method: http.MethodDelete, Path: controllers + "/" + liveController}, nil); err != nil {
			t.Errorf("delete %s: %v", liveController, err)
		}
	})
	deadline := time.Now().Add(3 * time.Minute)
	for {
		var groups []any
		if err := c.Get(ctx, "setup", "/cells/"+cell+"/virtual-controllers/"+liveController+"/motion-groups", "", &groups); err == nil && len(groups) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("virtual controller did not come up")
		}
		time.Sleep(3 * time.Second)
	}

	cs := connect(t, c, false)
	a := func(extra map[string]any) map[string]any {
		out := map[string]any{"cell": cell, "controller": liveController}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	ok := func(name string, args map[string]any) string {
		t.Helper()
		// other sessions may redeploy the cell meanwhile, so the controller can be
		// briefly gone
		text, res := call(t, cs, name, args)
		for i := 0; res.IsError && i < 15 && !strings.HasPrefix(text, "INVALID_ARGUMENT") && !strings.HasPrefix(text, "APPROVAL_REQUIRED"); i++ {
			t.Logf("%s → %s (retrying)", name, text)
			time.Sleep(3 * time.Second)
			text, res = call(t, cs, name, args)
		}
		if res.IsError {
			t.Fatalf("%s: %s", name, text)
		}
		t.Logf("%s → %s", name, text)
		return structured(t, res)
	}
	retry := func(name string, args map[string]any, want string) {
		t.Helper()
		for i := 0; ; i++ {
			out := ok(name, args)
			if strings.Contains(out, want) {
				return
			}
			if i == 20 {
				t.Fatalf("%s: %q not in %s", name, want, out)
			}
			time.Sleep(2 * time.Second)
		}
	}

	status := ok("get_virtual_controller_status", a(nil))
	var st struct {
		MotionGroups []struct {
			MotionGroup string `json:"motion_group"`
			DOF         int    `json:"dof"`
		} `json:"motion_groups"`
	}
	_ = json.Unmarshal([]byte(status), &st)
	mg := st.MotionGroups[0].MotionGroup
	m := func(extra map[string]any) map[string]any {
		return a(mergeMap(map[string]any{"motion_group": mg}, extra))
	}

	ok("set_virtual_emergency_stop", a(map[string]any{"active": true}))
	retry("get_virtual_controller_status", a(nil), `"emergency_stop":true`)
	ok("set_virtual_emergency_stop", a(map[string]any{"active": false}))
	retry("get_virtual_controller_status", a(nil), `"emergency_stop":false`)
	ok("set_virtual_operation_mode", a(map[string]any{"mode": "manual_t1"}))
	retry("get_virtual_controller_status", a(nil), `OPERATION_MODE_MANUAL_T1`)
	ok("set_virtual_operation_mode", a(map[string]any{"mode": "auto"}))
	ok("set_virtual_behavior", a(map[string]any{"behavior": "automatic_not_comply_with_cycletime"}))
	retry("get_virtual_controller_status", a(nil), `BEHAVIOR_AUTOMATIC_NOT_COMPLY_WITH_CYCLETIME`)
	ok("set_virtual_behavior", a(map[string]any{"behavior": "automatic"}))

	ok("get_virtual_motion_group", m(nil))
	ok("set_virtual_joint_position", m(map[string]any{"joint_positions": []float64{0.1, -1.2, 1.3, -1.6, -1.5, 0}}))
	retry("get_virtual_motion_group", m(nil), `"joint_positions":[0.1,-1.2,1.3,-1.6,-1.5,0]`)
	ok("add_virtual_tcp", m(map[string]any{"tcp": "mcp-test-tcp", "name": "test", "position": []float64{0, 0, 150}}))
	retry("get_virtual_motion_group", m(nil), `"tcp":"mcp-test-tcp"`)
	ok("delete_virtual_tcp", m(map[string]any{"tcp": "mcp-test-tcp"}))
	mounting := ok("get_virtual_motion_group", m(nil))
	var mo struct {
		Mounting struct {
			CoordinateSystem string `json:"coordinate_system"`
		} `json:"mounting"`
	}
	_ = json.Unmarshal([]byte(mounting), &mo)
	ok("set_virtual_mounting", m(map[string]any{"coordinate_system": mo.Mounting.CoordinateSystem, "position": []float64{0, 0, 100}}))
	retry("get_virtual_motion_group", m(nil), `"position":[0,0,100]`)

	ok("list_virtual_coordinate_systems", a(nil))
	ok("add_virtual_coordinate_system", a(map[string]any{"coordinate_system": "mcp-test-cs", "name": "table", "position": []float64{500, 0, 0}}))
	retry("list_virtual_coordinate_systems", a(nil), `"coordinate_system":"mcp-test-cs"`)
	ok("delete_virtual_coordinate_system", a(map[string]any{"coordinate_system": "mcp-test-cs"}))

	ok("list_virtual_safety_zones", a(nil))
	ok("add_virtual_safety_zone", a(map[string]any{"id": 42, "name": "mcp-test-zone", "shape": "sphere", "center": []float64{400, 0, 300}, "radius": 50.0}))
	retry("list_virtual_safety_zones", a(nil), `"name":"mcp-test-zone"`)
	ok("delete_virtual_safety_zone", a(map[string]any{"id": 42}))

	ios := ok("list_virtual_ios", a(map[string]any{"direction": "output", "search": "digital", "limit": 5}))
	var io struct {
		IOs []struct {
			IO        string `json:"io"`
			ValueType string `json:"value_type"`
		} `json:"ios"`
	}
	_ = json.Unmarshal([]byte(ios), &io)
	for _, entry := range io.IOs {
		if entry.ValueType == "boolean" {
			ok("set_virtual_io_values", a(map[string]any{"values": []map[string]any{{"io": entry.IO, "value": "true"}}}))
			retry("list_virtual_ios", a(map[string]any{"search": entry.IO, "limit": 200}), `"value":true`)
			ok("set_virtual_io_values", a(map[string]any{"values": []map[string]any{{"io": entry.IO, "value": "false"}}}))
			break
		}
	}

	ok("add_virtual_motion_group", a(map[string]any{"motion_group": "1@" + liveController, "motion_group_model": "UniversalRobots_UR3e"}))
	retry("get_virtual_controller_status", a(nil), `"motion_group":"1@`+liveController+`"`)
	ok("delete_virtual_motion_group", a(map[string]any{"motion_group": "1@" + liveController}))
}

func mergeMap(a, b map[string]any) map[string]any {
	for k, v := range b {
		a[k] = v
	}
	return a
}
