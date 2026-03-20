package trivial

import (
	"context"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
)

// ForgeFileContentFetcher adapts a gitforge FileAccessProvider into the
// domain FileContentFetcher interface.
type ForgeFileContentFetcher struct {
	provider forgeEntities.FileAccessProvider
	repo     forgeEntities.Repository
}

// NewForgeFileContentFetcher creates a new adapter.
func NewForgeFileContentFetcher(
	provider forgeEntities.FileAccessProvider,
	repo forgeEntities.Repository,
) *ForgeFileContentFetcher {
	return &ForgeFileContentFetcher{provider: provider, repo: repo}
}

// GetFileContent reads a file from the repository's default branch.
func (f *ForgeFileContentFetcher) GetFileContent(ctx context.Context, path string) (string, error) {
	return f.provider.GetFileContent(ctx, f.repo, path)
}

// HasFile checks whether a file exists in the repository.
func (f *ForgeFileContentFetcher) HasFile(ctx context.Context, path string) bool {
	return f.provider.HasFile(ctx, f.repo, path)
}
