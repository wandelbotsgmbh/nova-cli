package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// rejectUnknownSubcommands makes every command group below root fail on an
// unknown subcommand. cobra only does that for the root command; a group like
// "nova app" would otherwise print its help and exit 0 on "nova app delte".
//
// The check lives in Args because cobra validates args before running any
// PersistentPreRun hooks, so showing a group's help never triggers them. The
// RunE is never reached; cobra just requires one before it validates args.
func rejectUnknownSubcommands(root *cobra.Command) {
	// cobra adds "completion" only in Execute; add it now so it is covered too
	root.InitDefaultCompletionCmd()
	walkGroups(root)
}

func walkGroups(parent *cobra.Command) {
	for _, c := range parent.Commands() {
		walkGroups(c)
		if !c.HasSubCommands() || c.Runnable() {
			continue
		}
		c.Args = groupArgs
		c.RunE = func(cmd *cobra.Command, args []string) error { return cmd.Help() }
	}
}

func groupArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return pflag.ErrHelp
	}
	return unknownCommandError(cmd, args[0])
}

// unknownCommandError mirrors cobra's message for unknown root subcommands.
func unknownCommandError(cmd *cobra.Command, name string) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "unknown command %q for %q", name, cmd.CommandPath())
	if !cmd.DisableSuggestions {
		if cmd.SuggestionsMinimumDistance <= 0 {
			cmd.SuggestionsMinimumDistance = 2
		}
		if suggestions := cmd.SuggestionsFor(name); len(suggestions) > 0 {
			sb.WriteString("\n\nDid you mean this?\n")
			for _, s := range suggestions {
				fmt.Fprintf(&sb, "\t%s\n", s)
			}
		}
	}
	return fmt.Errorf("%s", sb.String())
}
