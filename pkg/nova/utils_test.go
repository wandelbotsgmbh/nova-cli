package nova

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestHostWithProtocol(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"127.0.0.1", "http://127.0.0.1"},
		{"localhost", "http://localhost"},
		{"some.random.domain", "http://some.random.domain"},
		{"http://wab.wandelbots.io", "http://wab.wandelbots.io"},
		{"wab.wandelbots.io", "https://wab.wandelbots.io"},
		{"https://google.com", "https://google.com"},
	}

	for _, tc := range testCases {
		result := HostWithProtocol(tc.input)
		if result != tc.expected {
			t.Errorf("expected %s but got %s", tc.expected, result)
		}
	}
}

func TestConvertJsonToYamlString(t *testing.T) {
	tests := []struct {
		name     string
		jsonStr  string
		expected string
		wantErr  bool
	}{
		{
			name: "simple JSON",
			jsonStr: `{
                "name": "John",
                "age": 30,
                "city": "New York"
            }`,
			expected: "name: John\nage: 30\ncity: New York\n",
			wantErr:  false,
		},
		{
			name: "nested JSON",
			jsonStr: `{
                "person": {
                    "name": "John",
                    "age": 30,
                    "city": "New York"
                }
            }`,
			expected: "person:\n    name: John\n    age: 30\n    city: New York\n",
			wantErr:  false,
		},
		{
			name:     "invalid JSON",
			jsonStr:  `{"name": "John", "age": 30, "city": "New York"`,
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConvertJsonToYamlString([]byte(tt.jsonStr))
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertJsonToYamlString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// TODO how to compare these fucking values
			var gotMap, expectMap map[string]interface{}
			err = yaml.Unmarshal(got, &gotMap)
			if err != nil {
				t.Errorf("ConvertJsonToYamlString() = %v", err)
			}

			err = yaml.Unmarshal([]byte(tt.expected), &expectMap)
			if err != nil {
				t.Errorf("ConvertJsonToYamlString() = %v", err)
				return
			}

			if !reflect.DeepEqual(gotMap, expectMap) {
				t.Errorf("ConvertJsonToYamlString() =\n%v\n\nwant\n%v", string(got), tt.expected)
			}
		})
	}
}

func TestConvertYamlToJsonString(t *testing.T) {
	tests := []struct {
		name     string
		yamlStr  string
		expected string
		wantErr  bool
	}{
		{
			name: "simple YAML",
			yamlStr: `name: John
age: 30
city: New York
`,
			expected: `{"name":"John","age":30,"city":"New York"}`,
			wantErr:  false,
		},
		{
			name: "nested YAML",
			yamlStr: `person:
  name: John
  age: 30
  city: New York
`,
			expected: `{"person":{"name":"John","age":30,"city":"New York"}}`,
			wantErr:  false,
		},
		{
			name:     "invalid YAML",
			yamlStr:  `name: John\nage: 30\ncity: New York`,
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConvertYamlToJsonString([]byte(tt.yamlStr))
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertYamlToJsonString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				var gotMap, expectMap map[string]interface{}
				if err := yaml.Unmarshal(got, &gotMap); err != nil {
					t.Errorf("Failed to unmarshal got JSON: %v", err)
					return
				}
				if err := yaml.Unmarshal([]byte(tt.expected), &expectMap); err != nil {
					t.Errorf("Failed to unmarshal expected JSON: %v", err)
					return
				}

				if !reflect.DeepEqual(gotMap, expectMap) {
					t.Errorf("ConvertYamlToJsonString() = %v, want %v", string(got), tt.expected)
				}
			}
		})
	}
}

func TestIsCurrentVersionLessThan(t *testing.T) {
	serverVersion := "25.1.0"

	// mock both /api/v1 and /api/v2 endpoints
	// Wrapper functions try V2 first, then fall back to V1
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/system/version" {
			// V2 endpoint - return success
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(serverVersion))
			return
		}

		if r.URL.Path == "/api/v1/system/version" {
			// V1 endpoint - return success (for fallback)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(serverVersion))
			return
		}

		t.Errorf("Expected to request '/api/v1/system/version' or '/api/v2/system/version', got: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	// same version
	res, err := IsCurrentVersionLessThan(ts.URL, serverVersion)
	if err != nil {
		t.Errorf("IsCurrentVersionLessThan() error = %v", err)
	}

	if res {
		t.Errorf("IsCurrentVersionLessThan() = %v, want %v", res, false)
	}

	// lower version
	res, err = IsCurrentVersionLessThan(ts.URL, "24.10.0")
	if err != nil {
		t.Errorf("IsCurrentVersionLessThan() error = %v", err)
	}

	if res {
		t.Errorf("IsCurrentVersionLessThan() = %v, want %v", res, false)
	}

	// higher version
	res, err = IsCurrentVersionLessThan(ts.URL, "25.2.0")
	if err != nil {
		t.Errorf("IsCurrentVersionLessThan() error = %v", err)
	}

	if !res {
		t.Errorf("IsCurrentVersionLessThan() = %v, want %v", res, true)
	}

	serverVersion = "25.1.0-rc.1"

	// patch version
	res, err = IsCurrentVersionLessThan(ts.URL, "25.1.0-rc.1")
	if err != nil {
		t.Errorf("IsCurrentVersionLessThan() error = %v", err)
	}

	if res {
		t.Errorf("IsCurrentVersionLessThan() = %v, want %v", res, false)
	}

	res, err = IsCurrentVersionLessThan(ts.URL, "25.1.0-rc.2")
	if err != nil {
		t.Errorf("IsCurrentVersionLessThan() error = %v", err)
	}

	if !res {
		t.Errorf("IsCurrentVersionLessThan() = %v, want %v", res, true)
	}

}

func TestIsCurrentVersionEqual(t *testing.T) {
	serverVersion := "25.1.0"

	// Create a test server that supports both V1 and V2 endpoints
	// Wrapper functions try V2 first, then fall back to V1
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/system/version" {
			// V2 endpoint - return success
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(serverVersion))
			return
		}

		if r.URL.Path == "/api/v1/system/version" {
			// V1 endpoint - return success (for fallback)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(serverVersion))
			return
		}

		t.Errorf("Expected to request '/api/v1/system/version' or '/api/v2/system/version', got: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	// Test equal versions
	res, err := IsCurrentVersionEqual(ts.URL, "25.1.0")
	if err != nil {
		t.Errorf("IsCurrentVersionEqual() error = %v", err)
	}

	if !res {
		t.Errorf("IsCurrentVersionEqual() = %v, want %v", res, true)
	}

	// Test different versions - current is lower
	res, err = IsCurrentVersionEqual(ts.URL, "25.2.0")
	if err != nil {
		t.Errorf("IsCurrentVersionEqual() error = %v", err)
	}

	if res {
		t.Errorf("IsCurrentVersionEqual() = %v, want %v", res, false)
	}

	// Test different versions - current is higher
	res, err = IsCurrentVersionEqual(ts.URL, "25.0.0")
	if err != nil {
		t.Errorf("IsCurrentVersionEqual() error = %v", err)
	}

	if res {
		t.Errorf("IsCurrentVersionEqual() = %v, want %v", res, false)
	}

	serverVersion = "25.1.0-rc.1"

	// Test with patch version
	res, err = IsCurrentVersionEqual(ts.URL, "25.1.0-rc.1")
	if err != nil {
		t.Errorf("IsCurrentVersionEqual() error = %v", err)
	}

	if !res {
		t.Errorf("IsCurrentVersionEqual() = %v, want %v", res, true)
	}

	res, err = IsCurrentVersionEqual(ts.URL, "25.1.0-rc.2")
	if err != nil {
		t.Errorf("IsCurrentVersionEqual() error = %v", err)
	}

	if res {
		t.Errorf("IsCurrentVersionEqual() = %v, want %v", res, false)
	}

}

func TestWaitForAllRunningRecoversAfterNonRunningStatus(t *testing.T) {
	statusRequestCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/system/version", "/api/v2/system/version":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("\"25.1.0\""))
			return
		case "/api/v1/system/status", "/api/v2/system/status":
			// expected
		default:
			t.Errorf("Expected to request version or status endpoint, got: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		statusRequestCount++
		phase := "Running"
		if statusRequestCount == 1 {
			phase = "Pending"
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{
				"service": "service-manager",
				"status": {
					"code": "` + phase + `",
					"severity": "Info"
				}
			}
		]`))
	}))
	defer ts.Close()

	err := WaitForAllRunning(ts.URL, time.Now(), 200*time.Millisecond, time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForAllRunning() error = %v", err)
	}

	if statusRequestCount < 2 {
		t.Fatalf("WaitForAllRunning() made %d status request(s), want at least 2", statusRequestCount)
	}
}
