package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/support"
)

const (
	backendName     = "claude"
	defaultBinary   = "claude"
	defaultModel    = "sonnet"
	defaultMaxTurns = 1
)

// diffReviewEnv keeps the CLI from loading instructions or tools a review never
// asked for: no CLAUDE.md, AGENTS.md or auto-memory is read as instructions
// (the reviewed repository's own guidelines reach the prompt deliberately, as
// data), and no claude.ai connector attached to the account becomes a tool.
//
//nolint:gochecknoglobals // read-only list used as a constant
var diffReviewEnv = []string{
	"CLAUDE_CODE_DISABLE_CLAUDE_MDS=1",
	"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1",
	"ENABLE_CLAUDEAI_MCP_SERVERS=false",
}

// AIReviewerRepository implements the AI reviewer using the Claude Code CLI.
type AIReviewerRepository struct {
	binaryPath string
	model      string
	maxTurns   int
}

// NewAIReviewerRepository creates a new Claude CLI AI reviewer repository.
func NewAIReviewerRepository(binaryPath string, model string, maxTurns int) *AIReviewerRepository {
	if binaryPath == "" {
		binaryPath = defaultBinary
	}
	// A relative path that names a directory (`./bin/claude`) is resolved here,
	// against code-guru's own working directory: the CLI starts in a directory of
	// its own (isolatedWorkingDirectory), and Go would resolve it there instead,
	// so the path keeps pointing where it did. A bare name is still looked up on
	// PATH.
	if !filepath.IsAbs(binaryPath) && strings.ContainsAny(binaryPath, `/\`) {
		if absolute, err := filepath.Abs(binaryPath); err == nil {
			binaryPath = absolute
		}
	}
	if model == "" {
		model = defaultModel
	}
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}

	return &AIReviewerRepository{
		binaryPath: binaryPath,
		model:      model,
		maxTurns:   maxTurns,
	}
}

// Name returns the backend identifier.
func (r *AIReviewerRepository) Name() string {
	return backendName
}

// ReviewDiff invokes the Claude CLI with rules and diff as input.
func (r *AIReviewerRepository) ReviewDiff(
	ctx context.Context,
	request entities.ReviewRequest,
) (*entities.ReviewResult, error) {
	systemPrompt := support.BuildSystemPromptFor(request)
	userPrompt := support.BuildUserPromptFor(request)

	logger.Debugf("sending review request to Claude CLI (model: %s, max-turns: %d)", r.model, r.maxTurns)

	args := []string{
		"--print",
		"--model", r.model,
		"--output-format", "json",
		"--max-turns", strconv.Itoa(r.maxTurns),
		// Only the user's own settings load, no MCP server starts and no
		// session is written. code-guru's working directory is often a
		// checkout of the code under review, whose `.claude/settings*.json`
		// (hooks, env, apiKeyHelper), `.mcp.json` and CLAUDE.md are the pull
		// request author's: loaded, a SessionStart hook there ran as the
		// operator with the provider token in its environment. The CLI also
		// starts in a directory of its own (isolatedWorkingDirectory), so no
		// project is found at all; these flags hold even if one were.
		"--setting-sources", "user",
		"--strict-mcp-config",
		"--no-session-persistence",
		// `--tools ""` removes every built-in tool from the model's scope.
		// A review is a ONE-SHOT text completion: the model must read the
		// prompt and emit JSON, never act. Without this flag the CLI runs
		// its full agentic loop with tools in scope, and the model reaches
		// for them — reviewed repositories whose guidelines say things like
		// "audit the entry length with <shell command> if in doubt" are read
		// as instructions to execute, not as documentation to apply. Every
		// such attempt costs a TURN, and the review JSON is the loop's final
		// text output, so a model that spends its turns on tool calls exits
		// with `error_max_turns` and produces NO review at all.
		//
		// `--disallowedTools` is NOT sufficient and was measured to fail:
		// it blocks tool EXECUTION but leaves the definitions in scope, so
		// the model still emits `tool_use` blocks and still burns the turns
		// (observed `stop_reason: "tool_use"`, `num_turns: 4` against a
		// 3-turn cap, with an empty `permission_denials` list). Only
		// `--tools ""` takes the tools out of scope entirely.
		//
		// Keep this BEFORE the system-prompt flag: `--tools` is variadic, and
		// a following `--`-prefixed flag is what terminates its value list.
		"--tools", "",
	}

	// The system prompt goes through a FILE, never argv. It embeds the whole
	// operator rule corpus, and Linux caps a single argv string at
	// `MAX_ARG_STRLEN` (128 KiB) — a limit independent of `ARG_MAX`, so no
	// amount of container headroom avoids it. Measured in production: a pull
	// request touching a Go file and a Python file loads both language rule
	// files on top of the universal set, assembling a 149 KB system prompt
	// that the kernel refused outright ("argument list too long"). The whole
	// review died at exec, before the model was ever reached, and every retry
	// died identically. Writing the prompt to a temp file removes the ceiling.
	promptFile, cleanupPromptFile, err := writeSystemPromptFile(systemPrompt)
	if err != nil {
		// Fall back to the inline flag rather than failing the review: a
		// deployment with no writable temp dir kept working before this
		// change and must keep working after it, up to the OS limit. An
		// oversized prompt on this path is classified below, not retried.
		logger.Warnf("failed to stage the system prompt in a file (%v); passing it inline instead", err)
		args = append(args, "--system-prompt", systemPrompt)
	} else {
		defer cleanupPromptFile()
		args = append(args, "--system-prompt-file", promptFile)
	}

	//nolint:gosec // binary path is from trusted configuration
	cmd := exec.CommandContext(ctx, r.binaryPath, args...)
	workDir, cleanupWorkDir := isolatedWorkingDirectory()
	defer cleanupWorkDir()
	cmd.Dir = workDir
	// Environ, read while Env is still nil, is this process's environment with
	// PWD set to Dir, so the CLI is not told it runs in the caller's directory.
	// Later entries win over earlier ones of the same name.
	cmd.Env = append(cmd.Environ(), diffReviewEnv...)

	// pass user prompt via stdin to avoid OS argument length limits
	cmd.Stdin = strings.NewReader(userPrompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err = cmd.Run(); err != nil {
		// `claude --print --output-format json` writes its error envelope
		// to stdout (the JSON the CLI promises) AND any auxiliary message
		// to stderr. Discarding stdout on a non-zero exit is what made
		// every claude crash look like `(stderr: )` in production logs
		// — captured live across PRs #NNNN / #NNNN / #NNNN / #NNNN
		// / #NNNN on `2026-05-01`. Truncate each stream to keep the
		// error line bounded; pass the byte slice (`*.Bytes()`) so the
		// full buffer is not stringified before truncation — under a
		// failure mode that produces megabytes of output (large diff,
		// runaway log) the `string(...)` conversion would copy the whole
		// payload to the heap before the cap fired.
		detail := fmt.Errorf(
			"claude CLI failed: %w (stderr: %s; stdout: %s)",
			err,
			support.TruncateBytesForLog(stderr.Bytes(), claudeFailureLogLimit),
			support.TruncateBytesForLog(stdout.Bytes(), claudeFailureLogLimit),
		)
		// The OS refused the exec itself because an argument was longer than
		// its per-argument limit — the process never started, so both streams
		// are empty and no amount of stream sniffing would classify it. This
		// is only reachable via the inline fallback above; wrap it with the
		// sentinel so the retry decorator skips the (kernel-deterministic)
		// re-sample and the PR gets operator-facing guidance instead of the
		// generic "usually transient — push a new commit", which cannot help.
		if support.LooksLikeArgumentListTooLong(err) {
			return nil, fmt.Errorf("%w (%w)", support.ErrArgumentListTooLong, detail)
		}
		// The Claude CLI wraps the Anthropic "prompt is too long" 400 in its
		// JSON error envelope (printed first, a few hundred bytes). Detect it
		// on a bounded view of each stream — never the full buffer, which a
		// runaway diff can blow up to megabytes — and wrap with the sentinel
		// so the failure is classified as too-large: no futile retry, and a
		// "split the PR" annotation instead of "usually transient".
		if looksLikeContextWindowFailure(stdout.Bytes()) || looksLikeContextWindowFailure(stderr.Bytes()) {
			return nil, fmt.Errorf("%w (%w)", support.ErrContextWindowExceeded, detail)
		}

		return nil, detail
	}

	return ParseClaudeResponse(stdout.Bytes())
}

// systemPromptFilePattern names the temp file the system prompt is staged in.
// The `code-guru-` prefix makes an orphaned file (killed process, SIGKILL
// before the cleanup ran) attributable to this bot rather than anonymous
// litter in a shared `/tmp`.
const systemPromptFilePattern = "code-guru-system-prompt-*.txt"

// writeSystemPromptFile stages the system prompt in a temp file and returns
// its path plus a cleanup closure the caller MUST defer. The file is created
// by `os.CreateTemp` with mode 0600 and a randomised name, so a co-tenant
// process on a shared `/tmp` can neither read the operator's rule corpus nor
// pre-create the path to feed the reviewer a prompt of its own.
//
// The cleanup closure is idempotent-friendly: a file already gone is not an
// error worth logging, since the only way to reach that state is an external
// tmp reaper, which has done exactly what the closure wanted.
func writeSystemPromptFile(prompt string) (string, func(), error) {
	file, err := os.CreateTemp("", systemPromptFilePattern)
	if err != nil {
		return "", nil, fmt.Errorf("creating system prompt file: %w", err)
	}

	path := file.Name()
	// The CLI starts in a directory of its own, so a path relative to this
	// process's directory (a relative TMPDIR) would not be found from there.
	if absolute, absErr := filepath.Abs(path); absErr == nil {
		path = absolute
	}
	cleanup := func() {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			logger.Warnf("failed to remove the system prompt file: %v", removeErr)
		}
	}

	if _, err = file.WriteString(prompt); err != nil {
		_ = file.Close()
		cleanup()

		return "", nil, fmt.Errorf("writing system prompt file: %w", err)
	}
	// Close before the CLI reads it: an unflushed write would hand the model a
	// truncated rule set, which is far worse than a failed review because it
	// looks like a successful one.
	if err = file.Close(); err != nil {
		cleanup()

		return "", nil, fmt.Errorf("closing system prompt file: %w", err)
	}

	return path, cleanup, nil
}

// contextWindowDetectLimit bounds how many leading bytes of each captured
// stream are scanned for a context-window marker. The CLI's JSON error
// envelope is small and printed first, so 8 KB is ample without stringifying a
// multi-megabyte runaway buffer.
const contextWindowDetectLimit = 8192

// looksLikeContextWindowFailure reports whether the leading bytes of a captured
// CLI stream name a context-window / prompt-too-long failure, bounding the
// scan so an oversized buffer is never fully materialised as a string.
func looksLikeContextWindowFailure(b []byte) bool {
	if len(b) > contextWindowDetectLimit {
		b = b[:contextWindowDetectLimit]
	}

	return support.LooksLikeContextWindowError(string(b))
}

// claudeFailureLogLimit caps each captured stream when claude exits
// non-zero. 4 KB per stream is enough to fit the typical CLI JSON error
// envelope (a couple hundred bytes) plus a short stderr backtrace; both
// are quoted via `support.TruncateForLog` so newlines / tabs cannot
// inject log lines.
const claudeFailureLogLimit = 4096

// ParseClaudeResponse parses the Claude CLI JSON output into a ReviewResult.
func ParseClaudeResponse(output []byte) (*entities.ReviewResult, error) {
	// Claude CLI with --output-format json returns a JSON object with a "result" field
	var cliResponse struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(output, &cliResponse); err == nil && cliResponse.Result != "" {
		return support.ParseReviewResponse(cliResponse.Result)
	}

	// fallback: try parsing the raw output directly
	return support.ParseReviewResponse(string(output))
}

// isolatedWorkingDirectory returns the directory one CLI run starts in, and
// the function that removes it: a fresh, empty temporary directory, never
// code-guru's own working directory. When no temporary directory can be made it
// falls back to the home directory, then to the filesystem root, as a
// deployment without a writable temporary directory must still review.
func isolatedWorkingDirectory() (string, func()) {
	// A relative TMPDIR would place the directory inside the caller's, which
	// is what the CLI must be kept out of.
	temporary := os.TempDir()
	err := fmt.Errorf("the temporary directory %q is not an absolute path", temporary)
	if filepath.IsAbs(temporary) {
		var dir string
		if dir, err = os.MkdirTemp(temporary, "code-guru-claude-"); err == nil {
			return dir, func() { _ = os.RemoveAll(dir) }
		}
	}
	logger.Warnf("failed to create a directory for the Claude CLI to start in (%v); using the home directory", err)
	if home, homeErr := os.UserHomeDir(); homeErr == nil && filepath.IsAbs(home) {
		return home, func() {}
	}
	return string(filepath.Separator), func() {}
}
