package credentials

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	credentialEntities "github.com/rios0rios0/gitforge/v4/pkg/credentials/domain/entities"
	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/support"
)

// ChainFallbackTokenRepository implements repositories.FallbackTokenRepository
// with a gitforge credential chain — in production its default one:
// GITHUB_TOKEN, GH_TOKEN, then the gh CLI's session for github.com on
// GitHub, and AZURE_DEVOPS_EXT_PAT, SYSTEM_ACCESSTOKEN, then the az CLI's
// session on Azure DevOps. Someone who ran `gh auth login` or `az login` does
// not have to export a token as well.
//
// The token goes where a configured one goes and nowhere else: to fixed hosts
// only — the gitforge providers' APIs, the pull request metadata fetchers'
// API hosts and, in the agentic mode, the checkout's constant clone host —
// never to a host taken from a URL, so a lookalike host cannot receive it.
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
// the organization, whose tenant the az CLI issues it in. The log names where
// the token came from, never the token.
func (r *ChainFallbackTokenRepository) GetToken(
	ctx context.Context, providerType, organization string,
) (string, error) {
	request := credentialEntities.CredentialRequest{ServiceType: support.ProviderServiceType(providerType)}
	if request.ServiceType == forgeEntities.AZUREDEVOPS {
		request.Organization = azureDevOpsOrganization(organization)
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

// azureDevOpsOrganization returns the name of the Azure DevOps organization
// an `organizations` entry or a pull request names, written either as the
// bare name or as its https://dev.azure.com/<name> or
// https://<name>.visualstudio.com URL. It returns "" for anything else, which
// leaves the Azure CLI on its default tenant.
func azureDevOpsOrganization(entry string) string {
	entry = strings.TrimSpace(entry)
	if !strings.Contains(entry, "://") {
		name, _, _ := strings.Cut(strings.Trim(entry, "/"), "/")
		return name
	}

	parsed, err := url.Parse(entry)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "dev.azure.com" {
		name, _, _ := strings.Cut(strings.Trim(parsed.Path, "/"), "/")
		return name
	}
	if name, ok := strings.CutSuffix(host, ".visualstudio.com"); ok && !strings.Contains(name, ".") {
		return name
	}
	return ""
}
