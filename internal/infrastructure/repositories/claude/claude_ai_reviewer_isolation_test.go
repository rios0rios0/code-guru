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
// directory held (`cwd.txt`), its environment (`env.txt`; where /proc exists,
// also `initial-env.txt`, the environment exactly as it was started with, before
// the shell resets PWD) and its arguments one per line (`argv.txt`), and whether
// it could read its `--system-prompt-file` (`prompt.txt`), then replies with
// `$FAKE_STDOUT`. It returns the stub
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
[ -r /proc/$$/environ ] && tr '\0' '\n' < /proc/$$/environ > "$FAKE_CONTEXT_DIR/initial-env.txt"
for a in "$@"; do printf '%s\n' "$a" >> "$FAKE_CONTEXT_DIR/argv.txt"; done
previous=""
for a in "$@"; do
  if [ "$previous" = "--system-prompt-file" ] && [ -r "$a" ]; then echo readable > "$FAKE_CONTEXT_DIR/prompt.txt"; fi
  previous="$a"
done
[ -n "$FAKE_STDOUT" ] && printf '%s' "$FAKE_STDOUT"
exit 0
`
	require.NoError(t, os.WriteFile(binPath, []byte(body), 0o755))
	return binPath, record
}

// copyFile copies the file at source to target, keeping it executable.
func copyFile(t *testing.T, source, target string) {
	t.Helper()
	content, err := os.ReadFile(source)
	require.NoError(t, err)
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o700))
	require.NoError(t, os.WriteFile(target, content, 0o755))
}

// recordedLines reads one of the stub's records as lines.
func recordedLines(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}

// recordedEnvironments names the stub's records of its environment: `env.txt`,
// and `initial-env.txt` where /proc let the stub record it.
func recordedEnvironments(t *testing.T, record string) []string {
	t.Helper()
	names := []string{"env.txt"}
	if _, err := os.Stat(filepath.Join(record, "initial-env.txt")); err == nil {
		names = append(names, "initial-env.txt")
	}
	return names
}

// recordedValues returns the values env's NAME=value entries give the variable
// name, and nothing else, so a failing assertion prints no other variable: the
// stub records this process's environment, which may hold the developer's own
// credentials.
func recordedValues(env []string, name string) []string {
	var values []string
	for _, entry := range env {
		if entryName, value, found := strings.Cut(entry, "="); found && entryName == name {
			values = append(values, value)
		}
	}
	return values
}

// codeGuruVariableNames returns the names, never the values, of the
// CODE_GURU_* variables among env's NAME=value entries.
func codeGuruVariableNames(env []string) []string {
	var names []string
	for _, entry := range env {
		if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, "CODE_GURU_") {
			names = append(names, name)
		}
	}
	return names
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
			assert.Equal(t, []string{"1"}, recordedValues(env, "CLAUDE_CODE_DISABLE_CLAUDE_MDS"))
			assert.Equal(t, []string{"1"}, recordedValues(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY"))
			assert.Equal(t, []string{"false"}, recordedValues(env, "ENABLE_CLAUDEAI_MCP_SERVERS"),
				"the claude.ai connectors stay off even when the caller's environment turns them on")
		})

	t.Run("should leave every CODE_GURU_ variable out of the CLI's environment and pass the rest on "+
		"when reviewing a diff", func(t *testing.T) {
		// given: code-guru's own settings, its secrets among them, in its environment
		t.Setenv("CODE_GURU_PROVIDER_TOKEN", "fixture-provider-token-placeholder")
		t.Setenv("CODE_GURU_WEBHOOK_SECRET", "fixture-webhook-secret-placeholder")
		t.Setenv("CODE_GURU_ANTHROPIC_API_KEY", "fixture-api-key-placeholder")
		t.Setenv("CODE_GURU_GITHUB_PRIVATE_KEY", "fixture-key-placeholder")
		t.Setenv("CODE_GURU_BACKEND", "claude")
		t.Setenv("FAKE_KEPT_VARIABLE", "kept")
		bin, record := writeContextRecordingClaudeBinary(t)
		t.Setenv("FAKE_CONTEXT_DIR", record)
		t.Setenv("FAKE_STDOUT", isolationReviewReply)
		repo := claude.NewAIReviewerRepository(bin, "sonnet", 1)

		// when
		_, err := repo.ReviewDiff(context.Background(), minimalReviewRequest())

		// then
		require.NoError(t, err)
		for _, name := range recordedEnvironments(t, record) {
			env := recordedLines(t, filepath.Join(record, name))
			assert.Empty(t, codeGuruVariableNames(env), "%s: code-guru's own variables must not reach the CLI", name)
			assert.Equal(t, []string{"kept"}, recordedValues(env, "FAKE_KEPT_VARIABLE"),
				"%s: every other variable must still reach the CLI", name)
		}
	})

	t.Run("should tell the CLI the directory it starts in through PWD when reviewing a diff", func(t *testing.T) {
		// given
		t.Chdir(t.TempDir())
		bin, record := writeContextRecordingClaudeBinary(t)
		t.Setenv("FAKE_CONTEXT_DIR", record)
		t.Setenv("FAKE_STDOUT", isolationReviewReply)
		repo := claude.NewAIReviewerRepository(bin, "sonnet", 1)

		// when
		_, err := repo.ReviewDiff(context.Background(), minimalReviewRequest())

		// then
		require.NoError(t, err)
		initialEnv := filepath.Join(record, "initial-env.txt")
		if _, statErr := os.Stat(initialEnv); statErr != nil {
			t.Skip("the stub cannot see the environment it was started with without /proc")
		}
		startedIn := recordedLines(t, filepath.Join(record, "cwd.txt"))[0]
		assert.Equal(t, []string{startedIn}, recordedValues(recordedLines(t, initialEnv), "PWD"))
	})

	t.Run("should run a binary_path relative to code-guru's working directory when it names a directory",
		func(t *testing.T) {
			// given
			workingDirectory := t.TempDir()
			t.Chdir(workingDirectory)
			bin, record := writeContextRecordingClaudeBinary(t)
			copyFile(t, bin, filepath.Join(workingDirectory, "bin", "fake-claude"))
			t.Setenv("FAKE_CONTEXT_DIR", record)
			t.Setenv("FAKE_STDOUT", isolationReviewReply)
			repo := claude.NewAIReviewerRepository(filepath.Join(".", "bin", "fake-claude"), "sonnet", 1)

			// when
			_, err := repo.ReviewDiff(context.Background(), minimalReviewRequest())

			// then
			require.NoError(t, err)
			assert.FileExists(t, filepath.Join(record, "cwd.txt"), "the relative binary must have run")
		})

	t.Run("should start the CLI in the home directory and hand it a readable prompt when TMPDIR is relative",
		func(t *testing.T) {
			// given: a relative TMPDIR would put the CLI's directory inside the caller's
			workingDirectory := t.TempDir()
			t.Chdir(workingDirectory)
			// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
			require.NoError(t, os.MkdirAll(filepath.Join(workingDirectory, "relative-tmp"), 0o700))
			t.Setenv("TMPDIR", "relative-tmp")
			home := t.TempDir()
			t.Setenv("HOME", home)
			bin, record := writeContextRecordingClaudeBinary(t)
			t.Setenv("FAKE_CONTEXT_DIR", record)
			t.Setenv("FAKE_STDOUT", isolationReviewReply)
			repo := claude.NewAIReviewerRepository(bin, "sonnet", 1)

			// when
			_, err := repo.ReviewDiff(context.Background(), minimalReviewRequest())

			// then
			require.NoError(t, err)
			resolvedHome, resolveErr := filepath.EvalSymlinks(home)
			require.NoError(t, resolveErr)
			assert.Equal(t, resolvedHome, recordedLines(t, filepath.Join(record, "cwd.txt"))[0])
			assert.Equal(t, []string{"readable"}, recordedLines(t, filepath.Join(record, "prompt.txt")),
				"the prompt file must be found from the directory the CLI starts in")
		})
}

func TestWithoutCodeGuruVariables(t *testing.T) {
	t.Parallel()

	environment := []string{
		"PATH=/usr/bin",
		"CODE_GURU_PROVIDER_TOKEN=dropped",
		"Code_Guru_Webhook_Secret=dropped where variable names ignore case",
		"CODE_GURUS=kept",
		"NOT_CODE_GURU_SETTING=kept",
		"=C:=C:\\work",
	}
	for _, scenario := range []struct {
		name       string
		ignoreCase bool
		want       []string
	}{
		{
			name:       "should drop only the variables named CODE_GURU_ in capitals when variable names match exactly",
			ignoreCase: false,
			want: []string{
				"PATH=/usr/bin",
				"Code_Guru_Webhook_Secret=dropped where variable names ignore case",
				"CODE_GURUS=kept",
				"NOT_CODE_GURU_SETTING=kept",
				"=C:=C:\\work",
			},
		},
		{
			name:       "should drop the variables named CODE_GURU_ in any case when variable names ignore case",
			ignoreCase: true,
			want:       []string{"PATH=/usr/bin", "CODE_GURUS=kept", "NOT_CODE_GURU_SETTING=kept", "=C:=C:\\work"},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			// given
			env := slices.Clone(environment)

			// when
			kept := claude.WithoutCodeGuruVariablesForTest(env, scenario.ignoreCase)

			// then
			assert.Equal(t, scenario.want, kept)
		})
	}
}
