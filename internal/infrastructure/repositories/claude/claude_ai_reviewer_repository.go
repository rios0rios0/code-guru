package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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
	systemPrompt := support.BuildSystemPrompt(request.Rules)
	userPrompt := support.BuildUserPromptWithConversation(
		request.PullRequest.Title,
		request.PullRequest.SourceBranch,
		request.PullRequest.TargetBranch,
		request.Diffs,
		request.Conversation,
	)

	logger.Debugf("sending review request to Claude CLI (model: %s, max-turns: %d)", r.model, r.maxTurns)

	//nolint:gosec // binary path is from trusted configuration
	cmd := exec.CommandContext(ctx, r.binaryPath,
		"--print",
		"--model", r.model,
		"--output-format", "json",
		"--max-turns", strconv.Itoa(r.maxTurns),
		"--system-prompt", systemPrompt,
	)

	// pass user prompt via stdin to avoid OS argument length limits
	cmd.Stdin = strings.NewReader(userPrompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
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
		return nil, fmt.Errorf(
			"claude CLI failed: %w (stderr: %s; stdout: %s)",
			err,
			support.TruncateBytesForLog(stderr.Bytes(), claudeFailureLogLimit),
			support.TruncateBytesForLog(stdout.Bytes(), claudeFailureLogLimit),
		)
	}

	return ParseClaudeResponse(stdout.Bytes())
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
