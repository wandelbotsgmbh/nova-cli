package utils

import "strings"

// DigestHex returns the bare hex of a content digest, stripping the algorithm
// prefix (e.g. "sha256:abc123" -> "abc123"). A value without a prefix is
// returned unchanged.
func DigestHex(digest string) string {
	if _, hex, ok := strings.Cut(digest, ":"); ok {
		return hex
	}
	return digest
}
