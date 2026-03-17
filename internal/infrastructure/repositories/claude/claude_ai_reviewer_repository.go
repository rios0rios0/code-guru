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
	userPrompt := support.BuildUserPrompt(
		request.PullRequest.Title,
		request.PullRequest.SourceBranch,
		request.PullRequest.TargetBranch,
		request.Diffs,
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
		return nil, fmt.Errorf("claude CLI failed: %w (stderr: %s)", err, stderr.String())
	}

	return ParseClaudeResponse(stdout.Bytes())
}

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
