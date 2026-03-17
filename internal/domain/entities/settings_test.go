//go:build unit

package entities_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

func TestNewSettingsFromEnv(t *testing.T) {
	t.Run("should build settings with openai defaults when backend env is set", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "openai")
		t.Setenv("CODE_GURU_OPENAI_API_KEY", "test-key-123")
		t.Setenv("CODE_GURU_RULES_PATH", "/tmp/rules")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Equal(t, "openai", settings.AI.Backend)
		assert.Equal(t, "test-key-123", settings.AI.OpenAI.APIKey)
		assert.Equal(t, "gpt-4o", settings.AI.OpenAI.Model)
		assert.Equal(t, "/tmp/rules", settings.Rules.Path)
	})

	t.Run("should build settings with anthropic backend", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "anthropic")
		t.Setenv("CODE_GURU_ANTHROPIC_API_KEY", "sk-ant-test")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Equal(t, "anthropic", settings.AI.Backend)
		assert.Equal(t, "sk-ant-test", settings.AI.Anthropic.APIKey)
		assert.Equal(t, "claude-sonnet-4-20250514", settings.AI.Anthropic.Model)
	})

	t.Run("should fail validation when openai api key is missing", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "openai")

		// when
		_, err := entities.NewSettingsFromEnv()

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "api_key is required")
	})

	t.Run("should fail validation when anthropic api key is missing", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "anthropic")

		// when
		_, err := entities.NewSettingsFromEnv()

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "api_key is required")
	})

	t.Run("should parse trivial adapters from comma-separated env var", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_TRIVIAL_ADAPTERS", "bump-go,docs-only")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.True(t, settings.Trivial.Enabled)
		assert.Equal(t, []string{"bump-go", "docs-only"}, settings.Trivial.Adapters)
	})

	t.Run("should set provider token from env var", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_PROVIDER_TOKEN", "ghp_test123")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		require.Len(t, settings.Providers, 1)
		assert.Equal(t, "ghp_test123", settings.Providers[0].Token)
	})

	t.Run("should default to openai backend when no backend specified", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_OPENAI_API_KEY", "test-key")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Equal(t, "openai", settings.AI.Backend)
	})
}
