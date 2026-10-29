package cmd

import "testing"

func TestSplitEntryName(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantCatalog   string
		wantEntryName string
	}{
		{
			name:          "only entry name",
			input:         "myapp",
			wantCatalog:   "",
			wantEntryName: "myapp",
		},
		{
			name:          "catalog and entry name",
			input:         "mycatalog/myapp",
			wantCatalog:   "mycatalog",
			wantEntryName: "myapp",
		},
		{
			name:          "empty string",
			input:         "",
			wantCatalog:   "",
			wantEntryName: "",
		},
		{
			name:          "multiple slashes",
			input:         "mycatalog/namespace/myapp",
			wantCatalog:   "mycatalog",
			wantEntryName: "namespace/myapp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCatalog, gotEntryName := splitEntryName(tt.input)

			if gotCatalog != tt.wantCatalog {
				t.Errorf("splitEntryName() catalog = %v, want %v", gotCatalog, tt.wantCatalog)
			}

			if gotEntryName != tt.wantEntryName {
				t.Errorf("splitEntryName() entryName = %v, want %v", gotEntryName, tt.wantEntryName)
			}
		})
	}
}
