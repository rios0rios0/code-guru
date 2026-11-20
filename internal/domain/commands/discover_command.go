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
			providers, orgErr := newOrganizationProviders(
				c.providerRegistry, c.fallbackTokens, reviewProvider, provCfg, org,
			)
			if orgErr != nil {
				logger.Errorf("skipping an organization of provider %q: %v", provCfg.Type, orgErr)
				continue
			}
			if result, ok := discoverOrganization(ctx, providers, provCfg.Type, org); ok {
				results = append(results, result)
			}
		}
	}

	return results, nil
}

// discoverOrganization lists the open pull requests of one organization. It
// only lists, quickly, so one token serves the whole organization.
func discoverOrganization(
	ctx context.Context, providers organizationProviders, providerType, org string,
) (DiscoverResult, bool) {
	orgProvider, _ := providers.provider(ctx)
	repos, err := orgProvider.DiscoverRepositories(ctx, providers.organization)
	if err != nil {
		logger.Errorf("failed to discover repos in %q: %v", org, err)
		return DiscoverResult{}, false
	}

	result := DiscoverResult{
		Provider:     providerType,
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
	return result, true
}
