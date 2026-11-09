package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/debug"
	"github.com/wandelbotsgmbh/nova-cli/pkg/ingress"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/novaapi"
	"golang.org/x/term"
)

var apiFlags struct {
	method    string
	rawFields []string
	fields    []string
	headers   []string
	input     string
	include   bool
	silent    bool
	output    string
	dryRun    bool
	timeout   time.Duration
	limit     int
}

var apiCmd = &cobra.Command{
	Use:   "api <path>",
	Short: "Send an authenticated request to the NOVA API of the instance",
	Long: `Send an authenticated request to the instance and print the answer, like 'gh api'.

The path is the complete path on the instance, starting with '/': the NOVA API v2 lives
under /api/v2, other paths (apps, NovaFlow) work as well. {cell} is replaced by the cell
of the instance; fill in other placeholders yourself. Host and credentials come from the
targeted instance (--instance, NOVA_INSTANCE or 'nova instance select'); credentials are
never sent to another host.

Find operations with 'nova api search <words>' and their parameters and body with
'nova api schema <operationId>' (both read the instance's v2 spec, cached per NOVA
version under ~/.nova/cache/api). Try a request with --dry-run first.

The answer goes to stdout as the instance sends it; with an HTTP error status the body is
still printed and nova exits with 1. Binary answers (archives, models) need --output.

Fields:
  -f key=value     string field
  -F key=value     typed field: true, false, null and numbers become JSON literals,
                   @file (or @- for stdin) reads the value from a file
  Nested keys like gh: pose[position][]=1 or items[][name]=a. Fields make the request a
  POST unless -X is given; with GET (or together with --input) they go to the query.
  --input file     the request body from a file (- for stdin)

The answer is printed as it arrives, so long polls (e.g. waitForIOEvent, with a longer
--timeout or --timeout 0) work. WebSocket streams (state streams, jogging, trajectory
execution) are refused with STREAM_NOT_SUPPORTED (exit 3): nova api speaks plain HTTP.

nova api has no guard rails: it can do what the instance's credentials allow, including
moving robots. Point it at an instance with real robots only when someone is at the cell.`,
	Example: `  nova api /api/v2/cells/{cell}/controllers
  nova api /api/v2/cells/{cell}/controllers/ur5 --include
  nova api /api/v2/cells/{cell}/store/objects/my-key -X PUT --input data.json
  nova api /api/v2/cells/{cell}/controllers/ur5/ios/values -X PUT --input outputs.json --dry-run
  nova api /api/v2/system/diagnosis-package/zip --output diagnosis.zip
  nova api search "inverse kinematics"
  nova api schema inverseKinematics`,
	Args: cobra.ExactArgs(1),
	RunE: runAPI,
}

var apiSearchCmd = &cobra.Command{
	Use:   "search <words>...",
	Short: "Find operations of the instance's NOVA API v2",
	Long: `Find operations of the instance's NOVA API v2 by words in their id, path, summary,
tags and description. Prints a JSON array, best matches first: operationId, method, path
(complete, for 'nova api'), summary and stream.`,
	Example: `  nova api search controller state
  nova api search "store object" --limit 3`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec, err := loadAPISpec(cmd.Context())
		if err != nil {
			return err
		}
		return writeAPIJSON(cmd, spec.Search(strings.Join(args, " "), apiFlags.limit))
	},
}

var apiSchemaCmd = &cobra.Command{
	Use:   "schema <operationId>",
	Short: "Show parameters and request body of an operation",
	Long: `Show what an operation of the instance's NOVA API v2 takes, as JSON: method, complete
path, parameters and request body (references expanded), and the schema names of
the answers.`,
	Example: `  nova api schema getRobotController
  nova api schema inverseKinematics`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec, err := loadAPISpec(cmd.Context())
		if err != nil {
			return err
		}
		op, ok := spec.Find(args[0])
		if !ok {
			return fmt.Errorf("no operation %q in the NOVA API %s of the instance: find one with 'nova api search <words>'", args[0], spec.APIVersion())
		}
		return writeAPIJSON(cmd, spec.Schema(*op))
	},
}

func init() {
	f := apiCmd.Flags()
	f.StringVarP(&apiFlags.method, "method", "X", "", "HTTP method (default: GET, or POST with fields or --input)")
	f.StringArrayVarP(&apiFlags.rawFields, "raw-field", "f", nil, "Add a string field key=value")
	f.StringArrayVarP(&apiFlags.fields, "field", "F", nil, "Add a typed field key=value (true, false, null, numbers, @file)")
	f.StringArrayVarP(&apiFlags.headers, "header", "H", nil, "Add a request header 'Key: value'")
	f.StringVar(&apiFlags.input, "input", "", "Read the request body from a file (- for stdin)")
	f.BoolVarP(&apiFlags.include, "include", "i", false, "Print the status line and response headers before the body")
	f.BoolVar(&apiFlags.silent, "silent", false, "Don't print the response body")
	f.StringVarP(&apiFlags.output, "output", "o", "", "Write the response body to a file (needed for binary answers)")
	f.BoolVar(&apiFlags.dryRun, "dry-run", false, "Check and print the request as JSON without sending it")
	f.DurationVar(&apiFlags.timeout, "timeout", time.Minute, "How long to wait for the answer's headers (0: no limit, for long polls)")
	_ = apiCmd.RegisterFlagCompletionFunc("method", cobra.FixedCompletions(
		[]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, cobra.ShellCompDirectiveNoFileComp))

	apiSearchCmd.Flags().IntVar(&apiFlags.limit, "limit", 10, "Maximum number of results (0: all)")
	apiCmd.AddCommand(apiSearchCmd, apiSchemaCmd)
	rootCmd.AddCommand(apiCmd)
}

// exitRefused ends nova when a request is refused before it is sent (ADR-0010).
const exitRefused = 3

// typedError is a failure with an ADR-0010 error code. executeRoot prints the
// message and hint, and as JSON when stderr is no terminal.
type typedError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
	Retryable bool   `json:"retryable"`
	exit      int
}

func (e *typedError) Error() string { return e.Message }

func apiCacheDir() string {
	home, err := homedir.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".nova", "cache", "api")
}

func apiClient() (*novaapi.Client, error) {
	host := currentHost()
	if host == "" {
		return nil, nova.ErrNoInstance
	}
	return novaapi.NewClient(host)
}

func loadAPISpec(ctx context.Context) (*novaapi.Spec, error) {
	c, err := apiClient()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, specTimeout())
	defer cancel()
	return c.LoadSpec(ctx, apiCacheDir())
}

// specTimeout bounds reading the NOVA version and the spec: --timeout, or a
// minute when it is 0 (no limit applies to the request itself, not to this).
func specTimeout() time.Duration {
	if apiFlags.timeout > 0 {
		return apiFlags.timeout
	}
	return time.Minute
}

func writeAPIJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

var placeholder = regexp.MustCompile(`\{[^}/]*\}`)

// fillPath replaces {cell} and rejects other placeholders in the path (not in
// the query).
func fillPath(path, cell string) (string, error) {
	p, q, hasQuery := strings.Cut(path, "?")
	p = strings.ReplaceAll(p, "{cell}", url.PathEscape(cell))
	if m := placeholder.FindString(p); m != "" {
		return "", fmt.Errorf("fill in %s in the path: only {cell} is replaced (with %q)", m, cell)
	}
	if hasQuery {
		p += "?" + q
	}
	return p, nil
}

func readInput(name string) ([]byte, error) {
	if name == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(name)
}

// apiRequest is what --dry-run prints.
type apiRequest struct {
	Method    string             `json:"method"`
	URL       string             `json:"url"`
	Operation *novaapi.Operation `json:"operation,omitempty"`
	Auth      string             `json:"auth"`
	Headers   map[string]string  `json:"headers,omitempty"`
	Body      any                `json:"body,omitempty"`
}

func runAPI(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if !strings.HasPrefix(args[0], "/") {
		return fmt.Errorf("the path must start with '/', e.g. /api/v2/cells/{cell}/controllers (or use 'nova api search')")
	}
	t := currentTarget()
	if t.Host == "" {
		return nova.ErrNoInstance
	}
	path, err := fillPath(args[0], t.Cell())
	if err != nil {
		return err
	}

	var fields []novaapi.Field
	for _, s := range apiFlags.rawFields {
		f, err := novaapi.ParseRawField(s)
		if err != nil {
			return err
		}
		fields = append(fields, f)
	}
	for _, s := range apiFlags.fields {
		f, err := novaapi.ParseTypedField(s, readInput)
		if err != nil {
			return err
		}
		fields = append(fields, f)
	}

	method := strings.ToUpper(apiFlags.method)
	if method == "" {
		method = http.MethodGet
		if len(fields) > 0 || apiFlags.input != "" {
			method = http.MethodPost
		}
	}

	var body []byte
	contentType := ""
	switch {
	case apiFlags.input != "":
		if body, err = readInput(apiFlags.input); err != nil {
			return fmt.Errorf("reading --input: %w", err)
		}
		contentType = "application/json"
		path = novaapi.AddQuery(path, fields)
	case len(fields) > 0 && (method == http.MethodGet || method == http.MethodHead):
		path = novaapi.AddQuery(path, fields)
	case len(fields) > 0:
		obj, err := novaapi.BuildBody(fields)
		if err != nil {
			return err
		}
		if body, err = json.Marshal(obj); err != nil {
			return err
		}
		contentType = "application/json"
	}

	headers := http.Header{}
	for _, h := range apiFlags.headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok || strings.TrimSpace(k) == "" {
			return fmt.Errorf("header %q: expected 'Key: value'", h)
		}
		headers.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	if headers.Get("Content-Type") == "" && contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	if headers.Get("Accept") == "" {
		headers.Set("Accept", "application/json, */*;q=0.8")
	}

	client, err := novaapi.NewClient(t.Host)
	if err != nil {
		return err
	}
	op := apiOperation(ctx, client, method, path)
	if novaapi.IsStream(op) {
		return &typedError{
			Code:    "STREAM_NOT_SUPPORTED",
			Message: fmt.Sprintf("%s is a WebSocket stream, which 'nova api' doesn't run", op.ID),
			Hint:    "WebSocket streams are not supported by nova api: use the NOVA SDK or an app",
			exit:    exitRefused,
		}
	}

	req, err := client.NewRequest(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body == nil {
		req.Body, req.ContentLength = http.NoBody, 0
	}
	for k, vs := range headers {
		req.Header[k] = vs
	}

	if apiFlags.dryRun {
		return writeAPIJSON(cmd, dryRunRequest(req, op, body))
	}
	if req.URL.Scheme == "http" && req.Header.Get("Authorization") != "" {
		fmt.Fprintf(os.Stderr, "Warning: sending credentials to %s over plain HTTP\n", req.URL.Host)
	}
	return sendAPIRequest(cmd, client, req)
}

// apiOperation looks up the v2 operation of a request in the instance's spec, to
// refuse streams and name the operation in --dry-run. Without a spec (older
// instance, other path) the request is sent as is.
func apiOperation(ctx context.Context, client *novaapi.Client, method, path string) *novaapi.Operation {
	if !strings.HasPrefix(path, novaapi.BasePath+"/") {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, specTimeout())
	defer cancel()
	spec, err := client.LoadSpec(ctx, apiCacheDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: can't read the instance's API spec, sending the request unchecked: %v\n", err)
		return nil
	}
	op, _ := novaapi.Match(spec.Operations(), method, path)
	return op
}

func dryRunRequest(req *http.Request, op *novaapi.Operation, body []byte) apiRequest {
	r := apiRequest{Method: req.Method, URL: req.URL.String(), Operation: op, Auth: "none"}
	switch auth := req.Header.Get("Authorization"); {
	case strings.HasPrefix(auth, "Bearer "):
		r.Auth = "bearer"
	case strings.HasPrefix(auth, "Basic "):
		r.Auth = "basic"
	}
	for k, vs := range req.Header {
		if k == "Authorization" || k == "User-Agent" {
			continue
		}
		if r.Headers == nil {
			r.Headers = map[string]string{}
		}
		r.Headers[k] = strings.Join(vs, ", ")
	}
	if len(body) > 0 {
		var v any
		if json.Unmarshal(body, &v) == nil {
			r.Body = v
		} else {
			r.Body = string(body)
		}
	}
	return r
}

func sendAPIRequest(cmd *cobra.Command, client *novaapi.Client, req *http.Request) error {
	// The timeout covers the wait for the headers, not the body: the timer stops
	// as soon as the headers are there. 0 waits as long as the instance takes.
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	var timer *time.Timer
	if apiFlags.timeout > 0 {
		timer = time.AfterFunc(apiFlags.timeout, cancel)
	}
	resp, err := client.RoundTrip(req.WithContext(ctx))
	if timer != nil && !timer.Stop() {
		if err == nil {
			resp.Body.Close()
		}
		return fmt.Errorf("%s %s: no answer within %s (--timeout)", req.Method, req.URL, apiFlags.timeout)
	}
	if err == nil {
		resp, err = novaapi.CheckAnswer(resp)
	}
	if errors.Is(err, novaapi.ErrUnauthorized) {
		return &typedError{Code: "UNAUTHORIZED", Message: err.Error(), Hint: "run 'nova login'", exit: 1}
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	br := bufio.NewReaderSize(resp.Body, 4096)
	head := novaapi.PeekStart(br)
	status := resp.StatusCode
	if code, ok := ingress.ErrorPageCode(resp.Header, head); ok {
		status = code
	}
	failed := status < 200 || status >= 300

	out := cmd.OutOrStdout()
	if apiFlags.include {
		writeResponseHead(out, resp)
	}

	if failed {
		b, _ := io.ReadAll(br)
		if !apiFlags.silent {
			writeBody(out, b)
		}
		msg := fmt.Sprintf("%s %s: HTTP %d", req.Method, req.URL, status)
		if d := ingress.ErrorDetail(b); d != "" && len(b) > 0 {
			msg += ": " + d
		}
		return errors.New(msg)
	}

	switch {
	case apiFlags.output != "":
		f, err := os.Create(apiFlags.output)
		if err != nil {
			return err
		}
		n, err := io.Copy(f, br)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("writing %s: %w", apiFlags.output, err)
		}
		cmd.PrintErrf("Wrote %d bytes to %s\n", n, apiFlags.output)
		return nil
	case apiFlags.silent:
		_, err := io.Copy(io.Discard, br)
		return err
	case !isTextual(resp.Header.Get("Content-Type"), head):
		return fmt.Errorf("the answer is binary (%s): pass --output <file>", resp.Header.Get("Content-Type"))
	}
	// Copied as it arrives: a chunked or slow answer shows up line by line.
	tw := &trailingWriter{w: out}
	n, err := io.Copy(tw, br)
	if err != nil {
		return fmt.Errorf("reading the answer: %w", err)
	}
	if n > 0 && tw.last != '\n' && isTerminal(out) {
		fmt.Fprintln(out)
	}
	debug.Logf("api: %s %s: %d, %d bytes", req.Method, req.URL, resp.StatusCode, n)
	return nil
}

// trailingWriter remembers the last byte written.
type trailingWriter struct {
	w    io.Writer
	last byte
}

func (t *trailingWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		t.last = p[len(p)-1]
	}
	return t.w.Write(p)
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// writeBody writes b and ends it with a newline on a terminal.
func writeBody(out io.Writer, b []byte) {
	_, _ = out.Write(b)
	if len(b) > 0 && b[len(b)-1] != '\n' && isTerminal(out) {
		fmt.Fprintln(out)
	}
}

func writeResponseHead(out io.Writer, resp *http.Response) {
	fmt.Fprintf(out, "%s %s\n", resp.Proto, resp.Status)
	keys := make([]string, 0, len(resp.Header))
	for k := range resp.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range resp.Header[k] {
			fmt.Fprintf(out, "%s: %s\n", k, v)
		}
	}
	fmt.Fprintln(out)
}

// isTextual reports whether an answer can go to stdout: JSON, text, XML, YAML,
// or (without content type) a body without NUL bytes.
func isTextual(contentType string, head []byte) bool {
	ct := strings.ToLower(contentType)
	switch {
	case ct == "":
		return !bytes.ContainsRune(head, 0)
	case strings.HasPrefix(ct, "text/"), strings.Contains(ct, "json"), strings.Contains(ct, "xml"),
		strings.Contains(ct, "yaml"), strings.Contains(ct, "javascript"):
		return true
	}
	return false
}
