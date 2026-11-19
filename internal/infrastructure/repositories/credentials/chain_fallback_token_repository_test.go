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
			"ExampleOrg/":                          "ExampleOrg",
			"https://dev.azure.com/ExampleOrg":     "ExampleOrg",
			"https://dev.azure.com/ExampleOrg/":    "ExampleOrg",
			"https://exampleorg.visualstudio.com/": "exampleorg",
			"https://ado.example.com/ExampleOrg":   "",
			"https://dev.azure.com/%zz":            "",
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
