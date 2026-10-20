package claude_test

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	claude "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/claude"
)

// lookupFrom reads a fixed environment instead of the process's own.
func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}
}

// readAll reads one handed-over descriptor to the end, as the CLI would.
func readAll(t *testing.T, file *os.File) string {
	t.Helper()
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	return string(data)
}

func TestHandOverCredentials(t *testing.T) {
	t.Parallel()
	if !claude.DescriptorHandoffSupported {
		t.Skip("this platform hands the credential over through the environment")
	}

	t.Run("should hand each credential over its own socket, numbered from descriptor 3", func(t *testing.T) {
		t.Parallel()

		// given
		env := lookupFrom(map[string]string{
			"CLAUDE_CODE_OAUTH_TOKEN": "fixture-oauth-token-placeholder",
			"ANTHROPIC_API_KEY":       "fixture-api-key-placeholder",
		})

		// when
		files, names, closeAll, err := claude.HandOverCredentialsForTest(env)

		// then
		require.NoError(t, err)
		defer closeAll()
		assert.Equal(t, []string{
			"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR=3",
			"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR=4",
		}, names)
		require.Len(t, files, 2)
		for _, file := range files {
			info, statErr := file.Stat()
			require.NoError(t, statErr)
			assert.NotZero(t, info.Mode()&os.ModeSocket, "a socket: the CLI reopens a pipe through /proc")
		}
		assert.Equal(t, "fixture-oauth-token-placeholder\n", readAll(t, files[0]),
			"one line, then end of stream: the server's end is already closed")
		assert.Equal(t, "fixture-api-key-placeholder\n", readAll(t, files[1]))
	})

	t.Run("should trim surrounding whitespace, as the CLI does", func(t *testing.T) {
		t.Parallel()

		// given: a value read from a secret file that ends in a newline.
		env := lookupFrom(map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "  fixture-oauth-token-placeholder\n"})

		// when
		files, _, closeAll, err := claude.HandOverCredentialsForTest(env)

		// then
		require.NoError(t, err)
		defer closeAll()
		require.Len(t, files, 1)
		assert.Equal(t, "fixture-oauth-token-placeholder\n", readAll(t, files[0]))
	})

	t.Run("should hand nothing over when no descriptor credential is set", func(t *testing.T) {
		t.Parallel()

		// given: a blank OAuth token, and a gateway token, which the CLI
		// reads only from its environment.
		env := lookupFrom(map[string]string{
			"CLAUDE_CODE_OAUTH_TOKEN": "   ",
			"ANTHROPIC_AUTH_TOKEN":    "fixture-gateway-token-placeholder",
		})

		// when
		files, names, closeAll, err := claude.HandOverCredentialsForTest(env)

		// then
		require.NoError(t, err)
		defer closeAll()
		assert.Empty(t, files)
		assert.Empty(t, names)
	})

	t.Run("should refuse a credential that spans more than one line, naming only the variable", func(t *testing.T) {
		t.Parallel()

		// given
		env := lookupFrom(map[string]string{"ANTHROPIC_API_KEY": "fixture-first\r\nfixture-second"})

		// when
		_, _, _, err := claude.HandOverCredentialsForTest(env)

		// then
		require.ErrorContains(t, err, "spans more than one line: ANTHROPIC_API_KEY")
		assert.NotContains(t, err.Error(), "fixture-first")
	})

	t.Run("should refuse a credential too large to wait in a socket nobody reads yet", func(t *testing.T) {
		t.Parallel()

		// given
		env := lookupFrom(map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": strings.Repeat("x", 8192)})

		// when
		_, _, _, err := claude.HandOverCredentialsForTest(env)

		// then
		require.ErrorContains(t, err, "handing CLAUDE_CODE_OAUTH_TOKEN to the claude CLI")
	})
}
