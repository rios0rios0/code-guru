package anthropic

import (
	"context"
	"errors"
	"fmt"

	anthropicSDK "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/support"
)

const (
	backendName  = "anthropic"
	defaultModel = "claude-sonnet-4-20250514"
	maxTokens    = 4096
)

// AIReviewerRepository implements the AI reviewer using the Anthropic Messages API.
type AIReviewerRepository struct {
	client anthropicSDK.Client
	model  string
}

// NewAIReviewerRepository creates a new Anthropic AI reviewer repository.
func NewAIReviewerRepository(apiKey string, model string) *AIReviewerRepository {
	if model == "" {
		model = defaultModel
	}

	return &AIReviewerRepository{
		client: anthropicSDK.NewClient(option.WithAPIKey(apiKey)),
		model:  model,
	}
}

// Name returns the backend identifier.
func (r *AIReviewerRepository) Name() string {
	return backendName
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

	message, err := r.client.Messages.New(ctx, anthropicSDK.MessageNewParams{
		Model:     r.model,
		MaxTokens: maxTokens,
		System: []anthropicSDK.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropicSDK.MessageParam{
			anthropicSDK.NewUserMessage(anthropicSDK.NewTextBlock(userPrompt)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic message creation failed: %w", err)
	}

	if len(message.Content) == 0 {
		return nil, errors.New("anthropic returned no content")
	}

	content := message.Content[0].Text
	return support.ParseReviewResponse(content)
}
