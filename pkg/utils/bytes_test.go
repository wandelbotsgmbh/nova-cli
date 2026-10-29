package utils

import "testing"

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{6451, "6.3 KiB"},
		{1048576, "1.0 MiB"},
		{358612992, "342.0 MiB"},
	}
	for _, tc := range tests {
		if got := HumanBytes(tc.n); got != tc.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestDigestHex(t *testing.T) {
	if got := DigestHex("sha256:abc123"); got != "abc123" {
		t.Errorf("DigestHex(prefixed) = %q, want abc123", got)
	}
	if got := DigestHex("abc123"); got != "abc123" {
		t.Errorf("DigestHex(bare) = %q, want abc123", got)
	}
}
