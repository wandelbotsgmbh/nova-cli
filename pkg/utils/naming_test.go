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

func TestTrimGithubArchiveName(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"wandelbotsgmbh-repo-1234567", "repo"},
		{"wandelbotsgmbh-repo-with-1234567", "repo-with"},
		{"user-project-abcdefg", "project"},
		{"organization-repo-9876543", "repo"},
		{"example-repo-1234", "repo"},
		// unexpected input thus we return the same string
		{"repo-1234", "repo-1234"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := TrimGithubArchiveName(tc.input)
			if result != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}
