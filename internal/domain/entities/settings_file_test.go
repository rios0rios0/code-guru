package entities_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// isolateSettingsDirectories points the home directory and the working
// directory at two empty temporary directories, and returns them.
func isolateSettingsDirectories(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	workingDirectory := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(workingDirectory)
	return home, workingDirectory
}

// writeSettingsFixture writes a minimal configuration file at path.
func writeSettingsFixture(t *testing.T, path string) {
	t.Helper()
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("ai:\n  backend: 'claude'\n"), 0o600))
}

// TestFindSettingsFile is deliberately not parallel: it points HOME at a
// temporary directory and changes the working directory, which t.Setenv and
// t.Chdir forbid in a parallel test.
//
//nolint:paralleltest // t.Setenv and t.Chdir panic under t.Parallel
func TestFindSettingsFile(t *testing.T) {
	t.Run("should return the home directory's file and report the working directory's as unread "+
		"when both directories have one", func(t *testing.T) {
		// given
		home, workingDirectory := isolateSettingsDirectories(t)
		writeSettingsFixture(t, filepath.Join(home, ".code-guru.yaml"))
		writeSettingsFixture(t, filepath.Join(workingDirectory, ".code-guru.yaml"))

		// when
		path, unread := entities.FindSettingsFile()

		// then
		assert.Equal(t, filepath.Join(home, ".code-guru.yaml"), path)
		assert.Equal(t, ".code-guru.yaml", unread)
	})

	for _, relative := range []string{".code-guru.yaml", filepath.Join(".config", "code-guru.yaml"),
		filepath.Join("configs", "code-guru.yml")} {
		t.Run("should not return "+relative+" in the working directory when the home directory has no file",
			func(t *testing.T) {
				// given
				_, workingDirectory := isolateSettingsDirectories(t)
				writeSettingsFixture(t, filepath.Join(workingDirectory, relative))

				// when
				path, unread := entities.FindSettingsFile()

				// then
				assert.Empty(t, path)
				assert.Equal(t, relative, unread)
			})
	}

	t.Run("should report nothing unread when the working directory is the home directory", func(t *testing.T) {
		// given
		home, _ := isolateSettingsDirectories(t)
		writeSettingsFixture(t, filepath.Join(home, ".code-guru.yaml"))
		t.Chdir(home)

		// when
		path, unread := entities.FindSettingsFile()

		// then
		assert.Equal(t, filepath.Join(home, ".code-guru.yaml"), path)
		assert.Empty(t, unread)
	})

	t.Run("should return the file in ~/.config when ~ has none", func(t *testing.T) {
		// given
		home, _ := isolateSettingsDirectories(t)
		writeSettingsFixture(t, filepath.Join(home, ".config", "code-guru.yaml"))

		// when
		path, unread := entities.FindSettingsFile()

		// then
		assert.Equal(t, filepath.Join(home, ".config", "code-guru.yaml"), path)
		assert.Empty(t, unread)
	})

	t.Run(
		"should not search a relative home directory, which would be the working directory again",
		func(t *testing.T) {
			// given
			_, workingDirectory := isolateSettingsDirectories(t)
			writeSettingsFixture(t, filepath.Join(workingDirectory, ".code-guru.yaml"))
			t.Setenv("HOME", ".")
			t.Setenv("USERPROFILE", ".")

			// when
			path, unread := entities.FindSettingsFile()

			// then
			assert.Empty(t, path)
			assert.Equal(t, ".code-guru.yaml", unread)
		},
	)

	t.Run("should return nothing when neither directory has a file", func(t *testing.T) {
		// given
		isolateSettingsDirectories(t)

		// when
		path, unread := entities.FindSettingsFile()

		// then
		assert.Empty(t, path)
		assert.Empty(t, unread)
	})
}

// TestProvideSettingsSettingsFile is deliberately not parallel: it points HOME
// at a temporary directory, changes the working directory and sets CODE_GURU_*
// variables, which t.Setenv and t.Chdir forbid in a parallel test.
//
//nolint:paralleltest // t.Setenv and t.Chdir panic under t.Parallel
func TestProvideSettingsSettingsFile(t *testing.T) {
	// checkoutSettings would make code-guru start a program from the checkout.
	const checkoutSettings = "ai:\n  backend: 'claude'\n  claude:\n    binary_path: '/checkout/not-claude'\n"

	t.Run("should not read the working directory's configuration when the server's settings are provided",
		func(t *testing.T) {
			// given
			_, workingDirectory := isolateSettingsDirectories(t)
			require.NoError(t, os.WriteFile(filepath.Join(workingDirectory, ".code-guru.yaml"),
				[]byte(checkoutSettings), 0o600))
			t.Setenv("CODE_GURU_BACKEND", "claude")
			t.Setenv("CODE_GURU_CLAUDE_BINARY_PATH", "")

			// when
			settings := entities.ProvideSettingsForTest()

			// then
			assert.Equal(t, "claude", settings.AI.Claude.BinaryPath)
		})

	t.Run("should read the home directory's configuration when the server's settings are provided",
		func(t *testing.T) {
			// given
			home, workingDirectory := isolateSettingsDirectories(t)
			require.NoError(t, os.WriteFile(filepath.Join(workingDirectory, ".code-guru.yaml"),
				[]byte(checkoutSettings), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(home, ".code-guru.yaml"),
				[]byte("ai:\n  backend: 'claude'\n  claude:\n    binary_path: '/opt/claude/bin/claude'\n"), 0o600))

			// when
			settings := entities.ProvideSettingsForTest()

			// then
			assert.Equal(t, "/opt/claude/bin/claude", settings.AI.Claude.BinaryPath)
		})
}

func TestNewSettingsClaudeBinaryPath(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		path     string
		accepted bool
	}{
		{"claude", true},
		{"/opt/claude/bin/claude", true},
		{"./bin/claude", false},
		{"bin/claude", false},
		{"~/bin/claude", false},
	} {
		outcome := "refuse"
		if scenario.accepted {
			outcome = "accept"
		}
		t.Run("should "+outcome+" the Claude binary path "+scenario.path+" when the settings are loaded",
			func(t *testing.T) {
				t.Parallel()

				// given
				path := filepath.Join(t.TempDir(), "code-guru.yaml")
				require.NoError(t, os.WriteFile(path,
					[]byte("ai:\n  backend: 'claude'\n  claude:\n    binary_path: '"+scenario.path+"'\n"), 0o600))

				// when
				settings, err := entities.NewSettings(path)

				// then
				if scenario.accepted {
					require.NoError(t, err)
					assert.Equal(t, scenario.path, settings.AI.Claude.BinaryPath)
					return
				}
				require.ErrorContains(t, err, "must be an absolute path or a command name found on PATH")
			})
	}
}
