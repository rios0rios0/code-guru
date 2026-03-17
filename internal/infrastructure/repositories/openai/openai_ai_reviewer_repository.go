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

// NewAIReviewerRepository creates a new OpenAI AI reviewer repository.
func NewAIReviewerRepository(apiKey string, model string) *AIReviewerRepository {
	if model == "" {
		model = defaultModel
	}

	return &AIReviewerRepository{
		client: oai.NewClient(apiKey),
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
	systemPrompt := support.BuildSystemPrompt(request.Rules)
	userPrompt := support.BuildUserPrompt(
		request.PullRequest.Title,
		request.PullRequest.SourceBranch,
		request.PullRequest.TargetBranch,
		request.Diffs,
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
