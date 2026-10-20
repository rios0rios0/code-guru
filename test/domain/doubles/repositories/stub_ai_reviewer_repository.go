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
	// RequiresWorkspaceValue makes the stub behave as a workspace reviewer
	// (repositories.WorkspaceReviewer); false keeps it a diff reviewer.
	RequiresWorkspaceValue bool
}

// RequiresWorkspace reports the configured RequiresWorkspaceValue.
func (r *StubAIReviewerRepository) RequiresWorkspace() bool {
	return r.RequiresWorkspaceValue
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
