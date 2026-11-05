package execx

import (
	"bytes"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func asTerminal(t *testing.T, tty bool) {
	t.Helper()
	orig := stderrIsTerminal
	stderrIsTerminal = func() bool { return tty }
	t.Cleanup(func() { stderrIsTerminal = orig })
}

func shell(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	return exec.Command("sh", "-c", script)
}

// A failed build without a terminal (agent, CI) carries the end of its output.
func TestRunAddsOutputTailToError(t *testing.T) {
	asTerminal(t, false)
	err := Run(shell(t, `for i in $(seq 1 30); do echo "#$i step"; done; echo "ERROR: pip failed" >&2; exit 3`))

	oe, ok := errors.AsType[*OutputError](err)
	if !ok {
		t.Fatalf("error %v (%T) is no *OutputError", err, err)
	}
	if len(oe.Output) != TailLines {
		t.Errorf("kept %d lines, want %d", len(oe.Output), TailLines)
	}
	if last := oe.Output[len(oe.Output)-1]; last != "ERROR: pip failed" {
		t.Errorf("last line = %q, want the error from stderr", last)
	}
	if oe.Output[0] != "#12 step" {
		t.Errorf("first kept line = %q, want #12 step", oe.Output[0])
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "exit status 3\nlast lines of output:\n  #12 step\n") {
		t.Errorf("message = %q", msg)
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); !ok || exitErr.ExitCode() != 3 {
		t.Errorf("exit code not preserved: %v", err)
	}
}

// On a terminal the tool keeps its TTY, and the error stays as it was.
func TestRunOnTerminalKeepsPlainError(t *testing.T) {
	asTerminal(t, true)
	err := Run(shell(t, `echo boom; exit 1`))
	if _, ok := errors.AsType[*OutputError](err); ok {
		t.Errorf("got output tail on a terminal: %v", err)
	}
	if err == nil || err.Error() != "exit status 1" {
		t.Errorf("err = %v, want exit status 1", err)
	}
}

func TestRunWritesToExtraWriters(t *testing.T) {
	for _, tty := range []bool{true, false} {
		asTerminal(t, tty)
		var buf bytes.Buffer
		if err := Run(shell(t, `echo "digest: sha256:abc"; echo note >&2`), &buf); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "digest: sha256:abc") || !strings.Contains(buf.String(), "note") {
			t.Errorf("tty=%v: extra writer got %q", tty, buf.String())
		}
	}
}

func TestRunSuccessHasNoError(t *testing.T) {
	asTerminal(t, false)
	if err := Run(shell(t, `echo fine`)); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestRunWithoutOutputKeepsPlainError(t *testing.T) {
	asTerminal(t, false)
	err := Run(shell(t, `exit 2`))
	if err == nil || err.Error() != "exit status 2" {
		t.Errorf("err = %v, want exit status 2", err)
	}
}

func TestTailCleansTerminalOutput(t *testing.T) {
	tail := NewTail(3)
	_, _ = tail.Write([]byte("\x1b[32mgreen\x1b[0m line\n\n   \nPushing  10%\rPushing  55%\rPushing 100%\r\n"))
	_, _ = tail.Write([]byte("split "))
	_, _ = tail.Write([]byte("write\nunterminated"))
	got := tail.Lines()
	want := []string{"Pushing 100%", "split write", "unterminated"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Lines() = %q, want %q", got, want)
	}

	long := NewTail(1)
	_, _ = long.Write([]byte(strings.Repeat("ä", maxLineLen) + "\n"))
	l := long.Lines()[0]
	if !strings.HasSuffix(l, "…") || len(l) > maxLineLen+len("…") || !strings.HasPrefix(l, "ää") {
		t.Errorf("long line not capped cleanly: %d bytes", len(l))
	}
}
