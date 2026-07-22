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

func TestNewSettingsTrivialAutoMergeAuthors(t *testing.T) {
	// Pins that the auto-merge author allowlist is configurable, so a
	// deployment can restrict unattended merges to trusted automation
	// accounts (autobump / autoupdate / config refresh) instead of
	// force-merging every trivial PR — including a human's docs PR —
	// past `Required reviewers`.

	t.Run("should parse CODE_GURU_TRIVIAL_AUTO_MERGE_AUTHORS from the env-only path", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_TRIVIAL_AUTO_MERGE_AUTHORS", "automation@example.com, svc-bump@example.com")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Equal(t, []string{"automation@example.com", "svc-bump@example.com"}, settings.Trivial.AutoMergeAllowedAuthors,
			"comma-separated authors must be split and trimmed so only those accounts auto-merge")
	})

	t.Run("should default to an empty allowlist when the env var is unset", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Empty(t, settings.Trivial.AutoMergeAllowedAuthors,
			"an empty allowlist preserves the historical any-author behaviour for backward compatibility")
	})

	t.Run("should override YAML auto_merge_allowed_authors when the env var is set", func(t *testing.T) {
		// given
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
  auto_merge_allowed_authors:
    - yaml-bot@example.com
`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		t.Setenv("CODE_GURU_TRIVIAL_AUTO_MERGE_AUTHORS", "automation@example.com")

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.Equal(t, []string{"automation@example.com"}, settings.Trivial.AutoMergeAllowedAuthors,
			"env var must override YAML so deployments can pin the allowlist per-environment")
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

func TestReviewAttempts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		maxAttempts int
		want        int
	}{
		{name: "should default to 3 when unset (zero)", maxAttempts: 0, want: 3},
		{name: "should default to 3 when negative", maxAttempts: -2, want: 3},
		{name: "should honour an explicit higher value", maxAttempts: 5, want: 5},
		{name: "should allow 1 to disable retries", maxAttempts: 1, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// given
			cfg := entities.AIConfig{MaxAttempts: tt.maxAttempts}

			// when / then
			assert.Equal(t, tt.want, cfg.ReviewAttempts())
		})
	}
}

func TestPromptBudgets(t *testing.T) {
	t.Parallel()

	t.Run("should default the guidelines budget to a quarter of a 1M-token window", func(t *testing.T) {
		t.Parallel()

		// given: the budget exists so a large but legitimate CLAUDE.md
		// reaches the model whole. 1 MiB is ~256k tokens at ~4 bytes per
		// token — about 25% of a 1M-token context window, leaving the rest
		// for the diff.
		cfg := entities.AIConfig{}

		// when / then
		assert.Equal(t, 1024*1024, cfg.GuidelinesBytes())
	})

	t.Run("should default the description budget below the guidelines budget", func(t *testing.T) {
		t.Parallel()

		// given: a description is intent context, whereas the guidelines
		// are the standard the diff is judged against — so the description
		// must never be allowed to claim the larger share.
		cfg := entities.AIConfig{}

		// when / then
		assert.Equal(t, 64*1024, cfg.PRDescriptionBytes())
		assert.Less(t, cfg.PRDescriptionBytes(), cfg.GuidelinesBytes())
	})

	budgetCases := []struct {
		name  string
		value int
		want  int
	}{
		{name: "should fall back to the default when unset (zero)", value: 0, want: 1024 * 1024},
		{name: "should fall back to the default when negative", value: -1, want: 1024 * 1024},
		{name: "should honour an explicit lower value for a small-window backend", value: 32768, want: 32768},
		{name: "should honour an explicit higher value", value: 4 * 1024 * 1024, want: 4 * 1024 * 1024},
	}
	for _, tt := range budgetCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// given
			cfg := entities.AIConfig{MaxGuidelinesBytes: tt.value}

			// when / then
			assert.Equal(t, tt.want, cfg.GuidelinesBytes())
		})
	}
}

func TestNewSettingsPromptBudgetEnv(t *testing.T) {
	// Pins that a deployment on a small-context-window backend can lower
	// the budgets from env alone, without re-rendering its YAML baseline.

	t.Run("should parse both budget env vars from the env-only path", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_AI_MAX_GUIDELINES_BYTES", "65536")
		t.Setenv("CODE_GURU_AI_MAX_PR_DESCRIPTION_BYTES", "8192")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Equal(t, 65536, settings.AI.GuidelinesBytes())
		assert.Equal(t, 8192, settings.AI.PRDescriptionBytes())
	})

	t.Run("should fall back to the shipped defaults when the env vars are unset", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Equal(t, 1024*1024, settings.AI.GuidelinesBytes(),
			"an unwired deployment must pick up the larger budget automatically")
		assert.Equal(t, 64*1024, settings.AI.PRDescriptionBytes())
	})

	t.Run("should ignore a non-numeric budget rather than truncating to nothing", func(t *testing.T) {
		// given: a typo'd env var must not silently zero the budget and
		// strip every repository's guidelines out of the prompt.
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_AI_MAX_GUIDELINES_BYTES", "not-a-number")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Equal(t, 1024*1024, settings.AI.GuidelinesBytes())
	})
}

func TestNewSettingsMaxAttempts(t *testing.T) {
	// Pins that the per-review AI retry budget is configurable so a flaky
	// backend (non-JSON responses, dropped sockets) re-samples instead of
	// failing the review on the first blip.

	t.Run("should parse CODE_GURU_AI_MAX_ATTEMPTS from the env-only path", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_AI_MAX_ATTEMPTS", "5")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Equal(t, 5, settings.AI.MaxAttempts)
		assert.Equal(t, 5, settings.AI.ReviewAttempts())
	})

	t.Run("should default to the resolver's 3 when the env var is unset", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Equal(t, 0, settings.AI.MaxAttempts, "unset env leaves the raw field zero")
		assert.Equal(t, 3, settings.AI.ReviewAttempts(), "the resolver then defaults to 3 so retries apply automatically")
	})

	t.Run("should override YAML max_attempts when the env var is set", func(t *testing.T) {
		// given
		dir := t.TempDir()
		path := filepath.Join(dir, "code-guru.yaml")
		const body = `ai:
  backend: openai
  openai:
    api_key: yaml-key
  max_attempts: 2
`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		t.Setenv("CODE_GURU_AI_MAX_ATTEMPTS", "4")

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.Equal(t, 4, settings.AI.MaxAttempts,
			"env var must override YAML so deployments can tune the retry budget per-environment")
	})
}

func TestProjectGuidelinesEnabled(t *testing.T) {
	t.Parallel()

	t.Run("should default to true when ProjectGuidelines is nil", func(t *testing.T) {
		t.Parallel()

		// given: nil pointer mirrors the YAML / env "unset" state — pin
		// this default so deployments pick up the reviewed repository's
		// CLAUDE.md without any operator action.
		ai := entities.AIConfig{ProjectGuidelines: nil}

		// when
		got := ai.ProjectGuidelinesEnabled()

		// then
		assert.True(t, got, "unset ProjectGuidelines must resolve to true (the default-ON contract)")
	})

	t.Run("should return true when ProjectGuidelines points to true", func(t *testing.T) {
		t.Parallel()

		// given
		v := true
		ai := entities.AIConfig{ProjectGuidelines: &v}

		// when / then
		assert.True(t, ai.ProjectGuidelinesEnabled())
	})

	t.Run("should return false only when operator explicitly opts out via false", func(t *testing.T) {
		t.Parallel()

		// given: explicit `project_guidelines: false` in YAML or
		// CODE_GURU_AI_PROJECT_GUIDELINES=false is the documented
		// opt-out path.
		v := false
		ai := entities.AIConfig{ProjectGuidelines: &v}

		// when / then
		assert.False(t, ai.ProjectGuidelinesEnabled())
	})
}

func TestContext1MEnabled(t *testing.T) {
	t.Parallel()

	t.Run("should default to true when Context1M is nil", func(t *testing.T) {
		t.Parallel()

		// given: nil mirrors the YAML / env "unset" state — the larger
		// context window is ON by default so large PRs are reviewable
		// without operator action.
		cfg := entities.AnthropicConfig{Context1M: nil}

		// when / then
		assert.True(t, cfg.Context1MEnabled(), "unset Context1M must resolve to true (default-ON contract)")
	})

	t.Run("should return true when Context1M points to true", func(t *testing.T) {
		t.Parallel()

		// given
		v := true
		cfg := entities.AnthropicConfig{Context1M: &v}

		// when / then
		assert.True(t, cfg.Context1MEnabled())
	})

	t.Run("should return false only when the operator explicitly opts out via false", func(t *testing.T) {
		t.Parallel()

		// given: explicit `context_1m: false` in YAML or
		// CODE_GURU_ANTHROPIC_CONTEXT_1M=false is the documented opt-out for
		// accounts / models that cannot use the 1M beta.
		v := false
		cfg := entities.AnthropicConfig{Context1M: &v}

		// when / then
		assert.False(t, cfg.Context1MEnabled())
	})
}

func TestNewSettingsFromEnvContext1M(t *testing.T) {
	t.Run("should leave Context1M nil when the env var is unset so the default-ON path takes over", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "anthropic")
		t.Setenv("CODE_GURU_ANTHROPIC_API_KEY", "test-key-123")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Nil(t, settings.AI.Anthropic.Context1M,
			"unset CODE_GURU_ANTHROPIC_CONTEXT_1M must leave the pointer nil so the default-ON resolver fires")
		assert.True(t, settings.AI.Anthropic.Context1MEnabled())
	})

	t.Run("should resolve to false when the operator explicitly sets the env var to false", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "anthropic")
		t.Setenv("CODE_GURU_ANTHROPIC_API_KEY", "test-key-123")
		t.Setenv("CODE_GURU_ANTHROPIC_CONTEXT_1M", "false")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		require.NotNil(t, settings.AI.Anthropic.Context1M)
		assert.False(t, settings.AI.Anthropic.Context1MEnabled())
	})
}

func TestNewSettingsFromEnvRefusalFallbackModel(t *testing.T) {
	t.Run("should read the refusal fallback model from the environment", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "anthropic")
		t.Setenv("CODE_GURU_ANTHROPIC_API_KEY", "test-key-123")
		t.Setenv("CODE_GURU_ANTHROPIC_REFUSAL_FALLBACK_MODEL", "claude-opus-4-1")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Equal(t, "claude-opus-4-1", settings.AI.Anthropic.RefusalFallbackModel)
	})

	t.Run("should default the refusal fallback model to empty (disabled)", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "anthropic")
		t.Setenv("CODE_GURU_ANTHROPIC_API_KEY", "test-key-123")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Empty(t, settings.AI.Anthropic.RefusalFallbackModel,
			"an unset env var must leave the fallback disabled (a refusal is reported as-is)")
	})
}

func TestNewSettingsFromEnvProjectGuidelines(t *testing.T) {
	t.Run("should leave ProjectGuidelines nil when the env var is not set so the default ON path takes over", func(t *testing.T) {
		// given: a minimal env-only configuration with no
		// CODE_GURU_AI_PROJECT_GUIDELINES setting at all.
		t.Setenv("CODE_GURU_BACKEND", "openai")
		t.Setenv("CODE_GURU_OPENAI_API_KEY", "test-key-123")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then: the field stays nil so ProjectGuidelinesEnabled returns
		// the documented default (true) without operator action.
		require.NoError(t, err)
		assert.Nil(t, settings.AI.ProjectGuidelines,
			"unset CODE_GURU_AI_PROJECT_GUIDELINES must leave the pointer nil so the default-ON resolver fires")
		assert.True(t, settings.AI.ProjectGuidelinesEnabled())
	})

	t.Run("should resolve to false when the operator explicitly sets the env var to false", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "openai")
		t.Setenv("CODE_GURU_OPENAI_API_KEY", "test-key-123")
		t.Setenv("CODE_GURU_AI_PROJECT_GUIDELINES", "false")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		require.NotNil(t, settings.AI.ProjectGuidelines)
		assert.False(t, *settings.AI.ProjectGuidelines)
		assert.False(t, settings.AI.ProjectGuidelinesEnabled())
	})

	t.Run("should leave the pointer nil on an unparseable env value so the default applies", func(t *testing.T) {
		// given: a typo (anything strconv.ParseBool rejects) must not
		// silently flip behaviour — we want the default ON, not OFF.
		t.Setenv("CODE_GURU_BACKEND", "openai")
		t.Setenv("CODE_GURU_OPENAI_API_KEY", "test-key-123")
		t.Setenv("CODE_GURU_AI_PROJECT_GUIDELINES", "maybe")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Nil(t, settings.AI.ProjectGuidelines)
		assert.True(t, settings.AI.ProjectGuidelinesEnabled())
	})
}

func TestNewSettingsProjectGuidelinesEnvOverride(t *testing.T) {
	// Pins that the project-guidelines kill switch works on the YAML path
	// too: deployments ship a YAML baseline and flip per-environment
	// behaviour via env, so CODE_GURU_AI_PROJECT_GUIDELINES must override
	// the file — otherwise an operator's opt-out on a YAML-configured pod
	// would be silently ignored (Copilot review on PR #215).

	t.Run("should override YAML project_guidelines when the env var is set to false", func(t *testing.T) {
		// given
		dir := t.TempDir()
		path := filepath.Join(dir, "code-guru.yaml")
		const body = `ai:
  backend: openai
  openai:
    api_key: yaml-key
  project_guidelines: true
`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		t.Setenv("CODE_GURU_AI_PROJECT_GUIDELINES", "false")

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.False(t, settings.AI.ProjectGuidelinesEnabled(),
			"env var must override YAML so an operator can disable the fetch per-environment")
	})

	t.Run("should keep the YAML value when the env var is unset", func(t *testing.T) {
		// given
		dir := t.TempDir()
		path := filepath.Join(dir, "code-guru.yaml")
		const body = `ai:
  backend: openai
  openai:
    api_key: yaml-key
  project_guidelines: false
`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.False(t, settings.AI.ProjectGuidelinesEnabled(),
			"an unset env var must leave the YAML opt-out authoritative")
	})

	t.Run("should leave the default ON when neither YAML nor env set the flag", func(t *testing.T) {
		// given
		dir := t.TempDir()
		path := filepath.Join(dir, "code-guru.yaml")
		const body = `ai:
  backend: openai
  openai:
    api_key: yaml-key
`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.Nil(t, settings.AI.ProjectGuidelines)
		assert.True(t, settings.AI.ProjectGuidelinesEnabled())
	})
}

func TestPullRequestMetadataEnabled(t *testing.T) {
	t.Parallel()

	t.Run("should default to true when PRMetadata is nil", func(t *testing.T) {
		t.Parallel()

		// given: the YAML / env "unset" state.
		ai := entities.AIConfig{PRMetadata: nil}

		// when
		got := ai.PullRequestMetadataEnabled()

		// then
		assert.True(t, got, "unset PRMetadata must resolve to true (the default-ON contract)")
	})

	t.Run("should return true when PRMetadata points to true", func(t *testing.T) {
		t.Parallel()

		// given
		v := true
		ai := entities.AIConfig{PRMetadata: &v}

		// when / then
		assert.True(t, ai.PullRequestMetadataEnabled())
	})

	t.Run("should return false when the operator explicitly opts out", func(t *testing.T) {
		t.Parallel()

		// given: `pr_metadata: false` in YAML or
		// CODE_GURU_AI_PR_METADATA=false is the documented opt-out.
		v := false
		ai := entities.AIConfig{PRMetadata: &v}

		// when / then
		assert.False(t, ai.PullRequestMetadataEnabled())
	})
}

func TestNewSettingsFromEnvPRMetadata(t *testing.T) {
	t.Run("should leave PRMetadata nil when the env var is not set so the default ON path takes over", func(t *testing.T) {
		// given: a minimal env-only configuration with no
		// CODE_GURU_AI_PR_METADATA setting at all.
		t.Setenv("CODE_GURU_BACKEND", "openai")
		t.Setenv("CODE_GURU_OPENAI_API_KEY", "test-key-123")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.Nil(t, settings.AI.PRMetadata,
			"unset CODE_GURU_AI_PR_METADATA must leave the pointer nil so the default-ON resolver fires")
		assert.True(t, settings.AI.PullRequestMetadataEnabled())
	})

	t.Run("should resolve to false when the operator explicitly sets the env var to false", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "openai")
		t.Setenv("CODE_GURU_OPENAI_API_KEY", "test-key-123")
		t.Setenv("CODE_GURU_AI_PR_METADATA", "false")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		require.NotNil(t, settings.AI.PRMetadata)
		assert.False(t, *settings.AI.PRMetadata)
		assert.False(t, settings.AI.PullRequestMetadataEnabled())
	})
}

func TestNewSettingsPRMetadataEnvOverride(t *testing.T) {
	// Pins that the PR-metadata kill switch works on the YAML path too,
	// mirroring the project-guidelines override contract: an operator's
	// CODE_GURU_AI_PR_METADATA=false on a YAML-configured pod must win.

	t.Run("should override YAML pr_metadata when the env var is set to false", func(t *testing.T) {
		// given
		dir := t.TempDir()
		path := filepath.Join(dir, "code-guru.yaml")
		const body = `ai:
  backend: openai
  openai:
    api_key: yaml-key
  pr_metadata: true
`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		t.Setenv("CODE_GURU_AI_PR_METADATA", "false")

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.False(t, settings.AI.PullRequestMetadataEnabled(),
			"env var must override YAML so an operator can disable the fetch per-environment")
	})

	t.Run("should keep the YAML opt-out when the env var is unset", func(t *testing.T) {
		// given
		dir := t.TempDir()
		path := filepath.Join(dir, "code-guru.yaml")
		const body = `ai:
  backend: openai
  openai:
    api_key: yaml-key
  pr_metadata: false
`
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.False(t, settings.AI.PullRequestMetadataEnabled(),
			"an unset env var must leave the YAML opt-out authoritative")
	})
}
