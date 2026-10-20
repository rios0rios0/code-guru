package entities_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

func TestNewSettingsFromEnvAgenticAndExecutor(t *testing.T) {
	t.Run("should read the agentic mode and the kubernetes executor from the environment", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_CLAUDE_MODEL", "claude-opus-5-5")
		t.Setenv("CODE_GURU_CLAUDE_MODE", " Agentic ")
		t.Setenv("CODE_GURU_CLAUDE_AGENTIC_CONFIG_PATH", "/claude-config")
		t.Setenv("CODE_GURU_CLAUDE_AGENTIC_WORKSPACE_DIR", "/workspace")
		t.Setenv("CODE_GURU_CLAUDE_AGENTIC_CLONE_DEPTH", "50")
		t.Setenv("CODE_GURU_CLAUDE_AGENTIC_TIMEOUT", "25m")
		t.Setenv("CODE_GURU_CLAUDE_AGENTIC_MAX_TURNS", "120")
		t.Setenv("CODE_GURU_CLAUDE_AGENTIC_EFFORT", "high")
		t.Setenv("CODE_GURU_CLAUDE_AGENTIC_MAX_BUDGET_USD", "7.5")
		t.Setenv("CODE_GURU_CLAUDE_AGENTIC_FALLBACK_MODEL", "claude-sonnet-4-6")
		t.Setenv("CODE_GURU_CLAUDE_AGENTIC_MIN_CONFIDENCE", "85")
		t.Setenv("CODE_GURU_CLAUDE_AGENTIC_SUBPROCESS_ENV_SCRUB", "false")
		t.Setenv("CODE_GURU_SERVER_EXECUTOR", "kubernetes")
		t.Setenv("CODE_GURU_SERVER_KUBERNETES_NAMESPACE", "reviews")
		t.Setenv("CODE_GURU_SERVER_KUBERNETES_JOB_TEMPLATE", "/etc/code-guru/review-job.json")
		t.Setenv("CODE_GURU_SERVER_KUBERNETES_MAX_CONCURRENT_JOBS", "6")
		t.Setenv("CODE_GURU_SERVER_KUBERNETES_POLL_INTERVAL", "5s")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.True(t, settings.AI.Claude.AgenticEnabled(), "case and padding from a template must not turn it off")
		assert.Equal(t, "claude-opus-5-5", settings.AI.Claude.Model)
		scrubOff := false
		assert.Equal(t, entities.ClaudeAgenticConfig{
			ConfigPath: "/claude-config", WorkspaceDir: "/workspace", CloneDepth: 50, Timeout: 25 * time.Minute,
			MaxTurns: 120, Effort: "high", MaxBudgetUSD: 7.5, FallbackModel: "claude-sonnet-4-6", MinConfidence: 85,
			SubprocessEnvScrub: &scrubOff,
		}, settings.AI.Claude.Agentic)
		assert.True(t, settings.Server.KubernetesExecutorEnabled())
		assert.Equal(t, entities.KubernetesExecutorConfig{
			Namespace: "reviews", JobTemplate: "/etc/code-guru/review-job.json", MaxConcurrentJobs: 6,
			PollInterval: 5 * time.Second,
		}, settings.Server.Kubernetes)
	})

	t.Run("should default to the diff mode and the in-process executor", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")

		// when
		settings, err := entities.NewSettingsFromEnv()

		// then
		require.NoError(t, err)
		assert.False(t, settings.AI.Claude.AgenticEnabled())
		assert.False(t, settings.Server.KubernetesExecutorEnabled())
	})

	t.Run("should reject an unknown claude mode instead of silently using diff", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_CLAUDE_MODE", "agentc")

		// when
		_, err := entities.NewSettingsFromEnv()

		// then
		require.ErrorContains(t, err, `ai.claude.mode "agentc" is not supported`)
	})

	t.Run("should reject the agentic mode on a backend other than claude", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "anthropic")
		t.Setenv("CODE_GURU_ANTHROPIC_API_KEY", "fixture-key-placeholder")
		t.Setenv("CODE_GURU_CLAUDE_MODE", "agentic")

		// when
		_, err := entities.NewSettingsFromEnv()

		// then
		require.ErrorContains(t, err, "requires ai.backend claude")
	})

	t.Run("should expand environment variables in the agentic paths, as rules.path does", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_TEST_HOME", "/srv/guru")
		agentic := entities.ClaudeAgenticConfig{
			ConfigPath:   " ${CODE_GURU_TEST_HOME}/.claude ",
			WorkspaceDir: "$CODE_GURU_TEST_HOME/work",
		}

		// when
		configPath, workspaceDir := agentic.ResolvedConfigPath(), agentic.ResolvedWorkspaceDir()

		// then
		assert.Equal(t, "/srv/guru/.claude", configPath)
		assert.Equal(t, "/srv/guru/work", workspaceDir)
	})

	t.Run("should reject an unknown executor", func(t *testing.T) {
		// given
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_SERVER_EXECUTOR", "nomad")

		// when
		_, err := entities.NewSettingsFromEnv()

		// then
		require.ErrorContains(t, err, `server.executor "nomad" is not supported`)
	})
}

func TestNewSettingsAgenticYAML(t *testing.T) {
	t.Parallel()

	t.Run("should read the agentic and executor blocks from YAML", func(t *testing.T) {
		t.Parallel()

		// given
		path := filepath.Join(t.TempDir(), "code-guru.yaml")
		require.NoError(t, os.WriteFile(path, []byte(`
ai:
  backend: 'claude'
  claude:
    model: 'claude-opus-5-5'
    mode: 'agentic'
    agentic:
      config_path: '/claude'
      timeout: '20m'
      min_confidence: 90
server:
  executor: 'kubernetes'
  kubernetes:
    job_template: '/etc/review-job.yaml'
    max_concurrent_jobs: 4
`), 0o600))

		// when
		settings, err := entities.NewSettings(path)

		// then
		require.NoError(t, err)
		assert.True(t, settings.AI.Claude.AgenticEnabled())
		assert.Equal(t, 20*time.Minute, settings.AI.Claude.Agentic.ReviewTimeout())
		assert.Equal(t, 90, settings.AI.Claude.Agentic.ConfidenceThreshold())
		assert.Equal(t, "/etc/review-job.yaml", settings.Server.Kubernetes.JobTemplate)
		assert.Equal(t, 4, settings.Server.Kubernetes.ConcurrentJobs())
	})
}

func TestAgenticAndExecutorResolvers(t *testing.T) {
	t.Parallel()

	t.Run("should apply the defaults to unset values", func(t *testing.T) {
		t.Parallel()

		// given
		agentic := entities.ClaudeAgenticConfig{}
		executor := entities.KubernetesExecutorConfig{}

		// when
		timeout, turns, confidence := agentic.ReviewTimeout(), agentic.ReviewMaxTurns(), agentic.ConfidenceThreshold()
		jobs, interval := executor.ConcurrentJobs(), executor.SchedulerInterval()

		// then
		assert.Equal(t, 30*time.Minute, timeout)
		assert.Equal(t, 200, turns)
		assert.Equal(t, 80, confidence)
		assert.Equal(t, 3, jobs)
		assert.Equal(t, 15*time.Second, interval)
	})

	t.Run("should clamp a confidence threshold above the scale", func(t *testing.T) {
		t.Parallel()

		// given
		agentic := entities.ClaudeAgenticConfig{MinConfidence: 150}

		// when
		threshold := agentic.ConfidenceThreshold()

		// then
		assert.Equal(t, 100, threshold)
	})
}
