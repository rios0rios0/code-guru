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

	// refusalStopReason is the `stop_reason` value Anthropic returns (on an
	// HTTP 200) when its content-safety classifiers decline to answer — common
	// for security-related code. The response carries little or no content, so
	// this must be checked BEFORE reading the content blocks.
	refusalStopReason = "refusal"
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

// WithRefusalFallbackModel sets the model the backend re-issues the review
// against when the primary model declines the content on content-safety
// grounds (`stop_reason: "refusal"`). Safety-classifier coverage varies by
// model, so a fallback to a different model can produce a review where the
// primary refused. Empty (the default) disables the fallback — a refusal is
// reported as-is.
func WithRefusalFallbackModel(model string) Option {
	return func(r *AIReviewerRepository) {
		r.refusalFallbackModel = model
	}
}

// AIReviewerRepository implements the AI reviewer using the Anthropic Messages API.
type AIReviewerRepository struct {
	httpClient           *http.Client
	apiKey               string
	model                string
	endpoint             string
	context1M            bool
	refusalFallbackModel string
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

type stopDetails struct {
	Category string `json:"category"`
}

type responsePayload struct {
	Content     []contentBlock `json:"content"`
	StopReason  string         `json:"stop_reason"`
	StopDetails *stopDetails   `json:"stop_details"`
}

type errorPayload struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ReviewDiff sends the PR diffs with rules context to Anthropic and returns
// review results. On a content-safety refusal (`stop_reason: "refusal"` — the
// model's safety classifiers declining the content, common for security-
// related code) it retries ONCE with the configured fallback model, since
// safety-classifier coverage varies by model; a fallback that also refuses (or
// is not configured) surfaces the original refusal.
func (r *AIReviewerRepository) ReviewDiff(
	ctx context.Context,
	request entities.ReviewRequest,
) (*entities.ReviewResult, error) {
	result, err := r.review(ctx, request, r.model)

	var refusal *support.ContentSafetyRefusalError
	if errors.As(err, &refusal) && r.refusalFallbackModel != "" && r.refusalFallbackModel != r.model {
		logger.Warnf(
			"anthropic model %s declined the review (content-safety refusal, category=%q); retrying with fallback model %s",
			r.model,
			refusal.Category,
			r.refusalFallbackModel,
		)
		fbResult, fbErr := r.review(ctx, request, r.refusalFallbackModel)
		if fbErr == nil {
			logger.Infof(
				"anthropic fallback model %s produced a review after the primary model's content-safety refusal",
				r.refusalFallbackModel,
			)

			return fbResult, nil
		}
		// Only surface the ORIGINAL refusal when the fallback ALSO refused — a
		// genuine content-safety decline (the annotation is right, a retry is
		// futile). If the fallback failed for a DIFFERENT reason (transient
		// 5xx, network, a context-window overflow on the fallback model),
		// surface THAT error so the retry decorator and command layer classify
		// it correctly instead of mislabelling it a content-safety refusal.
		if errors.Is(fbErr, support.ErrContentSafetyRefusal) {
			logger.Warnf(
				"anthropic refusal fallback model %s also refused; surfacing the original refusal",
				r.refusalFallbackModel,
			)

			return result, err
		}
		logger.Warnf(
			"anthropic refusal fallback model %s failed for a non-refusal reason (%s); surfacing the fallback error",
			r.refusalFallbackModel, support.TruncateForLog(fbErr.Error(), errorBodyPreview),
		)

		return fbResult, fbErr
	}

	return result, err
}

// review performs a single Anthropic Messages request with the given model and
// parses the response. A non-2xx body is classified (context-window overflow
// wrapped as ErrContextWindowExceeded); a 200 with `stop_reason: "refusal"` is
// returned as a *support.ContentSafetyRefusalError BEFORE the content is read, so
// the model's refusal prose is never mistaken for a review.
func (r *AIReviewerRepository) review(
	ctx context.Context,
	request entities.ReviewRequest,
	model string,
) (*entities.ReviewResult, error) {
	systemPrompt := support.BuildSystemPromptFor(request)
	userPrompt := support.BuildUserPromptFor(request)

	logger.Debugf("sending review request to Anthropic model %s", model)

	body, err := json.Marshal(requestPayload{
		Model:     model,
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
		return nil, classifyErrorResponse(httpResp.StatusCode, respBody)
	}

	var message responsePayload
	if unmarshalErr := json.Unmarshal(respBody, &message); unmarshalErr != nil {
		return nil, fmt.Errorf("anthropic response unmarshaling failed: %w", unmarshalErr)
	}

	// Check stop_reason BEFORE the content: a content-safety refusal returns
	// HTTP 200 with `stop_reason: "refusal"` and little or no content, so
	// reading the content first would either fail as "no text content" or treat
	// the model's refusal prose as a review. `stop_details.category` names the
	// policy that fired ("cyber", "bio", ...) — a coarse label safe to surface.
	if message.StopReason == refusalStopReason {
		category := ""
		if message.StopDetails != nil {
			category = message.StopDetails.Category
		}

		return nil, &support.ContentSafetyRefusalError{Category: category}
	}

	text := concatTextBlocks(message.Content)
	if text == "" {
		return nil, errors.New("anthropic returned no text content")
	}

	return support.ParseReviewResponse(text)
}

// classifyErrorResponse turns a non-2xx Anthropic response into an error,
// preferring the API's structured error message and wrapping a "prompt too
// long" body with support.ErrContextWindowExceeded so the too-large failure
// class is recognised by the retry decorator and command layer.
func classifyErrorResponse(status int, respBody []byte) error {
	var apiErr errorPayload
	var reviewErr error
	if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
		reviewErr = fmt.Errorf("anthropic API error (%s): %s", apiErr.Error.Type, apiErr.Error.Message)
	} else {
		reviewErr = fmt.Errorf(
			"anthropic API returned status %d: %s",
			status,
			truncate(string(respBody), errorBodyPreview),
		)
	}
	if support.LooksLikeContextWindowError(reviewErr.Error()) {
		return fmt.Errorf("%w (%w)", support.ErrContextWindowExceeded, reviewErr)
	}

	return reviewErr
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
