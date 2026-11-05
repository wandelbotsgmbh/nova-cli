// Package debug writes diagnostic output for --debug / NOVA_DEBUG: HTTP
// requests and responses, external commands and how the target was resolved.
//
// Everything goes to stderr, so stdout keeps carrying only a command's data and
// stays parseable with debug output on. Secrets (tokens, passwords, keys,
// kubeconfig credentials) are redacted before anything is written.
package debug

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// EnvVar enables debug output like --debug, e.g. for a whole agent session.
const EnvVar = "NOVA_DEBUG"

var (
	mu      sync.Mutex
	enabled bool
	out     io.Writer // nil: os.Stderr at the time of writing
)

// Enable turns debug output on or off.
func Enable(on bool) {
	mu.Lock()
	defer mu.Unlock()
	enabled = on
}

// Enabled reports whether debug output is on.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

// EnabledByEnv reports whether NOVA_DEBUG asks for debug output: any value
// except empty, "0" and "false".
func EnabledByEnv() bool {
	v := strings.TrimSpace(os.Getenv(EnvVar))
	if v == "" {
		return false
	}
	on, err := strconv.ParseBool(v)
	return err != nil || on
}

// SetOutput redirects debug output (tests); nil restores stderr.
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	out = w
}

// Logf writes one debug message, each line prefixed with "debug: ". The
// message is written as given: callers redact what may hold secrets.
func Logf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if !enabled {
		return
	}
	msg := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	var b strings.Builder
	for line := range strings.SplitSeq(msg, "\n") {
		b.WriteString("debug: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	w := out
	if w == nil {
		w = os.Stderr
	}
	_, _ = io.WriteString(w, b.String())
}

// Command is exec.Command that logs the command line when debug output is on.
func Command(name string, args ...string) *exec.Cmd {
	if Enabled() {
		Logf("exec: %s", Redact(quoteArgs(append([]string{name}, args...))))
	}
	return exec.Command(name, args...)
}

func quoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n\"'\\$`|&;<>()*?[]{}") {
			a = strconv.Quote(a)
		}
		quoted[i] = a
	}
	return strings.Join(quoted, " ")
}
