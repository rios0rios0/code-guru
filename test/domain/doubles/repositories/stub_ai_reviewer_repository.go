package repositories

import (
	"context"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// StubAIReviewerRepository is a test double that returns canned responses.
type StubAIReviewerRepository struct {
	NameValue   string
	Result      *entities.ReviewResult
	Err         error
	LastRequest entities.ReviewRequest
}

// Name returns the configured backend name.
func (r *StubAIReviewerRepository) Name() string {
	return r.NameValue
}

// ReviewDiff stores the request and returns the canned result.
func (r *StubAIReviewerRepository) ReviewDiff(
	_ context.Context,
	request entities.ReviewRequest,
) (*entities.ReviewResult, error) {
	r.LastRequest = request
	return r.Result, r.Err
}
