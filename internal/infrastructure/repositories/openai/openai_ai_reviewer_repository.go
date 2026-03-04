package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	oai "github.com/sashabaranov/go-openai"
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/support"
)

// jsonCodeBlockPattern matches JSON content inside markdown code fences.
var jsonCodeBlockPattern = regexp.MustCompile("(?s)```(?:json)?\\s*\\n(\\{.*?})\\s*\\n```")

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
	return ParseReviewResponse(content)
}

// ParseReviewResponse parses an AI response string into a ReviewResult.
func ParseReviewResponse(content string) (*entities.ReviewResult, error) {
	// try direct JSON parsing first
	var result entities.ReviewResult
	if err := json.Unmarshal([]byte(content), &result); err == nil {
		return &result, nil
	}

	// try extracting JSON from markdown code fences (```json ... ```)
	if matches := jsonCodeBlockPattern.FindStringSubmatch(content); len(matches) > 1 {
		var fencedResult entities.ReviewResult
		if err := json.Unmarshal([]byte(matches[1]), &fencedResult); err == nil {
			return &fencedResult, nil
		}
	}

	// final fallback: treat entire response as a summary
	logger.Warnf("failed to parse OpenAI response as JSON, treating as plain text")
	return &entities.ReviewResult{
		Summary: content,
	}, nil
}
