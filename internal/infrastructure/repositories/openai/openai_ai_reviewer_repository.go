package openai

import (
	"context"
	"errors"
	"fmt"

	oai "github.com/sashabaranov/go-openai"
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/support"
)

const (
	backendName        = "openai"
	defaultModel       = "gpt-4o"
	defaultTemperature = 0.2
)

// AIReviewerRepository implements the AI reviewer using OpenAI's chat completions API.
type AIReviewerRepository struct {
	client *oai.Client
	model  string
}

// Option configures an AIReviewerRepository at construction time. The
// only production option today is WithEndpoint, used by the unit test
// suite to point the underlying HTTP client at an `httptest.Server`
// without touching the real OpenAI API. Future tunables (organisation
// ID, custom transport) plug in here without growing the constructor
// signature.
type Option func(*config)

// config carries the resolved option values. Kept unexported so callers
// only mutate it through Option helpers.
type config struct {
	baseURL string
}

// WithEndpoint overrides the OpenAI API base URL the underlying client
// targets. Used by the test suite to redirect every request to a local
// `httptest.Server`; production callers leave this unset so the
// upstream `api.openai.com` default applies.
func WithEndpoint(baseURL string) Option {
	return func(c *config) { c.baseURL = baseURL }
}

// NewAIReviewerRepository creates a new OpenAI AI reviewer repository.
func NewAIReviewerRepository(apiKey string, model string, opts ...Option) *AIReviewerRepository {
	if model == "" {
		model = defaultModel
	}
	cfg := config{}
	for _, opt := range opts {
		opt(&cfg)
	}

	clientCfg := oai.DefaultConfig(apiKey)
	if cfg.baseURL != "" {
		clientCfg.BaseURL = cfg.baseURL
	}
	return &AIReviewerRepository{
		client: oai.NewClientWithConfig(clientCfg),
		model:  model,
	}
}

// Name returns the backend identifier.
func (r *AIReviewerRepository) Name() string {
	return backendName
}

// ReviewDiff sends the PR diffs with rules context to OpenAI and returns review results.
func (r *AIReviewerRepository) ReviewDiff(
	ctx context.Context,
	request entities.ReviewRequest,
) (*entities.ReviewResult, error) {
	systemPrompt := support.BuildSystemPromptFor(request)
	userPrompt := support.BuildUserPromptWithConversation(
		request.PullRequest.Title,
		request.PullRequest.SourceBranch,
		request.PullRequest.TargetBranch,
		request.Diffs,
		request.Conversation,
	)

	logger.Debugf("sending review request to OpenAI model %s", r.model)

	resp, err := r.client.CreateChatCompletion(ctx, oai.ChatCompletionRequest{
		Model: r.model,
		Messages: []oai.ChatCompletionMessage{
			{Role: oai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: oai.ChatMessageRoleUser, Content: userPrompt},
		},
		Temperature: defaultTemperature,
		ResponseFormat: &oai.ChatCompletionResponseFormat{
			Type: oai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI chat completion failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, errors.New("OpenAI returned no choices")
	}

	content := resp.Choices[0].Message.Content
	return support.ParseReviewResponse(content)
}
