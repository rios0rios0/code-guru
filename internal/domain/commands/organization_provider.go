package commands

import (
	"context"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	registry "github.com/rios0rios0/gitforge/v4/pkg/registry/infrastructure"
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

// organizationProvider returns the provider one organization of a configured
// entry is processed with: `provider`, built with the entry's token, unless
// the entry has no token and fallbackTokens supplies one for the
// organization. The fallback is asked now, at the organization's turn, rather
// than once before the batch, because its token can be short-lived (an Azure
// CLI token can come with minutes left) and is issued in the organization's
// own tenant on Azure DevOps. When it has no token either, a warning names
// every source it tried and the organization is processed as it always was.
func organizationProvider(
	ctx context.Context,
	providerRegistry *registry.ProviderRegistry,
	fallbackTokens repositories.FallbackTokenRepository,
	provider forgeEntities.ReviewProvider,
	entry entities.ProviderConfig,
	organization string,
) forgeEntities.ReviewProvider {
	if entry.Token != "" || fallbackTokens == nil {
		return provider
	}

	token, err := fallbackTokens.GetToken(ctx, entry.Type, organization)
	if err != nil {
		logger.Warn(err.Error())
		return provider
	}
	withToken, err := providerRegistry.GetReviewProvider(entry.Type, token)
	if err != nil {
		logger.Errorf("failed to initialize provider %q: %v", entry.Type, err)
		return provider
	}
	return withToken
}
