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
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/editor"
	"github.com/wandelbotsgmbh/nova-cli/pkg/novaflow"
	"golang.org/x/term"
)

var (
	flowEditNoValidate bool
	stdinReader        = bufio.NewReader(os.Stdin)
)

var flowEditCmd = &cobra.Command{
	Use:     "edit <id>",
	Short:   "Edit a flow as JSON in your editor",
	Long:    flowEditLong,
	Example: flowEditExample,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("'nova flow edit' needs a terminal for the editor; use 'nova flow get <id> -o <file>' and 'nova flow push <file>' instead")
		}
		c, err := newFlowClient()
		if err != nil {
			return err
		}
		if err := ensureNovaflow(cmd, c, false); err != nil {
			return err
		}
		e := editor.NewDefaultEditor()
		return editFlow(cmd.Context(), c, args[0], !flowEditNoValidate, flowEditor{
			edit: func(content []byte) ([]byte, error) {
				return e.EditInTempfile("nova-flow-", ".json", content)
			},
			ask: func(question string) bool {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s [Y/n] ", question)
				answer, err := stdinReader.ReadString('\n')
				if err != nil && answer == "" {
					return false // Ctrl-D
				}
				switch strings.ToLower(strings.TrimSpace(answer)) {
				case "", "y", "yes":
					return true
				}
				return false
			},
			msg: cmd.ErrOrStderr(),
		})
	},
}

// flowEditor is the interactive part of 'nova flow edit'.
type flowEditor struct {
	edit func(content []byte) ([]byte, error) // opens the editor
	ask  func(question string) bool           // yes/no question
	msg  io.Writer                            // messages (stderr)
}

// editFlow opens a flow in the editor and saves it back if it was changed.
// The update only succeeds while the flow is still at the revision it was
// read at. Content that can't be saved (invalid, conflict) can be edited
// again, or is kept in a file so that no work is lost.
func editFlow(ctx context.Context, c *novaflow.Client, id string, validate bool, fe flowEditor) error {
	raw, err := c.GetFlowRaw(ctx, id)
	if novaflow.IsStatus(err, http.StatusNotFound) {
		return fmt.Errorf("flow %q not found on cell %q", id, c.Cell())
	}
	if err != nil {
		return err
	}
	var orig novaflow.Flow
	if err := json.Unmarshal(raw, &orig); err != nil {
		return fmt.Errorf("reading flow %s: %w", id, err)
	}
	original, err := indentJSON(raw)
	if err != nil {
		return err
	}

	shown := original
	for {
		edited, err := fe.edit(shown)
		if err != nil {
			if !bytes.Equal(shown, original) {
				// the changes of the rounds before are only in shown
				return keepEdited(shown, fmt.Errorf("editor: %w", err), "")
			}
			return fmt.Errorf("editor: %w", err)
		}
		if len(bytes.TrimSpace(edited)) == 0 || sameJSON(edited, original) {
			fmt.Fprintln(fe.msg, "Edit cancelled, no changes made.")
			return nil
		}
		retry := !bytes.Equal(shown, original)
		if retry && bytes.Equal(edited, shown) {
			// closed again without fixing anything: stop asking
			return keepEdited(edited, errors.New("flow not saved"), "")
		}

		problem, err := checkEditedFlow(ctx, c, id, edited, validate, fe.msg)
		if err != nil {
			return keepEdited(edited, err, "")
		}
		if problem != nil {
			fmt.Fprintf(fe.msg, "ERROR: %v\n", problem)
			if fe.ask("Edit again?") {
				shown = edited
				continue
			}
			return keepEdited(edited, errors.New("flow not saved"), "")
		}

		f, _ := parseFlowFile(edited, "", "")
		err = c.UpdateFlow(ctx, f, orig.Revision)
		if errors.Is(err, novaflow.ErrFlowChanged) {
			return keepEdited(edited, err, fmt.Sprintf("compare them with 'nova flow get %s', replace the flow with them with 'nova flow push %%s --force'", id))
		}
		if err != nil {
			return keepEdited(edited, err, "")
		}
		fmt.Fprintf(fe.msg, "Updated flow %s\n", id)
		return nil
	}
}

// checkEditedFlow returns a problem the user can fix in the editor, or an
// error that ends the edit.
func checkEditedFlow(ctx context.Context, c *novaflow.Client, id string, edited []byte, validate bool, msg io.Writer) (problem, err error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(edited, &probe); err != nil {
		return fmt.Errorf("not a JSON object: %w", err), nil
	}
	for _, field := range []string{"id", "name", "tree"} {
		if v := bytes.TrimSpace(probe[field]); len(v) == 0 || string(v) == "null" || string(v) == `""` {
			return fmt.Errorf("%q is missing", field), nil
		}
	}
	f, perr := parseFlowFile(edited, "", "")
	if perr != nil {
		return perr, nil
	}
	if f.ID != id {
		return fmt.Errorf("the id can't be changed here (%q → %q); copy a flow with 'nova flow push <file> --id <new id>'", id, f.ID), nil
	}
	if !validate {
		return nil, nil
	}
	res, err := c.ValidateTree(ctx, f.Tree)
	if novaflow.IsStatus(err, http.StatusBadRequest) || novaflow.IsStatus(err, http.StatusUnprocessableEntity) {
		// a tree the control plane can't even read: fixable in the editor too
		return fmt.Errorf("the flow is not valid (--no-validate saves it anyway): %w", err), nil
	}
	if err != nil {
		return nil, fmt.Errorf("validating: %w", err)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(msg, "WARN: %s: %s\n", w.Code, w.Message)
	}
	if res.Valid {
		return nil, nil
	}
	var lines []string
	for _, e := range res.Errors {
		lines = append(lines, e.Code+": "+e.Message)
	}
	return fmt.Errorf("the flow is not valid (--no-validate saves it anyway):\n  %s", strings.Join(lines, "\n  ")), nil
}

// sameJSON reports whether a and b are the same JSON apart from formatting.
func sameJSON(a, b []byte) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

// keepEdited writes edited content to a file and adds its path to err. hint
// says what to do with the file (%s is its path); the default is to push it.
func keepEdited(edited []byte, err error, hint string) error {
	f, ferr := os.CreateTemp("", "nova-flow-edit-*.json")
	if ferr != nil {
		return fmt.Errorf("%w (your changes are lost: %v)", err, ferr)
	}
	defer f.Close()
	if _, werr := f.Write(edited); werr != nil {
		return fmt.Errorf("%w (your changes are lost: %v)", err, werr)
	}
	if hint == "" {
		hint = "save them with 'nova flow push %s'"
	}
	return fmt.Errorf("%w, your changes are in %s: %s", err, f.Name(), fmt.Sprintf(hint, f.Name()))
}

func init() {
	flowEditCmd.Flags().BoolVar(&flowEditNoValidate, "no-validate", false, "Save without letting the instance validate the tree first")
	flowCmd.AddCommand(flowEditCmd)
}
