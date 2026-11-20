package controllers_test

import (
	"os"
	"path/filepath"
	"testing"

	logger "github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers"
)

// checkoutSettings is a configuration file a pull request could add to its
// branch: read as code-guru's configuration, it would make code-guru start a
// program from the checkout instead of the Claude CLI.
const checkoutSettings = "ai:\n  backend: 'claude'\n  claude:\n    binary_path: './not-claude'\n"

// isolateReviewSettings points the home directory and the working directory
// at two empty temporary directories, leaves the environment-only settings
// valid (the Claude backend, the default binary), and returns both directories.
func isolateReviewSettings(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	workingDirectory := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODE_GURU_BACKEND", "claude")
	t.Setenv("CODE_GURU_CLAUDE_BINARY_PATH", "")
	t.Chdir(workingDirectory)
	return home, workingDirectory
}

// writeReviewSettings writes content to path, creating its directory.
func writeReviewSettings(t *testing.T, path, content string) {
	t.Helper()
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// TestReviewControllerSettingsFile is deliberately not parallel: it points HOME
// at a temporary directory, changes the working directory and sets CODE_GURU_*
// variables, which t.Setenv and t.Chdir forbid in a parallel test, and it
// captures the global logrus logger.
//
//nolint:paralleltest // t.Setenv and t.Chdir panic under t.Parallel
func TestReviewControllerSettingsFile(t *testing.T) {
	t.Run("should not start the program a working-directory configuration names when --config is not given",
		func(t *testing.T) {
			// given
			_, workingDirectory := isolateReviewSettings(t)
			writeReviewSettings(t, filepath.Join(workingDirectory, "configs", "code-guru.yaml"), checkoutSettings)
			hook := logrustest.NewLocal(logger.StandardLogger())
			defer hook.Reset()

			// when
			settings, err := controllers.ResolveSettingsForTest("")

			// then
			require.NoError(t, err)
			assert.Equal(t, "claude", settings.AI.Claude.BinaryPath)
			require.NotNil(t, hook.LastEntry())
			assert.Equal(t, logger.WarnLevel, hook.LastEntry().Level)
			assert.Contains(t, hook.LastEntry().Message, "not reading configs/code-guru.yaml: ")
			assert.Contains(t, hook.LastEntry().Message, "pass --config configs/code-guru.yaml to use it")
		})

	t.Run("should read a working-directory configuration when --config names it", func(t *testing.T) {
		// given
		_, workingDirectory := isolateReviewSettings(t)
		writeReviewSettings(t, filepath.Join(workingDirectory, ".code-guru.yaml"), checkoutSettings)
		hook := logrustest.NewLocal(logger.StandardLogger())
		defer hook.Reset()

		// when
		settings, err := controllers.ResolveSettingsForTest(".code-guru.yaml")

		// then
		require.NoError(t, err)
		assert.Equal(t, "./not-claude", settings.AI.Claude.BinaryPath)
		assert.Empty(t, hook.AllEntries())
	})

	t.Run("should read the home directory's configuration rather than the working directory's", func(t *testing.T) {
		// given
		home, workingDirectory := isolateReviewSettings(t)
		homeSettings := filepath.Join(home, ".config", "code-guru.yaml")
		writeReviewSettings(t, homeSettings,
			"ai:\n  backend: 'claude'\n  claude:\n    binary_path: '/opt/claude/bin/claude'\n")
		writeReviewSettings(t, filepath.Join(workingDirectory, ".code-guru.yaml"), checkoutSettings)
		hook := logrustest.NewLocal(logger.StandardLogger())
		defer hook.Reset()

		// when
		settings, err := controllers.ResolveSettingsForTest("")

		// then
		require.NoError(t, err)
		assert.Equal(t, "/opt/claude/bin/claude", settings.AI.Claude.BinaryPath)
		require.NotNil(t, hook.LastEntry())
		assert.Contains(t, hook.LastEntry().Message, "not reading .code-guru.yaml: ")
		assert.Contains(t, hook.LastEntry().Message, "using "+homeSettings)
	})
}
