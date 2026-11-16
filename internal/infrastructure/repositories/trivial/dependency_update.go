package trivial

import (
	"context"
	"fmt"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

// detectDependencyUpdate implements the logic the update-* detectors share.
//
// A dependency update may change three kinds of file: the ecosystem's
// dependency files (isDependencyFile), the changelog, and the pending chlog
// fragments that record the change, which a chlog repository keeps in
// `.changes/unreleased/` instead of a changelog entry. It must change at
// least one dependency file, so a change made of changelog records alone is
// never one. It must not be a release either (see declinesAsRelease); a
// release deletes the fragments it compiles, so a deleted fragment is never
// part of a dependency update. Any other file means the pull request is not
// a dependency update.
func detectDependencyUpdate(
	ctx context.Context,
	dctx repositories.DetectionContext,
	isDependencyFile func(filePath string) bool,
	summary string,
) repositories.DetectionResult {
	if len(dctx.Files) == 0 {
		return repositories.DetectionResult{}
	}
	changesDependency := false
	for _, f := range dctx.Files {
		switch {
		case isChangelog(f), isPendingChlogFragment(dctx, f):
			continue
		case isDependencyFile(f):
			changesDependency = true
		default:
			return repositories.DetectionResult{}
		}
	}
	if !changesDependency || declinesAsRelease(ctx, dctx) {
		return repositories.DetectionResult{}
	}
	return repositories.DetectionResult{
		Detected: true,
		Verdict:  verdictApprove,
		Summary:  fmt.Sprintf("%s (%d files). Auto-approved by trivial PR policy.", summary, len(dctx.Files)),
	}
}
