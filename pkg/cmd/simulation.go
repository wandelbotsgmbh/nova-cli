package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cli/browser"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
	"github.com/wandelbotsgmbh/nova-cli/pkg/novaapi"
	"github.com/wandelbotsgmbh/nova-cli/pkg/simulation"
	"github.com/wandelbotsgmbh/nova-cli/pkg/version"
	"gopkg.in/yaml.v3"
)

var simulationCmd = &cobra.Command{
	Use: "simulation",
	// the broker is not an instance; --instance / NOVA_INSTANCE don't apply
	Annotations: noTarget,
	Example: `# log in once (device flow in the browser)
nova simulation login

# find an app/profile with free capacity
nova simulation apps
nova simulation capacity isaac-24gb

# start a session and open its stream when it is ready
nova simulation session create my-session --app nova-x-isaac --profile isaac-24gb --open

# keep it running longer, suspend it to free the GPU, delete it when done
nova simulation session extend my-session --minutes 120
nova simulation session suspend my-session
nova simulation session delete my-session

# call the Isaac Sim extension API inside the session
nova simulation session api my-session omniservice /status`,
	Short: "EXPERIMENTAL, Wandelbots internal: Manage simulation streaming sessions",
	Long: `EXPERIMENTAL: Manage simulation streaming sessions (Isaac Sim, Blender, ...) on the streaming session broker.

Wandelbots internal: the simulation streaming service is currently only available to
Wandelbots employees, logging in needs a Wandelbots account. Customers and external users
interested in cloud simulation can reach out to Wandelbots (https://www.wandelbots.com/contact)
or their Wandelbots contact person.

The broker uses its own login (Microsoft Entra ID), separate from "nova login":

  nova simulation login`,
}

var simulationLoginEnv string

var simulationLoginCmd = &cobra.Command{
	Use: "login",
	Example: `# log in to the dev environment
nova simulation login`,
	Short: "Log in to the streaming session broker",
	Long: `Log in to the streaming session broker with the device flow of Microsoft Entra ID.

Wandelbots internal: logging in needs a Wandelbots account. Customers and external users
interested in cloud simulation can reach out to Wandelbots (https://www.wandelbots.com/contact).

The token is stored in the CLI configuration and refreshed automatically.
The broker URL can be overridden with "nova config set simulation-broker <url>".`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		print := func(format string, a ...any) { cmd.Printf(format, a...) }
		if err := simulation.Login(cmd.Context(), simulation.Environment(simulationLoginEnv), print); err != nil {
			return fmt.Errorf("%w\n\nnova simulation is Wandelbots internal and needs a Wandelbots account; "+
				"if you are interested in cloud simulation, reach out to Wandelbots: https://www.wandelbots.com/contact", err)
		}
		cmd.Println("Login successful")
		return nil
	},
}

var simulationLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored broker login",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return simulation.Logout()
	},
}

var simulationTokenCmd = &cobra.Command{
	Use: "token",
	Example: `# call the broker API directly
curl -H "Authorization: Bearer $(nova simulation token)" https://broker.omni.dev.wandelbots.io/sessions`,
	Short: "Print a valid broker access token",
	Long:  `Print a valid broker access token, refreshing it if needed. Handy for calling the broker API directly.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		token, _, err := simulation.AccessToken(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), token)
		return nil
	},
}

var simulationAppsCmd = &cobra.Command{
	Use:   "apps",
	Short: "List the applications, versions and profiles sessions can run",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := simulation.NewBroker(cmd.Context())
		if err != nil {
			return err
		}
		var apps []simulation.ApplicationInfo
		err = paginate(func(offset uint) (int, bool, error) {
			resp, err := c.ListApplicationsWithResponse(cmd.Context(), &simulation.ListApplicationsParams{Offset: &offset})
			if err := checkJSON(resp, err); err != nil {
				return 0, false, err
			}
			apps = append(apps, resp.JSON200.Value...)
			return len(resp.JSON200.Value), resp.JSON200.NextLink != nil, nil
		})
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "APP\tVERSION\tPROFILE\tDESCRIPTION")
		for _, a := range apps {
			for _, v := range a.Versions {
				for _, p := range v.Profiles {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Id, v.Version, p.Id, p.Name)
				}
			}
		}
		return w.Flush()
	},
}

var simulationCapacityCmd = &cobra.Command{
	Use: "capacity <profile>",
	Example: `# profile ids come from "nova simulation apps"
nova simulation capacity isaac-48gb`,
	Short: "Show how many more sessions of a profile fit right now",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := simulation.NewBroker(cmd.Context())
		if err != nil {
			return err
		}
		resp, err := c.GetCapacityWithResponse(cmd.Context(), args[0])
		if err := checkJSON(resp, err); err != nil {
			return err
		}
		capacity := resp.JSON200
		free, gpu := "unlimited", "-"
		if capacity.Free != nil {
			free = fmt.Sprint(*capacity.Free)
		}
		if capacity.Gpu != nil {
			gpu = *capacity.Gpu
		}
		fmt.Fprintf(cmd.OutOrStdout(), "profile: %s\ngpu: %s\nfree: %s\n", capacity.Profile, gpu, free)
		return nil
	},
}

var simulationSessionCmd = &cobra.Command{
	Use: "session",
	Example: `nova simulation session list
nova simulation session get my-session
nova simulation session open my-session`,
	Aliases: []string{"sessions"},
	Short:   "Manage your streaming sessions",
	Long: `Manage your streaming sessions.

A session is a lease on a GPU: when it expires the session is suspended (not deleted),
and a suspended session is removed after the broker's retention window.
Sessions are addressed by id or, if unique, by name.`,
}

var simulationSessionListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List your sessions",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := simulation.NewBroker(cmd.Context())
		if err != nil {
			return err
		}
		sessions, err := listSessions(cmd.Context(), c)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSTATUS\tCONFIG\tEXPIRES")
		for _, s := range sessions {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.Id, s.Name, s.Status, s.ConfigName, s.ExpiresAt.Local().Format("2006-01-02 15:04"))
		}
		return w.Flush()
	},
}

var simulationSessionGetCmd = &cobra.Command{
	Use: "get <session>",
	Example: `# by name (if unique) or by id
nova simulation session get my-session
nova simulation session get 66416162-239c-4aa0-b212-02b1323102cf`,
	Short:             "Show a session, including its stream routes",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeSimulationSession,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, id, err := brokerAndSession(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		resp, err := c.GetSessionWithResponse(cmd.Context(), id)
		if err := checkJSON(resp, err); err != nil {
			return err
		}
		return printYAML(cmd, resp.JSON200)
	},
}

var simulationSessionCreateFlags struct {
	app, profile, version, config, stage string
	minutes                              uint32
	wait                                 time.Duration
	open                                 bool
}

var simulationSessionCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Start a new session",
	Long: `Start a new session. "nova simulation apps" lists the apps, versions and profiles.

The name is cosmetic and need not be unique; the session id stays its identity.`,
	Example: `# Isaac Sim on a 24GB GPU slice, waiting until it is ready
nova simulation session create my-session --app nova-x-isaac --profile isaac-24gb --wait 10m`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		f := simulationSessionCreateFlags
		c, err := simulation.NewBroker(cmd.Context())
		if err != nil {
			return err
		}
		body := simulation.CreateSessionRequest{App: f.app, Profile: f.profile, Name: args[0]}
		if f.version != "" {
			body.Version = &f.version
		}
		if f.config != "" {
			body.ConfigName = &f.config
		}
		if f.stage != "" {
			body.UsdStageUri = &f.stage
		}
		if f.minutes > 0 {
			body.Minutes = &f.minutes
		}
		resp, err := c.CreateSessionWithResponse(cmd.Context(), body)
		if err := simulation.Check(resp, err); err != nil {
			return err
		}
		if resp.JSON201 == nil {
			return fmt.Errorf("broker: unexpected response: %s", string(resp.Body))
		}
		session := resp.JSON201
		if f.open && f.wait == 0 {
			f.wait = 15 * time.Minute
		}
		if f.wait > 0 && !session.Ready {
			if session, err = waitForSession(cmd, c, session.Id, f.wait); err != nil {
				return err
			}
		}
		if err := printYAML(cmd, session); err != nil {
			return err
		}
		if f.open {
			return openURL(cmd, c.StreamURL(session.Id.String()), false)
		}
		return nil
	},
}

var simulationSessionExtendMinutes uint32

var simulationSessionExtendCmd = &cobra.Command{
	Use: "extend <session>",
	Example: `# keep the session running for 2 more hours
nova simulation session extend my-session --minutes 120`,
	Short:             "Push the expiry of a session out",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeSimulationSession,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, id, err := brokerAndSession(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		resp, err := c.ExtendSessionWithResponse(cmd.Context(), id, simulation.ExtendRequest{Minutes: simulationSessionExtendMinutes})
		return printSessionResult(cmd, resp, err)
	},
}

var simulationSessionRenameCmd = &cobra.Command{
	Use:               "rename <session> <new-name>",
	Example:           `nova simulation session rename my-session demo-booth`,
	Short:             "Change the display name of a session",
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completeSimulationSession,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, id, err := brokerAndSession(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		resp, err := c.RenameSessionWithResponse(cmd.Context(), id, simulation.RenameRequest{Name: args[1]})
		return printSessionResult(cmd, resp, err)
	},
}

// sessionActionCmd builds the commands that only take a session id.
func sessionActionCmd(use, short string, call func(context.Context, *simulation.Broker, uuid.UUID) (simulation.Response, error)) *cobra.Command {
	return &cobra.Command{
		Use:               use + " <session>",
		Example:           `nova simulation session ` + use + ` my-session`,
		Short:             short,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeSimulationSession,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, id, err := brokerAndSession(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			resp, err := call(cmd.Context(), c, id)
			return printSessionResult(cmd, resp, err)
		},
	}
}

var (
	simulationSessionDeleteCmd = sessionActionCmd("delete", "Delete a session for good, freeing its GPU",
		func(ctx context.Context, c *simulation.Broker, id uuid.UUID) (simulation.Response, error) {
			r, err := c.DeleteSessionWithResponse(ctx, id)
			return r, err
		})
	simulationSessionSuspendCmd = sessionActionCmd("suspend", "Stop a session's pod to free the GPU, keeping the session",
		func(ctx context.Context, c *simulation.Broker, id uuid.UUID) (simulation.Response, error) {
			r, err := c.SuspendSessionWithResponse(ctx, id)
			return r, err
		})
	simulationSessionResumeCmd = sessionActionCmd("resume", "Start a suspended session again",
		func(ctx context.Context, c *simulation.Broker, id uuid.UUID) (simulation.Response, error) {
			r, err := c.ResumeSessionWithResponse(ctx, id)
			return r, err
		})
	simulationSessionRestartCmd = sessionActionCmd("restart", "Restart a session's pod",
		func(ctx context.Context, c *simulation.Broker, id uuid.UUID) (simulation.Response, error) {
			r, err := c.RestartSessionWithResponse(ctx, id)
			return r, err
		})
)

var simulationSessionOpenPrint bool

var simulationSessionOpenCmd = &cobra.Command{
	Use: "open <session>",
	Example: `# open the stream in the browser
nova simulation session open my-session

# only print the viewer URL, e.g. to share it
nova simulation session open my-session --print`,
	Short:             "Open the stream of a session in the browser",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeSimulationSession,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, id, err := brokerAndSession(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return openURL(cmd, c.StreamURL(id.String()), simulationSessionOpenPrint)
	},
}

var simulationSessionAPIFlags struct {
	requestFlags
	spec bool
}

var simulationSessionAPICmd = &cobra.Command{
	Use:   "api <session> [api [path]]",
	Short: "List the APIs inside a session, download their OpenAPI spec or send a request",
	Long: `List the HTTP APIs running inside a session: the Kit stream API ("stream") and the
Isaac Sim extension API ("omniservice"). With an api name, print its base URL, or with
--spec its OpenAPI spec. The stream page in the viewer has an API explorer for them.

With a path, send an authenticated request to the api and print the answer, like
'nova api': the path is relative to the api's base URL (/status goes to
<base-url>/status) and the request carries the simulation login ('nova simulation
login'), which only goes to the session's host.

Fields:
  -f key=value     string field
  -F key=value     typed field: true, false, null and numbers become JSON literals,
                   @file (or @- for stdin) reads the value from a file
  Nested keys like gh: pose[position][]=1 or items[][name]=a. Fields make the request a
  POST unless -X is given; with GET (or together with --input) they go to the query.
  --input file     the request body from a file (- for stdin)

The answer goes to stdout as it arrives; with an HTTP error status the body is still
printed and nova exits with 1. Binary answers need --output. WebSocket endpoints are
not supported: this sends plain HTTP requests.`,
	Example: `# list the APIs of a session
nova simulation session api my-session

# save the OpenAPI spec of the Isaac Sim extension API
nova simulation session api my-session omniservice --spec -o omniservice.json

# call an API
nova simulation session api my-session omniservice /status
nova simulation session api my-session stream /some/endpoint -X POST -F enabled=true

# print a request without sending it
nova simulation session api my-session omniservice /some/endpoint -f name=x --dry-run`,
	Args:              cobra.RangeArgs(1, 3),
	ValidArgsFunction: completeSimulationSessionAPI,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := &simulationSessionAPIFlags
		if len(args) == 3 {
			if f.spec {
				return errors.New("--spec and a path don't go together")
			}
			if !strings.HasPrefix(args[2], "/") || strings.HasPrefix(args[2], "//") {
				return fmt.Errorf("the path must start with a single '/', relative to the api's base URL, e.g. /status")
			}
			if strings.Contains(args[2], "#") {
				return fmt.Errorf("the path %q has a fragment (#), which is never sent: escape it as %%23", args[2])
			}
			if leavesBase(args[2]) {
				return fmt.Errorf("the path %q leaves the api's base URL: '.' and '..' segments are not allowed", args[2])
			}
		} else if name := changedRequestFlag(cmd); name != "" {
			return fmt.Errorf("--%s needs a path: nova simulation session api <session> <api> <path>", name)
		}
		var prepared *preparedRequest
		if len(args) == 3 {
			// before the broker lookups: bad fields fail without network
			var err error
			if prepared, err = prepareRequest(&f.requestFlags, args[2]); err != nil {
				return err
			}
		}

		c, id, err := brokerAndSession(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		resp, err := c.GetSessionWithResponse(cmd.Context(), id)
		if err := checkJSON(resp, err); err != nil {
			return err
		}
		apis, err := simulation.SessionAPIs(resp.JSON200)
		if err != nil {
			return err
		}

		if len(args) == 1 {
			if f.spec {
				return errors.New("--spec needs an api name: stream or omniservice")
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "API\tBASE URL\tTITLE")
			for _, a := range apis {
				fmt.Fprintf(w, "%s\t%s\t%s\n", a.Name, a.BaseURL, a.Title)
			}
			return w.Flush()
		}

		for _, a := range apis {
			if a.Name != args[1] {
				continue
			}
			switch {
			case prepared != nil:
				client, err := novaapi.NewBearerClient(a.BaseURL, c.Token)
				if err != nil {
					return err
				}
				hint := "run 'nova simulation login'"
				return prepared.send(cmd, client, nil, &f.requestFlags, apiLogin{hint: hint, on401: hint})
			case f.spec:
				return downloadSpec(cmd, a.SpecURL(), c.Token, f.output)
			default:
				fmt.Fprintln(cmd.OutOrStdout(), a.BaseURL)
				return nil
			}
		}
		return fmt.Errorf("unknown api %q, expected stream or omniservice", args[1])
	},
}

// leavesBase reports whether a path (without query) has '.' or '..' segments,
// which a server may resolve to a path outside the api's base URL.
func leavesBase(path string) bool {
	p, _, _ := strings.Cut(path, "?")
	for _, seg := range strings.Split(p, "/") {
		if u, err := url.PathUnescape(seg); err == nil {
			seg = u
		}
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// changedRequestFlag returns the name of a flag given on the command line that
// only applies to a request (all request flags but --output, which --spec uses).
func changedRequestFlag(cmd *cobra.Command) string {
	for _, name := range []string{"method", "raw-field", "field", "header", "input", "include", "silent", "dry-run", "timeout"} {
		if cmd.Flags().Changed(name) {
			return name
		}
	}
	return ""
}

var simulationAPIFlags struct {
	open, print, spec bool
	output            string
}

var simulationAPICmd = &cobra.Command{
	Use:   "api",
	Short: "Show, open or download the API docs of the streaming session broker",
	Example: `# open the interactive API docs of the broker
nova simulation api --open

# save the broker's OpenAPI spec
nova simulation api --spec -o broker.json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := simulation.NewBroker(cmd.Context())
		if err != nil {
			return err
		}
		broker := strings.TrimSuffix(c.Broker, "/")
		switch {
		case simulationAPIFlags.spec:
			return downloadSpec(cmd, broker+"/openapi.json", c.Token, simulationAPIFlags.output)
		case simulationAPIFlags.open:
			return openURL(cmd, broker+"/docs", false)
		default:
			fmt.Fprintf(cmd.OutOrStdout(), "url: %s\ndocs: %s/docs\nspec: %s/openapi.json\n", broker, broker, broker)
			return nil
		}
	},
}

var simulationConfigCmd = &cobra.Command{
	Use: "config",
	Example: `nova simulation config list
nova simulation config get nova-x-isaac/default > isaac.json
nova simulation config set nova-x-isaac/default -f isaac.json`,
	Short: "Manage the saved application configs sessions load and autosave to",
}

var simulationConfigListApp string

var simulationConfigListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List your saved configs",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := simulation.NewBroker(cmd.Context())
		if err != nil {
			return err
		}
		params := &simulation.ListConfigsParams{}
		if simulationConfigListApp != "" {
			params.App = &simulationConfigListApp
		}
		var configs []simulation.ConfigRef
		err = paginate(func(offset uint) (int, bool, error) {
			params.Offset = &offset
			resp, err := c.ListConfigsWithResponse(cmd.Context(), params)
			if err := checkJSON(resp, err); err != nil {
				return 0, false, err
			}
			configs = append(configs, resp.JSON200.Value...)
			return len(resp.JSON200.Value), resp.JSON200.NextLink != nil, nil
		})
		if err != nil {
			return err
		}
		for _, cfg := range configs {
			fmt.Fprintf(cmd.OutOrStdout(), "%s/%s\n", cfg.App, cfg.Name)
		}
		return nil
	},
}

var simulationConfigGetCmd = &cobra.Command{
	Use:     "get <app>/<name>",
	Example: `nova simulation config get nova-x-isaac/default`,
	Short:   "Print a saved config as JSON",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, name, err := splitConfigRef(args[0])
		if err != nil {
			return err
		}
		c, err := simulation.NewBroker(cmd.Context())
		if err != nil {
			return err
		}
		resp, err := c.GetConfigWithResponse(cmd.Context(), app, name)
		if err := simulation.Check(resp, err); err != nil {
			return err
		}
		var out bytes.Buffer
		if err := json.Indent(&out, resp.Body, "", "  "); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), out.String())
		return nil
	},
}

var simulationConfigSetFile string

var simulationConfigSetCmd = &cobra.Command{
	Use:   "set <app>/<name> -f <file>",
	Short: "Replace a saved config with a JSON file",
	Example: `# edit the default Isaac Sim config
nova simulation config get nova-x-isaac/default > isaac.json
nova simulation config set nova-x-isaac/default -f isaac.json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, name, err := splitConfigRef(args[0])
		if err != nil {
			return err
		}
		var data []byte
		if simulationConfigSetFile == "-" {
			data, err = io.ReadAll(cmd.InOrStdin())
		} else {
			data, err = os.ReadFile(simulationConfigSetFile)
		}
		if err != nil {
			return err
		}
		var obj map[string]any
		if err := json.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("config must be a JSON object: %w", err)
		}
		c, err := simulation.NewBroker(cmd.Context())
		if err != nil {
			return err
		}
		resp, err := c.PutConfigWithResponse(cmd.Context(), app, name, obj)
		return simulation.Check(resp, err)
	},
}

var simulationConfigDeleteCmd = &cobra.Command{
	Use:     "delete <app>/<name>",
	Example: `nova simulation config delete nova-x-isaac/old-setup`,
	Short:   "Delete a saved config",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, name, err := splitConfigRef(args[0])
		if err != nil {
			return err
		}
		c, err := simulation.NewBroker(cmd.Context())
		if err != nil {
			return err
		}
		resp, err := c.DeleteConfigWithResponse(cmd.Context(), app, name)
		return simulation.Check(resp, err)
	},
}

func init() {
	rootCmd.AddCommand(simulationCmd)
	simulationCmd.AddCommand(simulationLoginCmd, simulationLogoutCmd, simulationTokenCmd,
		simulationAppsCmd, simulationCapacityCmd, simulationSessionCmd, simulationConfigCmd, simulationAPICmd)
	simulationSessionCmd.AddCommand(simulationSessionListCmd, simulationSessionGetCmd, simulationSessionCreateCmd,
		simulationSessionDeleteCmd, simulationSessionRenameCmd, simulationSessionExtendCmd,
		simulationSessionSuspendCmd, simulationSessionResumeCmd, simulationSessionRestartCmd,
		simulationSessionOpenCmd, simulationSessionAPICmd)
	simulationConfigCmd.AddCommand(simulationConfigListCmd, simulationConfigGetCmd,
		simulationConfigSetCmd, simulationConfigDeleteCmd)

	envs := make([]string, 0, len(simulation.Environments()))
	for _, e := range simulation.Environments() {
		envs = append(envs, string(e))
	}
	simulationLoginCmd.Flags().StringVarP(&simulationLoginEnv, "env", "e", string(simulation.EnvDev),
		"Environment to log in to ("+strings.Join(envs, ", ")+")")

	f := simulationSessionCreateCmd.Flags()
	f.StringVar(&simulationSessionCreateFlags.app, "app", "", "Application id, e.g. nova-x-isaac (required)")
	f.StringVar(&simulationSessionCreateFlags.profile, "profile", "", "Runtime profile, e.g. isaac-24gb (required)")
	f.StringVar(&simulationSessionCreateFlags.version, "version", "", "Application version (default: the broker's default)")
	f.StringVar(&simulationSessionCreateFlags.config, "config", "", `Saved config to load and autosave to (default "default")`)
	f.StringVar(&simulationSessionCreateFlags.stage, "stage", "", "USD stage to load (omniverse://...)")
	f.Uint32Var(&simulationSessionCreateFlags.minutes, "minutes", 0, "Lease length in minutes (default: the broker's initial length)")
	f.DurationVar(&simulationSessionCreateFlags.wait, "wait", 0, "Wait up to this long for the session to become ready, e.g. 10m")
	f.BoolVar(&simulationSessionCreateFlags.open, "open", false, "Open the stream in the browser once the session is ready (waits 15m unless --wait is set)")
	_ = simulationSessionCreateCmd.MarkFlagRequired("app")
	_ = simulationSessionCreateCmd.MarkFlagRequired("profile")

	simulationSessionExtendCmd.Flags().Uint32Var(&simulationSessionExtendMinutes, "minutes", 0, "Minutes to push the expiry out (required)")
	_ = simulationSessionExtendCmd.MarkFlagRequired("minutes")

	simulationSessionOpenCmd.Flags().BoolVar(&simulationSessionOpenPrint, "print", false, "Only print the URL")
	addRequestFlags(simulationSessionAPICmd, &simulationSessionAPIFlags.requestFlags, "File to write the spec (--spec) or the response body to (default: stdout)",
		"Print the request as JSON without sending it")
	simulationSessionAPICmd.Flags().BoolVar(&simulationSessionAPIFlags.spec, "spec", false, "Download the OpenAPI spec of the api")
	simulationSessionAPICmd.Flags().DurationVar(&simulationSessionAPIFlags.timeout, "timeout", time.Minute, "How long to wait for the answer's headers (0: no limit)")

	simulationAPICmd.Flags().BoolVar(&simulationAPIFlags.open, "open", false, "Open the interactive API docs in the browser")
	simulationAPICmd.Flags().BoolVar(&simulationAPIFlags.spec, "spec", false, "Download the OpenAPI spec")
	simulationAPICmd.Flags().StringVarP(&simulationAPIFlags.output, "output", "o", "", "File to write the spec to (default: stdout)")
	simulationAPICmd.MarkFlagsMutuallyExclusive("open", "spec")

	simulationConfigListCmd.Flags().StringVar(&simulationConfigListApp, "app", "", "Only configs of this application")
	simulationConfigSetCmd.Flags().StringVarP(&simulationConfigSetFile, "file", "f", "", `JSON file with the config, "-" for stdin (required)`)
	_ = simulationConfigSetCmd.MarkFlagRequired("file")
}

// checkJSON checks a response that must carry a JSON200 body.
func checkJSON[T interface {
	simulation.Response
	GetJSON200() *J
}, J any](resp T, err error) error {
	if err := simulation.Check(resp, err); err != nil {
		return err
	}
	if resp.GetJSON200() == nil {
		return fmt.Errorf("broker: unexpected response: %s", string(resp.GetBody()))
	}
	return nil
}

// paginate calls page with increasing offsets until it reports no more items.
func paginate(page func(offset uint) (n int, more bool, err error)) error {
	var offset uint
	for {
		n, more, err := page(offset)
		if err != nil {
			return err
		}
		if !more || n == 0 {
			return nil
		}
		offset += uint(n)
	}
}

func listSessions(ctx context.Context, c *simulation.Broker) ([]simulation.Lease, error) {
	var sessions []simulation.Lease
	err := paginate(func(offset uint) (int, bool, error) {
		resp, err := c.ListSessionsWithResponse(ctx, &simulation.ListSessionsParams{Offset: &offset})
		if err := checkJSON(resp, err); err != nil {
			return 0, false, err
		}
		sessions = append(sessions, resp.JSON200.Value...)
		return len(resp.JSON200.Value), resp.JSON200.NextLink != nil, nil
	})
	return sessions, err
}

// brokerAndSession returns a broker client and the id of the session given by
// id or unique name.
func brokerAndSession(ctx context.Context, ref string) (*simulation.Broker, uuid.UUID, error) {
	c, err := simulation.NewBroker(ctx)
	if err != nil {
		return nil, uuid.Nil, err
	}
	if id, err := uuid.Parse(ref); err == nil {
		return c, id, nil
	}
	sessions, err := listSessions(ctx, c)
	if err != nil {
		return nil, uuid.Nil, err
	}
	var matches []simulation.Lease
	for _, s := range sessions {
		if s.Name == ref {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return nil, uuid.Nil, fmt.Errorf("no session with id or name %q", ref)
	case 1:
		return c, matches[0].Id, nil
	default:
		return nil, uuid.Nil, fmt.Errorf("%d sessions are named %q, use the session id", len(matches), ref)
	}
}

// printSessionResult checks a session action and prints the session if the
// broker returned it.
func printSessionResult(cmd *cobra.Command, resp simulation.Response, err error) error {
	if err := simulation.Check(resp, err); err != nil {
		return err
	}
	body := resp.GetBody()
	var session simulation.SessionResponse
	if len(body) > 0 && json.Unmarshal(body, &session) == nil && session.Id != uuid.Nil {
		return printYAML(cmd, &session)
	}
	return nil
}

func waitForSession(cmd *cobra.Command, c *simulation.Broker, id uuid.UUID, timeout time.Duration) (*simulation.SessionResponse, error) {
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	cmd.PrintErrf("Waiting for session %s to become ready...\n", id)
	for {
		resp, err := c.GetSessionWithResponse(ctx, id)
		if err := checkJSON(resp, err); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("session %s not ready after %s", id, timeout)
			}
			return nil, err
		}
		if resp.JSON200.Ready {
			return resp.JSON200, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("session %s not ready after %s (condition: %s)", id, timeout, resp.JSON200.Condition)
		case <-time.After(5 * time.Second):
		}
	}
}

// openURL opens url in the browser, or only prints it.
func openURL(cmd *cobra.Command, url string, printOnly bool) error {
	fmt.Fprintln(cmd.OutOrStdout(), url)
	if printOnly {
		return nil
	}
	return browser.OpenURL(url)
}

// downloadSpec writes the OpenAPI spec at url to output, or stdout if empty.
func downloadSpec(cmd *cobra.Command, url, token, output string) error {
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	_ = version.SetUserAgent(cmd.Context(), req)
	resp, err := httpx.Client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK || !json.Valid(body) {
		return fmt.Errorf("fetching %s: HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out bytes.Buffer
	if err := json.Indent(&out, body, "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	if output == "" {
		_, err = cmd.OutOrStdout().Write(out.Bytes())
		return err
	}
	return os.WriteFile(output, out.Bytes(), 0o644)
}

func splitConfigRef(ref string) (app, name string, err error) {
	app, name, ok := strings.Cut(ref, "/")
	if !ok || app == "" || name == "" {
		return "", "", fmt.Errorf("expected <app>/<name>, got %q", ref)
	}
	return app, name, nil
}

func printYAML(cmd *cobra.Command, v any) error {
	// via JSON, so the yaml keys match the API's field names
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return err
	}
	out, err := yaml.Marshal(generic)
	if err != nil {
		return err
	}
	_, err = cmd.OutOrStdout().Write(out)
	return err
}

func completeSimulationSessionAPI(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return completeSimulationSession(cmd, args, toComplete)
	case 1:
		return []string{"stream\tKit stream API", "omniservice\tIsaac Sim extension API"}, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func completeSimulationSession(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	c, err := simulation.NewBroker(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	sessions, err := listSessions(cmd.Context(), c)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	completions := make([]string, 0, len(sessions))
	for _, s := range sessions {
		completions = append(completions, s.Id.String()+"\t"+s.Name+" ("+s.Status+")")
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
