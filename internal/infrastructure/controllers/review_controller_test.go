package controllers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers"
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
			context.Background(), appAndPAT(), "github", "42", mintRecorder(&minted),
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
			"github",
			"",
			mintRecorder(&minted),
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
			"github",
			"42",
			mintRecorder(&minted),
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
		token, err := controllers.ResolveTokenForTest(context.Background(), appAndPAT(), "github", "42", mint)

		// then
		require.ErrorContains(t, err, "401")
		assert.Empty(t, token)
	})
}
