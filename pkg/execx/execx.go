// Package execx runs external tools (docker, podman, ssh) whose progress the
// user should see, and makes their failures self-explaining.
//
// Output streams to stderr as it comes. When stderr is not a terminal (an
// agent, CI, a log file), the last lines are kept and added to the error, so
// the cause of e.g. a failed image build sits next to the error message
// instead of somewhere in hundreds of lines of build log, or cut off by a
// tool that truncates long output. On a terminal the tool keeps its TTY (and
// with it e.g. BuildKit's interactive progress view); the user sees the cause
// right above the error anyway.
package execx

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"golang.org/x/term"
)

// TailLines is how many lines of output a failure carries.
const TailLines = 20

// maxLineLen caps a single line in the tail (minified JSON, base64, ...).
const maxLineLen = 300

// stderrIsTerminal is replaced in tests.
var stderrIsTerminal = func() bool { return term.IsTerminal(int(os.Stderr.Fd())) }

// Run runs cmd with stdout and stderr streamed to os.Stderr. A failure is
// returned as *OutputError carrying the last lines of output, unless stderr is
// a terminal. Output is also written to the extra writers (e.g. to parse it).
func Run(cmd *exec.Cmd, also ...io.Writer) error {
	if len(also) == 0 && stderrIsTerminal() {
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		return cmd.Run()
	}
	writers := append([]io.Writer{os.Stderr}, also...)
	var tail *Tail
	if !stderrIsTerminal() {
		tail = NewTail(TailLines)
		writers = append(writers, tail)
	}
	w := io.MultiWriter(writers...)
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	if err == nil || tail == nil {
		return err
	}
	if lines := tail.Lines(); len(lines) > 0 {
		return &OutputError{Err: err, Output: lines}
	}
	return err
}

// OutputError is a failed command together with the last lines it printed.
type OutputError struct {
	Err    error
	Output []string
}

func (e *OutputError) Error() string {
	var b strings.Builder
	b.WriteString(e.Err.Error())
	b.WriteString("\nlast lines of output:")
	for _, l := range e.Output {
		b.WriteString("\n  ")
		b.WriteString(l)
	}
	return b.String()
}

func (e *OutputError) Unwrap() error { return e.Err }

// Tail is an io.Writer keeping the last lines written to it, without terminal
// control sequences, carriage-return redraws and blank lines.
type Tail struct {
	n       int
	lines   []string
	partial bytes.Buffer
}

func NewTail(n int) *Tail { return &Tail{n: n} }

func (t *Tail) Write(p []byte) (int, error) {
	for _, c := range p {
		if c == '\n' {
			t.flush()
			continue
		}
		if t.partial.Len() < 4*maxLineLen {
			t.partial.WriteByte(c)
		}
	}
	return len(p), nil
}

// Lines returns the kept lines, including an unterminated last one.
func (t *Tail) Lines() []string {
	t.flush()
	return append([]string(nil), t.lines...)
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

func (t *Tail) flush() {
	if t.partial.Len() == 0 {
		return
	}
	line := ansi.ReplaceAllString(t.partial.String(), "")
	t.partial.Reset()
	// a progress line redrawn with \r: keep what was shown last
	if i := strings.LastIndex(strings.TrimRight(line, "\r"), "\r"); i >= 0 {
		line = line[i+1:]
	}
	line = strings.TrimRight(line, " \t\r")
	if strings.TrimSpace(line) == "" {
		return
	}
	if len(line) > maxLineLen {
		line = strings.ToValidUTF8(line[:maxLineLen], "") + "…"
	}
	t.lines = append(t.lines, line)
	if len(t.lines) > t.n {
		t.lines = t.lines[len(t.lines)-t.n:]
	}
}
