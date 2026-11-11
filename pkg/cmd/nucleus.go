package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	gopath "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/wandelbotsgmbh/nova-cli/pkg/nucleusservers"
	"github.com/wandelbotsgmbh/nova-cli/pkg/omniverse/nucleus"
)

var nucleusCmd = &cobra.Command{
	Use:   "nucleus",
	Short: "EXPERIMENTAL: Interact with an Omniverse Nucleus server",
	// a Nucleus server is its own target, independent of the NOVA instance (ADR-0018)
	Annotations: noTarget,
	Long: `EXPERIMENTAL: Commands for listing, copying, and removing files on an Omniverse Nucleus server.

Configuration:
  Nucleus servers are kept in their own list, independent of the NOVA instance. The
  commands work against the selected server, or the one named with --nucleus <name> /
  NOVA_NUCLEUS for a single invocation.

  # Add a server with its API token (read from stdin), list and select servers
  nova nucleus server add dev --host nucleus.dev.wandelbots.io --token-stdin < token.txt
  nova nucleus server list
  nova nucleus server select dev

  # Or set host and token of the selected server
  nova config set nucleus-host nucleus.dev.wandelbots.io
  nova config set nucleus-token <your-token>

  --nucleus-host, --nucleus-token and the NUCLEUS_TOKEN environment variable override
  them for one invocation; the token of a server is never sent to another host. TLS is
  enabled by default and can be disabled with --nucleus-tls=false.

  Verify your configuration with:
  nova nucleus server list

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
	Use:   "ls [path]",
	Short: "List files and directories",
	Long: `List the contents of a directory on the Nucleus server. Defaults to "/".

-o json prints an array of entries (path, type, size in bytes, created/modified as
RFC 3339 with created_by/modified_by, hash, version), whether or not -l is given.`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeNucleusPath,
	Example: `  nova nucleus ls /
  nova nucleus ls /Projects/
  nova nucleus ls -l /Projects/
  nova nucleus ls -o json /Projects/`,
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

		if nucleusOutput(cmd) == outputJSON {
			items := make([]nucleusEntryJSON, 0, len(entries))
			for _, e := range entries {
				items = append(items, toNucleusEntryJSON(e))
			}
			return writeJSON(cmd, items)
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
				fmt.Fprintln(cmd.OutOrStdout(), name)
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
  Nucleus -> Nucleus: nova nucleus cp n:/Projects/a.usd n:/Projects/b.usd

-o json prints source, destination and the copied files: "path" is the local file
(absolute), "nucleus_path" the file on Nucleus, "size" its size in bytes. A copy
within Nucleus lists only the destination.`,
	Args: cobra.ExactArgs(2),
	Example: `  # Upload a local file to Nucleus
  nova nucleus cp ./Scene.usd n:/Projects/Scene.usd

  # Upload a directory recursively
  nova nucleus cp ./MyProject/ n:/Projects/MyProject/

  # Download a file from Nucleus
  nova nucleus cp n:/Projects/Scene.usd ./Scene.usd

  # Download a directory recursively, 4 files at a time
  nova nucleus cp -j 4 n:/Projects/MyProject/ ./MyProject/

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
		res := nucleusCopyResult{Source: src, Destination: dst, Files: []nucleusCopiedFile{}}

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
			res.Files = append(res.Files, nucleusCopiedFile{NucleusPath: nDst})

		case !srcIsNucleus && dstIsNucleus:
			// Local -> Nucleus (upload, supports files and directories)
			nDst := stripNucleusPrefix(dst)
			parents, _ := cmd.Flags().GetBool("parents")

			info, err := os.Stat(src)
			if err != nil {
				return fmt.Errorf("stat %s: %w", src, err)
			}
			res.Source = absPath(src)
			uploaded := func(localPath, remotePath string, size int64) {
				res.Files = append(res.Files, nucleusCopiedFile{Path: absPath(localPath), NucleusPath: remotePath, Size: &size})
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
				uploaded(src, nDst, info.Size())
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
					if err := uploadFile(ctx, client, localPath, remotePath, fi.Size(), showProgress); err != nil {
						return err
					}
					uploaded(localPath, remotePath, fi.Size())
					return nil
				})
				if err != nil {
					return fmt.Errorf("uploading directory %s: %w", src, err)
				}
			}

		case srcIsNucleus && !dstIsNucleus:
			// Nucleus -> Local (download, supports files and directories)
			nSrc := stripNucleusPrefix(src)
			parallel, _ := cmd.Flags().GetInt("parallel")
			files, err := downloadRecursive(ctx, client, nSrc, dst, cmd.ErrOrStderr(), parallel)
			if err != nil {
				return err
			}
			res.Destination = absPath(dst)
			for _, f := range files {
				cf := nucleusCopiedFile{Path: absPath(f.local), NucleusPath: f.remote}
				if fi, err := os.Stat(f.local); err == nil {
					size := fi.Size()
					cf.Size = &size
				}
				res.Files = append(res.Files, cf)
			}
		}

		if nucleusOutput(cmd) == outputJSON {
			return writeJSON(cmd, res)
		}
		return nil
	},
}

// nucleusCopyResult is the JSON result of 'nucleus cp -o json'.
type nucleusCopyResult struct {
	// as given; local paths absolute, Nucleus paths with the n: prefix
	Source      string `json:"source"`
	Destination string `json:"destination"`
	// files written, in the order they were copied (directories are not listed)
	Files []nucleusCopiedFile `json:"files"`
}

// nucleusCopiedFile is one copied file of 'nucleus cp -o json'.
type nucleusCopiedFile struct {
	// local file, absolute; empty for a copy within Nucleus
	Path        string `json:"path,omitempty"`
	NucleusPath string `json:"nucleus_path"`
	// bytes transferred; not known for a copy within Nucleus
	Size *int64 `json:"size,omitempty"`
}

// nucleusEntryJSON is the JSON shape of an entry in 'nucleus ls -o json' and
// 'nucleus stat -o json'.
type nucleusEntryJSON struct {
	Path string `json:"path"`
	// file, folder or mount
	Type string `json:"type"`
	Size int64  `json:"size"`
	// RFC 3339, UTC
	Created    string `json:"created,omitempty"`
	CreatedBy  string `json:"created_by,omitempty"`
	Modified   string `json:"modified,omitempty"`
	ModifiedBy string `json:"modified_by,omitempty"`
	Hash       string `json:"hash,omitempty"`
	Version    int    `json:"version,omitempty"`
}

func toNucleusEntryJSON(e nucleus.Entry) nucleusEntryJSON {
	j := nucleusEntryJSON{
		Path: e.Path, Type: e.PathType, Size: e.Size,
		CreatedBy: e.CreatedBy, ModifiedBy: e.ModifiedBy, Hash: e.Hash, Version: e.Version,
	}
	if e.CreatedTimestamp > 0 {
		j.Created = e.CreatedTime().UTC().Format(time.RFC3339)
	}
	if e.ModifiedTimestamp > 0 {
		j.Modified = e.ModifiedTime().UTC().Format(time.RFC3339)
	}
	return j
}

// nucleusOutput returns the -o/--output value of cmd, text if it has none (the
// tests run the RunE functions on commands without the flag).
func nucleusOutput(cmd *cobra.Command) outputFormat {
	if f := cmd.Flags().Lookup("output"); f != nil {
		if o, ok := f.Value.(*outputFormat); ok {
			return *o
		}
	}
	return outputText
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
	Long: `Display detailed metadata for a file or directory on the Nucleus server.

-o json prints the entry as one object, in the shape of 'nucleus ls -o json'.`,
	Args:    cobra.ExactArgs(1),
	Example: `  nova nucleus stat /Projects/Scene.usd`,
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
		if nucleusOutput(cmd) == outputJSON {
			return writeJSON(cmd, toNucleusEntryJSON(*entry))
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Path:     %s\n", entry.Path)
		fmt.Fprintf(cmd.OutOrStdout(), "Type:     %s\n", entry.PathType)
		fmt.Fprintf(cmd.OutOrStdout(), "Size:     %d\n", entry.Size)
		if entry.CreatedBy != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Created:  %s by %s\n", entry.CreatedTime().Format(time.RFC3339), entry.CreatedBy)
		}
		if entry.ModifiedBy != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Modified: %s by %s\n", entry.ModifiedTime().Format(time.RFC3339), entry.ModifiedBy)
		}
		if entry.Hash != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Hash:     %s\n", entry.Hash)
		}
		if entry.Version > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Version:  %d\n", entry.Version)
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

	// -o/--output (ADR-0010); read with nucleusOutput
	for _, c := range []*cobra.Command{nucleusLsCmd, nucleusStatCmd, nucleusCpCmd} {
		addOutputFlag(c)
	}

	// rm-specific flags.
	nucleusRmCmd.Flags().BoolP("recursive", "r", false, "Recursively delete non-empty directories")

	// cp-specific flags.
	nucleusCpCmd.Flags().BoolP("parents", "p", false, "Create parent directories on the destination if they don't exist")
	nucleusCpCmd.Flags().BoolP("progress", "P", false, "Show upload progress (percent, speed, ETA) on stderr")
	nucleusCpCmd.Flags().IntP("parallel", "j", 2, "Files to download at once (Nucleus -> local); small Nucleus deployments return 5xx when this is high")
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

// downloadJob is one file to fetch during a recursive download.
type downloadJob struct {
	remote, local string
}

// downloadRecursive downloads a file or directory from Nucleus to the local
// filesystem, fetching up to parallel files at once.
//
// Files that still fail after the client's retries are skipped (nothing is
// written for them), reported on errOut as they happen, and summarized in the
// returned error so the command exits non-zero. Errors that aren't per-file
// (cancellation, listing failures, local filesystem errors) abort the whole
// download. On success it returns the files written.
func downloadRecursive(ctx context.Context, client *nucleus.Client, remotePath, localPath string, errOut io.Writer, parallel int) ([]downloadJob, error) {
	// Try as a file first.
	err := downloadOne(ctx, client, remotePath, localPath)
	if err == nil {
		return []downloadJob{{remote: remotePath, local: localPath}}, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	if nucleus.IsTransient(err) {
		// The server identified the path as a file but could not deliver it;
		// don't mask that by trying to treat it as a directory.
		return nil, fmt.Errorf("downloading %s: %w", remotePath, err)
	}

	// If the read failed, treat as directory.
	remoteDir := remotePath
	if !strings.HasSuffix(remoteDir, "/") {
		remoteDir += "/"
	}

	entries, listErr := client.List(ctx, remoteDir)
	if listErr != nil {
		// Return the original read error if listing also fails.
		return nil, fmt.Errorf("downloading %s: %w", remotePath, err)
	}

	// Walk the tree first (listings are cheap), creating local directories
	// as we go, then fetch the files with a worker pool.
	var jobs []downloadJob
	if err := collectDownloadJobs(ctx, client, entries, localPath, &jobs); err != nil {
		return nil, err
	}

	failed, err := runDownloads(ctx, client, jobs, errOut, parallel)
	if err != nil {
		return nil, err
	}
	if len(failed) > 0 {
		return nil, fmt.Errorf("%d file(s) failed to download and were skipped:\n  %s",
			len(failed), strings.Join(failed, "\n  "))
	}
	return jobs, nil
}

// downloadOne fetches a single file into localPath, creating the parent
// directory.
func downloadOne(ctx context.Context, client *nucleus.Client, remotePath, localPath string) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("creating local directory: %w", err)
	}
	return client.DownloadFile(ctx, remotePath, localPath)
}

// collectDownloadJobs lists entries recursively, creates the local
// directory tree and appends one job per file.
func collectDownloadJobs(ctx context.Context, client *nucleus.Client, entries []nucleus.Entry, localPath string, jobs *[]downloadJob) error {
	if err := os.MkdirAll(localPath, 0755); err != nil {
		return fmt.Errorf("creating local directory %s: %w", localPath, err)
	}
	for _, e := range entries {
		name := gopath.Base(e.Path)
		childLocal := filepath.Join(localPath, name)
		if !e.IsDir() {
			*jobs = append(*jobs, downloadJob{remote: e.Path, local: childLocal})
			continue
		}
		childRemote := e.Path
		if !strings.HasSuffix(childRemote, "/") {
			childRemote += "/"
		}
		children, err := client.List(ctx, childRemote)
		if err != nil {
			return fmt.Errorf("listing %s: %w", childRemote, err)
		}
		if err := collectDownloadJobs(ctx, client, children, childLocal, jobs); err != nil {
			return err
		}
	}
	return nil
}

// runDownloads fetches jobs with up to parallel workers. Per-file failures
// are reported on errOut and returned in failed; anything else (cancellation,
// local filesystem errors) stops all workers and is returned as err.
func runDownloads(ctx context.Context, client *nucleus.Client, jobs []downloadJob, errOut io.Writer, parallel int) (failed []string, err error) {
	if parallel < 1 {
		parallel = 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	queue := make(chan downloadJob)

	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range queue {
				err := client.DownloadFile(ctx, job.remote, job.local)
				if err == nil {
					continue
				}
				mu.Lock()
				switch {
				case ctx.Err() != nil:
					if firstErr == nil && !errors.Is(err, context.Canceled) {
						firstErr = err
					}
				case isLocalWriteError(err):
					if firstErr == nil {
						firstErr = fmt.Errorf("writing %s: %w", job.local, err)
					}
					cancel()
				default:
					fmt.Fprintf(errOut, "warning: skipping %s: %v\n", job.remote, err)
					failed = append(failed, job.remote)
				}
				mu.Unlock()
			}
		}()
	}

feed:
	for _, job := range jobs {
		select {
		case queue <- job:
		case <-ctx.Done():
			break feed
		}
	}
	close(queue)
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	sort.Strings(failed)
	return failed, nil
}

// isLocalWriteError reports whether a download failed because of the local
// filesystem rather than the server.
func isLocalWriteError(err error) bool {
	var pathErr *os.PathError
	return errors.As(err, &pathErr)
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

// nucleusConfig picks host, token and TLS: the resolved Nucleus server (--nucleus,
// NOVA_NUCLEUS, selected), overridden by --nucleus-host, --nucleus-token / NUCLEUS_TOKEN
// and --nucleus-tls. A server's token is only used for its own host (ADR-0006): with
// --nucleus-host pointing elsewhere, the token has to be given explicitly.
func nucleusConfig(cmd *cobra.Command) (nucleus.ClientConfig, error) {
	s, _, err := resolveNucleus()
	if err != nil {
		return nucleus.ClientConfig{}, err
	}
	cfg := nucleus.ClientConfig{Host: s.Host, APIToken: s.Token, TLS: s.UseTLS()}

	tlsFlag, _ := cmd.Flags().GetBool("nucleus-tls")
	if host, _ := cmd.Flags().GetString("nucleus-host"); host != "" && (s.Host == "" || !nucleusservers.SameHost(host, s.Host)) {
		cfg = nucleus.ClientConfig{Host: host, TLS: tlsFlag}
	} else if f := cmd.Flags().Lookup("nucleus-tls"); f != nil && f.Changed {
		cfg.TLS = tlsFlag
	}
	if token, _ := cmd.Flags().GetString("nucleus-token"); token != "" {
		cfg.APIToken = token
	} else if token := os.Getenv("NUCLEUS_TOKEN"); token != "" {
		cfg.APIToken = token
	}

	if cfg.Host == "" {
		return nucleus.ClientConfig{}, errNoNucleusServer
	}
	if cfg.APIToken == "" {
		if s.Name != "" && nucleusservers.SameHost(cfg.Host, s.Host) {
			return nucleus.ClientConfig{}, fmt.Errorf("nucleus server %q has no API token: set it with 'nova config set nucleus-token <token> --nucleus %s', or use --nucleus-token / NUCLEUS_TOKEN", s.Name, s.Name)
		}
		return nucleus.ClientConfig{}, fmt.Errorf("no API token for %s: use --nucleus-token or NUCLEUS_TOKEN (the token of a Nucleus server is only sent to its own host)", cfg.Host)
	}
	return cfg, nil
}

// dialNucleus creates a Nucleus client using flags, env vars, or config.
func dialNucleus(cmd *cobra.Command) (*nucleus.Client, error) {
	cfg, err := nucleusConfig(cmd)
	if err != nil {
		return nil, err
	}
	host, token, tls := cfg.Host, cfg.APIToken, cfg.TLS

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
