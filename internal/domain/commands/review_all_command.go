package commands

import (
	"context"
	"fmt"

	logger "github.com/sirupsen/logrus"

	registry "github.com/rios0rios0/gitforge/v4/pkg/registry/infrastructure"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

// ReviewAll is the interface for the review-all command (batch mode).
type ReviewAll interface {
	Execute(ctx context.Context, settings *entities.Settings, opts ReviewOptions) ([]entities.ReviewResult, error)
}

// ReviewAllCommand orchestrates reviewing all open PRs across configured providers.
type ReviewAllCommand struct {
	providerRegistry *registry.ProviderRegistry
	reviewCommand    Review
	fallbackTokens   repositories.FallbackTokenRepository
}

// NewReviewAllCommand creates a new ReviewAllCommand. `fallbackTokens`
// supplies a token for a provider entry configured without one (nil: none).
func NewReviewAllCommand(
	providerRegistry *registry.ProviderRegistry,
	reviewCommand Review,
	fallbackTokens repositories.FallbackTokenRepository,
) *ReviewAllCommand {
	return &ReviewAllCommand{
		providerRegistry: providerRegistry,
		reviewCommand:    reviewCommand,
		fallbackTokens:   fallbackTokens,
	}
}

// Execute reviews all open PRs across all configured providers and organizations.
func (c *ReviewAllCommand) Execute(
	ctx context.Context,
	settings *entities.Settings,
	opts ReviewOptions,
) ([]entities.ReviewResult, error) {
	var allResults []entities.ReviewResult
	totalErrors := 0

	for _, provCfg := range settings.Providers {
		reviewProvider, err := c.providerRegistry.GetReviewProvider(provCfg.Type, provCfg.Token)
		if err != nil {
			logger.Errorf("failed to initialize provider %q: %v", provCfg.Type, err)
			totalErrors++
			continue
		}

		logger.Infof("processing provider: %s", reviewProvider.Name())

		for _, org := range provCfg.Organizations {
			providers, orgErr := newOrganizationProviders(
				c.providerRegistry, c.fallbackTokens, reviewProvider, provCfg, org,
			)
			if orgErr != nil {
				logger.Errorf("skipping an organization of provider %q: %v", provCfg.Type, orgErr)
				totalErrors++
				continue
			}
			results, errs := c.processOrganization(ctx, providers, opts)
			allResults = append(allResults, results...)
			totalErrors += errs
		}
	}

	logger.Infof("batch review complete: %d PRs reviewed, %d errors", len(allResults), totalErrors)

	if totalErrors > 0 {
		return allResults, fmt.Errorf("encountered %d errors during batch review", totalErrors)
	}

	return allResults, nil
}

// processOrganization reviews every open pull request of one organization.
// With the runner's own credential each review gets a token obtained for it
// and refreshes it before posting (see organizationProviders.reviewOptions),
// and the forge is read again with a fresh token after a repository's
// reviews: the batch can outlast such a token many times over.
func (c *ReviewAllCommand) processOrganization(
	ctx context.Context,
	providers organizationProviders,
	opts ReviewOptions,
) ([]entities.ReviewResult, int) {
	var results []entities.ReviewResult
	errorCount := 0
	org := providers.organization

	logger.Infof("discovering repositories in %q...", org)

	listing, _ := providers.provider(ctx)
	repos, err := listing.DiscoverRepositories(ctx, org)
	if err != nil {
		logger.Errorf("failed to discover repos in %q: %v", org, err)
		return nil, 1
	}

	logger.Infof("found %d repositories in %q", len(repos), org)

	for _, repo := range repos {
		prs, listErr := listing.ListOpenPullRequests(ctx, repo)
		if listErr != nil {
			logger.Errorf("failed to list PRs for %s/%s: %v", repo.Organization, repo.Name, listErr)
			errorCount++
			continue
		}

		if len(prs) == 0 {
			continue
		}

		logger.Infof("%s/%s: %d open PRs", repo.Organization, repo.Name, len(prs))

		for _, pr := range prs {
			reviewer, runner := providers.provider(ctx)
			result, reviewErr := c.reviewCommand.Execute(ctx, reviewer, repo, pr, providers.reviewOptions(opts, runner))
			if reviewErr != nil {
				logger.Errorf("failed to review PR #%d in %s/%s: %v", pr.ID, repo.Organization, repo.Name, reviewErr)
				errorCount++
				continue
			}
			results = append(results, *result)
		}
		listing, _ = providers.provider(ctx)
	}

	return results, errorCount
}
