//go:build unit

package entities_test

import (
	"os"
	"path/filepath"
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

func TestNewSettingsTrivialEnvOverride(t *testing.T) {
	// Pins the contract that `CODE_GURU_TRIVIAL_ADAPTERS` overrides
	// `trivial.adapters` from a loaded YAML. The webhook dispatcher
	// reads `*Settings` once at boot and never re-reads env, so without
	// this overlay a deployment using a config file would silently
	// ignore the env var and the trivial path would stay dark — the
	// exact failure mode that surfaced on the dev cluster smoke PR
	// before this overlay landed.

	writeYAML := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "code-guru.yaml")
		const body = `ai:
  backend: openai
  openai:
    api_key: yaml-key
trivial:
  enabled: true
  adapters:
    - docs-only
`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		return path
	}

	t.Run("should override the YAML adapter list when CODE_GURU_TRIVIAL_ADAPTERS is set", func(t *testing.T) {
		// given: a YAML config that lists only `docs-only`, plus an env
		// var that asks for the bump detectors instead.
		path := writeYAML(t)
		t.Setenv("CODE_GURU_TRIVIAL_ADAPTERS", "bump-go,docs-only")

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.True(t, settings.Trivial.Enabled)
		assert.Equal(t, []string{"bump-go", "docs-only"}, settings.Trivial.Adapters,
			"env var must override YAML so deployments can flip adapters per-environment without re-rendering the config file")
	})

	t.Run("should preserve the YAML adapter list when CODE_GURU_TRIVIAL_ADAPTERS is unset", func(t *testing.T) {
		// given: same YAML, no env var.
		path := writeYAML(t)
		t.Setenv("CODE_GURU_TRIVIAL_ADAPTERS", "")

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.True(t, settings.Trivial.Enabled)
		assert.Equal(t, []string{"docs-only"}, settings.Trivial.Adapters,
			"unset env var must leave the YAML-loaded adapters in place")
	})

	t.Run("should honour CODE_GURU_TRIVIAL_AUTO_MERGE and CODE_GURU_TRIVIAL_MERGE_STRATEGY", func(t *testing.T) {
		// given
		path := writeYAML(t)
		t.Setenv("CODE_GURU_TRIVIAL_AUTO_MERGE", "true")
		t.Setenv("CODE_GURU_TRIVIAL_MERGE_STRATEGY", "squash")

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.True(t, settings.Trivial.AutoMerge,
			"CODE_GURU_TRIVIAL_AUTO_MERGE=true must reach Settings.Trivial.AutoMerge so the dispatcher path can opt in to merging trivial PRs")
		assert.Equal(t, "squash", settings.Trivial.MergeStrategy,
			"the merge strategy env var must reach Settings.Trivial.MergeStrategy so operators can pick `merge` / `squash` / `rebase` per environment")
	})
}

func TestNewSettingsBotIdentities(t *testing.T) {
	// Pins that the bot's posting identity is configurable. On a
	// self-hosted Azure DevOps deployment the bot posts under a service
	// account whose name does not start with `code-guru`; without a
	// configured identity (and absent self-detection), the re-review
	// conversation walk recognised no prior bot threads and the LLM
	// re-posted the same findings on every pass.

	t.Run("should parse CODE_GURU_BOT_IDENTITIES from the env-only path", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_BOT_IDENTITIES", "automation@example.com, svc-codeguru@example.com")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Equal(t, []string{"automation@example.com", "svc-codeguru@example.com"}, settings.BotIdentities,
			"comma-separated identities must be split and trimmed so the re-review walk can recognise the bot's own comments")
	})

	t.Run("should default to no configured identities when the env var is unset", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Empty(t, settings.BotIdentities,
			"no configured identities is valid — the built-in `code-guru` shape and self-detection still apply")
	})

	t.Run("should override the YAML bot_identities when CODE_GURU_BOT_IDENTITIES is set", func(t *testing.T) {
		// given: a YAML baseline that pins one identity, plus an env var
		// that asks for a different one.
		dir := t.TempDir()
		path := filepath.Join(dir, "code-guru.yaml")
		const body = `ai:
  backend: openai
  openai:
    api_key: yaml-key
bot_identities:
  - yaml-bot@example.com
`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		t.Setenv("CODE_GURU_BOT_IDENTITIES", "automation@example.com")

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.Equal(t, []string{"automation@example.com"}, settings.BotIdentities,
			"env var must override YAML so deployments can pin the bot identity per-environment")
	})

	t.Run("should preserve YAML bot_identities when CODE_GURU_BOT_IDENTITIES is unset", func(t *testing.T) {
		// given
		dir := t.TempDir()
		path := filepath.Join(dir, "code-guru.yaml")
		const body = `ai:
  backend: openai
  openai:
    api_key: yaml-key
bot_identities:
  - yaml-bot@example.com
`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		t.Setenv("CODE_GURU_BOT_IDENTITIES", "")

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.Equal(t, []string{"yaml-bot@example.com"}, settings.BotIdentities,
			"unset env var must leave the YAML-loaded identity in place")
	})
}

func TestNewSettingsFromEnvTrivialAutoMerge(t *testing.T) {
	t.Run("should default AutoMerge=false when the env var is unset", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.False(t, settings.Trivial.AutoMerge,
			"auto-merge MUST default off — opting in is an explicit operator decision because it bypasses human review")
		assert.Empty(t, settings.Trivial.MergeStrategy,
			"empty merge strategy lets gitforge fall back to the platform default")
	})

	t.Run("should parse AutoMerge=true and MergeStrategy from env", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_TRIVIAL_AUTO_MERGE", "true")
		t.Setenv("CODE_GURU_TRIVIAL_MERGE_STRATEGY", "rebase")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.True(t, settings.Trivial.AutoMerge)
		assert.Equal(t, "rebase", settings.Trivial.MergeStrategy)
	})
}
