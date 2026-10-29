package utils

import (
	"testing"
)

func TestVerifyProjectName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "simple valid name",
			input:   "valid-name",
			wantErr: false,
		},
		{
			name:    "simple invalid name",
			input:   "invalid name",
			wantErr: true,
		},
		{
			name:    "no special characters",
			input:   "invalid!name",
			wantErr: true,
		},
		{
			name:    "do not begin with -",
			input:   "-invalid-name",
			wantErr: true,
		},
		// Add more test cases as needed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyProjectName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("VerifyProjectName() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConvertToValidSubdomainName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Valid Name", "valid-app-name", "valid-app-name"},
		{"Another Valid Name", "valid.app-name", "valid.app-name"},
		{"Invalid Characters", "invalid__app_name!", "invalid--app-name"},
		{"Starts With Non-Alphanumeric", "-startwithdash", "startwithdash"},
		{"Ends With Non-Alphanumeric", "endswithdash-", "endswithdash"},
		{"Only Invalid Characters", "!!!", ""},
		{"Empty String", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := ConvertToValidSubdomainName(tc.input)

			if err != nil && tc.expected != "" {
				t.Errorf("getValidAppName(%q) returned an error: %v", tc.input, err)
			}

			if actual != tc.expected {
				t.Errorf("getValidAppName(%q) = %q, expected %q", tc.input, actual, tc.expected)
			}
		})
	}
}
