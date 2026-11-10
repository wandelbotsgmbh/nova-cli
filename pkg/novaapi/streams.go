package novaapi

import (
	"bufio"
	"io"
	"net/http"

	"github.com/wandelbotsgmbh/nova-cli/pkg/ingress"
)

// IsStream reports whether op is a WebSocket endpoint (x-serverstream,
// x-bidistream in the spec). `nova api` speaks plain HTTP and refuses them;
// streams get dedicated commands (ADR-0013).
func IsStream(op *Operation) bool {
	return op != nil && op.Stream
}

// peekSize is how much of a body can be inspected without consuming it: enough
// for the ingress' JSON error page, which echoes the request uri.
const peekSize = 64 << 10

// NewBodyReader wraps a body so its start can be inspected (PeekStart).
func NewBodyReader(body io.Reader) *bufio.Reader {
	return bufio.NewReaderSize(body, peekSize)
}

// PeekStart returns the start of the body without consuming it. It waits only
// for the first bytes, so a body that arrives slowly (long poll, chunked
// answer) is not held back. A JSON body that could be the ingress' error page
// ({"error": …) is read completely (up to peekSize), so the page can be
// recognized even when it is long or arrives in pieces.
func PeekStart(br *bufio.Reader, h http.Header) []byte {
	if _, err := br.Peek(1); err != nil {
		return nil
	}
	head, _ := br.Peek(min(br.Buffered(), 512))
	if !ingress.MaybeErrorPage(h, head) {
		return head
	}
	for n := br.Buffered(); n < peekSize; n = br.Buffered() {
		if _, err := br.Peek(n + 1); err != nil {
			break // EOF: the whole page is buffered
		}
	}
	head, _ = br.Peek(br.Buffered())
	return head
}
