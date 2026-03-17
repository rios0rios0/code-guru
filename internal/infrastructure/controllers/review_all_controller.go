package controllers

import (
	"context"
	"fmt"
	"os"

	configHelpers "github.com/rios0rios0/gitforge/pkg/config/domain/helpers"
	registry "github.com/rios0rios0/gitforge/pkg/registry/infrastructure"
	logger "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	infraRepos "github.com/rios0rios0/codeguru/internal/infrastructure/repositories"
)

// ReviewAllController handles the "review-all" subcommand (batch mode).
type ReviewAllController struct {
	providerRegistry  *registry.ProviderRegistry
	aiReviewerFactory *infraRepos.AIReviewerFactory
	rulesRepoFactory  *infraRepos.RulesRepositoryFactory
}

// NewReviewAllController creates a new ReviewAllController.
func NewReviewAllController(
	providerRegistry *registry.ProviderRegistry,
	aiReviewerFactory *infraRepos.AIReviewerFactory,
	rulesRepoFactory *infraRepos.RulesRepositoryFactory,
) *ReviewAllController {
	return &ReviewAllController{
		providerRegistry:  providerRegistry,
		aiReviewerFactory: aiReviewerFactory,
		rulesRepoFactory:  rulesRepoFactory,
	}
}

// GetBind returns the Cobra command metadata.
func (c *ReviewAllController) GetBind() entities.ControllerBind {
	return entities.ControllerBind{
		Use:   "review-all",
		Short: "Review all open PRs across configured providers",
		Long: `Discover repositories from each configured provider and organization,
list all open pull requests, and run an AI-powered code review on each one.

Requires a configuration file with provider tokens and organization lists.`,
	}
}

// Execute runs the batch review mode.
func (c *ReviewAllController) Execute(cmd *cobra.Command, _ []string) {
	ctx := context.Background()

	configPath, _ := cmd.Flags().GetString("config")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	verbose, _ := cmd.Flags().GetBool("verbose")

	if verbose {
		logger.SetLevel(logger.DebugLevel)
	}

	// load configuration
	cfgPath := configPath
	if cfgPath == "" {
		var err error
		cfgPath, err = configHelpers.FindConfigFile("code-guru")
		if err != nil {
			logger.Errorf(
				"no config file found: %v\nSpecify one with --config or create .code-guru.yaml",
				err,
			)
			return
		}
	}

	logger.Infof("using config file: %s", cfgPath)

	settings, err := entities.NewSettings(cfgPath)
	if err != nil {
		logger.Errorf("failed to load config: %v", err)
		return
	}

	// create dependencies from settings
	aiReviewer := c.aiReviewerFactory.Create(settings)
	rulesRepo := c.rulesRepoFactory.Create(settings)
	reviewCmd := commands.NewReviewCommand(aiReviewer, rulesRepo, nil)
	reviewAllCmd := commands.NewReviewAllCommand(c.providerRegistry, reviewCmd)

	results, err := reviewAllCmd.Execute(ctx, settings, commands.ReviewOptions{
		DryRun:  dryRun,
		Verbose: verbose,
	})
	if err != nil {
		logger.Warnf("batch review completed with errors: %v", err)
	}

	// print summary
	totalComments := 0
	for _, r := range results {
		totalComments += len(r.Comments)
	}

	_, _ = fmt.Fprintf(os.Stdout,
		"\nBatch review complete: %d PRs reviewed, %d comments\n",
		len(results),
		totalComments,
	)
}
