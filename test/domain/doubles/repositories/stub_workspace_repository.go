package repositories

import (
	"context"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// StubWorkspaceRepository is a test double for the workspace repository: it
// returns a canned workspace (or error) and records what it was asked to
// prepare and release.
type StubWorkspaceRepository struct {
	Workspace    *entities.ReviewWorkspace
	Err          error
	PrepareCalls int
	PreparedPR   forgeEntities.PullRequestDetail
	Released     []*entities.ReviewWorkspace
}

// Prepare records the call and returns the canned workspace.
func (s *StubWorkspaceRepository) Prepare(
	_ context.Context,
	_ forgeEntities.ReviewProvider,
	_ forgeEntities.Repository,
	pr forgeEntities.PullRequestDetail,
) (*entities.ReviewWorkspace, error) {
	s.PrepareCalls++
	s.PreparedPR = pr
	return s.Workspace, s.Err
}

// Release records the released workspace.
func (s *StubWorkspaceRepository) Release(workspace *entities.ReviewWorkspace) {
	s.Released = append(s.Released, workspace)
}
