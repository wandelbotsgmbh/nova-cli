package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	apiv1 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova"
	apiv2 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova/v2"
	"github.com/wandelbotsgmbh/nova-cli/pkg/appenv"
	"github.com/wandelbotsgmbh/nova-cli/pkg/credentials"
	"github.com/wandelbotsgmbh/nova-cli/pkg/docker"
	"github.com/wandelbotsgmbh/nova-cli/pkg/editor"
	"github.com/wandelbotsgmbh/nova-cli/pkg/generator"
	"github.com/wandelbotsgmbh/nova-cli/pkg/instances"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nova"
	"github.com/wandelbotsgmbh/nova-cli/pkg/oauth"
	"github.com/wandelbotsgmbh/nova-cli/pkg/remote"
	"github.com/wandelbotsgmbh/nova-cli/pkg/specfile"
	"github.com/wandelbotsgmbh/nova-cli/pkg/utils"
	"github.com/wandelbotsgmbh/nova-cli/pkg/version"

	"gopkg.in/yaml.v3"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	appProjectNameDefault    = "your-nova-app"
	appUseNovaV2SinceVersion = "25.7.0"
)

var (
	// flags
	appGenerator       string
	appSecretName      string
	appOmitCredentials bool
	appCopyImage       bool
	appCopySSHUser     string
	appCopySSHPort     int
	appCopyNoSudo      bool
	appCopyImportCmd   string
	appCopyRemoteDir   string
	appSetupSSHKey     bool
	appPlatform        string
)

var appDefaultSecrets = []string{
	"pull-secret-wandelbots-azurecr-io",
	"pull-secret-registry-1-docker-io",
	"pull-secret-container-registry-wabo-run",
}

// The .nova file gets read from the local directory and contains required field for servicemanager requests.
type DotNovaApp struct {
	Name               string                `yaml:"name"`
	GeneratedTimestamp string                `yaml:"generatedTimestamp"`
	Type               string                `yaml:"type"`
	DockerContextPath  string                `yaml:"dockerContextPath"`
	AppIcon            string                `yaml:"appIcon,omitempty"`
	Port               int                   `yaml:"port,omitempty"`
	ImageRegistry      string                `yaml:"imageRegistry,omitempty"`
	RegistryAuth       DotNovaAuth           `yaml:"registryAuth,omitempty"`
	Storage            DotNovaAppStorage     `yaml:"storage,omitempty"`
	Environment        DotNovaAppEnvironment `yaml:"environment,omitempty"`
	Resources          DotNovaAppResources   `yaml:"resources,omitempty"`
}

type DotNovaAppEnvironment = []struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type DotNovaAppStorage struct {
	Capacity  string `yaml:"capacity"`
	MountPath string `yaml:"mountPath"`
}

type DotNovaAppContext struct {
	NovaApp           DotNovaApp
	DockerContextPath string
}

// Additional resources that the application requires.
type DotNovaAppResources struct {
	IntelGpu    int    `yaml:"intel-gpu,omitempty"`
	MemoryLimit string `yaml:"memory-limit,omitempty"`
}

type DotNovaAuth struct {
	Username string   `yaml:"username"`
	Password string   `yaml:"password,omitempty"`
	Secrets  []string `yaml:"secrets,omitempty"`
}

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Create and install apps on Wandelbots NOVA OS",
	Long:  `Create and install apps on Wandelbots NOVA OS`,
}

var createAppCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Creates a new app for Wandelbots NOVA OS",
	Long:  createAppTxtLong,
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := appProjectNameDefault
		if len(args) > 0 {
			appName = strings.TrimSpace(args[0])
		}
		path, err := createAppProject(appName)
		if err != nil || *appCreateOutput != outputJSON {
			return err
		}
		return writeJSON(cmd, createdApp{Name: appName, Generator: appGenerator, Path: absPath(path)})
	},
}

// --reset and -o/--output of 'nova app update-env'
var (
	appEnvReset        bool
	appUpdateEnvOutput *outputFormat
)

var updateEnvAppCmd = &cobra.Command{
	Use:     "update-env [path]",
	Short:   "Writes the local development settings of an app project",
	Long:    updateEnvAppTxtLong,
	Example: updateEnvAppTxtExample,
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) > 0 {
			dir = args[0]
		}
		if *appUpdateEnvOutput != outputJSON {
			return writeAppEnv(cmd, dir, appEnvReset)
		}
		r, err := updateAppEnv(dir, appEnvReset)
		if err != nil {
			return err
		}
		return writeJSON(cmd, r)
	},
}

var installAppCmd = &cobra.Command{
	Use:     "install [source]",
	Short:   "Installs an app to Wandelbots NOVA OS",
	Long:    installAppTxtLong,
	Example: installAppTxtExample,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := currentHost()
		if host == "" {
			return fmt.Errorf("please provide the nova host via `nova config set host <host>`")
		}

		cellname := currentCell()

		if appSetupSSHKey && !appCopyImage {
			return fmt.Errorf("--setup-ssh-key only applies together with --copy-image")
		}

		if err := docker.ValidatePlatform(appPlatform); err != nil {
			return err
		}

		if len(args) > 0 && specfile.IsSpec(args[0]) {
			// install or update the app directly from provided json or yaml file
			return installFromExistingFile(host, cellname, args[0])
		}

		rt, err := docker.Detect(viper.GetString("container-runtime"))
		if err != nil {
			return err
		}

		appContext, err := getAppContext(args)
		if err != nil {
			return err
		}

		if appCopyImage {
			return installAppViaCopy(host, cellname, appContext, rt)
		}

		imageName := docker.CreateImageName(appContext.NovaApp.ImageRegistry, appContext.NovaApp.Name)

		applySecretFromFlag(&appContext)

		if !hasRegistryAuth(appContext.NovaApp) {
			creds, err := rt.GetRegistryCredentials(imageName)
			if err != nil {
				return fmt.Errorf("failed to read credentials for registry: %w", err)
			}
			appContext.NovaApp.RegistryAuth.Username = creds.Username
			appContext.NovaApp.RegistryAuth.Password = creds.Password
		}

		err = rt.BuildImage(appContext.DockerContextPath, imageName, appPlatform)
		if err != nil {
			return err
		}

		digest, err := rt.PushImage(imageName)
		if err != nil {
			return err
		}

		completeImageName := imageName + "@" + digest

		return installApp(host, cellname, completeImageName, appContext)
	},
}

func hasRegistryAuth(app DotNovaApp) bool {
	return app.RegistryAuth.Username != "" || app.RegistryAuth.Password != "" || len(app.RegistryAuth.Secrets) > 0
}

// Installs the app without pushing to a registry: the image is exported,
// copied to the instance via scp and imported into k3s containerd via ssh.
// By default only the image layers the instance is missing are transferred; if
// that incremental path fails for any reason it falls back to copying the whole
// image, so behaviour never regresses.
func installAppViaCopy(host string, cellname string, appContext DotNovaAppContext, rt docker.Runtime) error {
	if !remote.IsOpenSSHInstalled() {
		return fmt.Errorf("--copy-image requires the OpenSSH client (ssh and scp) in PATH. It is preinstalled on macOS, Linux and Windows 10 1809+; on Windows it can be enabled via `Add-WindowsCapability -Online -Name OpenSSH.Client*`")
	}

	imageName := docker.CreateLocalImageName(appContext.NovaApp.ImageRegistry, appContext.NovaApp.Name, time.Now())

	// the image is present on the instance after the import, so no pull credentials/secrets are needed
	appContext.NovaApp.RegistryAuth = DotNovaAuth{}

	target := remote.Target{
		Host: stripHostScheme(host),
		User: appCopySSHUser,
		Port: appCopySSHPort,
	}

	if appSetupSSHKey {
		if err := remote.SetupSSHAccess(target, firstToken(appCopyImportCmd), !appCopyNoSudo); err != nil {
			return err
		}
	}

	// With sudo, resolve the containerd tool's absolute path: it removes the
	// need for a PATH hack (sudo's secure_path lacks /usr/local/bin) and lets a
	// binary-scoped NOPASSWD sudoers rule match, so no password is needed.
	importCmd := appCopyImportCmd
	if !appCopyNoSudo {
		if abs, err := remote.ResolveCommandPath(target, firstToken(appCopyImportCmd)); err == nil && abs != "" {
			importCmd = replaceFirstToken(appCopyImportCmd, abs)
		}
	}

	if err := copyIncremental(target, imageName, appContext, importCmd, rt); err != nil {
		fmt.Fprintf(os.Stderr, "Incremental copy unavailable (%v); falling back to full image transfer.\n", err)
		if err := copyWholeImage(target, imageName, appContext, importCmd, rt); err != nil {
			return err
		}
	}

	return installApp(host, cellname, imageName, appContext)
}

func firstToken(s string) string {
	if fields := strings.Fields(s); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func replaceFirstToken(s string, repl string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return repl
	}
	fields[0] = repl
	return strings.Join(fields, " ")
}

// ctrBaseFromImportCommand derives the containerd control tool (e.g. "k3s ctr")
// from the configured import command ("k3s ctr images import"). The incremental
// path needs it for `content ls`/`content get`; a custom import command that
// doesn't follow the "<ctr> images import" shape returns false so the caller
// falls back to the whole-image path.
func ctrBaseFromImportCommand(importCmd string) (string, bool) {
	base, ok := strings.CutSuffix(strings.TrimSpace(importCmd), " images import")
	if !ok || base == "" {
		return "", false
	}
	return base, true
}

// copyIncremental builds the image as an OCI layout, transfers only the layers
// missing from the instance's content store, reassembles the image there and
// imports it.
func copyIncremental(target remote.Target, imageName string, appContext DotNovaAppContext, importCmd string, rt docker.Runtime) error {
	ctrBase, ok := ctrBaseFromImportCommand(importCmd)
	if !ok {
		return fmt.Errorf("custom --import-command is not compatible with incremental copy")
	}

	// build the image as an OCI layout (docker and podman both supported); use a
	// non-existent subpath so each runtime creates the layout dir itself
	ociParent, err := os.MkdirTemp("", "nova-app-oci-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(ociParent)
	ociDir := filepath.Join(ociParent, "layout")

	if err := rt.BuildImageOCI(appContext.DockerContextPath, imageName, appPlatform, ociDir); err != nil {
		return err
	}

	blobs, err := docker.ReadImageBlobs(ociDir, appPlatform)
	if err != nil {
		return err
	}

	present, err := remote.RemoteContentDigests(target, ctrBase, appCopyRemoteDir, !appCopyNoSudo)
	if err != nil {
		return err
	}

	// the full non-layer descriptor graph (any intermediate indexes + manifest +
	// config) is tiny and always shipped, so the partial layout stays valid even
	// when buildx wraps the manifest in an inner index; layers are shipped only
	// when their digest is not already in the remote content store
	include := map[string]bool{
		blobs.Manifest.Digest.String(): true,
		blobs.Config.Digest.String():   true,
	}
	for _, idx := range blobs.Indexes {
		include[idx.Digest.String()] = true
	}
	var fromStoreHex []string
	var sentBytes, totalBytes int64
	for _, layer := range blobs.Layers {
		totalBytes += layer.Size
		d := layer.Digest.String()
		if present[d] {
			fromStoreHex = append(fromStoreHex, utils.DigestHex(d))
		} else {
			include[d] = true
			sentBytes += layer.Size
		}
	}

	reused := len(fromStoreHex)
	fmt.Fprintf(os.Stderr, "Reusing %d/%d layers already on the instance; transferring %s of %s.\n",
		reused, len(blobs.Layers), utils.HumanBytes(sentBytes), utils.HumanBytes(totalBytes))

	partial, err := os.CreateTemp("", "nova-app-image-*.tar.gz")
	if err != nil {
		return err
	}
	partial.Close()
	defer os.Remove(partial.Name())

	if err := docker.WritePartialOCI(ociDir, partial.Name(), include); err != nil {
		return err
	}

	return remote.CopyAndImportIncremental(remote.IncrementalImportOptions{
		Target:       target,
		RemoteDir:    appCopyRemoteDir,
		UseSudo:      !appCopyNoSudo,
		CtrCommand:   ctrBase,
		FromStoreHex: fromStoreHex,
	}, partial.Name())
}

// copyWholeImage exports and transfers the entire image (the original,
// dependency-free path used as a fallback).
func copyWholeImage(target remote.Target, imageName string, appContext DotNovaAppContext, importCmd string, rt docker.Runtime) error {
	if err := rt.BuildImage(appContext.DockerContextPath, imageName, appPlatform); err != nil {
		return err
	}

	archive, err := os.CreateTemp("", "nova-app-image-*.tar.gz")
	if err != nil {
		return err
	}
	archive.Close()
	defer os.Remove(archive.Name())

	fmt.Fprintf(os.Stderr, "Saving image %s to local archive...\n", imageName)
	if err := docker.SaveImageGzipped(rt.Name(), imageName, archive.Name()); err != nil {
		return err
	}

	return remote.CopyAndImportImage(remote.ImportImageOptions{
		Target:        target,
		RemoteDir:     appCopyRemoteDir,
		UseSudo:       !appCopyNoSudo,
		ImportCommand: importCmd,
	}, archive.Name())
}

func stripHostScheme(host string) string {
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	return strings.TrimSuffix(host, "/")
}

var deleteAppCmd = &cobra.Command{
	Use:               "delete <name>",
	Short:             "Deletes an app from Wandelbots NOVA OS",
	Long:              `Deletes an app from Wandelbots NOVA OS"`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeApp,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := currentHost()
		cell := currentCell()

		appName := args[0]
		err := nova.DeleteApp(host, cell, appName)
		if err != nil {
			return err
		}
		return nil
	},
}

var listAppsCmd = &cobra.Command{
	Use:   "list",
	Short: "Lists all installed apps",
	Long:  `Lists all installed apps."`,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := currentHost()
		cell := currentCell()

		cellExists, err := nova.CellExists(host, cell)
		if err != nil {
			return err
		}
		if !cellExists {
			return fmt.Errorf("default cell '%s' not found", cell)
		}

		list, err := nova.ListApps(host, cell)
		if err != nil {
			return err
		}
		return writeNames(cmd, appListOutput, list)
	},
}

var editAppCmd = &cobra.Command{
	Use:               "edit <name>",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeApp,
	Short:             "edit an app configuration",
	Long:              editAppTxtLong,
	RunE: func(cmd *cobra.Command, args []string) error {
		host := currentHost()
		cell := currentCell()
		appName := args[0]

		app, err := nova.GetAppYaml(host, cell, appName)
		if err != nil {
			return err
		}

		e := editor.NewDefaultEditor()
		newContent, err := e.EditInTempfile("edit", "app.yaml", app)
		if err != nil {
			return err
		}

		if bytes.Equal(app, newContent) {
			return nil
		}

		return nova.UpdateAppConfigurationFromYaml(host, cell, appName, newContent)
	},
}

// -o/--output of 'app list' and 'app create'
var appListOutput, appCreateOutput *outputFormat

func init() {
	appListOutput = addOutputFlag(listAppsCmd)
	appCreateOutput = addOutputFlag(createAppCmd)
	appUpdateEnvOutput = addOutputFlag(updateEnvAppCmd)
	rootCmd.AddCommand(appCmd)

	// subcommands
	appCmd.AddCommand(createAppCmd)
	appCmd.AddCommand(installAppCmd)
	appCmd.AddCommand(listAppsCmd)
	appCmd.AddCommand(deleteAppCmd)
	appCmd.AddCommand(editAppCmd)
	appCmd.AddCommand(updateEnvAppCmd)
	updateEnvAppCmd.Flags().BoolVar(&appEnvReset, "reset", false, "Also replace NOVA_API, CELL_NAME and BASE_PATH (or the project type's equivalents) with the values of the targeted instance, e.g. after 'nova instance select'.")

	// TODO generate this flag somehow, with the available generators?
	// Otherwise possible apps needs to be put into the readme by hand.
	createAppCmd.Flags().StringVarP(&appGenerator, "generator", "g", "python_app", "The generator to use for the app project. Supported: nextjs_app, python_app, golang_app, rust_app")

	installAppCmd.Flags().StringVarP(&appSecretName, "registry-secret", "r", "",
		"Provide a custom secret name for instead of optaining credentials from your local docker config. Make sure the secret exists in the cell namespace.")

	installAppCmd.Flags().BoolVarP(&appOmitCredentials, "omit-credentials", "o", false, "Omit obtaining registry credentials from your local docker config. Default wandelbots registry pull secrets will be used.")

	installAppCmd.Flags().BoolVar(&appCopyImage, "copy-image", false, "Copy the image to the instance via ssh/scp and import it into containerd instead of pushing it to the registry. The configured host is used as ssh host.")
	installAppCmd.Flags().StringVar(&appCopySSHUser, "ssh-user", "", "SSH user for --copy-image. If empty, your ssh config or the current user applies.")
	installAppCmd.Flags().IntVar(&appCopySSHPort, "ssh-port", 0, "SSH port for --copy-image. If 0, your ssh config or the default port applies.")
	installAppCmd.Flags().BoolVar(&appCopyNoSudo, "no-sudo", false, "Run the remote import for --copy-image without sudo (e.g. when connecting as root).")
	installAppCmd.Flags().StringVar(&appCopyImportCmd, "import-command", "k3s ctr images import", "Remote command for --copy-image that imports an image tar from stdin. Note: plain ctr needs '-n k8s.io --address /run/k3s/containerd/containerd.sock'.")
	installAppCmd.Flags().StringVar(&appCopyRemoteDir, "remote-dir", "/tmp", "Remote directory for --copy-image where the image archive is placed temporarily.")
	installAppCmd.Flags().BoolVar(&appSetupSSHKey, "setup-ssh-key", false, "Before copying, set up password-free access on the instance for --copy-image: install your SSH public key and (unless --no-sudo) grant passwordless sudo for k3s via /etc/sudoers.d. Needs the SSH and one sudo password once.")

	installAppCmd.Flags().StringVar(&appPlatform, "platform", docker.DefaultPlatform, "Platform to build the app image for, matching the instance's CPU architecture. Supported: "+strings.Join(docker.SupportedPlatforms, ", "))

	installAppCmd.MarkFlagsMutuallyExclusive("copy-image", "registry-secret")
	installAppCmd.MarkFlagsMutuallyExclusive("copy-image", "omit-credentials")
}

// createdApp is the JSON result of 'app create -o json'.
type createdApp struct {
	Name      string `json:"name"`
	Generator string `json:"generator"`
	// the project directory
	Path string `json:"path"`
}

// createAppProject generates the project and returns its directory.
func createAppProject(appName string) (string, error) {
	// TODO we might need to verify the output path
	err := utils.VerifyProjectName(appName)
	if err != nil {
		return "", err
	}

	strippedAppName, err := utils.ConvertToValidSubdomainName(appName)
	// we could try to continue but the if the app name is not valid, the backend most likely will fail
	if err != nil {
		return "", err
	}

	cliVersion := version.GetCliVersion()

	appGen, err := getGenerator()
	if err != nil {
		return "", err
	}
	spec := generator.PluginSpec{
		Language:         appGen.GeneratorLanguage(),
		Name:             appName,
		PluginName:       strippedAppName,
		ModuleName:       appName,
		Timestamp:        fmt.Sprintf("%d", time.Now().UnixMilli()),
		GeneratorVersion: cliVersion,
		InstanceInformation: generator.InstanceInformation{
			NovaApiHost: currentHost(),
			CellName:    currentCell(),
		},
	}

	if basicAuth, err := credentials.GetBasicAuth(); err == nil {
		spec.InstanceInformation.BasicAuth = generator.BasicAuth{
			Username: basicAuth.Username,
			Password: basicAuth.Password,
		}
	}

	// the portal login only belongs to cloud instances (see oauth.SendsTokenTo)
	if token, err := oauth.LoadToken(); err == nil && oauth.SendsTokenTo(currentHost()) {
		spec.InstanceInformation.AccessToken = token.OAuth2Token.AccessToken
	}

	outpath := "./" + appName

	if _, err := os.Stat(outpath); !os.IsNotExist(err) {
		msg := fmt.Sprintf("Directory %s already exists. Contents may be overwritten. Do you want to continue?", outpath)
		if !utils.PromptUserConfirmation(msg) {
			// ADR-0010: an aborted action never exits 0
			return "", &typedError{
				Code:    "ABORTED",
				Message: fmt.Sprintf("directory %s already exists and overwriting it was declined", outpath),
				Hint:    "choose another app name, or remove the directory first",
				exit:    exitRefused,
			}
		}
	}

	if err := appGen.CreateProject(outpath, spec); err != nil {
		return "", err
	}

	// reset: the env files of the templates contain placeholders for the instance settings
	if err := writeAppEnv(rootCmd, outpath, true); err != nil {
		fmt.Fprintf(os.Stderr, "Could not write the local development settings: %v\nRun 'nova app update-env' in %s once that is fixed.\n", err, outpath)
	}
	return outpath, nil
}

// Writes the instance url, cell and a fresh access token into the gitignored env file of the
// app project in dir, using the file and variable names of its project type.
func writeAppEnv(cmd *cobra.Command, dir string, reset bool) error {
	r, err := updateAppEnv(dir, reset)
	if err != nil {
		return err
	}

	switch r.Token {
	case appEnvTokenUnused:
		cmd.Printf("Updated %s\n", r.path)
	case appEnvTokenRefreshed:
		cmd.Printf("Updated %s: access token refreshed\n", r.path)
	default:
		cmd.Printf("Updated %s: no access token needed for %s\n", r.path, r.novaApi)
	}

	// show the instance settings in effect, they are kept unless --reset
	differs := false
	for _, kv := range r.instance {
		note := ""
		if r.current[kv.Key] != kv.Value {
			note = fmt.Sprintf("  (configured: %s)", kv.Value)
			differs = true
		}
		cmd.Printf("  %s=%s%s\n", kv.Key, r.current[kv.Key], note)
	}
	if differs {
		cmd.Println("Some settings differ from the configured instance. Run 'nova app update-env --reset' to replace them.")
	}
	return nil
}

// what 'app update-env' did with the access token
const (
	appEnvTokenRefreshed = "refreshed"
	// written empty: the instance takes no portal token (see oauth.SendsTokenTo)
	appEnvTokenNotNeeded = "not_needed"
	// the project type has no token variable
	appEnvTokenUnused = "unused"
)

// appEnvResult is the JSON result of 'app update-env -o json'. It names the
// variables, never their values: the file holds the access token.
type appEnvResult struct {
	Path string `json:"path"`
	// refreshed, not_needed or unused
	Token     string           `json:"token"`
	Variables []appEnvVariable `json:"variables"`

	// path as given (relative to the working directory), for the text output
	path     string
	novaApi  string
	instance []appenv.KV
	current  map[string]string
}

// appEnvVariable is a variable of the project type's layout in the env file.
type appEnvVariable struct {
	Name string `json:"name"`
	// set: written by this run; kept: already in the file and left as it was
	Status string `json:"status"`
	// an instance setting whose value differs from the targeted instance
	// ('app update-env --reset' replaces it)
	Differs bool `json:"differs"`
}

// updateAppEnv writes the env file of the app project in dir (see writeAppEnv).
func updateAppEnv(dir string, reset bool) (*appEnvResult, error) {
	layout, err := appenv.DetectLayout(dir)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filepath.Join(dir, ".nova"))
	if err != nil {
		return nil, fmt.Errorf("%s is no nova app project folder: %w", dir, err)
	}
	app := DotNovaApp{}
	if err := yaml.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("invalid .nova file: %w", err)
	}

	host := currentHost()
	if host == "" {
		return nil, fmt.Errorf("%w: select one with 'nova instance select <name>' or pass --instance <name>", instances.ErrNoTarget)
	}
	cell := currentCell()

	// only portal instances need a token (see oauth.SendsTokenTo); refresh it so the file
	// holds a valid one
	accessToken := ""
	if layout.TokenKey != "" && oauth.SendsTokenTo(host) {
		if err := oauth.RefreshTokenOrLogin(host); err != nil {
			return nil, err
		}
		token, err := oauth.LoadToken()
		if err != nil {
			return nil, fmt.Errorf("failed to load the access token: %w", err)
		}
		accessToken = token.OAuth2Token.AccessToken
	}

	novaApi := nova.HostWithProtocol(host)
	settings := appenv.Settings{
		NovaApi:     novaApi,
		Cell:        cell,
		AppName:     app.Name,
		AccessToken: accessToken,
	}
	report, err := appenv.WriteReport(dir, layout, settings, reset)
	if err != nil {
		return nil, err
	}
	current, err := appenv.Read(report.Path)
	if err != nil {
		return nil, err
	}

	r := &appEnvResult{
		Path:      absPath(report.Path),
		Token:     appEnvTokenNotNeeded,
		Variables: []appEnvVariable{},
		path:      report.Path,
		novaApi:   novaApi,
		instance:  layout.Instance(settings),
		current:   current,
	}
	switch {
	case layout.TokenKey == "":
		r.Token = appEnvTokenUnused
	case accessToken != "":
		r.Token = appEnvTokenRefreshed
	}
	configured := map[string]string{}
	for _, kv := range r.instance {
		configured[kv.Key] = kv.Value
	}
	add := func(names []string, status string) {
		for _, n := range names {
			want, ok := configured[n]
			r.Variables = append(r.Variables, appEnvVariable{Name: n, Status: status, Differs: ok && current[n] != want})
		}
	}
	add(report.Set, "set")
	add(report.Kept, "kept")
	return r, nil
}

func installApp(host, cell, completeImageName string, appContext DotNovaAppContext) error {
	_, err := nova.CreateCellIfNotExists(host, cell)
	if err != nil {
		return err
	}

	updateApp, err := appAllreadyInstalled(host, cell, appContext.NovaApp.Name)
	if err != nil {
		return err
	}

	// Determine API version to use correct schema (V1 uses camelCase, V2 uses snake_case)
	cap := nova.GetHostCapabilityPublic(host)
	var appConfigJSON []byte

	switch cap {
	case nova.CapV1Only:
		// V1 API expects camelCase field names
		appConfig := mapDotNovaAppToApiApp(appContext, completeImageName)
		appConfigJSON, err = json.Marshal(appConfig)
	case nova.CapV2Only:
		// V2 API expects snake_case field names
		appConfig := mapDotNovaAppToApiV2App(appContext, completeImageName)
		appConfigJSON, err = json.Marshal(appConfig)
	default:
		// Unknown capability; try V2 first for forward compatibility
		appConfig := mapDotNovaAppToApiV2App(appContext, completeImageName)
		appConfigJSON, err = json.Marshal(appConfig)
	}

	if err != nil {
		return err
	}

	if updateApp {
		fmt.Fprintf(os.Stderr, "App '%s' already installed. Updating it...\n", appContext.NovaApp.Name)
		return nova.UpdateAppConfigurationFromJson(host, cell, appContext.NovaApp.Name, appConfigJSON)
	} else {
		fmt.Fprintf(os.Stderr, "Installing app '%s'...\n", appContext.NovaApp.Name)
		return nova.AddAppFromJson(host, cell, appContext.NovaApp.Name, appConfigJSON)
	}

}

func appendEnv(source DotNovaAppEnvironment, target *apiv2.ContainerEnvironment) *apiv2.ContainerEnvironment {
	if len(source) > 0 {
		if target == nil {
			target = &apiv2.ContainerEnvironment{}
		}

		for _, e := range source {
			ce := struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}{
				Name:  e.Name,
				Value: e.Value,
			}

			tmp := append(*target, ce)
			target = &tmp
		}
	}
	return target
}

// converts a NovaAppContext to apiv2.App configuration
func mapDotNovaAppToApiV2App(appContext DotNovaAppContext, completeImageName string) apiv2.App {
	appConfig := apiv2.App{
		Name:    appContext.NovaApp.Name,
		Port:    &appContext.NovaApp.Port,
		AppIcon: appContext.NovaApp.AppIcon,
		ContainerImage: apiv2.ContainerImage{
			Image: completeImageName,
		},
	}

	// Handle Secrets
	if len(appContext.NovaApp.RegistryAuth.Secrets) > 0 {
		appConfig.ContainerImage.Secrets = mapDotNovaAuthToSecrets(appContext.NovaApp.RegistryAuth)
	}

	// Handle Password Auth
	if appContext.NovaApp.RegistryAuth.Username != "" && appContext.NovaApp.RegistryAuth.Password != "" {
		appConfig.ContainerImage.Credentials = &apiv2.ImageCredentials{
			Registry: appContext.NovaApp.ImageRegistry,
			User:     appContext.NovaApp.RegistryAuth.Username,
			Password: appContext.NovaApp.RegistryAuth.Password,
		}
	}

	// Handle Resources
	if appContext.NovaApp.Resources.IntelGpu != 0 {
		if appConfig.Resources == nil {
			appConfig.Resources = &apiv2.ContainerResources{}
		}
		appConfig.Resources.IntelGpu = &appContext.NovaApp.Resources.IntelGpu
	}

	if appContext.NovaApp.Resources.MemoryLimit != "" {
		if appConfig.Resources == nil {
			appConfig.Resources = &apiv2.ContainerResources{}
		}
		appConfig.Resources.MemoryLimit = &appContext.NovaApp.Resources.MemoryLimit
	}

	// Handle Storage
	if appContext.NovaApp.Storage.Capacity != "" {
		appConfig.Storage = &apiv2.ContainerStorage{
			Capacity:  appContext.NovaApp.Storage.Capacity,
			MountPath: appContext.NovaApp.Storage.MountPath,
		}
	}

	// Handle Environment
	environments := appendEnv(appContext.NovaApp.Environment, appConfig.Environment)
	if environments != nil {
		appConfig.Environment = environments
	}

	return appConfig
}

// converts a NovaAppContext to apiv1.App configuration
func mapDotNovaAppToApiApp(appContext DotNovaAppContext, completeImageName string) apiv1.App {
	appConfig := apiv1.App{
		Name:    appContext.NovaApp.Name,
		Port:    &appContext.NovaApp.Port,
		AppIcon: appContext.NovaApp.AppIcon,
		ContainerImage: apiv1.ContainerImage{
			Image: completeImageName,
		},
	}

	// Handle Secrets
	if len(appContext.NovaApp.RegistryAuth.Secrets) > 0 {
		appConfig.ContainerImage.Secrets = mapDotNovaAuthToSecrets(appContext.NovaApp.RegistryAuth)
	}

	// Handle Password Auth
	if appContext.NovaApp.RegistryAuth.Username != "" && appContext.NovaApp.RegistryAuth.Password != "" {
		if appConfig.ContainerImage.Credentials == nil {
			appConfig.ContainerImage.Credentials = &apiv1.ImageCredentials{
				Registry: appContext.NovaApp.ImageRegistry,
				User:     appContext.NovaApp.RegistryAuth.Username,
				Password: appContext.NovaApp.RegistryAuth.Password,
			}
		}
	}

	// Handle Resources
	if appContext.NovaApp.Resources.IntelGpu != 0 {
		if appConfig.Resources == nil {
			appConfig.Resources = &apiv1.ContainerResources{}
		}
		appConfig.Resources.IntelGpu = &appContext.NovaApp.Resources.IntelGpu
	}

	if appContext.NovaApp.Resources.MemoryLimit != "" {
		fmt.Fprintln(os.Stderr, "WARN: Memory limit not supported in NOVA v1 API")
	}

	// Handle Storage
	if appContext.NovaApp.Storage.Capacity != "" {
		appConfig.Storage = &apiv1.ContainerStorage{
			Capacity:  appContext.NovaApp.Storage.Capacity,
			MountPath: appContext.NovaApp.Storage.MountPath,
		}
	}

	// Handle Environment
	environments := appendEnv(appContext.NovaApp.Environment, appConfig.Environment)
	if environments != nil {
		appConfig.Environment = environments
	}

	return appConfig
}

func appAllreadyInstalled(host, cell, app string) (bool, error) {
	apps, err := nova.ListApps(host, cell)
	if err != nil {
		return false, err
	}

	if slices.Contains(apps, app) {
		return true, nil
	}
	return false, nil
}

// We expect the .nova and Dockerfile in the same directory at the same level for now.
func getAppContext(args []string) (DotNovaAppContext, error) {
	projectPath := "."
	if len(args) > 0 {
		projectPath = args[0]
	}

	// accept the .nova file itself as well, it's an easy mix-up with its project folder
	if filepath.Base(projectPath) == ".nova" && isRegularFile(projectPath) {
		projectPath = filepath.Dir(projectPath)
	}

	if !isRegularFile(filepath.Join(projectPath, "Dockerfile")) {
		return DotNovaAppContext{}, noProjectFolderError(projectPath)
	}

	novaFile, err := readNovaApp(projectPath)
	if err != nil {
		return DotNovaAppContext{}, err
	}

	appContext := DotNovaAppContext{
		DockerContextPath: projectPath,
		NovaApp:           *novaFile,
	}

	return appContext, nil
}

// isRegularFile reports whether path exists and is not a directory. Any stat
// error (not found, "not a directory" for a path below a file, permissions)
// counts as missing.
func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func noProjectFolderError(path string) error {
	if path == "." {
		path = "current directory"
	}
	return fmt.Errorf("%s is no nova app project folder (expected a Dockerfile and a .nova file in it)", path)
}

func readNovaApp(path string) (*DotNovaApp, error) {
	novaFile := filepath.Join(path, ".nova")
	if !isRegularFile(novaFile) {
		return nil, noProjectFolderError(path)
	}

	data, err := os.ReadFile(novaFile)
	if err != nil {
		return nil, err
	}

	app := &DotNovaApp{}
	err = yaml.Unmarshal(data, app)
	if err != nil {
		return nil, err
	}

	if app.AppIcon == "" {
		app.AppIcon = "app_icon.png"
	}

	if app.Port == 0 {
		app.Port = 3000
	}

	if app.ImageRegistry == "" {
		app.ImageRegistry = viper.GetString("image-registry")
	}

	if app.ImageRegistry == "" {
		return nil, fmt.Errorf("please provide an image-registry via `nova config set image-registry <registry>` or within the `.nova` file with the field `imageRegistry`")
	}

	err = app.verify()
	return app, err
}

func (app *DotNovaApp) verify() error {
	if strings.Trim(app.Name, " ") == "" {
		return fmt.Errorf("app name is missing")
	}
	return nil
}

func completeApp(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	apps, err := nova.ListApps(currentHost(), currentCell())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var comp []string
	for _, app := range apps {
		if toComplete == "" || strings.HasPrefix(app, toComplete) {
			comp = append(comp, app)
		}
	}

	return comp, cobra.ShellCompDirectiveNoFileComp
}

// appGenerators are the values of 'app create --generator'.
var appGenerators = map[string]generator.PluginGenerator{
	"python_app": generator.NewPythonGenerator(),
	"nextjs_app": generator.NewNextJsAppGenerator(),
	"golang_app": generator.NewGolangAppGenerator(),
	"rust_app":   generator.NewRustAppGenerator(),
}

// getGenerator returns the generator of --generator; an unknown name is an error,
// not a silent fallback to python (a typo like "golang" would generate the wrong project).
func getGenerator() (generator.PluginGenerator, error) {
	if gen, ok := appGenerators[appGenerator]; ok {
		return gen, nil
	}
	names := make([]string, 0, len(appGenerators))
	for n := range appGenerators {
		names = append(names, n)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("unknown generator %q: use one of %s", appGenerator, strings.Join(names, ", "))
}

func installFromExistingFile(host, cellname, pathToFile string) error {
	app, err := specfile.UnmarshalSpec(pathToFile)
	if err != nil {
		return err
	}
	appName, ok := app["name"].(string)
	if !ok {
		return fmt.Errorf("app name is missing. make sure to provide valid app spec")
	}

	alreadyInstalled, err := appAllreadyInstalled(host, cellname, appName)
	if err != nil {
		return err
	}

	appJson, err := json.Marshal(app)
	if err != nil {
		return err
	}

	// Note: The app is installed using the wrapper functions which automatically
	// detect whether to use V2 or V1 API based on the Nova instance version
	if alreadyInstalled {
		fmt.Fprintf(os.Stderr, "App '%s' already installed. Updating it...\n", appName)
		return nova.UpdateAppConfigurationFromJson(host, cellname, appName, appJson)
	}

	fmt.Fprintf(os.Stderr, "Installing app '%s'...\n", appName)
	return nova.AddAppFromJson(host, cellname, appName, appJson)
}

func mapDotNovaAuthToSecrets(auth DotNovaAuth) *[]struct {
	Name string `json:"name"`
} {
	var secrets []struct {
		Name string `json:"name"`
	}
	for _, secretName := range auth.Secrets {
		secrets = append(secrets, struct {
			Name string `json:"name"`
		}{
			Name: secretName,
		})
	}
	return &secrets
}

func applySecretFromFlag(appContext *DotNovaAppContext) {
	if appOmitCredentials {
		appContext.NovaApp.RegistryAuth.Secrets = appDefaultSecrets
	}

	if appSecretName != "" {
		// User set --registry-secret=somevalue
		appContext.NovaApp.RegistryAuth.Secrets = []string{appSecretName}
	}
}
