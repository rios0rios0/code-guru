package main

import (
	"os"

	logger "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers"
)

// version is set at build time via -ldflags.
// During development, it defaults to "dev".
//

var version = "dev"

func buildRootCommand(reviewController *controllers.ReviewController) *cobra.Command {
	//nolint:exhaustruct // minimal Command initialization with required fields only
	cmd := &cobra.Command{
		Use:     "code-guru [pr-url]",
		Short:   "AI-powered code review tool",
		Version: version,
		Long: `Code Guru automatically reviews pull requests using AI (OpenAI or Claude Code).
It analyzes code diffs against configurable review rules and posts
comments directly on the pull request.

Supports GitHub and Azure DevOps as Git hosting providers.

Usage modes:
  code-guru <pr-url>       Review a single PR by URL
  code-guru review-all     Review all open PRs across configured providers
  code-guru discover       List open PRs without reviewing them`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if len(args) == 0 {
				return command.Help()
			}
			reviewController.Execute(command, args)
			return nil
		},
	}

	cmd.PersistentFlags().StringP("config", "c", "", "path to config file")
	cmd.PersistentFlags().String("backend", "", "AI backend: openai, claude, or anthropic")
	cmd.PersistentFlags().String("rules-path", "", "path to rules directory")
	cmd.PersistentFlags().Bool("dry-run", false, "show review without posting comments")
	cmd.PersistentFlags().BoolP("verbose", "v", false, "enable verbose output")

	return cmd
}

func addSubcommands(rootCmd *cobra.Command, appContext *internal.AppInternal) {
	for _, controller := range appContext.GetControllers() {
		bind := controller.GetBind()
		ctrl := controller
		//nolint:exhaustruct // minimal Command initialization with required fields only
		subCmd := &cobra.Command{
			Use:   bind.Use,
			Short: bind.Short,
			Long:  bind.Long,
			Run: func(command *cobra.Command, arguments []string) {
				ctrl.Execute(command, arguments)
			},
		}
		if binder, ok := ctrl.(entities.FlagBinder); ok {
			binder.BindFlags(subCmd)
		}
		rootCmd.AddCommand(subCmd)
	}
}

func main() {
	//nolint:exhaustruct // minimal TextFormatter initialization with required fields only
	logger.SetFormatter(&logger.TextFormatter{
		ForceColors:   true,
		FullTimestamp: true,
	})
	if os.Getenv("DEBUG") == "true" {
		logger.SetLevel(logger.DebugLevel)
	}

	reviewController := injectReviewController()
	cobraRoot := buildRootCommand(reviewController)

	appContext := injectAppContext()
	addSubcommands(cobraRoot, appContext)

	if err := cobraRoot.Execute(); err != nil {
		logger.Fatalf("error executing 'code-guru': %s", err)
	}
}
