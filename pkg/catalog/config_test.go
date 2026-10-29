package catalog

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{
			name:    "valid simple name",
			input:   "test",
			wantErr: nil,
		},
		{
			name:    "valid name with numbers",
			input:   "test123",
			wantErr: nil,
		},
		{
			name:    "valid name with underscore",
			input:   "test_name",
			wantErr: nil,
		},
		{
			name:    "valid name with hyphen",
			input:   "test-name",
			wantErr: nil,
		},
		{
			name:    "valid name with mixed characters",
			input:   "testName123_with-hyphens",
			wantErr: nil,
		},
		{
			name:    "valid name with max length",
			input:   "a" + strings.Repeat("b", 99), // 100 characters total
			wantErr: nil,
		},
		{
			name:    "empty name",
			input:   "",
			wantErr: ErrNameEmpty,
		},
		{
			name:    "name starting with number",
			input:   "1test",
			wantErr: ErrNameInvalidChars,
		},
		{
			name:    "name with disallowed special characters",
			input:   "test@name",
			wantErr: ErrNameInvalidChars,
		},
		{
			name:    "name too long",
			input:   "a" + strings.Repeat("b", 100), // 101 characters total
			wantErr: ErrNameLenght,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateName(tt.input)
			if tt.wantErr != nil && err != tt.wantErr {
				t.Errorf("ValidateName() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && err != nil {
				t.Errorf("ValidateName() error = %v, wantErr nil", err)
			}
		})
	}
}

func TestValidateUrl(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr error
	}{
		{
			name:    "valid http URL",
			url:     "http://example.com",
			wantErr: nil,
		},
		{
			name:    "valid https URL",
			url:     "https://example.com",
			wantErr: nil,
		},
		{
			name:    "valid URL with path",
			url:     "https://example.com/path",
			wantErr: nil,
		},
		{
			name:    "valid URL with query parameters",
			url:     "https://example.com/path?query=value",
			wantErr: nil,
		},
		{
			name:    "valid URL with port",
			url:     "https://example.com:8080",
			wantErr: nil,
		},
		{
			name:    "invalid scheme",
			url:     "ftp://example.com",
			wantErr: ErrUrlSchemeInvalid,
		},
		{
			name:    "missing scheme",
			url:     "example.com",
			wantErr: ErrUrlSchemeInvalid,
		},
		{
			name:    "invalid URL format",
			url:     "http://",
			wantErr: ErrUrlHostMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUrl(tt.url)
			if (err != nil) != (tt.wantErr != nil) {
				t.Errorf("ValidateUrl() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr != nil && err != tt.wantErr {
				t.Errorf("ValidateUrl() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
