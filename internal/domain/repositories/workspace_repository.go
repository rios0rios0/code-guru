package repositories

import (
	"context"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// WorkspaceRepository checks a pull request out into a disposable local
// workspace, for reviewers that read the repository rather than only the diff
// (see WorkspaceReviewer).
//
// The provider is passed per call for the same reason as on
// PullRequestMetadataRepository: the webhook server builds a fresh
// authenticated provider per delivery, so the clone credentials are resolved
// per call from provider.AuthToken() and never cached.
type WorkspaceRepository interface {
	// Prepare clones the repository, checks out the pull request's source
	// head and resolves the merge-base with its target branch. Unlike the
	// best-effort metadata fetches, a failure here is fatal to the review:
	// a workspace reviewer has nothing to read without it.
	Prepare(
		ctx context.Context,
		provider forgeEntities.ReviewProvider,
		repo forgeEntities.Repository,
		pr forgeEntities.PullRequestDetail,
	) (*entities.ReviewWorkspace, error)

	// Release removes everything Prepare created. Safe to call with nil and
	// more than once.
	Release(workspace *entities.ReviewWorkspace)
}
