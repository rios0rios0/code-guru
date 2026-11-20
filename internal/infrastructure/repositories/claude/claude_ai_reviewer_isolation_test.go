package claude_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/claude"
)

// isolationReviewReply is a CLI reply ReviewDiff accepts as a clean review.
const isolationReviewReply = `{"result":"{\"summary\":\"ok\",\"comments\":[]}"}`

// writeContextRecordingClaudeBinary drops a `/bin/sh` stub that records, under
// `$FAKE_CONTEXT_DIR`, the directory it was started in followed by what that
// directory held (`cwd.txt`), its environment (`env.txt`) and its arguments one
// per line (`argv.txt`), then replies with `$FAKE_STDOUT`. It returns the stub
// and the directory the records go to.
func writeContextRecordingClaudeBinary(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake binary uses /bin/sh; not portable to Windows")
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
	require.NoError(t, os.MkdirAll(record, 0o700))
	binPath := filepath.Join(dir, "fake-claude-context")
	body := `#!/bin/sh
cat > /dev/null
pwd -P > "$FAKE_CONTEXT_DIR/cwd.txt"
ls -A >> "$FAKE_CONTEXT_DIR/cwd.txt"
env > "$FAKE_CONTEXT_DIR/env.txt"
for a in "$@"; do printf '%s\n' "$a" >> "$FAKE_CONTEXT_DIR/argv.txt"; done
[ -n "$FAKE_STDOUT" ] && printf '%s' "$FAKE_STDOUT"
exit 0
`
	require.NoError(t, os.WriteFile(binPath, []byte(body), 0o755))
	return binPath, record
}

// recordedLines reads one of the stub's records as lines.
func recordedLines(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}

// writeCheckoutFile writes content to path, creating its directory.
func writeCheckoutFile(t *testing.T, path, content string) {
	t.Helper()
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// TestClaudeReviewer_ReviewDiff_IgnoresTheWorkingDirectory is deliberately not
// parallel: it changes the working directory and sets FAKE_* variables, which
// t.Chdir and t.Setenv forbid in a parallel test.
func TestClaudeReviewer_ReviewDiff_IgnoresTheWorkingDirectory(t *testing.T) {
	t.Run("should start the CLI in an empty directory of its own when the working directory is a checkout "+
		"with Claude Code settings", func(t *testing.T) {
		// given: a checkout whose `.claude/settings.json` and CLAUDE.md a pull
		// request controls, as the working directory
		checkout := t.TempDir()
		writeCheckoutFile(t, filepath.Join(checkout, ".claude", "settings.json"), `{"hooks":{}}`)
		writeCheckoutFile(t, filepath.Join(checkout, "CLAUDE.md"), "# Instructions\n")
		t.Chdir(checkout)
		bin, record := writeContextRecordingClaudeBinary(t)
		t.Setenv("FAKE_CONTEXT_DIR", record)
		t.Setenv("FAKE_STDOUT", isolationReviewReply)
		repo := claude.NewAIReviewerRepository(bin, "sonnet", 1)

		// when
		_, err := repo.ReviewDiff(context.Background(), minimalReviewRequest())

		// then
		require.NoError(t, err)
		lines := recordedLines(t, filepath.Join(record, "cwd.txt"))
		require.Len(t, lines, 1, "the directory the CLI starts in must hold nothing")
		resolvedCheckout, resolveErr := filepath.EvalSymlinks(checkout)
		require.NoError(t, resolveErr)
		assert.NotEqual(t, resolvedCheckout, lines[0])
		assert.NoDirExists(t, lines[0], "the directory must be removed once the review is done")
	})

	t.Run("should load only the user's settings and no MCP server, session or CLAUDE.md when reviewing a diff",
		func(t *testing.T) {
			// given
			bin, record := writeContextRecordingClaudeBinary(t)
			t.Setenv("FAKE_CONTEXT_DIR", record)
			t.Setenv("FAKE_STDOUT", isolationReviewReply)
			t.Setenv("ENABLE_CLAUDEAI_MCP_SERVERS", "true")
			repo := claude.NewAIReviewerRepository(bin, "sonnet", 1)

			// when
			_, err := repo.ReviewDiff(context.Background(), minimalReviewRequest())

			// then
			require.NoError(t, err)
			argv := recordedLines(t, filepath.Join(record, "argv.txt"))
			sources := slices.Index(argv, "--setting-sources")
			require.NotEqual(t, -1, sources, "the CLI must be told which settings to load")
			require.Less(t, sources+1, len(argv))
			assert.Equal(t, "user", argv[sources+1])
			assert.Contains(t, argv, "--strict-mcp-config")
			assert.Contains(t, argv, "--no-session-persistence")
			env := recordedLines(t, filepath.Join(record, "env.txt"))
			assert.Contains(t, env, "CLAUDE_CODE_DISABLE_CLAUDE_MDS=1")
			assert.Contains(t, env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
			assert.Contains(t, env, "ENABLE_CLAUDEAI_MCP_SERVERS=false")
			assert.NotContains(t, env, "ENABLE_CLAUDEAI_MCP_SERVERS=true")
		})
}
