package controllers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rios0rios0/gitforge/v4/pkg/providers/infrastructure/github"
	registry "github.com/rios0rios0/gitforge/v4/pkg/registry/infrastructure"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers"
	"github.com/rios0rios0/codeguru/internal/support"
	doubles "github.com/rios0rios0/codeguru/test/domain/doubles/repositories"
)

func TestReviewControllerBindFlags(t *testing.T) {
	t.Parallel()

	t.Run("should register the flags the kubernetes executor passes to a review Job", func(t *testing.T) {
		t.Parallel()

		// given
		cmd := &cobra.Command{Use: "review"}

		// when
		(&controllers.ReviewController{}).BindFlags(cmd)
		parseErr := cmd.Flags().Parse([]string{"--mentioned", "--job-name", "code-guru-review-abc"})

		// then
		require.NoError(t, parseErr)
		mentioned, err := cmd.Flags().GetBool("mentioned")
		require.NoError(t, err)
		assert.True(t, mentioned)
		jobName, err := cmd.Flags().GetString("job-name")
		require.NoError(t, err)
		assert.Equal(t, "code-guru-review-abc", jobName)
	})

	t.Run("should default to a first review outside any review Job", func(t *testing.T) {
		t.Parallel()

		// given
		cmd := &cobra.Command{Use: "review"}

		// when
		(&controllers.ReviewController{}).BindFlags(cmd)
		parseErr := cmd.Flags().Parse(nil)

		// then
		require.NoError(t, parseErr)
		mentioned, _ := cmd.Flags().GetBool("mentioned")
		jobName, _ := cmd.Flags().GetString("job-name")
		assert.False(t, mentioned)
		assert.Empty(t, jobName)
	})
}

func TestReviewControllerResolveToken(t *testing.T) {
	t.Parallel()

	// appAndPAT is a deployment with both GitHub credentials configured.
	appAndPAT := func() *entities.Settings {
		return &entities.Settings{
			Providers: []entities.ProviderConfig{{Type: "github", Token: "fixture-pat-placeholder"}},
			GitHubApp: entities.GitHubAppConfig{AppID: 7, PrivateKey: "fixture-key-placeholder"},
		}
	}
	// mintRecorder mints a fixed token and records the installation asked for.
	mintRecorder := func(minted *[]int64) func(context.Context, entities.GitHubAppConfig, int64) (string, error) {
		return func(_ context.Context, _ entities.GitHubAppConfig, installationID int64) (string, error) {
			*minted = append(*minted, installationID)
			return "fixture-installation-token-placeholder", nil
		}
	}

	t.Run("should use the installation the Job names over a configured PAT, as the server does", func(t *testing.T) {
		t.Parallel()

		// given
		var minted []int64

		// when
		token, err := controllers.ResolveTokenForTest(
			context.Background(),
			appAndPAT(),
			controllers.TokenRequestForTest{ProviderType: "github", InstallationID: "42"},
			mintRecorder(&minted),
			nil,
		)

		// then
		require.NoError(t, err)
		assert.Equal(t, "fixture-installation-token-placeholder", token)
		assert.Equal(t, []int64{42}, minted)
	})

	t.Run("should use the configured token when no installation is named", func(t *testing.T) {
		t.Parallel()

		// given
		var minted []int64

		// when
		token, err := controllers.ResolveTokenForTest(
			context.Background(),
			appAndPAT(),
			controllers.TokenRequestForTest{ProviderType: "github"},
			mintRecorder(&minted),
			nil,
		)

		// then
		require.NoError(t, err)
		assert.Equal(t, "fixture-pat-placeholder", token)
		assert.Empty(t, minted)
	})

	t.Run("should use the configured token when the App is not configured", func(t *testing.T) {
		t.Parallel()

		// given
		var minted []int64
		settings := appAndPAT()
		settings.GitHubApp = entities.GitHubAppConfig{}

		// when
		token, err := controllers.ResolveTokenForTest(
			context.Background(),
			settings,
			controllers.TokenRequestForTest{ProviderType: "github", InstallationID: "42"},
			mintRecorder(&minted),
			nil,
		)

		// then
		require.NoError(t, err)
		assert.Equal(t, "fixture-pat-placeholder", token)
		assert.Empty(t, minted)
	})

	t.Run("should fail rather than switch to the PAT when the installation token cannot be minted", func(t *testing.T) {
		t.Parallel()

		// given
		mint := func(context.Context, entities.GitHubAppConfig, int64) (string, error) {
			return "", errors.New("token exchange returned 401")
		}

		// when
		token, err := controllers.ResolveTokenForTest(
			context.Background(),
			appAndPAT(),
			controllers.TokenRequestForTest{ProviderType: "github", InstallationID: "42"},
			mint,
			nil,
		)

		// then
		require.ErrorContains(t, err, "401")
		assert.Empty(t, token)
	})
}

func TestReviewControllerTokenRequest(t *testing.T) {
	t.Parallel()

	pullRequest := func() *support.ParsedPRURL {
		return &support.ParsedPRURL{
			ProviderType: "azuredevops", Organization: "ExampleOrg", Project: "Platform", RepoName: "demo", PRID: 7,
		}
	}

	t.Run("should treat a review started with --job-name as a review Job of the server", func(t *testing.T) {
		t.Parallel()

		// given
		parsed := pullRequest()

		// when
		request := controllers.NewTokenRequestForTest(parsed, "code-guru-review-abc")

		// then
		assert.True(t, request.ReviewJob)
		assert.Equal(t, "azuredevops", request.ProviderType)
		assert.Equal(t, "ExampleOrg", request.Organization)
	})

	t.Run("should treat a review started without --job-name as one a person runs", func(t *testing.T) {
		t.Parallel()

		// given
		parsed := pullRequest()

		// when
		request := controllers.NewTokenRequestForTest(parsed, "")

		// then
		assert.False(t, request.ReviewJob)
		assert.Equal(t, "azuredevops", request.ProviderType)
		assert.Equal(t, "ExampleOrg", request.Organization)
	})
}

func TestReviewControllerResolveTokenFallback(t *testing.T) {
	t.Parallel()

	// tokenless is a configuration whose entry for the provider has no token,
	// as when its `${VAR}` names a variable that is not set.
	tokenless := func(providerType string) *entities.Settings {
		return &entities.Settings{Providers: []entities.ProviderConfig{{Type: providerType}}}
	}
	// personal stands in for the token of the person running the review.
	personal := func() *doubles.StubFallbackTokenRepository {
		return &doubles.StubFallbackTokenRepository{Token: "fixture-personal-token-placeholder"}
	}
	// noMint fails the test's review if it tries to mint an installation token.
	noMint := func(context.Context, entities.GitHubAppConfig, int64) (string, error) {
		return "", errors.New("unexpected installation token exchange")
	}

	t.Run("should fall back to the token of the person running the review when no token is configured",
		func(t *testing.T) {
			t.Parallel()

			// given
			fallback := personal()

			// when
			token, err := controllers.ResolveTokenForTest(
				context.Background(),
				tokenless("github"),
				controllers.TokenRequestForTest{ProviderType: "github", Organization: "rios0rios0"},
				noMint,
				fallback,
			)

			// then
			require.NoError(t, err)
			assert.Equal(t, "fixture-personal-token-placeholder", token)
			assert.Equal(t,
				[]doubles.FallbackTokenRequest{{ProviderType: "github", Organization: "rios0rios0"}},
				fallback.Requests)
		})

	t.Run("should ask for a token for the pull request's Azure DevOps organization", func(t *testing.T) {
		t.Parallel()

		// given
		fallback := personal()

		// when
		token, err := controllers.ResolveTokenForTest(
			context.Background(),
			tokenless("azuredevops"),
			controllers.TokenRequestForTest{ProviderType: "azuredevops", Organization: "ExampleOrg"},
			noMint,
			fallback,
		)

		// then
		require.NoError(t, err)
		assert.Equal(t, "fixture-personal-token-placeholder", token)
		assert.Equal(t,
			[]doubles.FallbackTokenRequest{{ProviderType: "azuredevops", Organization: "ExampleOrg"}},
			fallback.Requests)
	})

	t.Run("should use the configured token without asking for the person's", func(t *testing.T) {
		t.Parallel()

		// given
		fallback := personal()
		settings := &entities.Settings{
			Providers: []entities.ProviderConfig{{Type: "github", Token: "fixture-pat-placeholder"}},
		}

		// when
		token, err := controllers.ResolveTokenForTest(
			context.Background(),
			settings,
			controllers.TokenRequestForTest{ProviderType: "github"},
			noMint,
			fallback,
		)

		// then
		require.NoError(t, err)
		assert.Equal(t, "fixture-pat-placeholder", token)
		assert.Empty(t, fallback.Requests)
	})

	t.Run("should fail rather than switch to the person's token when the installation token cannot be minted",
		func(t *testing.T) {
			t.Parallel()

			// given
			fallback := personal()
			settings := tokenless("github")
			settings.GitHubApp = entities.GitHubAppConfig{AppID: 7, PrivateKey: "fixture-key-placeholder"}
			mint := func(context.Context, entities.GitHubAppConfig, int64) (string, error) {
				return "", errors.New("token exchange returned 401")
			}

			// when
			token, err := controllers.ResolveTokenForTest(
				context.Background(),
				settings,
				controllers.TokenRequestForTest{ProviderType: "github", InstallationID: "42"},
				mint,
				fallback,
			)

			// then
			require.ErrorContains(t, err, "401")
			assert.Empty(t, token)
			assert.Empty(t, fallback.Requests)
		})

	t.Run("should fail rather than use the person's token in a review Job the server started", func(t *testing.T) {
		t.Parallel()

		// given: the pod's environment holds a token, but the Job must
		// authenticate as the server did.
		fallback := personal()

		// when
		token, err := controllers.ResolveTokenForTest(
			context.Background(),
			tokenless("github"),
			controllers.TokenRequestForTest{ProviderType: "github", ReviewJob: true},
			noMint,
			fallback,
		)

		// then
		require.EqualError(t, err, `no token configured for provider "github"`)
		assert.Empty(t, token)
		assert.Empty(t, fallback.Requests)
	})

	t.Run("should fail with the fallback's error, which names every source it tried", func(t *testing.T) {
		t.Parallel()

		// given
		cause := errors.New(`no token configured for provider "github" and no GitHub credential available: ` +
			"GITHUB_TOKEN and GH_TOKEN are not set; the gh CLI is not installed")
		fallback := &doubles.StubFallbackTokenRepository{Err: cause}

		// when
		token, err := controllers.ResolveTokenForTest(
			context.Background(),
			tokenless("github"),
			controllers.TokenRequestForTest{ProviderType: "github"},
			noMint,
			fallback,
		)

		// then
		require.ErrorIs(t, err, cause)
		assert.Empty(t, token)
	})
}

// TestReviewControllerResolveTokenEnvironment holds the cases that depend on
// CODE_GURU_PROVIDER_TOKEN, which the review reads from the process
// environment; t.Setenv rules out t.Parallel.
func TestReviewControllerResolveTokenEnvironment(t *testing.T) {
	noMint := func(context.Context, entities.GitHubAppConfig, int64) (string, error) {
		return "", errors.New("unexpected installation token exchange")
	}

	t.Run("should prefer CODE_GURU_PROVIDER_TOKEN over the token of the person running the review",
		func(t *testing.T) {
			// given
			t.Setenv("CODE_GURU_PROVIDER_TOKEN", "fixture-provider-token-placeholder")
			fallback := &doubles.StubFallbackTokenRepository{Token: "fixture-personal-token-placeholder"}

			// when
			token, err := controllers.ResolveTokenForTest(
				context.Background(),
				&entities.Settings{},
				controllers.TokenRequestForTest{ProviderType: "github"},
				noMint,
				fallback,
			)

			// then
			require.NoError(t, err)
			assert.Equal(t, "fixture-provider-token-placeholder", token)
			assert.Empty(t, fallback.Requests)
		})

	t.Run("should prefer CODE_GURU_PROVIDER_TOKEN when the provider's entry has a token that resolved to nothing",
		func(t *testing.T) {
			// given: the README's own example, `token: '${GITHUB_TOKEN}'` with
			// GITHUB_TOKEN unset, beside an explicitly exported token.
			t.Setenv("CODE_GURU_PROVIDER_TOKEN", "fixture-explicit-bot-token-placeholder")
			settings := &entities.Settings{Providers: []entities.ProviderConfig{{Type: "github", Token: ""}}}
			fallback := &doubles.StubFallbackTokenRepository{Token: "fixture-runner-gh-session-placeholder"}

			// when
			token, err := controllers.ResolveTokenForTest(
				context.Background(),
				settings,
				controllers.TokenRequestForTest{ProviderType: "github", Organization: "acme"},
				noMint,
				fallback,
			)

			// then
			require.NoError(t, err)
			assert.Equal(t, "fixture-explicit-bot-token-placeholder", token)
			assert.Empty(t, fallback.Requests, "an exported token must win before the runner's own credential is asked")
		})

	t.Run("should fall back to the token of the person running the review when nothing is configured at all",
		func(t *testing.T) {
			// given
			t.Setenv("CODE_GURU_PROVIDER_TOKEN", "")
			fallback := &doubles.StubFallbackTokenRepository{Token: "fixture-personal-token-placeholder"}

			// when
			token, err := controllers.ResolveTokenForTest(
				context.Background(),
				&entities.Settings{},
				controllers.TokenRequestForTest{ProviderType: "azuredevops", Organization: "ExampleOrg"},
				noMint,
				fallback,
			)

			// then
			require.NoError(t, err)
			assert.Equal(t, "fixture-personal-token-placeholder", token)
			assert.Equal(t,
				[]doubles.FallbackTokenRequest{{ProviderType: "azuredevops", Organization: "ExampleOrg"}},
				fallback.Requests)
		})
}

func TestReviewControllerRunnerCredential(t *testing.T) {
	t.Parallel()

	noMint := func(context.Context, entities.GitHubAppConfig, int64) (string, error) {
		return "", errors.New("unexpected installation token exchange")
	}
	// githubProviders builds real gitforge GitHub providers, which make no
	// request until they are used.
	githubProviders := func() *registry.ProviderRegistry {
		providers := registry.NewProviderRegistry()
		providers.RegisterFactory("github", github.NewProvider)
		return providers
	}

	t.Run("should report a token from the fallback as the runner's own", func(t *testing.T) {
		t.Parallel()

		// given
		fallback := &doubles.StubFallbackTokenRepository{Token: "fixture-personal-token-placeholder"}
		settings := &entities.Settings{Providers: []entities.ProviderConfig{{Type: "github"}}}

		// when
		token, runner, err := controllers.ResolveTokenWithProvenanceForTest(
			context.Background(), settings, controllers.TokenRequestForTest{ProviderType: "github"}, noMint, fallback,
		)

		// then
		require.NoError(t, err)
		assert.Equal(t, "fixture-personal-token-placeholder", token)
		assert.True(t, runner)
	})

	t.Run("should not report a configured token as the runner's own", func(t *testing.T) {
		t.Parallel()

		// given
		fallback := &doubles.StubFallbackTokenRepository{Token: "fixture-personal-token-placeholder"}
		settings := &entities.Settings{
			Providers: []entities.ProviderConfig{{Type: "github", Token: "fixture-pat-placeholder"}},
		}

		// when
		token, runner, err := controllers.ResolveTokenWithProvenanceForTest(
			context.Background(), settings, controllers.TokenRequestForTest{ProviderType: "github"}, noMint, fallback,
		)

		// then
		require.NoError(t, err)
		assert.Equal(t, "fixture-pat-placeholder", token)
		assert.False(t, runner)
	})

	t.Run("should mark the review and refresh its token from the fallback before it posts", func(t *testing.T) {
		t.Parallel()

		// given
		fallback := &doubles.StubFallbackTokenRepository{Token: "fixture-fresh-token-placeholder"}
		request := controllers.TokenRequestForTest{ProviderType: "github", Organization: "rios0rios0"}

		// when
		opts := controllers.RunnerReviewOptionsForTest(githubProviders(), fallback, request)
		require.NotNil(t, opts.RefreshProvider)
		fresh, err := opts.RefreshProvider(context.Background())

		// then
		require.NoError(t, err)
		assert.True(t, opts.RunnerCredential)
		assert.Equal(t, "fixture-fresh-token-placeholder", fresh.AuthToken())
		assert.Equal(t,
			[]doubles.FallbackTokenRequest{{ProviderType: "github", Organization: "rios0rios0"}},
			fallback.Requests)
	})

	t.Run("should fail the refresh when the fallback has no token any more", func(t *testing.T) {
		t.Parallel()

		// given
		cause := errors.New("no GitHub credential available")
		fallback := &doubles.StubFallbackTokenRepository{Err: cause}

		// when
		opts := controllers.RunnerReviewOptionsForTest(
			githubProviders(), fallback, controllers.TokenRequestForTest{ProviderType: "github"},
		)
		fresh, err := opts.RefreshProvider(context.Background())

		// then
		require.ErrorIs(t, err, cause)
		assert.Nil(t, fresh)
	})
}

func TestReviewControllerReviewOptions(t *testing.T) {
	t.Parallel()

	t.Run("should carry the flags and the settings into the review's options", func(t *testing.T) {
		t.Parallel()

		// given
		settings := &entities.Settings{
			AI:            entities.AIConfig{ReviewDrafts: true},
			Trivial:       entities.TrivialConfig{AutoMerge: true, MergeStrategy: "squash"},
			BotIdentities: []string{"svc-codeguru@example.com"},
		}

		// when
		opts := controllers.ReviewOptionsFromForTest(settings, true, false, true)

		// then
		assert.True(t, opts.DryRun)
		assert.False(t, opts.Verbose)
		assert.True(t, opts.UserMentioned)
		assert.True(t, opts.ReviewDrafts)
		assert.True(t, opts.TrivialAutoMerge)
		assert.Equal(t, "squash", opts.TrivialMergeStrategy)
		assert.Equal(t, []string{"svc-codeguru@example.com"}, opts.BotIdentities)
		assert.False(t, opts.RunnerCredential, "only a token from the fallback marks the review")
		assert.Nil(t, opts.RefreshProvider)
	})
}
