package claude_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	claude "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/claude"
	"github.com/rios0rios0/codeguru/internal/support"
)

// fakeAgentCLI is a `/bin/sh` stand-in for the Claude CLI. The reviewer gives
// the CLI a scrubbed environment, so nothing can steer the fake through env
// vars: its whole behaviour — the stdout it prints, the exit code, an optional
// delay — is baked into the script, and it records its argv (NUL-separated),
// environment, working directory and stdin into `recordDir`, and what it reads
// from each credential descriptor (fakeCLIDescriptorProbe).
type fakeAgentCLI struct {
	binary    string
	recordDir string
}

func newFakeAgentCLI(t *testing.T, stdout string, exitCode int, delaySeconds int) fakeAgentCLI {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake binary uses /bin/sh")
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
	require.NoError(t, os.MkdirAll(record, 0o700))
	stdoutFile := filepath.Join(dir, "stdout.json")
	require.NoError(t, os.WriteFile(stdoutFile, []byte(stdout), 0o600))
	script := "#!/bin/sh\n" +
		"RECORD='" + record + "'\n" +
		"printf '%s\\0' \"$@\" > '" + record + "/args'\n" +
		"env > '" + record + "/env'\n" +
		"pwd > '" + record + "/cwd'\n" +
		fakeCLIDescriptorProbe +
		"cat > '" + record + "/stdin'\n"
	if delaySeconds > 0 {
		script += "exec sleep " + strconv.Itoa(delaySeconds) + "\n"
	}
	script += "cat '" + stdoutFile + "'\nexit " + strconv.Itoa(exitCode) + "\n"
	binary := filepath.Join(dir, "fake-claude")
	require.NoError(t, os.WriteFile(binary, []byte(script), 0o755))
	return fakeAgentCLI{binary: binary, recordDir: record}
}

// fakeCLIDescriptorProbe records, for each credential descriptor the CLI is
// told about, the line the descriptor held (in a file named after the
// variable) and whether it was a socket (`<variable>.type`).
const fakeCLIDescriptorProbe = `for v in CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR; do
  eval "fd=\${$v:-}"
  [ -n "$fd" ] || continue
  if [ -S "/dev/fd/$fd" ]; then echo socket > "$RECORD/$v.type"; else echo other > "$RECORD/$v.type"; fi
  eval "IFS= read -r line <&$fd" && printf '%s' "$line" > "$RECORD/$v"
done
`

// ran reports whether the fake was started at all.
func (f fakeAgentCLI) ran() bool {
	_, err := os.Stat(filepath.Join(f.recordDir, "args"))
	return err == nil
}

// newAgenticReviewer builds the reviewer under test with no model credential
// of the shell the tests run in within reach, so no result depends on it.
func newAgenticReviewer(binary string, config entities.ClaudeAgenticConfig) *claude.AgenticReviewerRepository {
	return claude.NewAgenticReviewerRepository(binary, "m", config).WithCredentialsForTest(nil)
}

func (f fakeAgentCLI) args(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.recordDir, "args"))
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
}

func (f fakeAgentCLI) env(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.recordDir, "env"))
	require.NoError(t, err)
	env := map[string]string{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			env[key] = value
		}
	}
	return env
}

func (f fakeAgentCLI) read(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.recordDir, name))
	require.NoError(t, err)
	return strings.TrimSpace(string(raw))
}

// flagValue returns the value following a flag in argv.
func flagValue(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func agentEnvelope(t *testing.T, structured any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "num_turns": 9,
		"total_cost_usd": 0.42, "structured_output": structured, "permission_denials": []any{},
	})
	require.NoError(t, err)
	return string(data)
}

func agenticWorkspace(t *testing.T) *entities.ReviewWorkspace {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "repo")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
	require.NoError(t, os.MkdirAll(path, 0o700))
	return &entities.ReviewWorkspace{
		Root: root, Path: path, SourceBranch: "feat/x", TargetBranch: "main",
		BaseCommit: "base", HeadCommit: "head",
		ChangedFiles: []entities.WorkspaceFileChange{{Path: "a.go", Status: "M", Additions: 1}},
	}
}

func agenticRequestFor(workspace *entities.ReviewWorkspace) entities.ReviewRequest {
	return entities.ReviewRequest{
		PullRequest: forgeEntities.PullRequestDetail{
			PullRequest: forgeEntities.PullRequest{
				ID:    7,
				Title: "feat: x",
			},
			SourceBranch: "feat/x",
			TargetBranch: "main",
		},
		Rules:     []entities.Rule{{Name: "testing", Content: "Every exported function needs a test."}},
		Workspace: workspace,
	}
}

func TestAgenticReviewerReviewDiff(t *testing.T) {
	t.Parallel()

	t.Run("should write settings that deny by default, block outside reads and disable hooks", func(t *testing.T) {
		t.Parallel()

		// given
		cli := newFakeAgentCLI(t, agentEnvelope(t, map[string]any{"summary": "s", "verdict": "approve"}), 0, 0)
		workspace := agenticWorkspace(t)
		reviewer := newAgenticReviewer(cli.binary, entities.ClaudeAgenticConfig{})

		// when
		_, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(workspace))

		// then
		require.NoError(t, err)
		raw, readErr := os.ReadFile(flagValue(cli.args(t), "--settings"))
		require.NoError(t, readErr)
		var settings struct {
			DisableAllHooks bool `json:"disableAllHooks"`
			Permissions     struct {
				DefaultMode   string   `json:"defaultMode"`
				BlockOutside  bool     `json:"blockReadsOutsideWorkingDirectories"`
				DisableBypass string   `json:"disableBypassPermissionsMode"`
				Allow         []string `json:"allow"`
				Deny          []string `json:"deny"`
			} `json:"permissions"`
		}
		require.NoError(t, json.Unmarshal(raw, &settings))
		assert.True(t, settings.DisableAllHooks)
		assert.Equal(t, "dontAsk", settings.Permissions.DefaultMode)
		assert.True(t, settings.Permissions.BlockOutside)
		assert.Equal(t, "disable", settings.Permissions.DisableBypass)
		assert.Contains(t, settings.Permissions.Allow, "Bash(git log:*)")
		for _, denied := range []string{"Bash(git -*)", "Bash(git *--no-index*)", "Read(//proc/**)", "WebFetch"} {
			assert.Contains(t, settings.Permissions.Deny, denied)
		}
		assert.Contains(t, string(raw), `"instructionFiles": "managed-only"`,
			"no AGENTS.md or CLAUDE.md is auto-loaded as instructions")
	})

	t.Run("should fall back to the final text when the CLI returned no structured output", func(t *testing.T) {
		t.Parallel()

		// given
		envelope := `{"type":"result","subtype":"success","is_error":false,"result":` +
			`"{\"summary\":\"s\",\"verdict\":\"approve\",\"comments\":[],\"checked\":{},\"dropped\":[]}"}`
		cli := newFakeAgentCLI(t, envelope, 0, 0)
		reviewer := newAgenticReviewer(cli.binary, entities.ClaudeAgenticConfig{})

		// when
		result, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(agenticWorkspace(t)))

		// then
		require.NoError(t, err)
		assert.Equal(t, "approve", result.Verdict)
	})

	t.Run("should classify a stop at the turn cap as an incomplete review", func(t *testing.T) {
		t.Parallel()

		// given
		cli := newFakeAgentCLI(t, `{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":200}`, 1, 0)
		reviewer := newAgenticReviewer(cli.binary, entities.ClaudeAgenticConfig{})

		// when
		_, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(agenticWorkspace(t)))

		// then
		require.ErrorIs(t, err, support.ErrAgenticReviewIncomplete)
	})

	t.Run("should classify a run past its timeout as an incomplete review", func(t *testing.T) {
		t.Parallel()

		// given
		cli := newFakeAgentCLI(t, agentEnvelope(t, map[string]any{}), 0, 5)
		reviewer := newAgenticReviewer(cli.binary, entities.ClaudeAgenticConfig{
			Timeout: 200 * time.Millisecond,
		})

		// when
		_, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(agenticWorkspace(t)))

		// then
		require.ErrorIs(t, err, support.ErrAgenticReviewIncomplete)
	})

	t.Run("should report an API error as a backend failure, not an incomplete review", func(t *testing.T) {
		t.Parallel()

		// given: the shape the CLI prints when the model endpoint answers 503.
		cli := newFakeAgentCLI(
			t,
			`{"type":"result","subtype":"success","is_error":true,"api_error_status":503,"result":"API Error: 503"}`,
			1,
			0,
		)
		reviewer := newAgenticReviewer(cli.binary, entities.ClaudeAgenticConfig{})

		// when
		_, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(agenticWorkspace(t)))

		// then
		require.Error(t, err)
		require.NotErrorIs(t, err, support.ErrAgenticReviewIncomplete, "a transient outage may be retried")
		assert.Contains(t, err.Error(), "claude CLI reported an error")
	})

	t.Run("should refuse to run without a checkout", func(t *testing.T) {
		t.Parallel()

		// given
		reviewer := claude.NewAgenticReviewerRepository("unused", "m", entities.ClaudeAgenticConfig{})

		// when
		_, err := reviewer.ReviewDiff(context.Background(), entities.ReviewRequest{})

		// then
		require.ErrorContains(t, err, "needs a checkout")
	})

	t.Run("should install skills and commands without their tool grants, and nothing else", func(t *testing.T) {
		t.Parallel()

		// given: an operator tree with a skill that pre-approves its own build.
		tree := t.TempDir()
		skill := "---\nname: go-review\nallowed-tools:\n  - Bash(make *)\n  - Edit\ndescription: Go checklist.\n---\n\nCheck slices.\n"
		writeTreeFile(t, tree, "skills/go-review/SKILL.md", skill)
		writeTreeFile(t, tree, "commands/check.md", "---\nallowed-tools: Bash(npm test)\n---\nRun checks.\n")
		writeTreeFile(t, tree, "skills/broken/SKILL.md", "---\nallowed-tools: Bash(make *)\nthe fence never closes\n")
		writeTreeFile(t, tree, "rules/golang.md", "# Go\n")
		writeTreeFile(t, tree, "settings.json", `{"permissions":{"allow":["Bash"]}}`)
		require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(tree, "skills", "leak.md")))
		cli := newFakeAgentCLI(t, agentEnvelope(t, map[string]any{"summary": "s", "verdict": "approve"}), 0, 0)
		workspace := agenticWorkspace(t)
		reviewer := newAgenticReviewer(cli.binary, entities.ClaudeAgenticConfig{ConfigPath: tree})

		// when
		_, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(workspace))

		// then
		require.NoError(t, err)
		configDir := filepath.Join(workspace.Root, "agent", "config")
		installed, readErr := os.ReadFile(filepath.Join(configDir, "skills", "go-review", "SKILL.md"))
		require.NoError(t, readErr)
		assert.Equal(t, "---\nname: go-review\ndescription: Go checklist.\n---\n\nCheck slices.\n", string(installed))
		command, readErr := os.ReadFile(filepath.Join(configDir, "commands", "check.md"))
		require.NoError(t, readErr)
		assert.NotContains(t, string(command), "allowed-tools")
		assert.NoDirExists(t, filepath.Join(configDir, "rules"), "rules reach the agent through the prompt, once")
		assert.NoFileExists(t, filepath.Join(configDir, "settings.json"), "only the bot writes the agent's settings")
		assert.NoFileExists(t, filepath.Join(configDir, "skills", "leak.md"), "symlinks are never followed")
		assert.NoFileExists(t, filepath.Join(configDir, "skills", "broken", "SKILL.md"),
			"a frontmatter that cannot be read with certainty is not installed at all")
	})
}

// TestAgenticReviewerEnvironment holds the cases that set process environment
// (t.Setenv) to prove what does and does not reach the CLI; t.Setenv rules out
// t.Parallel.
func TestAgenticReviewerEnvironment(t *testing.T) {
	t.Run(
		"should run the CLI hardened, in the checkout, and return the filtered structured review",
		func(t *testing.T) {
			// given
			t.Setenv("CODE_GURU_PROVIDER_TOKEN", "fixture-provider-token-placeholder")
			t.Setenv("CODE_GURU_WEBHOOK_SECRET", "fixture-webhook-secret-placeholder")
			t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "fixture-oauth-token-placeholder")
			structured := map[string]any{
				"summary": "Adds x.", "verdict": "request_changes",
				"checked": map[string]any{"correctness": "c", "security": "s", "design": "d"},
				"comments": []any{
					map[string]any{
						"file":       "a.go",
						"line":       3,
						"dimension":  "bug",
						"confidence": 90,
						"severity":   "error",
						"body":       "nil deref",
					},
					map[string]any{
						"file":       "a.go",
						"line":       9,
						"dimension":  "design",
						"confidence": 70,
						"severity":   "info",
						"body":       "naming",
					},
				},
				"dropped": []any{
					map[string]any{
						"file":       "a.go",
						"line":       1,
						"dimension":  "design",
						"confidence": 30,
						"reason":     "style",
					},
				},
			}
			cli := newFakeAgentCLI(t, agentEnvelope(t, structured), 0, 0)
			workspace := agenticWorkspace(t)
			reviewer := claude.NewAgenticReviewerRepository(cli.binary, "claude-opus-5-5", entities.ClaudeAgenticConfig{
				MaxTurns: 77, Effort: "high", MaxBudgetUSD: 2.5, FallbackModel: "claude-sonnet-4-6",
			}).WithScrubSandboxForTest(true)

			// when
			result, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(workspace))

			// then
			require.NoError(t, err)
			require.Len(t, result.Comments, 1, "the 70-confidence finding is below the default bar of 80")
			assert.Equal(t, "nil deref", result.Comments[0].Body)
			require.Len(t, result.Dropped, 2)
			assert.Equal(t, "below the confidence threshold of 80", result.Dropped[1].Reason)
			assert.Equal(t, "request_changes", result.Verdict)

			args := cli.args(t)
			assert.Equal(t, "claude-opus-5-5", flagValue(args, "--model"))
			assert.Equal(t, "json", flagValue(args, "--output-format"))
			assert.JSONEq(t, support.AgenticReviewSchema, flagValue(args, "--json-schema"))
			assert.Equal(t, "77", flagValue(args, "--max-turns"))
			assert.Equal(t, "Read,Grep,Glob,Bash,Skill", flagValue(args, "--tools"))
			assert.Equal(t, "dontAsk", flagValue(args, "--permission-mode"))
			assert.Equal(t, "none", flagValue(args, "--permission-prompts"))
			assert.Equal(t, "user", flagValue(args, "--setting-sources"),
				"the checkout's own .claude settings, hooks and MCP servers must never load")
			assert.Contains(t, args, "--strict-mcp-config")
			assert.Contains(t, args, "--no-session-persistence")
			assert.Equal(t, "high", flagValue(args, "--effort"))
			assert.Equal(t, "2.5", flagValue(args, "--max-budget-usd"))
			assert.Equal(t, "claude-sonnet-4-6", flagValue(args, "--fallback-model"))
			assert.Contains(t, flagValue(args, "--allowedTools"), "Bash(git blame:*)",
				"the allow rules travel as one comma-joined argument")
			assert.Contains(t, flagValue(args, "--disallowedTools"), "WebFetch")

			systemPrompt, readErr := os.ReadFile(flagValue(args, "--append-system-prompt-file"))
			require.NoError(t, readErr)
			assert.Contains(t, string(systemPrompt), "Every exported function needs a test.")
			assert.Contains(t, cli.read(t, "stdin"), "Head commit (HEAD): head")

			cwd, _ := filepath.EvalSymlinks(cli.read(t, "cwd"))
			wantCwd, _ := filepath.EvalSymlinks(workspace.Path)
			assert.Equal(t, wantCwd, cwd, "the agent's working directory is the checkout")

			env := cli.env(t)
			assert.NotContains(t, env, "CLAUDE_CODE_OAUTH_TOKEN",
				"the model credential never enters the CLI's environment, where /proc/<pid>/environ exposes it")
			assert.Equal(t, "3", env["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"])
			assert.Equal(t, "fixture-oauth-token-placeholder", cli.read(t, "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"),
				"the CLI reads the credential from the descriptor it is told about")
			assert.Equal(t, "socket", cli.read(t, "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR.type"),
				"a socket, which the CLI reads directly and closes; a pipe it would reopen through /proc")
			assert.NotContains(t, env, "CODE_GURU_PROVIDER_TOKEN", "the provider token must never reach the agent")
			assert.NotContains(t, env, "CODE_GURU_WEBHOOK_SECRET")
			assert.Equal(t, filepath.Join(workspace.Root, "agent", "config"), env["CLAUDE_CONFIG_DIR"])
			assert.Equal(t, filepath.Join(workspace.Root, "agent", "home"), env["HOME"])
			assert.Equal(t, "1", env["CLAUDE_CODE_SUBPROCESS_ENV_SCRUB"])
			assert.Equal(t, "1", env["CLAUDE_CODE_DISABLE_CLAUDE_MDS"])
			assert.Equal(t, "false", env["ENABLE_CLAUDEAI_MCP_SERVERS"])
			assert.Equal(t, os.DevNull, env["GIT_CONFIG_GLOBAL"])
		},
	)

	t.Run("should redact the model credential from everything it returns", func(t *testing.T) {
		// given: a review that quotes the credential the agent could have read.
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "fixture-oauth-token-placeholder")
		structured := map[string]any{"summary": "found fixture-oauth-token-placeholder in env", "verdict": "comment"}
		cli := newFakeAgentCLI(t, agentEnvelope(t, structured), 0, 0)
		reviewer := claude.NewAgenticReviewerRepository(cli.binary, "m", entities.ClaudeAgenticConfig{})

		// when
		result, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(agenticWorkspace(t)))

		// then
		require.NoError(t, err)
		assert.Equal(t, "found [redacted] in env", result.Summary)
	})

	t.Run("should never pass CLAUDE_CODE_REMOTE, which makes the CLI write a credential to disk", func(t *testing.T) {
		// given: with CLAUDE_CODE_REMOTE set, the CLI persists a credential it
		// read from a descriptor to a well-known file for its subprocesses.
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
		t.Setenv("CLAUDE_CODE_REMOTE", "true")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "fixture-oauth-token-placeholder")
		cli := newFakeAgentCLI(t, agentEnvelope(t, map[string]any{"summary": "s", "verdict": "approve"}), 0, 0)
		reviewer := claude.NewAgenticReviewerRepository(cli.binary, "m", entities.ClaudeAgenticConfig{})

		// when
		_, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(agenticWorkspace(t)))

		// then
		require.NoError(t, err)
		assert.NotContains(t, cli.env(t), "CLAUDE_CODE_REMOTE")
		assert.Equal(t, "fixture-oauth-token-placeholder", cli.read(t, "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"))
	})
}

func TestAgenticReviewerSubprocessEnvScrub(t *testing.T) {
	t.Parallel()

	oauth := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "fixture-oauth-token-placeholder"}
	gateway := map[string]string{"ANTHROPIC_AUTH_TOKEN": "fixture-gateway-token-placeholder"}
	enabled, disabled := true, false
	cases := []struct {
		name        string
		setting     *bool
		installed   bool
		credentials map[string]string
		wantScrub   bool
		wantRefusal bool
	}{
		{
			name:      "should scrub where bubblewrap and socat are installed",
			installed: true, credentials: oauth, wantScrub: true,
		},
		{
			name:      "should review without the scrub where they are missing, as in the container image",
			installed: false, credentials: oauth,
		},
		{
			name:    "should ask for the scrub when it is required, even where it cannot run",
			setting: &enabled, installed: false, credentials: oauth, wantScrub: true,
		},
		{
			name:    "should never ask for the scrub when it is turned off",
			setting: &disabled, installed: true, credentials: oauth,
		},
		{
			name:      "should keep a credential the environment carries behind the scrub where it can run",
			installed: true, credentials: gateway, wantScrub: true,
		},
		{
			name:        "should refuse to start the agent when its commands could read a credential the environment carries",
			installed:   false,
			credentials: gateway,
			wantRefusal: true,
		},
		{
			name:    "should hand the environment a credential without the scrub only when told to",
			setting: &disabled, installed: false, credentials: gateway,
		},
		{
			name:      "should review without the scrub where no credential is configured at all",
			installed: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// given
			structured := map[string]any{"summary": "s", "verdict": "approve"}
			cli := newFakeAgentCLI(t, agentEnvelope(t, structured), 0, 0)
			reviewer := claude.NewAgenticReviewerRepository(
				cli.binary, "m", entities.ClaudeAgenticConfig{SubprocessEnvScrub: tc.setting},
			).WithScrubSandboxForTest(tc.installed).WithCredentialsForTest(tc.credentials)

			// when
			_, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(agenticWorkspace(t)))

			// then
			if tc.wantRefusal {
				require.ErrorIs(t, err, support.ErrAgentCredentialUnprotected)
				assert.Contains(t, err.Error(), "ANTHROPIC_AUTH_TOKEN", "the refusal names the credential")
				assert.NotContains(t, err.Error(), "fixture-gateway-token-placeholder", "and never its value")
				assert.False(t, cli.ran(), "the agent never starts with its credential exposed")
				return
			}
			require.NoError(t, err)
			env := cli.env(t)
			value, set := env["CLAUDE_CODE_SUBPROCESS_ENV_SCRUB"]
			assert.Equal(t, tc.wantScrub, set)
			if tc.wantScrub {
				assert.Equal(t, "1", value)
			}
			assert.NotContains(t, env, "CLAUDE_CODE_OAUTH_TOKEN", "the OAuth token always travels by descriptor")
			assert.Equal(t, tc.credentials["ANTHROPIC_AUTH_TOKEN"], env["ANTHROPIC_AUTH_TOKEN"],
				"a credential the CLI reads from nowhere else is still handed over through its environment")
		})
	}
}

// TestAgenticReviewerCredentialHandoff covers the descriptors the CLI is
// handed its model credential over, end to end through ReviewDiff.
func TestAgenticReviewerCredentialHandoff(t *testing.T) {
	t.Parallel()

	t.Run("should hand the API key over its own descriptor, after the OAuth token's", func(t *testing.T) {
		t.Parallel()

		// given
		cli := newFakeAgentCLI(t, agentEnvelope(t, map[string]any{"summary": "s", "verdict": "approve"}), 0, 0)
		reviewer := claude.NewAgenticReviewerRepository(cli.binary, "m", entities.ClaudeAgenticConfig{}).
			WithCredentialsForTest(map[string]string{
				"CLAUDE_CODE_OAUTH_TOKEN": "fixture-oauth-token-placeholder",
				"ANTHROPIC_API_KEY":       "fixture-api-key-placeholder",
			})

		// when
		_, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(agenticWorkspace(t)))

		// then
		require.NoError(t, err)
		env := cli.env(t)
		assert.Equal(t, "3", env["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"])
		assert.Equal(t, "4", env["CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR"])
		assert.Equal(t, "fixture-api-key-placeholder", cli.read(t, "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR"))
		assert.NotContains(t, env, "ANTHROPIC_API_KEY", "the API key never enters the CLI's environment either")
	})

	t.Run("should refuse a credential that spans more than one line, without running the CLI", func(t *testing.T) {
		t.Parallel()

		// given: the CLI reads a descriptor up to the first newline.
		cli := newFakeAgentCLI(t, agentEnvelope(t, map[string]any{"summary": "s", "verdict": "approve"}), 0, 0)
		reviewer := claude.NewAgenticReviewerRepository(cli.binary, "m", entities.ClaudeAgenticConfig{}).
			WithCredentialsForTest(map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "fixture-first\nfixture-second"})

		// when
		_, err := reviewer.ReviewDiff(context.Background(), agenticRequestFor(agenticWorkspace(t)))

		// then
		require.ErrorContains(t, err, "CLAUDE_CODE_OAUTH_TOKEN")
		assert.NotContains(t, err.Error(), "fixture-first", "an error never quotes the credential")
		assert.False(t, cli.ran())
	})
}

func writeTreeFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

func TestAgenticReviewerIdentity(t *testing.T) {
	t.Parallel()

	t.Run("should be a workspace reviewer named after the claude backend", func(t *testing.T) {
		t.Parallel()

		// given
		reviewer := claude.NewAgenticReviewerRepository("", "", entities.ClaudeAgenticConfig{})

		// when
		requiresWorkspace, name := reviewer.RequiresWorkspace(), reviewer.Name()

		// then
		assert.True(t, requiresWorkspace)
		assert.Equal(t, "claude", name)
	})
}
