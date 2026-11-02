package nucleus

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// flatHashReference is an independent, deliberately naive implementation of
// sha-256-flat used to cross-check FlatHash. Verified against a live server:
// a 6,343,517-byte file with hash_bsize 1048576 hashed to the server's value
// with this scheme and to something else with plain sha256.
func flatHashReference(data []byte, bsize int) string {
	var cat []byte
	for len(data) > bsize {
		h := sha256.Sum256(data[:bsize])
		cat = append(cat, h[:]...)
		data = data[bsize:]
	}
	h := sha256.Sum256(data)
	cat = append(cat, h[:]...)
	out := sha256.Sum256(cat)
	return hex.EncodeToString(out[:])
}

func TestFlatHash(t *testing.T) {
	data := []byte("hello world")
	if got, want := FlatHash(data, 4), flatHashReference(data, 4); got != want {
		t.Errorf("FlatHash(bsize=4) = %s, want %s", got, want)
	}
	if got, want := FlatHash(data, 11), flatHashReference(data, 11); got != want {
		t.Errorf("FlatHash(exact block) = %s, want %s", got, want)
	}
	// A single block is sha256(sha256(data)).
	inner := sha256.Sum256(data)
	outer := sha256.Sum256(inner[:])
	if got, want := FlatHash(data, 1<<20), hex.EncodeToString(outer[:]); got != want {
		t.Errorf("FlatHash(single block) = %s, want sha256(sha256(data)) %s", got, want)
	}
	plain := sha256.Sum256(data)
	if FlatHash(data, 1<<20) == hex.EncodeToString(plain[:]) {
		t.Error("FlatHash must not equal plain sha256")
	}
}

func TestExpectedContentVerify(t *testing.T) {
	data := []byte("hello world")
	digest := FlatHash(data, 4)

	tests := []struct {
		name    string
		want    expectedContent
		data    []byte
		wantErr string
	}{
		{"size and hash match", expectedContent{Size: 11, HashType: "sha-256-flat", HashValue: digest, BlockSize: 4}, data, ""},
		{"uppercase hash matches", expectedContent{Size: 11, HashType: "sha-256-flat", HashValue: strings.ToUpper(digest), BlockSize: 4}, data, ""},
		{"size unknown, hash matches", expectedContent{Size: -1, HashType: "sha-256-flat", HashValue: digest, BlockSize: 4}, data, ""},
		{"nothing to verify", expectedContent{Size: -1}, data, ""},
		{"unknown hash type is ignored", expectedContent{Size: 11, HashType: "sha-256-blocked", HashValue: "nope", BlockSize: 4}, data, ""},
		{"unknown block size skips hash", expectedContent{Size: 11, HashType: "sha-256-flat", HashValue: "nope"}, data, ""},
		{"size mismatch", expectedContent{Size: 10}, data, "size mismatch: got 11 bytes, server reported 10"},
		{"wrong block size mismatches", expectedContent{Size: 11, HashType: "sha-256-flat", HashValue: digest, BlockSize: 8}, data, "sha-256-flat mismatch"},
		{"hash mismatch", expectedContent{Size: 11, HashType: "sha-256-flat", HashValue: strings.Repeat("0", 64), BlockSize: 4}, data, "sha-256-flat mismatch"},
		{"empty file", expectedContent{Size: 0, HashType: "sha-256-flat", HashValue: "whatever", BlockSize: 4}, []byte{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.want.verify(tt.data)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("verify() = %v, want nil", err)
			case tt.wantErr != "" && err == nil:
				t.Errorf("verify() = nil, want error containing %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("verify() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestErrorPageSummary(t *testing.T) {
	page := "Error 503: Service Unavailable\nThe server is temporarily overloading or down\n\nHost: nucleus\n"
	if got := errorPageSummary([]byte(page)); got != "Error 503: Service Unavailable" {
		t.Errorf("errorPageSummary = %q", got)
	}
	if got := errorPageSummary([]byte("\n  \n")); got != "" {
		t.Errorf("errorPageSummary(blank) = %q, want empty", got)
	}
	long := strings.Repeat("x", 200)
	if got := errorPageSummary([]byte(long)); len([]rune(got)) != 121 || !strings.HasSuffix(got, "…") {
		t.Errorf("errorPageSummary(long) = %q, want truncated to 120 chars + ellipsis", got)
	}
}

func TestIsTextContent(t *testing.T) {
	if !isTextContent("text/plain; charset=utf-8") || !isTextContent("text/html") {
		t.Error("text/* should be text content")
	}
	if isTextContent("application/octet-stream") || isTextContent("") {
		t.Error("binary / unknown content type must not be text")
	}
}

// The streaming hasher must agree with the one-shot reference regardless of
// how the input is chunked relative to block boundaries.
func TestFlatHasherStreaming(t *testing.T) {
	data := make([]byte, 10_007)
	for i := range data {
		data[i] = byte(i*31 + 7)
	}
	for _, bsize := range []int{1, 4, 1000, 1024, 10_007, 1 << 20} {
		want := flatHashReference(data, bsize)
		for _, chunk := range []int{1, 3, 999, 1024, 4096, len(data)} {
			h := newFlatHasher(bsize)
			for i := 0; i < len(data); i += chunk {
				h.Write(data[i:min(i+chunk, len(data))])
			}
			if got := h.Sum(); got != want {
				t.Errorf("bsize=%d chunk=%d: got %s, want %s", bsize, chunk, got, want)
			}
		}
	}
	if got, want := newFlatHasher(4).Sum(), hex.EncodeToString(func() []byte { s := sha256.Sum256(nil); return s[:] }()); got != want {
		t.Errorf("empty input: got %s, want sha256(\"\") %s", got, want)
	}
}
