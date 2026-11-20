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
	return newDiscoveryRecordingRegistryFor("github", fallback, log)
}

// newDiscoveryRecordingRegistryFor returns a registry whose `name` providers
// record into log.
func newDiscoveryRecordingRegistryFor(
	name string, fallback *doubles.StubFallbackTokenRepository, log *[]string,
) *registry.ProviderRegistry {
	providers := registry.NewProviderRegistry()
	providers.RegisterFactory(name, func(token string) forgeEntities.ForgeProvider {
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

func TestDiscoverCommandAzureDevOpsOrganizations(t *testing.T) {
	t.Parallel()

	t.Run("should hand the provider and the fallback the bare name of an organization written as a URL",
		func(t *testing.T) {
			t.Parallel()

			// given
			var log []string
			fallback := &doubles.StubFallbackTokenRepository{Token: "fixture-runner-token-placeholder"}
			command := commands.NewDiscoverCommand(
				newDiscoveryRecordingRegistryFor("azuredevops", fallback, &log),
				fallback,
			)
			settings := &entities.Settings{Providers: []entities.ProviderConfig{{
				Type: "azuredevops",
				Organizations: []string{
					"ExampleOrg",
					"https://dev.azure.com/ExampleOrg/",
					"https://exampleorg.visualstudio.com/DefaultCollection",
				},
			}}}

			// when
			_, err := command.Execute(context.Background(), settings)

			// then
			require.NoError(t, err)
			assert.Equal(t, []string{
				`discover ExampleOrg with "fixture-runner-token-placeholder" after 1 token request(s)`,
				`discover ExampleOrg with "fixture-runner-token-placeholder" after 2 token request(s)`,
				`discover exampleorg with "fixture-runner-token-placeholder" after 3 token request(s)`,
			}, log)
			assert.Equal(t, []doubles.FallbackTokenRequest{
				{ProviderType: "azuredevops", Organization: "ExampleOrg"},
				{ProviderType: "azuredevops", Organization: "ExampleOrg"},
				{ProviderType: "azuredevops", Organization: "exampleorg"},
			}, fallback.Requests)
		})

	t.Run("should skip only the organization that is not on Azure DevOps", func(t *testing.T) {
		t.Parallel()

		// given
		var log []string
		fallback := &doubles.StubFallbackTokenRepository{Token: "fixture-runner-token-placeholder"}
		command := commands.NewDiscoverCommand(
			newDiscoveryRecordingRegistryFor("azuredevops", fallback, &log),
			fallback,
		)
		settings := &entities.Settings{Providers: []entities.ProviderConfig{{
			Type:          "azuredevops",
			Organizations: []string{"https://attacker.example/x", "ExampleOrg"},
		}}}

		// when
		results, err := command.Execute(context.Background(), settings)

		// then
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, []string{
			`discover ExampleOrg with "fixture-runner-token-placeholder" after 1 token request(s)`,
		}, log)
	})
}

// pullRequestsProvider is a ReviewProvider whose organization holds the
// given repositories, each with one open pull request. Any other call panics
// (the embedded interface is nil).
type pullRequestsProvider struct {
	forgeEntities.ReviewProvider

	token        string
	repositories []string
}

func (p *pullRequestsProvider) Name() string      { return "github" }
func (p *pullRequestsProvider) AuthToken() string { return p.token }

func (p *pullRequestsProvider) DiscoverRepositories(context.Context, string) ([]forgeEntities.Repository, error) {
	repositories := make([]forgeEntities.Repository, 0, len(p.repositories))
	for _, name := range p.repositories {
		repositories = append(repositories, forgeEntities.Repository{Name: name, Organization: "acme"})
	}
	return repositories, nil
}

func (p *pullRequestsProvider) ListOpenPullRequests(
	context.Context, forgeEntities.Repository,
) ([]forgeEntities.PullRequestDetail, error) {
	return []forgeEntities.PullRequestDetail{{ID: 1}}, nil
}

// sequencedFallbackTokens hands out a new, numbered token at every request,
// so a test can tell how recently the token a step used was obtained. Once
// `exhausted` is set it has none left.
type sequencedFallbackTokens struct {
	issued    int
	exhausted bool
}

func (f *sequencedFallbackTokens) GetToken(context.Context, string, string) (string, error) {
	if f.exhausted {
		return "", errors.New("no GitHub credential available")
	}
	f.issued++
	return fmt.Sprintf("fixture-token-%d", f.issued), nil
}

// recordedReview is what one review of the batch was given.
type recordedReview struct {
	repository string
	token      string
	// newest is the newest token the fallback had issued when the review
	// started.
	newest  string
	options commands.ReviewOptions
}

// reviewRecorder is a Review that records what each review was given.
type reviewRecorder struct {
	fallback *sequencedFallbackTokens
	reviews  []recordedReview
}

func (r *reviewRecorder) Execute(
	_ context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	_ forgeEntities.PullRequestDetail,
	opts commands.ReviewOptions,
) (*entities.ReviewResult, error) {
	r.reviews = append(r.reviews, recordedReview{
		repository: repo.Name,
		token:      provider.AuthToken(),
		newest:     fmt.Sprintf("fixture-token-%d", r.fallback.issued),
		options:    opts,
	})
	return &entities.ReviewResult{}, nil
}

// pullRequestsRegistry returns a registry whose "github" providers hold
// three repositories with a pull request each.
func pullRequestsRegistry() *registry.ProviderRegistry {
	providers := registry.NewProviderRegistry()
	providers.RegisterFactory("github", func(token string) forgeEntities.ForgeProvider {
		return &pullRequestsProvider{token: token, repositories: []string{"api", "web", "cli"}}
	})
	return providers
}

func TestReviewAllCommandTokenLifetime(t *testing.T) {
	t.Parallel()

	t.Run("should give each review a token of the runner's obtained for it, and a way to refresh it",
		func(t *testing.T) {
			t.Parallel()

			// given
			fallback := &sequencedFallbackTokens{}
			recorder := &reviewRecorder{fallback: fallback}
			command := commands.NewReviewAllCommand(pullRequestsRegistry(), recorder, fallback)
			settings := &entities.Settings{Providers: []entities.ProviderConfig{
				{Type: "github", Organizations: []string{"acme"}},
			}}

			// when
			_, err := command.Execute(context.Background(), settings, commands.ReviewOptions{})

			// then
			require.NoError(t, err)
			require.Len(t, recorder.reviews, 3)
			seen := map[string]bool{}
			for _, review := range recorder.reviews {
				assert.Equal(
					t,
					review.newest,
					review.token,
					"%s: its token was obtained right before it",
					review.repository,
				)
				assert.False(t, seen[review.token], "%s: no earlier review used its token", review.repository)
				seen[review.token] = true
				assert.True(t, review.options.RunnerCredential, review.repository)
				require.NotNil(t, review.options.RefreshProvider, review.repository)
			}
			before := fallback.issued
			fresh, refreshErr := recorder.reviews[0].options.RefreshProvider(context.Background())
			require.NoError(t, refreshErr)
			assert.Equal(t, fmt.Sprintf("fixture-token-%d", before+1), fresh.AuthToken(),
				"the refresh obtains a new token")
		})

	t.Run("should fail a review's refresh when the fallback has no token left", func(t *testing.T) {
		t.Parallel()

		// given
		fallback := &sequencedFallbackTokens{}
		recorder := &reviewRecorder{fallback: fallback}
		command := commands.NewReviewAllCommand(pullRequestsRegistry(), recorder, fallback)
		settings := &entities.Settings{Providers: []entities.ProviderConfig{
			{Type: "github", Organizations: []string{"acme"}},
		}}
		_, err := command.Execute(context.Background(), settings, commands.ReviewOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, recorder.reviews)
		fallback.exhausted = true

		// when
		fresh, refreshErr := recorder.reviews[0].options.RefreshProvider(context.Background())

		// then: the review keeps the provider it started with rather than
		// switching to one without a token.
		require.Error(t, refreshErr)
		assert.Nil(t, fresh)
	})

	t.Run("should keep the configured token for every review and never refresh it", func(t *testing.T) {
		t.Parallel()

		// given
		fallback := &sequencedFallbackTokens{}
		recorder := &reviewRecorder{fallback: fallback}
		command := commands.NewReviewAllCommand(pullRequestsRegistry(), recorder, fallback)
		settings := &entities.Settings{Providers: []entities.ProviderConfig{
			{Type: "github", Token: "fixture-pat-placeholder", Organizations: []string{"acme"}},
		}}

		// when
		_, err := command.Execute(context.Background(), settings, commands.ReviewOptions{})

		// then
		require.NoError(t, err)
		require.Len(t, recorder.reviews, 3)
		assert.Zero(t, fallback.issued)
		for _, review := range recorder.reviews {
			assert.Equal(t, "fixture-pat-placeholder", review.token, review.repository)
			assert.False(t, review.options.RunnerCredential, review.repository)
			assert.Nil(t, review.options.RefreshProvider, review.repository)
		}
	})
}

func TestDiscoverCommandListsOpenPullRequests(t *testing.T) {
	t.Parallel()

	t.Run("should list every repository's open pull requests with the organization's token", func(t *testing.T) {
		t.Parallel()

		// given
		command := commands.NewDiscoverCommand(pullRequestsRegistry(), nil)
		settings := &entities.Settings{Providers: []entities.ProviderConfig{
			{Type: "github", Token: "fixture-pat-placeholder", Organizations: []string{"acme"}},
		}}

		// when
		results, err := command.Execute(context.Background(), settings)

		// then
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "acme", results[0].Organization)
		require.Len(t, results[0].Repositories, 3)
		for _, repository := range results[0].Repositories {
			assert.Len(t, repository.PullRequests, 1, repository.Repository.Name)
		}
	})
}
