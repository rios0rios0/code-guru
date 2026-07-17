package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/support"
)

const (
	backendName      = "anthropic"
	defaultModel     = "claude-sonnet-4-20250514"
	maxTokens        = 4096
	apiEndpoint      = "https://api.anthropic.com/v1/messages"
	apiVersion       = "2023-06-01"
	requestTimeout   = 120 * time.Second
	contentTypeValue = "application/json"
	maxResponseBytes = 10 * 1024 * 1024
	errorBodyPreview = 512
	textBlockType    = "text"

	// context1MBeta is the Anthropic beta flag that raises the input
	// context window from the default 200K tokens to 1M for the Claude
	// Sonnet 4 family — a 5x increase that lets far larger pull requests be
	// reviewed in a single pass before they hit ErrContextWindowExceeded.
	// Sent via the Anthropic-Beta header only when the operator leaves the
	// 1M window enabled (the default; see AIConfig.Context1MEnabled). For
	// prompts under 200K tokens the flag is a no-op, so it is safe on small
	// PRs; on very large prompts (200K–1M tokens) long-context pricing may
	// apply, which is why it is operator-toggleable.
	context1MBeta   = "context-1m-2025-08-07"
	betaHeaderField = "Anthropic-Beta"
)

// Option configures an AIReviewerRepository.
type Option func(*AIReviewerRepository)

// WithEndpoint overrides the default Anthropic Messages API endpoint. Intended for tests.
func WithEndpoint(url string) Option {
	return func(r *AIReviewerRepository) {
		r.endpoint = url
	}
}

// WithContext1M toggles the 1M-token context-window beta (context1MBeta). The
// factory passes the operator-resolved value (default ON); a bare constructor
// leaves it off. Enabling it lets large PRs that would otherwise overflow the
// 200K default window be reviewed in one pass.
func WithContext1M(enabled bool) Option {
	return func(r *AIReviewerRepository) {
		r.context1M = enabled
	}
}

// AIReviewerRepository implements the AI reviewer using the Anthropic Messages API.
type AIReviewerRepository struct {
	httpClient *http.Client
	apiKey     string
	model      string
	endpoint   string
	context1M  bool
}

// NewAIReviewerRepository creates a new Anthropic AI reviewer repository.
func NewAIReviewerRepository(apiKey string, model string, opts ...Option) *AIReviewerRepository {
	if model == "" {
		model = defaultModel
	}

	repo := &AIReviewerRepository{
		httpClient: &http.Client{Timeout: requestTimeout},
		apiKey:     apiKey,
		model:      model,
		endpoint:   apiEndpoint,
	}
	for _, opt := range opts {
		opt(repo)
	}
	return repo
}

// Name returns the backend identifier.
func (r *AIReviewerRepository) Name() string {
	return backendName
}

type messagePayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type requestPayload struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system,omitempty"`
	Messages  []messagePayload `json:"messages"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsePayload struct {
	Content []contentBlock `json:"content"`
}

type errorPayload struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ReviewDiff sends the PR diffs with rules context to Anthropic and returns review results.
func (r *AIReviewerRepository) ReviewDiff(
	ctx context.Context,
	request entities.ReviewRequest,
) (*entities.ReviewResult, error) {
	systemPrompt := support.BuildSystemPromptFor(request)
	userPrompt := support.BuildUserPromptFor(request)

	logger.Debugf("sending review request to Anthropic model %s", r.model)

	body, err := json.Marshal(requestPayload{
		Model:     r.model,
		MaxTokens: maxTokens,
		System:    systemPrompt,
		Messages: []messagePayload{
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic request marshaling failed: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic request creation failed: %w", err)
	}
	httpReq.Header.Set("X-Api-Key", r.apiKey)
	httpReq.Header.Set("Anthropic-Version", apiVersion)
	httpReq.Header.Set("Content-Type", contentTypeValue)
	if r.context1M {
		httpReq.Header.Set(betaHeaderField, context1MBeta)
	}

	httpResp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("anthropic response read failed: %w", err)
	}

	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		var apiErr errorPayload
		var reviewErr error
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			reviewErr = fmt.Errorf("anthropic API error (%s): %s", apiErr.Error.Type, apiErr.Error.Message)
		} else {
			reviewErr = fmt.Errorf(
				"anthropic API returned status %d: %s",
				httpResp.StatusCode,
				truncate(string(respBody), errorBodyPreview),
			)
		}
		// A "prompt is too long" 400 is a distinct, deterministic failure
		// class: the PR is bigger than the context window, so retrying is
		// futile and the PR annotation must guide the author to shrink it
		// rather than push a new commit. Wrap with the sentinel so the retry
		// decorator and command layer classify it with a single errors.Is.
		if support.LooksLikeContextWindowError(reviewErr.Error()) {
			return nil, fmt.Errorf("%w (%w)", support.ErrContextWindowExceeded, reviewErr)
		}

		return nil, reviewErr
	}

	var message responsePayload
	if unmarshalErr := json.Unmarshal(respBody, &message); unmarshalErr != nil {
		return nil, fmt.Errorf("anthropic response unmarshaling failed: %w", unmarshalErr)
	}

	text := concatTextBlocks(message.Content)
	if text == "" {
		return nil, errors.New("anthropic returned no text content")
	}

	return support.ParseReviewResponse(text)
}

func concatTextBlocks(blocks []contentBlock) string {
	var builder strings.Builder
	for _, block := range blocks {
		if block.Type == textBlockType {
			builder.WriteString(block.Text)
		}
	}
	return builder.String()
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "... (truncated)"
}
