package credentials_test

import (
	"fmt"
	"testing"

	credentialEntities "github.com/rios0rios0/gitforge/v4/pkg/credentials/domain/entities"
	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	forgeDoubles "github.com/rios0rios0/gitforge/v4/test/doubles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/credentials"
	"github.com/rios0rios0/codeguru/internal/support"
)

func TestChainFallbackTokenRepositoryGetToken(t *testing.T) {
	t.Parallel()

	// personal stands in for the credential of the person running the command.
	personal := func() *forgeDoubles.CredentialResolverStub {
		return &forgeDoubles.CredentialResolverStub{
			Credential: credentialEntities.Credential{Token: "fixture-personal-token-placeholder", Source: "gh CLI"},
		}
	}

	t.Run("should return the credential's token when the chain finds one", func(t *testing.T) {
		t.Parallel()

		// given
		resolver := personal()
		repository := credentials.NewChainFallbackTokenRepository(resolver)

		// when
		token, err := repository.GetToken(t.Context(), "github", "rios0rios0")

		// then
		require.NoError(t, err)
		assert.Equal(t, "fixture-personal-token-placeholder", token)
		assert.Equal(t,
			[]credentialEntities.CredentialRequest{{ServiceType: forgeEntities.GITHUB}},
			resolver.Requests(),
			"a GitHub token is not tied to an organization")
	})

	t.Run("should ask for the tenant of the Azure DevOps organization, however it is written", func(t *testing.T) {
		t.Parallel()

		// given
		cases := map[string]string{
			"ExampleOrg":                           "ExampleOrg",
			"https://dev.azure.com/ExampleOrg/":    "ExampleOrg",
			"https://exampleorg.visualstudio.com/": "exampleorg",
		}

		for organization, want := range cases {
			resolver := personal()
			repository := credentials.NewChainFallbackTokenRepository(resolver)

			// when
			_, err := repository.GetToken(t.Context(), "azuredevops", organization)

			// then
			require.NoError(t, err, organization)
			assert.Equal(t,
				[]credentialEntities.CredentialRequest{{ServiceType: forgeEntities.AZUREDEVOPS, Organization: want}},
				resolver.Requests(),
				organization)
		}
	})

	t.Run("should refuse an Azure DevOps organization on another host without asking the chain", func(t *testing.T) {
		t.Parallel()

		// given: what used to leave az on its default tenant and hand the
		// token to the host the entry named.
		organizations := []string{"https://ado.example.com/ExampleOrg", "https://127.0.0.1:8443/attacker-org", ""}

		for _, organization := range organizations {
			resolver := personal()
			repository := credentials.NewChainFallbackTokenRepository(resolver)

			// when
			token, err := repository.GetToken(t.Context(), "azuredevops", organization)

			// then
			require.ErrorIs(t, err, support.ErrNotAzureDevOpsOrganization, organization)
			assert.Empty(t, token, organization)
			assert.Empty(t, resolver.Requests(), organization)
		}
	})

	t.Run("should name every source the chain tried when it finds no credential", func(t *testing.T) {
		t.Parallel()

		// given
		resolver := &forgeDoubles.CredentialResolverStub{
			Err: fmt.Errorf("%w: GITHUB_TOKEN and GH_TOKEN are not set; the gh CLI is not installed",
				credentialEntities.ErrNoCredential),
		}
		repository := credentials.NewChainFallbackTokenRepository(resolver)

		// when
		token, err := repository.GetToken(t.Context(), "github", "")

		// then
		require.ErrorIs(t, err, credentialEntities.ErrNoCredential)
		require.ErrorContains(t, err, `no token configured for provider "github"`)
		require.ErrorContains(t, err, "GITHUB_TOKEN and GH_TOKEN are not set; the gh CLI is not installed")
		assert.Empty(t, token)
	})

	t.Run("should fail when the credential holds no token", func(t *testing.T) {
		t.Parallel()

		// given
		resolver := &forgeDoubles.CredentialResolverStub{Credential: credentialEntities.Credential{Source: "gh CLI"}}
		repository := credentials.NewChainFallbackTokenRepository(resolver)

		// when
		token, err := repository.GetToken(t.Context(), "github", "")

		// then
		require.ErrorIs(t, err, credentialEntities.ErrNoCredential)
		assert.Empty(t, token)
	})
}
