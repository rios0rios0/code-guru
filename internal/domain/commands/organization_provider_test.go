package commands_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	registry "github.com/rios0rios0/gitforge/v4/pkg/registry/infrastructure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	doubles "github.com/rios0rios0/codeguru/test/domain/doubles/repositories"
)

// discoveryRecordingProvider is a ReviewProvider whose organizations hold no
// repositories. For each organization it is asked to discover, it records the
// token it was built with and how many fallback tokens had been asked for by
// then, so a test can tell which token served which organization, and when
// that token was resolved. Any other call panics (the embedded interface is
// nil).
type discoveryRecordingProvider struct {
	forgeEntities.ReviewProvider

	token    string
	fallback *doubles.StubFallbackTokenRepository
	log      *[]string
}

func (p *discoveryRecordingProvider) Name() string { return "github" }

func (p *discoveryRecordingProvider) DiscoverRepositories(
	_ context.Context, organization string,
) ([]forgeEntities.Repository, error) {
	*p.log = append(*p.log, fmt.Sprintf("discover %s with %q after %d token request(s)",
		organization, p.token, len(p.fallback.Requests)))
	return nil, nil
}

// newDiscoveryRecordingRegistry returns a registry whose "github" providers
// record into log.
func newDiscoveryRecordingRegistry(
	fallback *doubles.StubFallbackTokenRepository, log *[]string,
) *registry.ProviderRegistry {
	providers := registry.NewProviderRegistry()
	providers.RegisterFactory("github", func(token string) forgeEntities.ForgeProvider {
		return &discoveryRecordingProvider{token: token, fallback: fallback, log: log}
	})
	return providers
}

// twoOrganizations is a configuration whose GitHub entry has no token.
func twoOrganizations() *entities.Settings {
	return &entities.Settings{Providers: []entities.ProviderConfig{
		{Type: "github", Organizations: []string{"FirstOrg", "SecondOrg"}},
	}}
}

// organizationTokens answers each organization with a token of its own.
func organizationTokens() *doubles.StubFallbackTokenRepository {
	return &doubles.StubFallbackTokenRepository{Tokens: map[string]string{
		"FirstOrg":  "fixture-first-token-placeholder",
		"SecondOrg": "fixture-second-token-placeholder",
	}}
}

func TestReviewAllCommandFallbackTokens(t *testing.T) {
	t.Parallel()

	t.Run("should ask for the person's token at each organization's turn when the entry has none",
		func(t *testing.T) {
			t.Parallel()

			// given
			var log []string
			fallback := organizationTokens()
			command := commands.NewReviewAllCommand(newDiscoveryRecordingRegistry(fallback, &log), nil, fallback)

			// when
			_, err := command.Execute(context.Background(), twoOrganizations(), commands.ReviewOptions{})

			// then
			require.NoError(t, err)
			assert.Equal(t, []string{
				`discover FirstOrg with "fixture-first-token-placeholder" after 1 token request(s)`,
				`discover SecondOrg with "fixture-second-token-placeholder" after 2 token request(s)`,
			}, log)
			assert.Equal(t, []doubles.FallbackTokenRequest{
				{ProviderType: "github", Organization: "FirstOrg"},
				{ProviderType: "github", Organization: "SecondOrg"},
			}, fallback.Requests)
		})

	t.Run("should use the configured token without asking for the person's", func(t *testing.T) {
		t.Parallel()

		// given
		var log []string
		fallback := organizationTokens()
		command := commands.NewReviewAllCommand(newDiscoveryRecordingRegistry(fallback, &log), nil, fallback)
		settings := twoOrganizations()
		settings.Providers[0].Token = "fixture-pat-placeholder"

		// when
		_, err := command.Execute(context.Background(), settings, commands.ReviewOptions{})

		// then
		require.NoError(t, err)
		assert.Equal(t, []string{
			`discover FirstOrg with "fixture-pat-placeholder" after 0 token request(s)`,
			`discover SecondOrg with "fixture-pat-placeholder" after 0 token request(s)`,
		}, log)
		assert.Empty(t, fallback.Requests)
	})

	t.Run("should process an organization as before when the person has no token either", func(t *testing.T) {
		t.Parallel()

		// given
		var log []string
		fallback := &doubles.StubFallbackTokenRepository{Err: errors.New("no GitHub credential available")}
		command := commands.NewReviewAllCommand(newDiscoveryRecordingRegistry(fallback, &log), nil, fallback)

		// when
		_, err := command.Execute(context.Background(), twoOrganizations(), commands.ReviewOptions{})

		// then
		require.NoError(t, err)
		assert.Equal(t, []string{
			`discover FirstOrg with "" after 1 token request(s)`,
			`discover SecondOrg with "" after 2 token request(s)`,
		}, log)
	})
}

func TestDiscoverCommandFallbackTokens(t *testing.T) {
	t.Parallel()

	t.Run("should ask for the person's token at each organization's turn when the entry has none",
		func(t *testing.T) {
			t.Parallel()

			// given
			var log []string
			fallback := organizationTokens()
			command := commands.NewDiscoverCommand(newDiscoveryRecordingRegistry(fallback, &log), fallback)

			// when
			results, err := command.Execute(context.Background(), twoOrganizations())

			// then
			require.NoError(t, err)
			assert.Len(t, results, 2)
			assert.Equal(t, []string{
				`discover FirstOrg with "fixture-first-token-placeholder" after 1 token request(s)`,
				`discover SecondOrg with "fixture-second-token-placeholder" after 2 token request(s)`,
			}, log)
		})

	t.Run("should not ask for a token when no fallback is wired", func(t *testing.T) {
		t.Parallel()

		// given
		var log []string
		fallback := organizationTokens()
		command := commands.NewDiscoverCommand(newDiscoveryRecordingRegistry(fallback, &log), nil)

		// when
		_, err := command.Execute(context.Background(), twoOrganizations())

		// then
		require.NoError(t, err)
		assert.Equal(t, []string{
			`discover FirstOrg with "" after 0 token request(s)`,
			`discover SecondOrg with "" after 0 token request(s)`,
		}, log)
	})
}
