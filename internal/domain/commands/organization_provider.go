package commands

import (
	"context"
	"errors"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	registry "github.com/rios0rios0/gitforge/v4/pkg/registry/infrastructure"
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	"github.com/rios0rios0/codeguru/internal/support"
)

// errNoFreshToken reports that the fallback had no token to refresh a review
// with; the review then keeps the one it started with.
var errNoFreshToken = errors.New("the fallback has no token for this provider")

// organizationProviders hands out the providers one organization of a
// configured provider entry is processed with, for each step of a batch.
type organizationProviders struct {
	providerRegistry *registry.ProviderRegistry
	fallbackTokens   repositories.FallbackTokenRepository
	entryProvider    forgeEntities.ReviewProvider
	entry            entities.ProviderConfig

	// organization is the organization as the provider is given it: an
	// Azure DevOps entry's bare name, any other entry as written.
	organization string
}

// newOrganizationProviders checks one organization of a configured entry. An
// Azure DevOps organization must name one on Azure DevOps (see
// support.AzureDevOpsOrganizationName) and is handed to the provider as its
// bare name, so the provider builds https://dev.azure.com/<name> itself:
// gitforge keeps any other https:// entry as the base URL of its requests,
// and would send the token there, whether it was configured or is the
// runner's own. Whatever the token's source, a refused entry is refused here,
// before any token is looked up or any request is made.
func newOrganizationProviders(
	providerRegistry *registry.ProviderRegistry,
	fallbackTokens repositories.FallbackTokenRepository,
	entryProvider forgeEntities.ReviewProvider,
	entry entities.ProviderConfig,
	organization string,
) (organizationProviders, error) {
	if support.ProviderServiceType(entry.Type) == forgeEntities.AZUREDEVOPS {
		name, err := support.AzureDevOpsOrganizationName(organization)
		if err != nil {
			return organizationProviders{}, err
		}
		organization = name
	}
	return organizationProviders{
		providerRegistry: providerRegistry,
		fallbackTokens:   fallbackTokens,
		entryProvider:    entryProvider,
		entry:            entry,
		organization:     organization,
	}, nil
}

// provider returns the provider for the next step of the batch, and whether
// it authenticates with the runner's own credential. With a configured token
// it is always the entry's provider. Without one, the fallback is asked again
// at every call, because a token it hands over can expire within minutes (an
// Azure CLI token can come with only a few left). When the fallback has no
// token either, a warning names every source it tried and the entry's
// provider is used, as before the fallback existed.
func (p organizationProviders) provider(ctx context.Context) (forgeEntities.ReviewProvider, bool) {
	if p.entry.Token != "" || p.fallbackTokens == nil {
		return p.entryProvider, false
	}

	token, err := p.fallbackTokens.GetToken(ctx, p.entry.Type, p.organization)
	if err != nil {
		logger.Warn(err.Error())
		return p.entryProvider, false
	}
	withToken, err := p.providerRegistry.GetReviewProvider(p.entry.Type, token)
	if err != nil {
		logger.Errorf("failed to initialize provider %q: %v", p.entry.Type, err)
		return p.entryProvider, false
	}
	return withToken, true
}

// reviewOptions returns the options of one pull request's review: opts as
// they are when the review uses a configured token, and, when it uses the
// runner's own (runner), opts marked RunnerCredential with a RefreshProvider
// that asks the fallback again before the review posts its results.
func (p organizationProviders) reviewOptions(opts ReviewOptions, runner bool) ReviewOptions {
	if !runner {
		return opts
	}
	opts.RunnerCredential = true
	opts.RefreshProvider = func(ctx context.Context) (forgeEntities.ReviewProvider, error) {
		provider, fresh := p.provider(ctx)
		if !fresh {
			return nil, errNoFreshToken
		}
		return provider, nil
	}
	return opts
}
