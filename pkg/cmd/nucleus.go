package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	gopath "path"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus"
)

var nucleusCmd = &cobra.Command{
	Use:   "nucleus",
	Short: "EXPERIMENTAL: Interact with an Omniverse Nucleus server",
	Long: `EXPERIMENTAL: Commands for listing, copying, and removing files on an Omniverse Nucleus server.

Configuration:
  Before using nucleus commands you need to configure the server host and an API token.

  # Set the Nucleus server host (persisted in config)
  nova config set nucleus-host nucleus.dev.wandelbots.io

  # Set the API token (persisted in config)
  nova config set nucleus-token <your-token>

  Alternatively, use flags (--nucleus-host, --nucleus-token) or the NUCLEUS_TOKEN
  environment variable for per-invocation overrides. TLS is enabled by default and
  can be disabled with --nucleus-tls=false.

  Verify your configuration with:
  nova config view

Usage examples:
  nova nucleus ls /
  nova nucleus ls -l /Projects/
  nova nucleus cp ./Scene.usd n:/Projects/Scene.usd
  nova nucleus cp n:/Projects/Scene.usd ./local.usd
  nova nucleus mv /Projects/Old.usd /Projects/New.usd
  nova nucleus rm /Projects/Unused.usd
  nova nucleus mkdir /Projects/NewFolder/
  nova nucleus stat /Projects/Scene.usd`,
}

var nucleusLsCmd = &cobra.Command{
	Use:               "ls [path]",
	Short:             "List files and directories",
	Long:              `List the contents of a directory on the Nucleus server. Defaults to "/".`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeNucleusPath,
	Example: `  nova nucleus ls /
  nova nucleus ls /Projects/
  nova nucleus ls -l /Projects/`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "/"
		if len(args) > 0 {
			path = args[0]
		}

		client, err := dialNucleus(cmd)
		if err != nil {
			return err
		}
		defer client.Close()

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		entries, err := client.List(ctx, path)
		if err != nil {
			return fmt.Errorf("listing %s: %w", path, err)
		}

		long, _ := cmd.Flags().GetBool("long")
		if long {
			printEntriesLong(cmd, entries)
		} else {
			for _, e := range entries {
				name := e.Path
				if e.IsDir() && !strings.HasSuffix(name, "/") {
					name += "/"
				}
				cmd.Println(name)
			}
		}

		return nil
	},
}

var nucleusCpCmd = &cobra.Command{
	Use:               "cp <source> <destination>",
	Short:             "Copy files between local filesystem and Nucleus",
	ValidArgsFunction: completeNucleusCpPath,
	Long: `Copy files or directories between the local filesystem and a Nucleus server, or within Nucleus.

Use the "n:" prefix to indicate a Nucleus path. Paths without the prefix
are treated as local filesystem paths. Directories are copied recursively.

Supported directions:
  local  -> Nucleus:  nova nucleus cp ./file.usd n:/Projects/file.usd
  Nucleus -> local:   nova nucleus cp n:/Projects/file.usd ./file.usd
  Nucleus -> Nucleus: nova nucleus cp n:/Projects/a.usd n:/Projects/b.usd`,
	Args: cobra.ExactArgs(2),
	Example: `  # Upload a local file to Nucleus
  nova nucleus cp ./Scene.usd n:/Projects/Scene.usd

  # Upload a directory recursively
  nova nucleus cp ./MyProject/ n:/Projects/MyProject/

  # Download a file from Nucleus
  nova nucleus cp n:/Projects/Scene.usd ./Scene.usd

  # Download a directory recursively
  nova nucleus cp n:/Projects/MyProject/ ./MyProject/

  # Copy within Nucleus
  nova nucleus cp n:/Projects/a.usd n:/Projects/b.usd

  # Upload and create parent directories if needed
  nova nucleus cp -p ./Scene.usd n:/Projects/new/dir/Scene.usd`,
	RunE: func(cmd *cobra.Command, args []string) error {
		src, dst := args[0], args[1]
		srcIsNucleus := isNucleusPath(src)
		dstIsNucleus := isNucleusPath(dst)

		if !srcIsNucleus && !dstIsNucleus {
			return fmt.Errorf("at least one path must use the n: prefix (e.g. n:/Projects/file.usd)")
		}

		client, err := dialNucleus(cmd)
		if err != nil {
			return err
		}
		defer client.Close()

		// No fixed deadline: transfers can be arbitrarily large (multi-GB USD
		// scenes take >5min on a normal uplink). Ctrl-C cancels via the command
		// context; the LFT HTTP layer has its own dial/response-header timeouts.
		ctx := cmd.Context()

		switch {
		case srcIsNucleus && dstIsNucleus:
			// Nucleus -> Nucleus (server-side copy)
			nSrc := stripNucleusPrefix(src)
			nDst := stripNucleusPrefix(dst)
			parents, _ := cmd.Flags().GetBool("parents")
			if parents {
				if err := client.EnsureParentDirs(ctx, nDst); err != nil {
					return fmt.Errorf("creating parent directories: %w", err)
				}
			}
			if err := client.Copy(ctx, nSrc, nDst); err != nil {
				return fmt.Errorf("copy %s -> %s: %w", nSrc, nDst, err)
			}

		case !srcIsNucleus && dstIsNucleus:
			// Local -> Nucleus (upload, supports files and directories)
			nDst := stripNucleusPrefix(dst)
			parents, _ := cmd.Flags().GetBool("parents")

			info, err := os.Stat(src)
			if err != nil {
				return fmt.Errorf("stat %s: %w", src, err)
			}

			showProgress, _ := cmd.Flags().GetBool("progress")

			if !info.IsDir() {
				// Single file upload (streamed; not loaded into memory).
				if parents {
					if err := client.EnsureParentDirs(ctx, nDst); err != nil {
						return fmt.Errorf("creating parent directories: %w", err)
					}
				}
				if err := uploadFile(ctx, client, src, nDst, info.Size(), showProgress); err != nil {
					return fmt.Errorf("uploading to %s: %w", nDst, err)
				}
			} else {
				// Recursive directory upload.
				srcBase := filepath.Clean(src)
				dstBase := strings.TrimSuffix(nDst, "/") + "/"

				// Ensure the destination directory (and parents with -p) exist.
				if parents {
					if err := client.EnsureParentDirs(ctx, dstBase); err != nil {
						return fmt.Errorf("creating parent directories: %w", err)
					}
				}
				if err := client.CreateFolder(ctx, dstBase); err != nil {
					// Ignore if it already exists.
					if !strings.Contains(err.Error(), "ALREADY_EXISTS") {
						return fmt.Errorf("creating destination %s: %w", dstBase, err)
					}
				}

				err := filepath.Walk(srcBase, func(localPath string, fi os.FileInfo, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					rel, _ := filepath.Rel(srcBase, localPath)
					if rel == "." {
						return nil // skip the root directory itself
					}
					// Convert OS path separators to forward slashes for Nucleus.
					remotePath := dstBase + filepath.ToSlash(rel)
					if fi.IsDir() {
						if !strings.HasSuffix(remotePath, "/") {
							remotePath += "/"
						}
						return client.CreateFolder(ctx, remotePath)
					}
					return uploadFile(ctx, client, localPath, remotePath, fi.Size(), showProgress)
				})
				if err != nil {
					return fmt.Errorf("uploading directory %s: %w", src, err)
				}
			}

		case srcIsNucleus && !dstIsNucleus:
			// Nucleus -> Local (download, supports files and directories)
			nSrc := stripNucleusPrefix(src)
			if err := downloadRecursive(ctx, client, nSrc, dst); err != nil {
				return err
			}
		}

		return nil
	},
}

var nucleusRmCmd = &cobra.Command{
	Use:               "rm <path>",
	Short:             "Remove a file or directory",
	ValidArgsFunction: completeNucleusPath,
	Long:              `Remove a file or directory on the Nucleus server. Use -r to remove non-empty directories.`,
	Args:              cobra.ExactArgs(1),
	Example: `  nova nucleus rm /Projects/OldFile.usd
  nova nucleus rm -r /Projects/OldDir/`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		recursive, _ := cmd.Flags().GetBool("recursive")

		client, err := dialNucleus(cmd)
		if err != nil {
			return err
		}
		defer client.Close()

		ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
		defer cancel()

		if recursive {
			err = client.DeleteRecursive(ctx, path)
		} else {
			err = client.Delete(ctx, path)
		}
		if err != nil {
			if strings.Contains(err.Error(), "FOLDER_NOT_EMPTY") {
				return fmt.Errorf("directory %s is not empty, use -r to delete recursively", path)
			}
			return fmt.Errorf("remove %s: %w", path, err)
		}

		return nil
	},
}

var nucleusMvCmd = &cobra.Command{
	Use:               "mv <source> <destination>",
	Short:             "Move or rename a file or directory",
	ValidArgsFunction: completeNucleusPath,
	Long:              `Move or rename a file or directory on the Nucleus server.`,
	Args:              cobra.ExactArgs(2),
	Example:           `  nova nucleus mv /Projects/Old.usd /Projects/New.usd`,
	RunE: func(cmd *cobra.Command, args []string) error {
		src, dst := args[0], args[1]

		client, err := dialNucleus(cmd)
		if err != nil {
			return err
		}
		defer client.Close()

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		if err := client.Move(ctx, src, dst); err != nil {
			return fmt.Errorf("move %s -> %s: %w", src, dst, err)
		}

		return nil
	},
}

var nucleusMkdirCmd = &cobra.Command{
	Use:               "mkdir <path>",
	Short:             "Create a directory",
	ValidArgsFunction: completeNucleusPath,
	Long:              `Create a directory on the Nucleus server.`,
	Args:              cobra.ExactArgs(1),
	Example:           `  nova nucleus mkdir /Projects/NewFolder/`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]

		client, err := dialNucleus(cmd)
		if err != nil {
			return err
		}
		defer client.Close()

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		if err := client.CreateFolder(ctx, path); err != nil {
			return fmt.Errorf("mkdir %s: %w", path, err)
		}

		return nil
	},
}

var nucleusStatCmd = &cobra.Command{
	Use:               "stat <path>",
	Short:             "Show details about a file or directory",
	ValidArgsFunction: completeNucleusPath,
	Long:              `Display detailed metadata for a file or directory on the Nucleus server.`,
	Args:              cobra.ExactArgs(1),
	Example:           `  nova nucleus stat /Projects/Scene.usd`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]

		client, err := dialNucleus(cmd)
		if err != nil {
			return err
		}
		defer client.Close()

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		entry, err := client.Stat(ctx, path)
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}

		cmd.Printf("Path:     %s\n", entry.Path)
		cmd.Printf("Type:     %s\n", entry.PathType)
		cmd.Printf("Size:     %d\n", entry.Size)
		if entry.CreatedBy != "" {
			cmd.Printf("Created:  %s by %s\n", entry.CreatedTime().Format(time.RFC3339), entry.CreatedBy)
		}
		if entry.ModifiedBy != "" {
			cmd.Printf("Modified: %s by %s\n", entry.ModifiedTime().Format(time.RFC3339), entry.ModifiedBy)
		}
		if entry.Hash != "" {
			cmd.Printf("Hash:     %s\n", entry.Hash)
		}
		if entry.Version > 0 {
			cmd.Printf("Version:  %d\n", entry.Version)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(nucleusCmd)
	nucleusCmd.AddCommand(nucleusLsCmd)
	nucleusCmd.AddCommand(nucleusCpCmd)
	nucleusCmd.AddCommand(nucleusRmCmd)
	nucleusCmd.AddCommand(nucleusMvCmd)
	nucleusCmd.AddCommand(nucleusMkdirCmd)
	nucleusCmd.AddCommand(nucleusStatCmd)

	// Persistent flags available to all nucleus subcommands.
	nucleusCmd.PersistentFlags().String("nucleus-host", "", "Nucleus server host (overrides config)")
	nucleusCmd.PersistentFlags().String("nucleus-token", "", "Nucleus API token (overrides NUCLEUS_TOKEN env)")
	nucleusCmd.PersistentFlags().Bool("nucleus-tls", true, "Use TLS (wss://)")

	// ls-specific flags.
	nucleusLsCmd.Flags().BoolP("long", "l", false, "Show detailed listing")

	// rm-specific flags.
	nucleusRmCmd.Flags().BoolP("recursive", "r", false, "Recursively delete non-empty directories")

	// cp-specific flags.
	nucleusCpCmd.Flags().BoolP("parents", "p", false, "Create parent directories on the destination if they don't exist")
	nucleusCpCmd.Flags().BoolP("progress", "P", false, "Show upload progress (percent, speed, ETA) on stderr")
}

// completeNucleusPath provides tab completion for Nucleus paths.
func completeNucleusPath(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeNucleusRemotePath(cmd, toComplete)
}

// completeNucleusCpPath provides tab completion for the cp command.
// It supports both local filesystem paths and Nucleus paths (prefixed with "n:").
func completeNucleusCpPath(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) >= 2 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// If the user is typing a Nucleus path, complete from the server.
	if strings.HasPrefix(toComplete, nucleusPrefix) {
		nucPath := toComplete[len(nucleusPrefix):]
		comp, directive := completeNucleusRemotePath(cmd, nucPath)
		// Re-add the n: prefix to each suggestion.
		for i, c := range comp {
			comp[i] = nucleusPrefix + c
		}
		return comp, directive
	}

	// Offer "n:" as a suggestion if the user hasn't typed anything or typed "n".
	if toComplete == "" || strings.HasPrefix(nucleusPrefix, toComplete) {
		return []string{nucleusPrefix}, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveDefault
	}

	// Otherwise fall back to local filesystem completion.
	return nil, cobra.ShellCompDirectiveDefault
}

// completeNucleusRemotePath returns completions for a Nucleus server path.
func completeNucleusRemotePath(cmd *cobra.Command, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Determine the directory to list.
	dir := "/"
	if toComplete != "" {
		if strings.HasSuffix(toComplete, "/") {
			dir = toComplete
		} else {
			dir = gopath.Dir(toComplete) + "/"
		}
	}

	client, err := dialNucleus(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
	defer cancel()

	entries, err := client.List(ctx, dir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var comp []string
	for _, e := range entries {
		candidate := e.Path
		if e.IsDir() && !strings.HasSuffix(candidate, "/") {
			candidate += "/"
		}
		if strings.HasPrefix(candidate, toComplete) {
			comp = append(comp, candidate)
		}
	}

	// Don't add a space after directories so the user can keep completing.
	return comp, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// downloadRecursive downloads a file or directory from Nucleus to the local filesystem.
func downloadRecursive(ctx context.Context, client *nucleus.Client, remotePath, localPath string) error {
	// Try as a file first.
	data, err := client.ReadFile(ctx, remotePath)
	if err == nil {
		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			return fmt.Errorf("creating local directory: %w", err)
		}
		return os.WriteFile(localPath, data, 0644)
	}

	// If ReadFile failed, treat as directory.
	remoteDir := remotePath
	if !strings.HasSuffix(remoteDir, "/") {
		remoteDir += "/"
	}

	entries, listErr := client.List(ctx, remoteDir)
	if listErr != nil {
		// Return the original read error if listing also fails.
		return fmt.Errorf("downloading %s: %w", remotePath, err)
	}

	if err := os.MkdirAll(localPath, 0755); err != nil {
		return fmt.Errorf("creating local directory %s: %w", localPath, err)
	}

	for _, e := range entries {
		name := gopath.Base(e.Path)
		childLocal := filepath.Join(localPath, name)
		if e.IsDir() {
			childRemote := e.Path
			if !strings.HasSuffix(childRemote, "/") {
				childRemote += "/"
			}
			if err := downloadRecursive(ctx, client, childRemote, childLocal); err != nil {
				return err
			}
		} else {
			data, err := client.ReadFile(ctx, e.Path)
			if err != nil {
				return fmt.Errorf("downloading %s: %w", e.Path, err)
			}
			if err := os.WriteFile(childLocal, data, 0644); err != nil {
				return fmt.Errorf("writing %s: %w", childLocal, err)
			}
		}
	}
	return nil
}

// uploadFile streams a local file to Nucleus, optionally rendering progress
// (percent, speed, ETA) to stderr.
func uploadFile(ctx context.Context, client *nucleus.Client, localPath, remotePath string, size int64, showProgress bool) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("opening local file %s: %w", localPath, err)
	}
	defer f.Close()

	var r io.Reader = f
	var pr *progressReader
	if showProgress {
		pr = newProgressReader(f, size, filepath.Base(localPath))
		r = pr
	}
	if err := client.WriteFileFrom(ctx, remotePath, r, size); err != nil {
		if pr != nil {
			fmt.Fprintln(os.Stderr)
		}
		return err
	}
	if pr != nil {
		pr.finish()
	}
	return nil
}

// dialNucleus creates a Nucleus client using flags, env vars, or config.
func dialNucleus(cmd *cobra.Command) (*nucleus.Client, error) {
	host, _ := cmd.Flags().GetString("nucleus-host")
	if host == "" {
		host = viper.GetString("nucleus-host")
	}
	if host == "" {
		return nil, fmt.Errorf("nucleus host not configured — set via --nucleus-host flag or `nova config set nucleus-host <host>`")
	}

	token, _ := cmd.Flags().GetString("nucleus-token")
	if token == "" {
		token = os.Getenv("NUCLEUS_TOKEN")
	}
	if token == "" {
		token = viper.GetString("nucleus-token")
	}
	if token == "" {
		return nil, fmt.Errorf("nucleus API token not set — use --nucleus-token flag, NUCLEUS_TOKEN env var, or `nova config set nucleus-token <token>`")
	}

	tls, _ := cmd.Flags().GetBool("nucleus-tls")

	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()

	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
		Host:     host,
		APIToken: token,
		TLS:      tls,
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to Nucleus at %s: %w", host, err)
	}

	return client, nil
}

// printEntriesLong prints entries in a detailed table format.
func printEntriesLong(cmd *cobra.Command, entries []nucleus.Entry) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tSIZE\tMODIFIED\tPATH")
	for _, e := range entries {
		modified := "-"
		if e.ModifiedTimestamp > 0 {
			modified = e.ModifiedTime().Format("2006-01-02 15:04")
		}
		size := "-"
		if e.PathType == "file" {
			size = formatSize(e.Size)
		}
		name := e.Path
		if e.IsDir() && !strings.HasSuffix(name, "/") {
			name += "/"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.PathType, size, modified, name)
	}
	w.Flush()
}

// formatSize returns a human-readable file size.
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fG", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1fM", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1fK", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

const nucleusPrefix = "n:"

// isNucleusPath returns true if the path starts with "n:".
func isNucleusPath(path string) bool {
	return len(path) > len(nucleusPrefix) && path[:len(nucleusPrefix)] == nucleusPrefix
}

// stripNucleusPrefix removes the "n:" prefix from a path.
func stripNucleusPrefix(path string) string {
	if isNucleusPath(path) {
		return path[len(nucleusPrefix):]
	}
	return path
}
