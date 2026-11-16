package repositories

import (
	"context"
	"fmt"
)

// StubFileDiffFetcher is a test double for the FileDiffFetcher interface. It
// serves the diffs it was given and an error for any other path, and records
// every path it was asked for.
type StubFileDiffFetcher struct {
	// Diffs maps a changed file's path to its unified diff.
	Diffs map[string]string
	// Err, when set, is returned for every path instead of a diff.
	Err error
	// Requested lists the paths asked for, in order.
	Requested []string
}

// GetFileDiff returns the configured diff of path.
func (s *StubFileDiffFetcher) GetFileDiff(_ context.Context, path string) (string, error) {
	s.Requested = append(s.Requested, path)
	if s.Err != nil {
		return "", s.Err
	}
	diff, ok := s.Diffs[path]
	if !ok {
		return "", fmt.Errorf("no diff for %s", path)
	}
	return diff, nil
}
