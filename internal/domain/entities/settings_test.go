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

func TestNativeReviewSubmissionEnabled(t *testing.T) {
	t.Parallel()

	t.Run("should default to true when SubmitNativeReview is nil", func(t *testing.T) {
		t.Parallel()

		// given: nil pointer mirrors the YAML / env "unset" state — pin
		// this default so a future refactor that flips the polarity has
		// to update this test deliberately.
		ai := entities.AIConfig{SubmitNativeReview: nil}

		// when
		got := ai.NativeReviewSubmissionEnabled()

		// then
		assert.True(t, got, "unset SubmitNativeReview must resolve to true (the default-ON contract)")
	})

	t.Run("should return true when SubmitNativeReview points to true", func(t *testing.T) {
		t.Parallel()

		// given
		v := true
		ai := entities.AIConfig{SubmitNativeReview: &v}

		// when / then
		assert.True(t, ai.NativeReviewSubmissionEnabled())
	})

	t.Run("should return false only when operator explicitly opts out via false", func(t *testing.T) {
		t.Parallel()

		// given: explicit `submit_native_review: false` in YAML or
		// CODE_GURU_AI_SUBMIT_NATIVE_REVIEW=false is the documented
		// opt-out path.
		v := false
		ai := entities.AIConfig{SubmitNativeReview: &v}

		// when / then
		assert.False(t, ai.NativeReviewSubmissionEnabled())
	})
}

func TestNewSettingsFromEnvNativeReviewDefault(t *testing.T) {
	t.Run("should leave SubmitNativeReview nil when the env var is not set so the default ON path takes over", func(t *testing.T) {
		// given: a minimal env-only configuration with no
		// CODE_GURU_AI_SUBMIT_NATIVE_REVIEW setting at all.
		t.Setenv("CODE_GURU_BACKEND", "openai")
		t.Setenv("CODE_GURU_OPENAI_API_KEY", "test-key-123")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then: the field stays nil so NativeReviewSubmissionEnabled
		// returns the documented default (true) without operator action.
		require.NoError(t, err)
		assert.Nil(t, settings.AI.SubmitNativeReview,
			"unset CODE_GURU_AI_SUBMIT_NATIVE_REVIEW must leave the pointer nil so the default-ON resolver fires")
		assert.True(t, settings.AI.NativeReviewSubmissionEnabled())
	})

	t.Run("should resolve to false when the operator explicitly sets the env var to false", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "openai")
		t.Setenv("CODE_GURU_OPENAI_API_KEY", "test-key-123")
		t.Setenv("CODE_GURU_AI_SUBMIT_NATIVE_REVIEW", "false")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		require.NotNil(t, settings.AI.SubmitNativeReview)
		assert.False(t, *settings.AI.SubmitNativeReview)
		assert.False(t, settings.AI.NativeReviewSubmissionEnabled())
	})

	t.Run("should leave the pointer nil on an unparseable env value so the default applies", func(t *testing.T) {
		// given: a typo (anything strconv.ParseBool rejects) must not
		// silently flip behaviour — we want the default ON, not OFF.
		t.Setenv("CODE_GURU_BACKEND", "openai")
		t.Setenv("CODE_GURU_OPENAI_API_KEY", "test-key-123")
		t.Setenv("CODE_GURU_AI_SUBMIT_NATIVE_REVIEW", "yesplease")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Nil(t, settings.AI.SubmitNativeReview)
		assert.True(t, settings.AI.NativeReviewSubmissionEnabled())
	})

	t.Run("should honour an explicit opt-out shipped with surrounding whitespace (Helm templating)", func(t *testing.T) {
		// given: Helm / templating frequently injects a trailing newline
		// or space when rendering values into a Pod's env. Without
		// trimming, `strconv.ParseBool("false ")` errors and the
		// resolver falls back to the default ON — silently flipping the
		// operator's explicit opt-out into the very behaviour they
		// disabled. Pin the trim contract here.
		t.Setenv("CODE_GURU_BACKEND", "openai")
		t.Setenv("CODE_GURU_OPENAI_API_KEY", "test-key-123")
		t.Setenv("CODE_GURU_AI_SUBMIT_NATIVE_REVIEW", "false \n")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		require.NotNil(t, settings.AI.SubmitNativeReview)
		assert.False(t, *settings.AI.SubmitNativeReview)
		assert.False(t, settings.AI.NativeReviewSubmissionEnabled())
	})
}
