package commands

import (
	"context"
	"fmt"
	"sync"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"

	"github.com/rios0rios0/codeguru/internal/support"
)

// Statuses a provider gives a file the pull request deletes: gitforge's own
// vocabulary says "deleted", and GitHub's API, which gitforge passes through
// unchanged, says "removed".
const (
	fileStatusDeleted = "deleted"
	fileStatusRemoved = "removed"
)

// hasUnnamedFile reports whether the provider listed a changed file without
// its path. A provider that names a deleted file only by its OldPath can
// also drop every deleted file after the first from the list, because it
// de-duplicates the entries on the path they all lack. Such a list cannot be
// trusted to be complete, and a trivial verdict on it could approve
// deletions nobody saw.
func hasUnnamedFile(files []forgeEntities.PullRequestFile) bool {
	for _, f := range files {
		if f.Path == "" {
			return true
		}
	}
	return false
}

// isDeletedFile reports whether the pull request deletes f.
func isDeletedFile(f forgeEntities.PullRequestFile) bool {
	return f.Status == fileStatusDeleted || f.Status == fileStatusRemoved
}

// deletedFilePaths returns the normalised paths of the files the pull
// request deletes, or nil when it deletes none.
func deletedFilePaths(files []forgeEntities.PullRequestFile) map[string]bool {
	var deleted map[string]bool
	for _, f := range files {
		if !isDeletedFile(f) {
			continue
		}
		if deleted == nil {
			deleted = make(map[string]bool)
		}
		deleted[normalizeFilePath(f.Path)] = true
	}
	return deleted
}

// pullRequestDiffFetcher serves one changed file's diff to the trivial
// detectors. It returns the per-file patch when the provider listed one
// (GitHub does), and otherwise that file's section of the pull request's
// unified diff, which it downloads once, and only when a detector first asks
// for a diff: most pull requests are settled on their file names alone.
//
// It is the domain FileDiffFetcher's adapter over the provider passed to
// Execute, like forgeFileContentFetcherAdapter. Detection runs on one
// goroutine; the [sync.Once] only makes the single download explicit.
type pullRequestDiffFetcher struct {
	provider forgeEntities.ReviewProvider
	repo     forgeEntities.Repository
	prID     int
	patches  map[string]string

	once   sync.Once
	chunks map[string]string
	err    error
}

func newPullRequestDiffFetcher(
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	files []forgeEntities.PullRequestFile,
) *pullRequestDiffFetcher {
	patches := make(map[string]string, len(files))
	for _, f := range files {
		if f.Patch != "" {
			patches[normalizeFilePath(f.Path)] = f.Patch
		}
	}
	return &pullRequestDiffFetcher{provider: provider, repo: repo, prID: prID, patches: patches}
}

// GetFileDiff returns path's diff. A provider may build the unified diff
// file by file and return the files it could read together with an error
// for those it could not; every file's section stands on its own, so a
// section that is present is served even then. Only a file with no section
// gets an error.
func (f *pullRequestDiffFetcher) GetFileDiff(ctx context.Context, path string) (string, error) {
	key := normalizeFilePath(path)
	if patch, ok := f.patches[key]; ok {
		return patch, nil
	}
	f.once.Do(func() {
		fullDiff, err := f.provider.GetPullRequestDiff(ctx, f.repo, f.prID)
		f.chunks = support.SplitUnifiedDiff(fullDiff)
		f.err = err
	})
	if chunk, ok := support.LookupChunkByPath(f.chunks, key); ok {
		return chunk, nil
	}
	if f.err != nil {
		return "", fmt.Errorf("failed to get the diff of %s: %w", key, f.err)
	}
	return "", fmt.Errorf("the pull request diff has no section for %s", key)
}
