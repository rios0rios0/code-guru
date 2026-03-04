//go:build unit

package support_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestParsePullRequestURL(t *testing.T) {
	t.Parallel()

	t.Run("should parse GitHub PR URL", func(t *testing.T) {
		// given
		rawURL := "https://github.com/rios0rios0/code-guru/pull/42"

		// when
		result, err := support.ParsePullRequestURL(rawURL)

		// then
		require.NoError(t, err)
		assert.Equal(t, "github", result.ProviderType)
		assert.Equal(t, "rios0rios0", result.Organization)
		assert.Equal(t, "code-guru", result.RepoName)
		assert.Equal(t, 42, result.PRID)
		assert.Empty(t, result.Project)
	})

	t.Run("should parse Azure DevOps PR URL", func(t *testing.T) {
		// given
		rawURL := "https://dev.azure.com/myorg/myproject/_git/myrepo/pullrequest/123"

		// when
		result, err := support.ParsePullRequestURL(rawURL)

		// then
		require.NoError(t, err)
		assert.Equal(t, "azuredevops", result.ProviderType)
		assert.Equal(t, "myorg", result.Organization)
		assert.Equal(t, "myproject", result.Project)
		assert.Equal(t, "myrepo", result.RepoName)
		assert.Equal(t, 123, result.PRID)
	})

	t.Run("should return error for unsupported host", func(t *testing.T) {
		// given
		rawURL := "https://gitlab.com/org/repo/merge_requests/1"

		// when
		_, err := support.ParsePullRequestURL(rawURL)

		// then
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported provider host")
	})

	t.Run("should return error for invalid GitHub URL format", func(t *testing.T) {
		// given
		rawURL := "https://github.com/org/repo"

		// when
		_, err := support.ParsePullRequestURL(rawURL)

		// then
		assert.Error(t, err)
	})

	t.Run("should return error for invalid PR ID", func(t *testing.T) {
		// given
		rawURL := "https://github.com/org/repo/pull/abc"

		// when
		_, err := support.ParsePullRequestURL(rawURL)

		// then
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid PR ID")
	})
}
