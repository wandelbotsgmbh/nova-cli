package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wandelbotsgmbh/nova-cli/pkg/docker"
	"github.com/wandelbotsgmbh/nova-cli/pkg/modelpack"
)

var (
	modelProvider  string
	modelVersion   string
	modelName      string
	modelOutputDir string
	modelTag       string
)

var modelCmd = &cobra.Command{
	Use:   "model",
	Short: "EXPERIMENTAL: Build and push model images",
	Long:  `EXPERIMENTAL: Build and push model images to a container registry using a scratch-based multi-platform image.`,
}

var modelInitCmd = &cobra.Command{
	Use:   "init <source>",
	Short: "EXPERIMENTAL: Download a model and prepare it for building",
	Long: `EXPERIMENTAL: Downloads model files from a provider (e.g. HuggingFace) into a directory
and generates a Dockerfile and model.yaml metadata file.

The source can be a HuggingFace model URL/identifier or a local directory path.
Use --output-dir to specify where the model files, Dockerfile, and metadata are placed.

After init, use 'nova model build' to build and push the image.

Examples:
  nova model init meta-llama/Llama-2-7b --output-dir ./llama-2-7b
  nova model init https://huggingface.co/meta-llama/Llama-2-7b -o ./llama -n my-llama
  nova model init ./local-model-dir --output-dir ./build`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return modelpack.Init(modelpack.InitOptions{
			Source:    args[0],
			Name:      modelName,
			Version:   modelVersion,
			Provider:  modelProvider,
			OutputDir: modelOutputDir,
		})
	},
}

var modelBuildCmd = &cobra.Command{
	Use:   "build [path]",
	Short: "EXPERIMENTAL: Build and push a model image",
	Long: `EXPERIMENTAL: Builds a multi-platform model image (linux/amd64, linux/arm64) using the Dockerfile
and model.yaml created by 'nova model init', and pushes it to the configured registry.

The path should point to a directory containing a Dockerfile and model.yaml
(as created by 'nova model init').

Use --tag to specify the full image tag (e.g. myregistry.io/models/llama:1.0.0).

Examples:
  nova model build ./llama-2-7b --tag myregistry.io/models/llama:1.0.0
  nova model build . --tag ghcr.io/org/my-model:latest`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := "."
		if len(args) > 0 {
			projectPath = args[0]
		}

		if modelTag == "" {
			return fmt.Errorf("please provide a full image tag via --tag (e.g. myregistry.io/models/llama:1.0.0)")
		}

		rt, err := docker.Detect(viper.GetString("container-runtime"))
		if err != nil {
			return err
		}

		return modelpack.BuildAndPush(projectPath, modelTag, rt)
	},
}

func init() {
	rootCmd.AddCommand(modelCmd)
	modelCmd.AddCommand(modelInitCmd)
	modelCmd.AddCommand(modelBuildCmd)

	modelInitCmd.Flags().StringVarP(&modelOutputDir, "output-dir", "o", ".", "Directory to download model files and generate Dockerfile")
	modelInitCmd.Flags().StringVarP(&modelName, "name", "n", "", "Model name (defaults to name derived from source)")
	modelInitCmd.Flags().StringVarP(&modelVersion, "version", "v", "1.0.0", "Model version")
	modelInitCmd.Flags().StringVarP(&modelProvider, "provider", "p", "", "Model provider (huggingface, modelscope, mlflow). Auto-detected from URL if not specified.")

	modelBuildCmd.Flags().StringVarP(&modelTag, "tag", "t", "", "Full image tag including registry (e.g. myregistry.io/models/llama:1.0.0)")
	_ = modelBuildCmd.MarkFlagRequired("tag")
}
