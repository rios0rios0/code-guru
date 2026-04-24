package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
)

// AIReviewerRepository implements the AI reviewer using the Anthropic Messages API.
type AIReviewerRepository struct {
	httpClient *http.Client
	apiKey     string
	model      string
}

// NewAIReviewerRepository creates a new Anthropic AI reviewer repository.
func NewAIReviewerRepository(apiKey string, model string) *AIReviewerRepository {
	if model == "" {
		model = defaultModel
	}

	return &AIReviewerRepository{
		httpClient: &http.Client{Timeout: requestTimeout},
		apiKey:     apiKey,
		model:      model,
	}
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
	systemPrompt := support.BuildSystemPrompt(request.Rules)
	userPrompt := support.BuildUserPrompt(
		request.PullRequest.Title,
		request.PullRequest.SourceBranch,
		request.PullRequest.TargetBranch,
		request.Diffs,
	)

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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic request creation failed: %w", err)
	}
	httpReq.Header.Set("X-Api-Key", r.apiKey)
	httpReq.Header.Set("Anthropic-Version", apiVersion)
	httpReq.Header.Set("Content-Type", contentTypeValue)

	httpResp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic response read failed: %w", err)
	}

	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		var apiErr errorPayload
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("anthropic API error (%s): %s", apiErr.Error.Type, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("anthropic API returned status %d: %s", httpResp.StatusCode, string(respBody))
	}

	var message responsePayload
	if unmarshalErr := json.Unmarshal(respBody, &message); unmarshalErr != nil {
		return nil, fmt.Errorf("anthropic response unmarshaling failed: %w", unmarshalErr)
	}

	if len(message.Content) == 0 {
		return nil, errors.New("anthropic returned no content")
	}

	return support.ParseReviewResponse(message.Content[0].Text)
}
