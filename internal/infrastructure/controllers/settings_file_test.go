package controllers_test

import (
	"os"
	"path/filepath"
	"testing"

	logger "github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers"
)

// checkoutSettings is a configuration file a pull request could add to its
// branch: read as code-guru's configuration, it would make code-guru start a
// program of the pull request author's choosing instead of the Claude CLI.
const checkoutSettings = "ai:\n  backend: 'claude'\n  claude:\n    binary_path: '/checkout/not-claude'\n"

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
	t.Run("should keep the default Claude binary when a working-directory configuration names another "+
		"and --config is not given",
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
			assert.Contains(t, hook.LastEntry().Message, "move your configuration to ~/.config/code-guru.yaml")
			assert.NotContains(t, hook.LastEntry().Message, "--config configs/code-guru.yaml")
		})

	t.Run("should name every working-directory configuration it leaves unread "+
		"when the working directory has several and --config is not given", func(t *testing.T) {
		// given
		_, workingDirectory := isolateReviewSettings(t)
		writeReviewSettings(t, filepath.Join(workingDirectory, ".code-guru.yaml"), checkoutSettings)
		writeReviewSettings(t, filepath.Join(workingDirectory, "configs", "code-guru.yaml"), checkoutSettings)
		hook := logrustest.NewLocal(logger.StandardLogger())
		defer hook.Reset()

		// when
		settings, err := controllers.ResolveSettingsForTest("")

		// then
		require.NoError(t, err)
		assert.Equal(t, "claude", settings.AI.Claude.BinaryPath)
		require.Len(t, hook.AllEntries(), 1, "one warning names them all")
		assert.Equal(t, logger.WarnLevel, hook.LastEntry().Level)
		assert.Contains(t, hook.LastEntry().Message, "not reading .code-guru.yaml, configs/code-guru.yaml: ")
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
		assert.Equal(t, "/checkout/not-claude", settings.AI.Claude.BinaryPath)
		assert.Empty(t, hook.AllEntries())
	})

	t.Run("should read the home directory's configuration rather than the working directory's "+
		"when both directories have one", func(t *testing.T) {
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
		assert.NotContains(t, hook.LastEntry().Message, "--config .code-guru.yaml",
			"naming the checkout's file would hand its author the configuration again")
	})
}

// TestBatchSettingsFile is deliberately not parallel: it points HOME at a
// temporary directory and changes the working directory, which t.Setenv and
// t.Chdir forbid in a parallel test.
//
//nolint:paralleltest // t.Setenv and t.Chdir panic under t.Parallel
func TestBatchSettingsFile(t *testing.T) {
	t.Run("should refuse to run when only the working directory has a configuration and --config is not given",
		func(t *testing.T) {
			// given
			_, workingDirectory := isolateReviewSettings(t)
			writeReviewSettings(t, filepath.Join(workingDirectory, ".code-guru.yaml"), checkoutSettings)

			// when
			settings, err := controllers.BatchSettingsForTest("")

			// then
			require.ErrorIs(t, err, controllers.ErrNoSettingsFile)
			assert.Nil(t, settings)
		})

	t.Run("should read the home directory's configuration when --config is not given", func(t *testing.T) {
		// given
		home, workingDirectory := isolateReviewSettings(t)
		writeReviewSettings(t, filepath.Join(home, ".code-guru.yaml"),
			"ai:\n  backend: 'claude'\n  claude:\n    binary_path: '/opt/claude/bin/claude'\n")
		writeReviewSettings(t, filepath.Join(workingDirectory, ".code-guru.yaml"), checkoutSettings)

		// when
		settings, err := controllers.BatchSettingsForTest("")

		// then
		require.NoError(t, err)
		assert.Equal(t, "/opt/claude/bin/claude", settings.AI.Claude.BinaryPath)
	})

	t.Run("should read the file --config names when it is given", func(t *testing.T) {
		// given
		_, workingDirectory := isolateReviewSettings(t)
		writeReviewSettings(t, filepath.Join(workingDirectory, "review.yaml"), checkoutSettings)

		// when
		settings, err := controllers.BatchSettingsForTest("review.yaml")

		// then
		require.NoError(t, err)
		assert.Equal(t, "/checkout/not-claude", settings.AI.Claude.BinaryPath)
	})
}

// TestServeSettingsWarnings is deliberately not parallel: it points HOME at a
// temporary directory, changes the working directory and captures the global
// logrus logger, which t.Setenv and t.Chdir forbid in a parallel test.
//
//nolint:paralleltest // t.Setenv and t.Chdir panic under t.Parallel
func TestServeSettingsWarnings(t *testing.T) {
	t.Run("should warn that the server leaves a working-directory configuration unread", func(t *testing.T) {
		// given
		_, workingDirectory := isolateReviewSettings(t)
		writeReviewSettings(t, filepath.Join(workingDirectory, ".code-guru.yaml"), checkoutSettings)
		hook := logrustest.NewLocal(logger.StandardLogger())
		defer hook.Reset()

		// when
		controllers.WarnAboutUnreadServerSettingsForTest()

		// then
		require.Len(t, hook.AllEntries(), 1)
		assert.Equal(t, logger.WarnLevel, hook.LastEntry().Level)
		assert.Contains(t, hook.LastEntry().Message, "not reading .code-guru.yaml: the server reads its configuration")
	})

	t.Run("should name every working-directory configuration the server leaves unread when there are several",
		func(t *testing.T) {
			// given
			_, workingDirectory := isolateReviewSettings(t)
			writeReviewSettings(t, filepath.Join(workingDirectory, ".code-guru.yml"), checkoutSettings)
			writeReviewSettings(t, filepath.Join(workingDirectory, ".config", "code-guru.yaml"), checkoutSettings)
			hook := logrustest.NewLocal(logger.StandardLogger())
			defer hook.Reset()

			// when
			controllers.WarnAboutUnreadServerSettingsForTest()

			// then
			require.Len(t, hook.AllEntries(), 1)
			assert.Equal(t, logger.WarnLevel, hook.LastEntry().Level)
			assert.Contains(t, hook.LastEntry().Message,
				"not reading .code-guru.yml, .config/code-guru.yaml: the server reads its configuration")
		})

	t.Run("should say nothing when there is nothing the server leaves unread", func(t *testing.T) {
		// given
		isolateReviewSettings(t)
		hook := logrustest.NewLocal(logger.StandardLogger())
		defer hook.Reset()

		// when
		controllers.WarnAboutUnreadServerSettingsForTest()

		// then
		assert.Empty(t, hook.AllEntries())
	})
}

// logrusExit carries the status logrus would have exited with.
type logrusExit struct{ status int }

// runUntilLogrusExit runs run until it ends or calls logrus's exit, as Fatalf
// does, and returns the status it would have exited with (-1 when it did not).
func runUntilLogrusExit(t *testing.T, run func()) int {
	t.Helper()
	status := -1
	previous := logger.StandardLogger().ExitFunc
	logger.StandardLogger().ExitFunc = func(code int) { panic(logrusExit{code}) }
	t.Cleanup(func() { logger.StandardLogger().ExitFunc = previous })
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				exit, ok := recovered.(logrusExit)
				if !ok {
					panic(recovered)
				}
				status = exit.status
			}
		}()
		run()
	}()
	return status
}

// commandWithConfig is a command whose --config flag holds configPath.
func commandWithConfig(t *testing.T, configPath string) *cobra.Command {
	t.Helper()
	command := &cobra.Command{}
	command.Flags().String("config", "", "")
	require.NoError(t, command.Flags().Set("config", configPath))
	return command
}

// TestSettingsFileEntryPoints drives each command's Execute, so the wiring
// between the command and its settings lookup is pinned, not only the helpers.
// It is deliberately not parallel: it points HOME at a temporary directory,
// changes the working directory, replaces the process exit and captures the
// global logrus logger, which t.Setenv and t.Chdir forbid in a parallel test.
//
//nolint:paralleltest // t.Setenv and t.Chdir panic under t.Parallel
func TestSettingsFileEntryPoints(t *testing.T) {
	t.Run("should exit 1 when review-all finds only a working-directory configuration", func(t *testing.T) {
		// given
		_, workingDirectory := isolateReviewSettings(t)
		writeReviewSettings(t, filepath.Join(workingDirectory, ".code-guru.yaml"), checkoutSettings)
		exitStatus := controllers.RecordExitForTest(t)
		controller := controllers.NewReviewAllController(nil, nil, nil, nil, nil)

		// when
		controller.Execute(&cobra.Command{}, nil)

		// then
		assert.Equal(t, 1, *exitStatus)
	})

	t.Run("should exit 1 when discover finds only a working-directory configuration", func(t *testing.T) {
		// given
		_, workingDirectory := isolateReviewSettings(t)
		writeReviewSettings(t, filepath.Join(workingDirectory, ".code-guru.yaml"), checkoutSettings)
		exitStatus := controllers.RecordExitForTest(t)
		controller := controllers.NewDiscoverController(nil)

		// when
		controller.Execute(&cobra.Command{}, nil)

		// then
		assert.Equal(t, 1, *exitStatus)
	})

	t.Run("should exit 1 when review-all cannot load the file --config names", func(t *testing.T) {
		// given
		isolateReviewSettings(t)
		exitStatus := controllers.RecordExitForTest(t)
		controller := controllers.NewReviewAllController(nil, nil, nil, nil, nil)

		// when
		controller.Execute(commandWithConfig(t, "missing.yaml"), nil)

		// then
		assert.Equal(t, 1, *exitStatus)
	})

	t.Run("should stop the server when it is given --config", func(t *testing.T) {
		// given
		isolateReviewSettings(t)
		hook := logrustest.NewLocal(logger.StandardLogger())
		defer hook.Reset()
		controller := controllers.NewServeController(nil, &entities.Settings{})

		// when
		status := runUntilLogrusExit(t, func() { controller.Execute(commandWithConfig(t, "server.yaml"), nil) })

		// then
		assert.Equal(t, 1, status)
		require.NotNil(t, hook.LastEntry())
		assert.Contains(
			t,
			hook.LastEntry().Message,
			"serve: not reading server.yaml: the server does not take --config",
		)
	})

	t.Run("should warn about a working-directory configuration before the server checks its settings",
		func(t *testing.T) {
			// given
			_, workingDirectory := isolateReviewSettings(t)
			writeReviewSettings(t, filepath.Join(workingDirectory, ".code-guru.yaml"), checkoutSettings)
			hook := logrustest.NewLocal(logger.StandardLogger())
			defer hook.Reset()
			controller := controllers.NewServeController(nil, &entities.Settings{})

			// when
			status := runUntilLogrusExit(t, func() { controller.Execute(&cobra.Command{}, nil) })

			// then
			assert.Equal(t, 1, status, "settings without a backend stop the server")
			var warnings []string
			for _, entry := range hook.AllEntries() {
				if entry.Level == logger.WarnLevel {
					warnings = append(warnings, entry.Message)
				}
			}
			require.Len(t, warnings, 1)
			assert.Contains(t, warnings[0], "not reading .code-guru.yaml: the server reads its configuration")
		})
}
