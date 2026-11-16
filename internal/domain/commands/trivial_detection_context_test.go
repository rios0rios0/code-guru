package commands_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	doubles "github.com/rios0rios0/codeguru/test/domain/doubles/repositories"
)

func TestTrivialApproveOnlyAdaptersNeverAutoMerge(t *testing.T) {
	t.Parallel()

	// Pins the approve-only gate: an approve-only adapter's verdict is
	// posted and voted exactly like any other trivial verdict, but the merge
	// is always left to a human, even with auto-merge on, policy bypass on
	// and an allow-listed author. Release pull requests depend on it: they
	// are approved without the LLM and shipped only when someone merges them.

	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	botPR := forgeEntities.PullRequestDetail{
		ID: 4242, Title: "chore(bump): bumped version to 1.4.0", URL: "https://example/pr/4242",
		Author: "automation@example.com",
	}
	autoMergeOn := commands.ReviewOptions{
		SubmitNativeReview:      true,
		TrivialAutoMerge:        true,
		TrivialBypassPolicy:     true,
		TrivialAutoMergeAuthors: []string{"automation@example.com"},
	}

	newCmd := func(adapter string) (*commands.ReviewCommand, *recordingReviewProvider) {
		registry := &recordingRegistry{verdict: "approve", detectorName: adapter}
		rc := commands.NewReviewCommand(failingAIReviewer{}, &doubles.StubRulesRepository{}, registry, nil)
		provider := &recordingReviewProvider{
			files: []forgeEntities.PullRequestFile{{Path: "/CHANGELOG.md", Status: "modified"}},
		}
		return rc, provider
	}

	t.Run("should approve but not merge when the matching adapter is approve-only", func(t *testing.T) {
		t.Parallel()

		// given
		rc, provider := newCmd("bump-go")
		opts := autoMergeOn
		opts.TrivialApproveOnlyAdapters = []string{"bump-go", "bump-node"}

		// when
		result, err := rc.Execute(context.Background(), provider, repo, botPR, opts)

		// then
		require.NoError(t, err)
		assert.Equal(t, "approve", result.Verdict)
		assert.Empty(t, provider.merges, "an approve-only adapter's verdict must never merge the pull request")
		require.Len(t, provider.submissions, 1, "the approval is still voted")
		assert.Equal(t, forgeEntities.ReviewVerdictApprove, provider.submissions[0].Verdict)
		require.Len(t, provider.calls, 1, "the verdict is still announced once")
		assert.Contains(t, provider.calls[0].body, "**Code Guru review")
	})

	t.Run("should still auto-merge when the matching adapter is not approve-only", func(t *testing.T) {
		t.Parallel()

		// given
		rc, provider := newCmd("update-go")
		opts := autoMergeOn
		opts.TrivialApproveOnlyAdapters = []string{"bump-go", "bump-node"}

		// when
		_, err := rc.Execute(context.Background(), provider, repo, botPR, opts)

		// then
		require.NoError(t, err)
		require.Len(t, provider.merges, 1, "an adapter outside the approve-only list keeps its auto-merge")
	})
}

func TestTrivialDetectionTrustsOnlyCompleteFileLists(t *testing.T) {
	t.Parallel()

	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	pr := forgeEntities.PullRequestDetail{ID: 4242, Title: "release", URL: "https://example/pr/4242"}

	t.Run("should skip trivial detection when the provider lists a file without its path", func(t *testing.T) {
		t.Parallel()

		// given -- a deleted file named only by its old path: a provider that
		// lists one this way can drop the other deleted files from the list
		registry := &recordingRegistry{verdict: "approve"}
		rc := commands.NewReviewCommand(failingAIReviewer{}, &doubles.StubRulesRepository{}, registry, nil)
		provider := &recordingReviewProvider{
			files: []forgeEntities.PullRequestFile{
				{Path: "", OldPath: "/.changes/unreleased/1790000000000000001-aaaa.yaml", Status: "deleted"},
				{Path: "/CHANGELOG.md", Status: "modified"},
			},
		}

		// when
		early, needsReview, err := rc.Triage(
			context.Background(),
			provider,
			repo,
			pr,
			commands.ReviewOptions{DryRun: true},
		)

		// then
		require.NoError(t, err)
		assert.Nil(t, early)
		assert.True(t, needsReview, "a pull request whose file list may be incomplete goes to the AI review")
		assert.Nil(t, registry.lastFiles, "no detector may settle a pull request on a list that may be incomplete")
	})

	t.Run("should mark the files the provider lists as deleted", func(t *testing.T) {
		t.Parallel()

		// given
		registry := &recordingRegistry{verdict: "approve"}
		rc := commands.NewReviewCommand(failingAIReviewer{}, &doubles.StubRulesRepository{}, registry, nil)
		provider := &recordingReviewProvider{
			files: []forgeEntities.PullRequestFile{
				{
					Path:    "/.changes/unreleased/1790000000000000001-aaaa.yaml",
					OldPath: "/.changes/unreleased/1790000000000000001-aaaa.yaml",
					Status:  "deleted",
				},
				{Path: "/CHANGELOG.md", Status: "modified"},
			},
		}

		// when
		_, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{DryRun: true})

		// then
		require.NoError(t, err)
		assert.Equal(
			t,
			[]string{".changes/unreleased/1790000000000000001-aaaa.yaml", "CHANGELOG.md"},
			registry.lastFiles,
		)
		assert.Equal(
			t,
			map[string]bool{".changes/unreleased/1790000000000000001-aaaa.yaml": true},
			registry.lastDeleted,
		)
	})

	t.Run("should mark a file the provider reports as removed as deleted", func(t *testing.T) {
		t.Parallel()

		// given
		registry := &recordingRegistry{verdict: "approve"}
		rc := commands.NewReviewCommand(failingAIReviewer{}, &doubles.StubRulesRepository{}, registry, nil)
		provider := &recordingReviewProvider{
			files: []forgeEntities.PullRequestFile{
				{Path: "docs/old-guide.md", Status: "removed"},
				{Path: "README.md", Status: "modified"},
			},
		}

		// when
		_, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{DryRun: true})

		// then
		require.NoError(t, err)
		assert.Equal(t, map[string]bool{"docs/old-guide.md": true}, registry.lastDeleted)
	})
}

func TestTrivialDetectionServesFileDiffs(t *testing.T) {
	t.Parallel()

	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	pr := forgeEntities.PullRequestDetail{ID: 4242, Title: "release", URL: "https://example/pr/4242"}

	const changelogSection = "diff --git a/CHANGELOG.md b/CHANGELOG.md\n" +
		"--- a/CHANGELOG.md\n" +
		"+++ b/CHANGELOG.md\n" +
		"@@ -1,2 +1,3 @@\n" +
		" ## [Unreleased]\n" +
		"+## [1.4.0] - 2026-01-31\n"
	const mainSection = "diff --git a/cmd/main.go b/cmd/main.go\n" +
		"--- a/cmd/main.go\n" +
		"+++ b/cmd/main.go\n" +
		"@@ -1 +1 @@\n" +
		"-// @version 1.3.0\n" +
		"+// @version 1.4.0\n"

	run := func(
		t *testing.T,
		provider forgeEntities.ReviewProvider,
		paths ...string,
	) *diffReadingRegistry {
		t.Helper()
		registry := &diffReadingRegistry{paths: paths}
		rc := commands.NewReviewCommand(failingAIReviewer{}, &doubles.StubRulesRepository{}, registry, nil)
		_, err := rc.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{DryRun: true})
		require.NoError(t, err)
		return registry
	}

	t.Run("should serve a file's own patch without downloading the pull request diff", func(t *testing.T) {
		t.Parallel()

		// given
		provider := &countingDiffProvider{recordingReviewProvider: &recordingReviewProvider{
			files: []forgeEntities.PullRequestFile{
				{
					Path:   "cmd/main.go",
					Status: "modified",
					Patch:  "@@ -1 +1 @@\n-// @version 1.3.0\n+// @version 1.4.0\n",
				},
			},
		}}

		// when
		registry := run(t, provider, "cmd/main.go")

		// then
		assert.Equal(t, "@@ -1 +1 @@\n-// @version 1.3.0\n+// @version 1.4.0\n", registry.diffs["cmd/main.go"])
		assert.Zero(t, provider.diffCalls, "a listed patch needs no download")
	})

	t.Run("should download the unified diff once and serve each file's section", func(t *testing.T) {
		t.Parallel()

		// given -- no per-file patches, and the leading `/` some providers prefix
		provider := &countingDiffProvider{
			recordingReviewProvider: &recordingReviewProvider{
				files: []forgeEntities.PullRequestFile{
					{Path: "/CHANGELOG.md", Status: "modified"},
					{Path: "/cmd/main.go", Status: "modified"},
				},
			},
			fullDiff: changelogSection + "\n" + mainSection,
		}

		// when
		registry := run(t, provider, "CHANGELOG.md", "cmd/main.go")

		// then
		assert.Contains(t, registry.diffs["CHANGELOG.md"], "+## [1.4.0] - 2026-01-31")
		assert.Contains(t, registry.diffs["cmd/main.go"], "+// @version 1.4.0")
		assert.NotContains(t, registry.diffs["CHANGELOG.md"], "@version", "each file gets its own section only")
		assert.Equal(t, 1, provider.diffCalls, "the pull request diff is downloaded once")
	})

	t.Run("should serve the sections a partial diff carries and fail only the missing ones", func(t *testing.T) {
		t.Parallel()

		// given -- the provider built one file's section and failed on the other
		provider := &countingDiffProvider{
			recordingReviewProvider: &recordingReviewProvider{
				files: []forgeEntities.PullRequestFile{
					{Path: "/CHANGELOG.md", Status: "modified"},
					{Path: "/cmd/main.go", Status: "modified"},
				},
			},
			fullDiff: mainSection,
			diffErr:  errors.New("failed to fetch old version of /CHANGELOG.md"),
		}

		// when
		registry := run(t, provider, "cmd/main.go", "CHANGELOG.md")

		// then
		assert.Contains(t, registry.diffs["cmd/main.go"], "+// @version 1.4.0")
		require.Error(t, registry.errs["CHANGELOG.md"])
		assert.Contains(t, registry.errs["CHANGELOG.md"].Error(), "failed to fetch old version")
	})
}

// countingDiffProvider adds `GetPullRequestDiff` to the recording provider and
// counts its calls.
type countingDiffProvider struct {
	*recordingReviewProvider

	fullDiff  string
	diffErr   error
	diffCalls int
}

func (p *countingDiffProvider) GetPullRequestDiff(
	_ context.Context,
	_ forgeEntities.Repository,
	_ int,
) (string, error) {
	p.diffCalls++
	return p.fullDiff, p.diffErr
}

// diffReadingRegistry is a TrivialDetectorRegistry that reads the diff of
// each of its paths through the detection context, records what it got, and
// then settles the pull request so the AI is never reached.
type diffReadingRegistry struct {
	paths []string
	diffs map[string]string
	errs  map[string]error
}

func (r *diffReadingRegistry) Detect(
	ctx context.Context,
	dctx repositories.DetectionContext,
) (repositories.TrivialDetector, repositories.DetectionResult, bool) {
	r.diffs = make(map[string]string)
	r.errs = make(map[string]error)
	for _, path := range r.paths {
		diff, err := dctx.FileDiffFetcher.GetFileDiff(ctx, path)
		if err != nil {
			r.errs[path] = err
			continue
		}
		r.diffs[path] = diff
	}
	return &stubNamedDetector{name: "stub"}, repositories.DetectionResult{
		Detected: true,
		Verdict:  "approve",
		Summary:  "trivial",
	}, true
}
