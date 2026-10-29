package sows

import (
	"encoding/json"
	"testing"
)

func TestBuildRequestRoundTrip(t *testing.T) {
	params := map[string]any{
		"version":   1,
		"api_token": "test-token",
	}
	data, err := BuildRequest(42, "Tokens", "auth_with_api_token", params)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	// Verify header
	if data[0] != TypeRequest {
		t.Errorf("type = %d, want %d", data[0], TypeRequest)
	}

	// Request ID = 42 in LE
	if data[1] != 42 || data[2] != 0 || data[3] != 0 || data[4] != 0 {
		t.Errorf("requestID bytes = %v, want [42 0 0 0]", data[1:5])
	}

	// Method string should be null-terminated
	methodEnd := -1
	for i := 5; i < len(data); i++ {
		if data[i] == 0 {
			methodEnd = i
			break
		}
	}
	if methodEnd < 0 {
		t.Fatal("no null terminator found for method")
	}
	method := string(data[5:methodEnd])
	if method != "Tokens.auth_with_api_token" {
		t.Errorf("method = %q, want %q", method, "Tokens.auth_with_api_token")
	}
}

func TestBuildResponseRoundTrip(t *testing.T) {
	params := map[string]string{
		"status":       "OK",
		"access_token": "tok-123",
	}
	data, err := BuildResponse(7, true, params)
	if err != nil {
		t.Fatalf("BuildResponse: %v", err)
	}

	resp, err := ParseResponse(data)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if resp.Type != TypeResponse {
		t.Errorf("type = %d, want %d", resp.Type, TypeResponse)
	}
	if resp.RequestID != 7 {
		t.Errorf("requestID = %d, want 7", resp.RequestID)
	}
	if !resp.Last {
		t.Error("expected last = true")
	}

	var result map[string]string
	if err := json.Unmarshal(resp.ParamsJSON, &result); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if result["status"] != "OK" {
		t.Errorf("status = %q, want OK", result["status"])
	}
	if result["access_token"] != "tok-123" {
		t.Errorf("access_token = %q, want tok-123", result["access_token"])
	}
}

func TestParseResponseError(t *testing.T) {
	// Build an error response manually: type(0) + requestID(4) + code(2) + message
	data := []byte{
		TypeError,
		1, 0, 0, 0, // requestID = 1
		0x90, 0x01, // errorCode = 400 (LE)
	}
	data = append(data, []byte("bad request")...)

	resp, err := ParseResponse(data)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if resp.Type != TypeError {
		t.Errorf("type = %d, want %d", resp.Type, TypeError)
	}
	if resp.ErrorCode != 400 {
		t.Errorf("errorCode = %d, want 400", resp.ErrorCode)
	}
	if resp.ErrorMessage != "bad request" {
		t.Errorf("errorMessage = %q, want %q", resp.ErrorMessage, "bad request")
	}
}

func TestParseResponseDone(t *testing.T) {
	data := []byte{TypeDone, 5, 0, 0, 0}
	resp, err := ParseResponse(data)
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if resp.Type != TypeDone {
		t.Errorf("type = %d, want %d", resp.Type, TypeDone)
	}
	if resp.RequestID != 5 {
		t.Errorf("requestID = %d, want 5", resp.RequestID)
	}
}

func TestParseResponseTooShort(t *testing.T) {
	_, err := ParseResponse([]byte{1, 2})
	if err == nil {
		t.Fatal("expected error for short data")
	}
}

func TestParseResponseParamsLenOverflow(t *testing.T) {
	// paramsLen = 0xFFFFFFFF would wrap 10+paramsLen to 9 in uint32 arithmetic.
	data := []byte{TypeResponse, 1, 0, 0, 0, 1, 0xFF, 0xFF, 0xFF, 0xFF, '{', '}'}
	if _, err := ParseResponse(data); err == nil {
		t.Fatal("expected error for oversized paramsLen")
	}
}
