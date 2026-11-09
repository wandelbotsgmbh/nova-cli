package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

// outputFormat is the value of -o/--output (ADR-0010): text for people, json for
// agents and scripts. The JSON shapes are part of the CLI contract: fields may be
// added, never renamed or removed.
type outputFormat string

const (
	outputText outputFormat = "text"
	outputJSON outputFormat = "json"
)

func (o *outputFormat) String() string { return string(*o) }
func (o *outputFormat) Type() string   { return "format" }
func (o *outputFormat) Set(s string) error {
	switch f := outputFormat(s); f {
	case outputText, outputJSON:
		*o = f
		return nil
	}
	return fmt.Errorf("must be text or json")
}

// addOutputFlag adds -o/--output to cmd. It is a local flag, not a global one:
// some commands use -o/--output for a file (flow get, simulation api).
func addOutputFlag(cmd *cobra.Command) *outputFormat {
	o := outputText
	cmd.Flags().VarP(&o, "output", "o", "Output format: text or json")
	_ = cmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions(
		[]string{string(outputText), string(outputJSON)}, cobra.ShellCompDirectiveNoFileComp))
	return &o
}

// writeJSON writes v as indented JSON to stdout.
func writeJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// namedItem is the JSON shape of list entries that only have a name.
type namedItem struct {
	Name string `json:"name"`
}

// writeNames prints names one per line, or as a JSON array of {"name": …}.
func writeNames(cmd *cobra.Command, o *outputFormat, names []string) error {
	if *o == outputJSON {
		items := make([]namedItem, 0, len(names))
		for _, n := range names {
			items = append(items, namedItem{Name: n})
		}
		return writeJSON(cmd, items)
	}
	for _, n := range names {
		fmt.Fprintln(cmd.OutOrStdout(), n)
	}
	return nil
}

// absPath reports written files with absolute paths, so the result doesn't depend
// on the caller's working directory.
func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
