package credentials

import (
	"context"
	"fmt"

	credentialEntities "github.com/rios0rios0/gitforge/v4/pkg/credentials/domain/entities"
	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/support"
)

// ChainFallbackTokenRepository implements repositories.FallbackTokenRepository
// with a gitforge credential chain — in production its default one:
// GH_TOKEN, GITHUB_TOKEN, then the gh CLI's session for github.com on
// GitHub, and AZURE_DEVOPS_EXT_PAT, SYSTEM_ACCESSTOKEN, then the az CLI's
// session on Azure DevOps. Someone who ran `gh auth login` or `az login` does
// not have to export a token as well.
//
// The token goes where a configured one goes and nowhere else: to fixed hosts
// only — the gitforge providers' APIs, which are handed an Azure DevOps
// organization as its bare name, the pull request metadata fetchers' API
// hosts and, in the agentic mode, the checkout's constant clone host — never
// to a host taken from a URL, so a lookalike host cannot receive it.
type ChainFallbackTokenRepository struct {
	resolver credentialEntities.CredentialResolver
}

// NewChainFallbackTokenRepository creates the repository over a credential
// resolver: credentialsInfra.NewDefaultCredentialResolver() in production, a
// stub in tests, so no test runs the developer's gh or az.
func NewChainFallbackTokenRepository(resolver credentialEntities.CredentialResolver) *ChainFallbackTokenRepository {
	return &ChainFallbackTokenRepository{resolver: resolver}
}

// GetToken asks the chain for a token for the provider and, on Azure DevOps,
// the organization, whose tenant the az CLI issues it in. An Azure DevOps
// organization that does not name one on Azure DevOps (see
// support.AzureDevOpsOrganizationName) is refused before the chain is asked:
// a token is never obtained for a host that is not Azure DevOps, nor issued
// in whichever tenant az defaults to. The log names where the token came
// from, never the token.
func (r *ChainFallbackTokenRepository) GetToken(
	ctx context.Context, providerType, organization string,
) (string, error) {
	request := credentialEntities.CredentialRequest{ServiceType: support.ProviderServiceType(providerType)}
	if request.ServiceType == forgeEntities.AZUREDEVOPS {
		name, err := support.AzureDevOpsOrganizationName(organization)
		if err != nil {
			return "", fmt.Errorf(
				"no token configured for provider %q and refusing the fallback: %w",
				providerType,
				err,
			)
		}
		request.Organization = name
	}

	credential, err := r.resolver.Resolve(ctx, request)
	if err != nil {
		return "", fmt.Errorf("no token configured for provider %q and %w", providerType, err)
	}
	if credential.Token == "" {
		return "", fmt.Errorf(
			"no token configured for provider %q and %w", providerType, credentialEntities.ErrNoCredential,
		)
	}

	// The source, never the token: it says whose identity the command acts as.
	logger.Infof("no %s token configured; using the credential from the %s",
		request.ServiceType.DisplayName(), credential.Source)
	return credential.Token, nil
}
