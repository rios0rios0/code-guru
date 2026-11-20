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
	t.Run("should return the home directory's file and report the working directory's as unread", func(t *testing.T) {
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
