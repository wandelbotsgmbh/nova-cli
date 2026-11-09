package novaapi

import "bufio"

// IsStream reports whether op is a WebSocket endpoint (x-serverstream,
// x-bidistream in the spec). `nova api` speaks plain HTTP and refuses them;
// streams get dedicated commands (ADR-0013).
func IsStream(op *Operation) bool {
	return op != nil && op.Stream
}

// PeekStart returns the start of the body that is already there, without
// consuming it: it waits for the first bytes only, so a body that arrives slowly
// (long poll, chunked answer) is not held back.
func PeekStart(br *bufio.Reader) []byte {
	if _, err := br.Peek(1); err != nil {
		return nil
	}
	n := br.Buffered()
	if n > 512 {
		n = 512
	}
	head, _ := br.Peek(n)
	return head
}
