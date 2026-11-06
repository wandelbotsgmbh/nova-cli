package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nucleusservers"
)

// nucleusFlag is the --nucleus flag of 'nova nucleus' and 'nova config'.
var nucleusFlag string

var (
	nucleusServerHost       string
	nucleusServerTokenStdin bool
	nucleusServerTLS        bool
	nucleusServerSelect     bool
)

func loadNucleusRegistry() (*nucleusservers.Registry, error) {
	return nucleusservers.Load(viper.GetViper())
}

// resolveNucleus returns the Nucleus server of this invocation: --nucleus, then
// NOVA_NUCLEUS, then the selected one. Without any, the server is empty.
func resolveNucleus() (nucleusservers.Server, instances.Source, error) {
	r, err := loadNucleusRegistry()
	if err != nil {
		return nucleusservers.Server{}, instances.SourceNone, err
	}
	return r.Resolve(nucleusFlag, os.Getenv(nucleusservers.EnvVar))
}

// errNoNucleusServer explains how to get a Nucleus server.
var errNoNucleusServer = fmt.Errorf("%w: add one with 'nova nucleus server add <name> --host <host> --token-stdin', or pass --nucleus-host and --nucleus-token", nucleusservers.ErrNoServer)

var nucleusServerCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage the Nucleus servers the CLI knows",
	Long: `Manage the local list of Nucleus servers. A Nucleus server is independent of the NOVA
instance: 'nova nucleus' commands work against the selected server, or the one named
with --nucleus / NOVA_NUCLEUS for a single invocation.

Tokens are stored per server and only sent to that server's host.`,
	Example: `$ nova nucleus server add dev --host nucleus.dev.wandelbots.io --token-stdin < token.txt
$ nova nucleus server list
$ nova nucleus server select dev
$ nova nucleus ls / --nucleus wb`,
}

var nucleusServerAddCmd = &cobra.Command{
	Use:   "add <name> --host <host>",
	Short: "Add a Nucleus server",
	Long: `Add a Nucleus server to the local list. The API token is read from stdin with
--token-stdin (it never appears in the shell history), or set later with
'nova config set nucleus-token <token> --nucleus <name>'.

If no server is selected yet, the new one is selected; --select selects it anyway.`,
	Example: `$ nova nucleus server add dev --host nucleus.dev.wandelbots.io --token-stdin < token.txt
$ printf '%s' "$NUCLEUS_TOKEN" | nova nucleus server add wb --host nucleus.wb.wandelbots.io --token-stdin --select`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := loadNucleusRegistry()
		if err != nil {
			return err
		}
		s := nucleusservers.Server{Name: args[0], Host: strings.TrimSpace(nucleusServerHost)}
		if !nucleusServerTLS {
			off := false
			s.TLS = &off
		}
		if nucleusServerTokenStdin {
			if s.Token, err = readToken(cmd.InOrStdin()); err != nil {
				return err
			}
		}
		if existing, ok := r.FindByHost(s.Host); ok {
			fmt.Fprintf(cmd.ErrOrStderr(), "note: %q already points to %s\n", existing.Name, existing.Host)
		}
		if err := r.Add(s); err != nil {
			return err
		}
		if r.Current == "" || nucleusServerSelect {
			r.Current = s.Name
		}
		if err := r.Save(viper.GetViper()); err != nil {
			return err
		}
		msg := fmt.Sprintf("added Nucleus server %q (%s)", s.Name, s.Host)
		if r.Current == s.Name {
			msg += ", selected"
		}
		if s.Token == "" {
			msg += fmt.Sprintf("; set its token with 'nova config set nucleus-token <token> --nucleus %s'", s.Name)
		}
		fmt.Fprintln(cmd.ErrOrStderr(), msg)
		return nil
	},
}

func readToken(in io.Reader) (string, error) {
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", errors.New("--token-stdin: no token on stdin")
	}
	return token, nil
}

var nucleusServerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the Nucleus servers; '*' marks the selected one",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := loadNucleusRegistry()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\tNAME\tHOST\tTLS\tTOKEN")
		for _, s := range r.Servers {
			mark := ""
			if s.Name == r.Current {
				mark = "*"
			}
			token := "-"
			if s.Token != "" {
				token = "set"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n", mark, s.Name, s.Host, s.UseTLS(), token)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if o := nucleusOverride(); o != "" && o != r.Current {
			fmt.Fprintf(cmd.ErrOrStderr(), "note: this invocation uses %q (--nucleus / %s)\n", o, nucleusservers.EnvVar)
		}
		return nil
	},
}

func nucleusOverride() string {
	if nucleusFlag != "" {
		return nucleusFlag
	}
	return os.Getenv(nucleusservers.EnvVar)
}

var nucleusServerSelectCmd = &cobra.Command{
	Use:               "select <name>",
	Short:             "Select the Nucleus server commands work against",
	Long:              `Select the Nucleus server commands work against. For a single invocation use --nucleus <name> or NOVA_NUCLEUS instead.`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeNucleusServer,
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := loadNucleusRegistry()
		if err != nil {
			return err
		}
		if err := r.Select(args[0]); err != nil {
			return err
		}
		return r.Save(viper.GetViper())
	},
}

var nucleusServerRemoveCmd = &cobra.Command{
	Use:               "remove <name>",
	Short:             "Remove a Nucleus server from the local list (with its token)",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeNucleusServer,
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := loadNucleusRegistry()
		if err != nil {
			return err
		}
		selected := r.Current
		if err := r.Remove(args[0]); err != nil {
			return err
		}
		if err := r.Save(viper.GetViper()); err != nil {
			return err
		}
		if selected == args[0] {
			fmt.Fprintf(cmd.ErrOrStderr(), "removed the selected Nucleus server %q; select another with 'nova nucleus server select <name>'\n", args[0])
		}
		return nil
	},
}

func completeNucleusServer(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	r, err := loadNucleusRegistry()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, s := range r.Servers {
		if strings.HasPrefix(s.Name, toComplete) {
			names = append(names, s.Name+"\t"+s.Host)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// setNucleusValue writes nucleus-host or nucleus-token of the resolved Nucleus server
// ('nova config set'). Without a server, nucleus-host adds one named after the host.
func setNucleusValue(cmd *cobra.Command, key, value string) error {
	r, err := loadNucleusRegistry()
	if err != nil {
		return err
	}
	s, _, err := r.Resolve(nucleusFlag, os.Getenv(nucleusservers.EnvVar))
	if err != nil {
		return err
	}
	if key == "nucleus-host" {
		if other, ok := r.FindByHost(value); ok && other.Name != s.Name {
			// switching servers the old way: re-hosting this entry would drop a
			// valid token and duplicate the other entry
			return fmt.Errorf("the Nucleus server %q already has host %s: select it with 'nova nucleus server select %s' or use --nucleus %s", other.Name, value, other.Name, other.Name)
		}
	}
	if s.Name == "" {
		if key != "nucleus-host" {
			return errNoNucleusServer
		}
		s = nucleusservers.Server{Name: r.FreeName(nucleusservers.NameFromHost(value))}
		r.Current = s.Name
		fmt.Fprintf(cmd.ErrOrStderr(), "added Nucleus server %q, selected\n", s.Name)
	}
	switch key {
	case "nucleus-host":
		if s.Host != "" && !nucleusservers.SameHost(s.Host, value) && s.Token != "" {
			// the token belongs to the old host (ADR-0006)
			s.Token = ""
			fmt.Fprintf(cmd.ErrOrStderr(), "note: the token of %q was for %s and was removed; set the new one with 'nova config set nucleus-token <token> --nucleus %s'\n", s.Name, s.Host, s.Name)
		}
		s.Host = value
	case "nucleus-token":
		s.Token = value
	}
	if err := r.Put(s); err != nil {
		return err
	}
	if err := r.Save(viper.GetViper()); err != nil {
		return err
	}
	// the server isn't tied to the instance: always say which one was written
	fmt.Fprintf(cmd.ErrOrStderr(), "set %s of Nucleus server %q (%s)\n", key, s.Name, s.Host)
	return nil
}

func init() {
	nucleusCmd.AddCommand(nucleusServerCmd)
	nucleusServerCmd.AddCommand(nucleusServerAddCmd, nucleusServerListCmd, nucleusServerSelectCmd, nucleusServerRemoveCmd)

	for _, c := range []*cobra.Command{nucleusCmd, configCmd} {
		c.PersistentFlags().StringVar(&nucleusFlag, "nucleus", "",
			"Name of the Nucleus server for this invocation (overrides the selected one; env: "+nucleusservers.EnvVar+")")
		_ = c.RegisterFlagCompletionFunc("nucleus", func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completeNucleusServer(cmd, nil, toComplete)
		})
	}

	nucleusServerAddCmd.Flags().StringVar(&nucleusServerHost, "host", "", "Host of the Nucleus server, e.g. nucleus.dev.wandelbots.io (required)")
	_ = nucleusServerAddCmd.MarkFlagRequired("host")
	nucleusServerAddCmd.Flags().BoolVar(&nucleusServerTokenStdin, "token-stdin", false, "Read the API token from stdin")
	nucleusServerAddCmd.Flags().BoolVar(&nucleusServerTLS, "tls", true, "Use TLS (wss://)")
	nucleusServerAddCmd.Flags().BoolVar(&nucleusServerSelect, "select", false, "Select the new server (it is always selected if none is selected yet)")
}
