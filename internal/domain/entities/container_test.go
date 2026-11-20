package entities_test

import (
	"os"
	"path/filepath"
	"testing"

	logger "github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// captureLogs records what the standard logger logs, down to the debug level,
// for the rest of the test.
func captureLogs(t *testing.T) *logrustest.Hook {
	t.Helper()
	hook := logrustest.NewLocal(logger.StandardLogger())
	previous := logger.GetLevel()
	logger.SetLevel(logger.DebugLevel)
	t.Cleanup(func() {
		logger.SetLevel(previous)
		hook.Reset()
	})
	return hook
}

// TestProvideSettingsLoadError is deliberately not parallel: it points HOME at
// a temporary directory, changes the working directory, sets CODE_GURU_*
// variables and captures the global logrus logger, which t.Setenv and t.Chdir
// forbid in a parallel test.
func TestProvideSettingsLoadError(t *testing.T) {
	const missingKey = "ai.anthropic.api_key is required when backend is anthropic"

	t.Run("should record why the settings were not loaded and log it only at debug level "+
		"when the only configuration file is in the working directory and the CODE_GURU_ variables do not validate",
		func(t *testing.T) {
			// given
			_, workingDirectory := isolateSettingsDirectories(t)
			writeSettingsFixture(t, filepath.Join(workingDirectory, ".code-guru.yaml"))
			t.Setenv("CODE_GURU_BACKEND", "anthropic")
			t.Setenv("CODE_GURU_ANTHROPIC_API_KEY", "")
			hook := captureLogs(t)

			// when
			settings := entities.ProvideSettingsForTest()

			// then
			require.ErrorContains(t, settings.LoadError(), missingKey)
			require.Len(t, hook.AllEntries(), 1, "every subcommand runs this provider; it must not log an error")
			assert.Equal(t, logger.DebugLevel, hook.LastEntry().Level)
			assert.Contains(t, hook.LastEntry().Message, missingKey)
		})

	t.Run("should warn about the home directory's configuration and record why the CODE_GURU_ variables "+
		"did not load when neither loads", func(t *testing.T) {
		// given
		home, _ := isolateSettingsDirectories(t)
		require.NoError(t, os.WriteFile(filepath.Join(home, ".code-guru.yaml"),
			[]byte("ai:\n  backend: 'claude'\n  claude:\n    binary_path: './bin/claude'\n"), 0o600))
		t.Setenv("CODE_GURU_BACKEND", "anthropic")
		t.Setenv("CODE_GURU_ANTHROPIC_API_KEY", "")
		hook := captureLogs(t)

		// when
		settings := entities.ProvideSettingsForTest()

		// then
		require.ErrorContains(t, settings.LoadError(), missingKey)
		entries := hook.AllEntries()
		require.Len(t, entries, 2)
		assert.Equal(
			t,
			logger.WarnLevel,
			entries[0].Level,
			"the operator's own file failing to load is worth a warning",
		)
		assert.Contains(t, entries[0].Message, "settings: failed to load")
		assert.Equal(t, logger.DebugLevel, entries[1].Level)
	})

	t.Run("should record no load error when the CODE_GURU_ variables validate", func(t *testing.T) {
		// given
		isolateSettingsDirectories(t)
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("CODE_GURU_CLAUDE_BINARY_PATH", "")
		hook := captureLogs(t)

		// when
		settings := entities.ProvideSettingsForTest()

		// then
		require.NoError(t, settings.LoadError())
		assert.Equal(t, "claude", settings.AI.Backend)
		assert.Empty(t, hook.AllEntries())
	})

	t.Run("should record no load error when the home directory's configuration loads", func(t *testing.T) {
		// given
		home, _ := isolateSettingsDirectories(t)
		writeSettingsFixture(t, filepath.Join(home, ".config", "code-guru.yaml"))
		t.Setenv("CODE_GURU_BACKEND", "anthropic")
		t.Setenv("CODE_GURU_ANTHROPIC_API_KEY", "")

		// when
		settings := entities.ProvideSettingsForTest()

		// then
		require.NoError(t, settings.LoadError())
		assert.Equal(t, "claude", settings.AI.Backend)
	})
}
