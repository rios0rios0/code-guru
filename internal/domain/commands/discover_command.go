package commands

import (
	"context"

	logger "github.com/sirupsen/logrus"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	registry "github.com/rios0rios0/gitforge/v4/pkg/registry/infrastructure"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

// DiscoverResult holds the discovery output for a single organization.
type DiscoverResult struct {
	Provider     string
	Organization string
	Repositories []DiscoverRepoResult
}

// DiscoverRepoResult holds a repository and its open pull requests.
type DiscoverRepoResult struct {
	Repository   forgeEntities.Repository
	PullRequests []forgeEntities.PullRequestDetail
}

// Discover is the interface for the discover command.
type Discover interface {
	Execute(ctx context.Context, settings *entities.Settings) ([]DiscoverResult, error)
}

// DiscoverCommand discovers repositories and lists open PRs across providers.
type DiscoverCommand struct {
	providerRegistry *registry.ProviderRegistry
	fallbackTokens   repositories.FallbackTokenRepository
}

// NewDiscoverCommand creates a new DiscoverCommand. `fallbackTokens` supplies
// a token for a provider entry configured without one (nil: none).
func NewDiscoverCommand(
	providerRegistry *registry.ProviderRegistry,
	fallbackTokens repositories.FallbackTokenRepository,
) *DiscoverCommand {
	return &DiscoverCommand{providerRegistry: providerRegistry, fallbackTokens: fallbackTokens}
}

// Execute discovers repositories and lists open PRs for all configured providers.
func (c *DiscoverCommand) Execute(
	ctx context.Context,
	settings *entities.Settings,
) ([]DiscoverResult, error) {
	var results []DiscoverResult

	for _, provCfg := range settings.Providers {
		reviewProvider, err := c.providerRegistry.GetReviewProvider(provCfg.Type, provCfg.Token)
		if err != nil {
			logger.Errorf("failed to initialize provider %q: %v", provCfg.Type, err)
			continue
		}

		for _, org := range provCfg.Organizations {
			orgProvider := organizationProvider(
				ctx, c.providerRegistry, c.fallbackTokens, reviewProvider, provCfg, org,
			)
			repos, discoverErr := orgProvider.DiscoverRepositories(ctx, org)
			if discoverErr != nil {
				logger.Errorf("failed to discover repos in %q: %v", org, discoverErr)
				continue
			}

			result := DiscoverResult{
				Provider:     provCfg.Type,
				Organization: org,
			}

			for _, repo := range repos {
				prs, listErr := orgProvider.ListOpenPullRequests(ctx, repo)
				if listErr != nil {
					logger.Debugf("failed to list PRs for %s/%s: %v", repo.Organization, repo.Name, listErr)
					continue
				}

				if len(prs) == 0 {
					continue
				}

				result.Repositories = append(result.Repositories, DiscoverRepoResult{
					Repository:   repo,
					PullRequests: prs,
				})
			}

			results = append(results, result)
		}
	}

	return results, nil
}
