package novaapi

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMatch(t *testing.T) {
	s, err := ParseSpec([]byte(matchSpec), "")
	if err != nil {
		t.Fatal(err)
	}
	ops := s.Operations()
	tests := []struct {
		method, path, id string
		params           map[string]string
	}{
		{"GET", "/api/v2/cells", "listCells", map[string]string{}},
		{"get", "/api/v2/cells/my%20cell?x=1", "getCell", map[string]string{"cell": "my cell"}},
		{"PUT", "/api/v2/cells/cell/controllers/ur5/ios/values", "setOutputValues", map[string]string{"cell": "cell", "controller": "ur5"}},
		{"GET", "/api/v2/cells/cell/store/objects/a", "getObject", map[string]string{"cell": "cell", "key": "a"}},
		{"POST", "/api/v2/cells", "deployCell", map[string]string{}},
	}
	for _, tt := range tests {
		op, params := Match(ops, tt.method, tt.path)
		if op == nil || op.ID != tt.id {
			t.Errorf("%s %s: got %v, want %s", tt.method, tt.path, op, tt.id)
			continue
		}
		if !reflect.DeepEqual(params, tt.params) {
			t.Errorf("%s %s: params %v, want %v", tt.method, tt.path, params, tt.params)
		}
	}
	for _, p := range []string{"/api/v2/cells/cell/nothing", "/cell/forest/trees", "/api/v2/cells//controllers"} {
		if op, _ := Match(ops, "GET", p); op != nil {
			t.Errorf("GET %s matched %s", p, op.ID)
		}
	}
}

func TestBuildBody(t *testing.T) {
	read := func(name string) ([]byte, error) { return []byte("content of " + name), nil }
	var fields []Field
	for _, s := range []string{"name=a", "n=3", "x=1.5", "ok=true", "none=null", "hex=0x10", "nan=NaN", "big=12345678901234567890", "exp=1e3", "dec=1.50", "lead=01", "plus=+1", "file=@f.txt", "pose[position][]=1", "pose[position][]=2", "items[][a]=1", "items[][b]=2", "items[][a]=3"} {
		f, err := ParseTypedField(s, read)
		if err != nil {
			t.Fatal(err)
		}
		fields = append(fields, f)
	}
	raw, _ := ParseRawField("raw=true")
	fields = append(fields, raw)
	body, err := BuildBody(fields)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(body)
	want := `{"big":12345678901234567890,"dec":1.50,"exp":1e3,"file":"content of f.txt","hex":"0x10","items":[{"a":1,"b":2},{"a":3}],"lead":"01","n":3,"name":"a","nan":"NaN","none":null,"ok":true,"plus":"+1","pose":{"position":[1,2]},"raw":"true","x":1.5}`
	if string(got) != want {
		t.Errorf("body\n got %s\nwant %s", got, want)
	}

	for _, bad := range [][]string{{"a=1", "a[b]=2"}, {"a=1", "a[]=2"}} {
		var fs []Field
		for _, s := range bad {
			f, _ := ParseRawField(s)
			fs = append(fs, f)
		}
		if _, err := BuildBody(fs); err == nil {
			t.Errorf("%v: want an error", bad)
		}
	}
	if _, err := ParseRawField("novalue"); err == nil {
		t.Error("field without = must fail")
	}
	if q := AddQuery("/p?x=1", []Field{{"a[]", json.Number("1")}, {"a[]", json.Number("2")}, {"s", "x y"}, {"f", json.Number("0.00001")}}); q != "/p?x=1&a=1&a=2&f=0.00001&s=x+y" {
		t.Errorf("query %s", q)
	}
}

const matchSpec = `
openapi: 3.1.0
info: {version: 2.8.0}
paths:
  /cells: {get: {operationId: listCells}, post: {operationId: deployCell}}
  /cells/{cell}: {get: {operationId: getCell}}
  /cells/{cell}/controllers/{controller}/ios/values: {put: {operationId: setOutputValues}}
  /cells/{cell}/store/objects/{key}: {get: {operationId: getObject}}
  /cells/{cell}/controllers/{controller}/ios/wait-for: {post: {operationId: waitForIOEvent}}
`

const testSpec = `
openapi: 3.1.0
info: {title: NOVA, version: 2.8.0}
servers: [{url: /api/v2}]
paths:
  /cells/{cell}/controllers/{controller}:
    parameters:
      - $ref: '#/components/parameters/Cell'
    get:
      operationId: getRobotController
      summary: Robot Controller
      tags: [Controller]
      description: Get the configuration for a robot controller.
      parameters:
        - {name: controller, in: path, required: true, schema: {type: string}}
      responses:
        200:
          description: ok
          content:
            application/json:
              schema: {$ref: '#/components/schemas/RobotController'}
  /cells/{cell}/controllers/{controller}/state-stream:
    get:
      operationId: streamRobotControllerState
      summary: Stream State
      x-serverstream: true
      responses: {'200': {description: ok}}
  /cells/{cell}/kinematic/inverse:
    post:
      operationId: inverseKinematics
      summary: Inverse kinematics
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Node'}
      responses: {'200': {description: ok}}
components:
  parameters:
    Cell: {name: cell, in: path, required: true, schema: {type: string, default: cell}}
  schemas:
    RobotController:
      type: object
      x-go-type-name: RC
      properties: {name: {type: string}}
    Node:
      type: object
      properties:
        children: {type: array, items: {$ref: '#/components/schemas/Node'}}
`

func TestSpecSearchAndSchema(t *testing.T) {
	s, err := ParseSpec([]byte(testSpec), "26.8.0")
	if err != nil {
		t.Fatal(err)
	}
	if s.APIVersion() != "2.8.0" || len(s.Operations()) != 3 {
		t.Fatalf("version %s, %d operations", s.APIVersion(), len(s.Operations()))
	}
	if op, _ := Match(s.Operations(), "GET", "/api/v2/cells/c/controllers/x/state-stream"); !IsStream(op) {
		t.Fatalf("stream not recognized: %+v", op)
	}

	hits := s.Search("robot controllers", 0)
	if len(hits) == 0 || hits[0].ID != "getRobotController" {
		t.Fatalf("search: %+v", hits)
	}
	if op, _ := Match(s.Operations(), "GET", "/api/v2/cells/c/controllers/x"); IsStream(op) {
		t.Error("plain GET taken for a stream")
	}
	if hits := s.Search("zzz", 0); hits == nil || len(hits) != 0 {
		t.Errorf("no match must be an empty list, got %#v", hits)
	}

	op, ok := s.Find("GETROBOTCONTROLLER")
	if !ok {
		t.Fatal("Find is case-insensitive")
	}
	sch := s.Schema(*op)
	if len(sch.Parameters) != 2 || sch.Parameters[0].Name != "cell" || sch.Parameters[1].Name != "controller" {
		t.Errorf("parameters %+v", sch.Parameters)
	}
	if r := sch.Responses["200"]; r.Schema != "RobotController" || r.ContentType != "application/json" {
		t.Errorf("responses %+v", sch.Responses)
	}

	inv, _ := s.Find("inverseKinematics")
	b, _ := json.Marshal(s.Schema(*inv).RequestBody)
	if !strings.Contains(string(b), `"recursive":true`) || !strings.Contains(string(b), `"children"`) {
		t.Errorf("recursive schema not cut: %s", b)
	}
	stream, _ := s.Find("streamRobotControllerState")
	if s.Schema(*stream).Refused == "" {
		t.Error("schema of a stream must say it is refused")
	}
}

// A long poll is a plain HTTP request: it is sent, only WebSockets are refused.
func TestLongPollIsNoStream(t *testing.T) {
	s, err := ParseSpec([]byte(matchSpec), "")
	if err != nil {
		t.Fatal(err)
	}
	if op, _ := Match(s.Operations(), "POST", "/api/v2/cells/c/controllers/x/ios/wait-for"); op == nil || IsStream(op) {
		t.Errorf("waitForIOEvent: %+v", op)
	}
}

// PeekStart must not wait for more than the first bytes of a slow body.
func TestPeekStartDoesNotBlock(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	go func() { _, _ = w.Write([]byte("abc")) }()
	done := make(chan []byte)
	go func() { done <- PeekStart(NewBodyReader(r), http.Header{"Content-Type": {"application/json"}}) }()
	select {
	case head := <-done:
		if string(head) != "abc" {
			t.Errorf("head %q", head)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PeekStart waits for more than the available bytes")
	}
}
