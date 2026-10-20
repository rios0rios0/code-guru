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

// WorkspaceReviewer is implemented by AI reviewers that review a local
// checkout of the pull request instead of only the diff text they are handed.
// When RequiresWorkspace reports true, the review command prepares the
// checkout through a WorkspaceRepository before calling ReviewDiff and hands
// it over as `ReviewRequest.Workspace`; it also skips the provider-side work a
// reviewer with the whole repository in front of it does not need (the
// assembled diff, the fetched guidelines file).
//
// Decorators (the retry wrapper) must forward it, or the review command would
// treat a wrapped workspace reviewer as a diff reviewer and call it without a
// checkout.
type WorkspaceReviewer interface {
	RequiresWorkspace() bool
}
