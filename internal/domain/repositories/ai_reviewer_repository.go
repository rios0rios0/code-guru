package repositories

import (
	"context"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// AIReviewerRepository abstracts the AI engine used to review code.
type AIReviewerRepository interface {
	// Name returns the backend identifier (e.g. "openai", "claude").
	Name() string

	// ReviewDiff sends file diffs with rules context to the AI and returns review results.
	ReviewDiff(ctx context.Context, request entities.ReviewRequest) (*entities.ReviewResult, error)
}
