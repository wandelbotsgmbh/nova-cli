package httpx

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
)

// maxLoggedBody is how much of a request or response body debug output shows.
const maxLoggedBody = 4 << 10

// loggedResponseHeaders are the response headers debug output shows; the rest
// (caching, tracing, ingress details) rarely explains a failure.
var loggedResponseHeaders = []string{"Content-Type", "Content-Length", "Location", "Www-Authenticate", "Retry-After"}

// debugRoundTrip sends req through next and logs request and response. The
// response body is logged once the caller has read or closed it, so streams and
// large downloads are not buffered.
func debugRoundTrip(next http.RoundTripper, req *http.Request) (*http.Response, error) {
	start := time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "http: > %s %s", req.Method, debug.RedactURL(req.URL))
	for _, k := range slices.Sorted(maps.Keys(req.Header)) {
		for _, v := range req.Header[k] {
			fmt.Fprintf(&b, "\nhttp: > %s: %s", k, debug.RedactHeader(k, v))
		}
	}
	if body := requestBody(req); body != "" {
		fmt.Fprintf(&b, "\nhttp: > %s", indent(body, "http: > "))
	}
	debug.Logf("%s", b.String())

	resp, err := next.RoundTrip(req)
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		debug.Logf("http: < %s %s failed after %s: %v", req.Method, debug.RedactURL(req.URL), elapsed, err)
		return resp, err
	}

	b.Reset()
	fmt.Fprintf(&b, "http: < %s (%s %s, %s)", resp.Status, req.Method, debug.RedactURL(req.URL), elapsed)
	for _, k := range loggedResponseHeaders {
		for _, v := range resp.Header.Values(k) {
			fmt.Fprintf(&b, "\nhttp: < %s: %s", k, debug.RedactHeader(k, v))
		}
	}
	debug.Logf("%s", b.String())
	if resp.Body != nil && resp.Body != http.NoBody {
		resp.Body = &loggedBody{ReadCloser: resp.Body, text: isText(resp.Header.Get("Content-Type"))}
	}
	return resp, nil
}

// requestBody returns the redacted start of req's body without consuming it.
// Bodies that can't be replayed (streams) are only announced.
func requestBody(req *http.Request) string {
	if req.Body == nil || req.Body == http.NoBody {
		return ""
	}
	if !isText(req.Header.Get("Content-Type")) {
		return fmt.Sprintf("[%s body, %s]", contentType(req.Header.Get("Content-Type")), sizeOf(req.ContentLength))
	}
	if req.GetBody == nil {
		return "[streamed body, not shown]"
	}
	body, err := req.GetBody()
	if err != nil {
		return "[body not shown: " + err.Error() + "]"
	}
	defer body.Close()
	data, _ := io.ReadAll(io.LimitReader(body, maxLoggedBody+1))
	return formatBody(data, req.ContentLength)
}

// loggedBody logs the start of a response body when it is closed.
type loggedBody struct {
	io.ReadCloser
	text   bool
	buf    bytes.Buffer
	n      int64
	logged bool
}

func (b *loggedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n += int64(n)
	if b.text && b.buf.Len() <= maxLoggedBody {
		b.buf.Write(p[:min(n, maxLoggedBody+1-b.buf.Len())])
	}
	if err == io.EOF {
		b.log(true)
	}
	return n, err
}

func (b *loggedBody) Close() error {
	b.log(false)
	return b.ReadCloser.Close()
}

func (b *loggedBody) log(complete bool) {
	if b.logged {
		return
	}
	b.logged = true
	switch {
	case b.n == 0 && complete:
		debug.Logf("http: < [empty body]")
	case !b.text:
		debug.Logf("http: < [binary body, %s read]", sizeOf(b.n))
	default:
		size := int64(-1)
		if complete {
			size = b.n
		}
		debug.Logf("http: < %s", indent(formatBody(b.buf.Bytes(), size), "http: < "))
	}
}

// formatBody redacts data and marks it as truncated when it is longer than
// maxLoggedBody (size is the full length, -1 if unknown).
func formatBody(data []byte, size int64) string {
	truncated := len(data) > maxLoggedBody
	if truncated {
		data = data[:maxLoggedBody]
	}
	s := debug.Redact(strings.TrimRight(string(data), "\n"))
	if truncated {
		if size > 0 {
			s += fmt.Sprintf("\n[truncated, %s in total]", sizeOf(size))
		} else {
			s += "\n[truncated]"
		}
	}
	return s
}

func indent(s, prefix string) string {
	return strings.ReplaceAll(s, "\n", "\n"+prefix)
}

func isText(ct string) bool {
	t := contentType(ct)
	switch {
	case t == "", strings.HasPrefix(t, "text/"):
		// error pages of ingresses often come without a content type
		return true
	case strings.HasSuffix(t, "json"), strings.HasSuffix(t, "+json"),
		strings.HasSuffix(t, "yaml"), strings.HasSuffix(t, "xml"),
		t == "application/x-www-form-urlencoded", t == "application/javascript":
		return true
	}
	return false
}

func contentType(ct string) string {
	t, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(ct))
	}
	return t
}

func sizeOf(n int64) string {
	if n < 0 {
		return "unknown size"
	}
	return fmt.Sprintf("%d bytes", n)
}
