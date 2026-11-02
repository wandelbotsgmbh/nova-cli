package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
)

var authEnvironment string
var authRefreshToken string

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to portal.wandelbots.io",
	Long: `Log in to portal.wandelbots.io.

You have the option to choose between an interactive login or directly provide a refresh token for non-interactive environments like CI/CD pipelines.
For wandelbots internal development purposes, you can also specify the environment to login into via --env (dev, stg, prod) flag.
Each environment keeps its own login; the last one you log in to becomes the default for
commands that don't target a cloud instance. Cloud instances remember their environment,
and --env / NOVA_ENV override it for a single command.
`,
	Example: `
# Login via interactive device auth flow
nova login

# Login via provided refresh token.
# To optain such a refresh token you can do "nova login" on your local machine
# and after successful login copy the token from "~/.nova/config.yaml" file.
nova login --token <your-refresh-token>

# Login into a specific environment (dev, stg, prod)
nova login --env dev
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		name := authEnvironment
		if name == "" {
			name = os.Getenv(envVarName)
		}
		if name == "" {
			name = string(oauth.AuthProd)
		}
		env, err := oauth.ParseEnvironment(name)
		if err != nil {
			return err
		}
		oauth.SetActiveEnvironment(env)

		if len(authRefreshToken) > 0 {
			// login via provided refresh token
			err = oauth.LoginWithProvidedRefreshToken(ctx, env, authRefreshToken)
		} else {
			// login via device auth flow or refresh existing token if token already exists
			err = oauth.LoginWithRefreshToken(ctx, env, true)
		}
		if err != nil {
			return err
		}

		// the last explicit login sets the default environment (as before, where the
		// single login decided it); logins of other environments are kept
		if err := oauth.SetDefaultEnvironment(env); err != nil {
			return err
		}
		cmd.Printf("Login successful (%s)\n", env)
		return nil
	},
}

var viewTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Prints the a valid login token.",
	Long: `This will print the valid login token, if you are logged in.
If the token is expired, it will be automatically refreshed and a valid token is returned.
If you were not logged in before, it will trigger the login process.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := oauth.RefreshTokenOrLogin("https://" + oauth.WABO_CLOUD_HOST)
		if err != nil {
			return err
		}

		if token, err := oauth.LoadToken(); err == nil && token.OAuth2Token.AccessToken != "" {
			fmt.Println(token.OAuth2Token.AccessToken)
		} else {
			return errors.New("failed to optain access token")
		}
		return nil
	},
}

func init() {
	loginCmd.AddCommand(viewTokenCmd)
	rootCmd.AddCommand(loginCmd)

	loginCmd.Flags().StringVarP(&authEnvironment, "env", "e", "", "For wandelbots internal development purposes. Environment to login into: dev, stg, prod (default: NOVA_ENV, else prod)")
	loginCmd.Flags().StringVarP(&authRefreshToken, "token", "t", "", "Provide a refreshtoken to login with. This will skip the interactive login process and will optain a new access token using the provided refresh token. This is useful for CI/CD pipelines or other non-interactive environments.")
}
