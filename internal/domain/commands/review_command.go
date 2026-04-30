package commands

import (
	"context"
	"fmt"

	logger "github.com/sirupsen/logrus"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	"github.com/rios0rios0/codeguru/internal/support"
)

// Review is the interface for the review command.
type Review interface {
	Execute(
		ctx context.Context,
		provider forgeEntities.ReviewProvider,
		repo forgeEntities.Repository,
		pr forgeEntities.PullRequestDetail,
		opts ReviewOptions,
	) (*entities.ReviewResult, error)
}

// ReviewOptions holds runtime options for a single review.
type ReviewOptions struct {
	DryRun   bool
	Verbose  bool
	CIPassed bool
}

// ReviewCommand orchestrates a single PR review.
type ReviewCommand struct {
	aiReviewer       repositories.AIReviewerRepository
	rulesRepo        repositories.RulesRepository
	detectorRegistry repositories.TrivialDetectorRegistry
}

// NewReviewCommand creates a new ReviewCommand.
func NewReviewCommand(
	aiReviewer repositories.AIReviewerRepository,
	rulesRepo repositories.RulesRepository,
	detectorRegistry repositories.TrivialDetectorRegistry,
) *ReviewCommand {
	return &ReviewCommand{
		aiReviewer:       aiReviewer,
		rulesRepo:        rulesRepo,
		detectorRegistry: detectorRegistry,
	}
}

// Execute performs a review of a single pull request.
func (c *ReviewCommand) Execute(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	pr forgeEntities.PullRequestDetail,
	opts ReviewOptions,
) (*entities.ReviewResult, error) {
	logger.Infof("reviewing PR #%d: %s", pr.ID, pr.Title)

	// fetch changed files
	files, err := provider.GetPullRequestFiles(ctx, repo, pr.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR files: %w", err)
	}

	if len(files) == 0 {
		logger.Infof("PR #%d has no changed files, skipping", pr.ID)
		return &entities.ReviewResult{PullRequestURL: pr.URL, Verdict: "comment", Summary: "no files changed"}, nil
	}

	// collect file paths
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}

	// check trivial PR detection (no LLM call needed)
	if c.detectorRegistry != nil && opts.CIPassed {
		if result := c.handleTrivialDetection(ctx, provider, repo, pr, paths, opts.DryRun); result != nil {
			return result, nil
		}
	}

	// build diffs and run AI review
	diffs, err := c.buildDiffs(ctx, provider, repo, pr.ID, files)
	if err != nil {
		return nil, err
	}

	// load rules for detected languages
	languages := support.ClassifyFiles(paths)
	rules, err := c.rulesRepo.LoadForLanguages(languages, paths)
	if err != nil {
		return nil, fmt.Errorf("failed to load rules: %w", err)
	}

	logger.Infof("loaded %d rules for languages: %v", len(rules), languages)

	// build review request and call AI
	request := entities.ReviewRequest{
		Repository:  repo,
		PullRequest: pr,
		Diffs:       diffs,
		Rules:       rules,
	}

	result, err := c.aiReviewer.ReviewDiff(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("AI review failed: %w", err)
	}

	result.PullRequestURL = pr.URL

	if !opts.DryRun {
		c.postComments(ctx, provider, repo, pr.ID, result)
	}

	return result, nil
}

func (c *ReviewCommand) handleTrivialDetection(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	pr forgeEntities.PullRequestDetail,
	paths []string,
	dryRun bool,
) *entities.ReviewResult {
	// build detection context with file content fetcher if available
	dctx := repositories.DetectionContext{
		Files:    paths,
		RepoName: repo.Name,
	}
	if fap, ok := provider.(forgeEntities.FileAccessProvider); ok {
		dctx.FileContentFetcher = &forgeFileContentFetcherAdapter{provider: fap, repo: repo}
	}

	detector, detection, found := c.detectorRegistry.Detect(ctx, dctx)
	if !found {
		return nil
	}

	logger.Infof(
		"PR #%d detected as trivial by %q adapter (verdict=%s), skipping LLM review",
		pr.ID, detector.Name(), detection.Verdict,
	)

	result := &entities.ReviewResult{
		PullRequestURL: pr.URL,
		Verdict:        detection.Verdict,
		Summary:        detection.Summary,
	}

	if !dryRun {
		switch detection.Verdict {
		case "approve":
			c.postApprovalComment(ctx, provider, repo, pr.ID, detection.Summary)
		case "reject":
			c.postRejectionComment(ctx, provider, repo, pr.ID, detection.Summary)
		}
	}

	return result
}

// forgeFileContentFetcherAdapter adapts a ReviewProvider (type-asserted to
// FileAccessProvider) into the domain FileContentFetcher interface.
// This lives here in the command layer because it bridges the provider
// passed to Execute with the domain interface.
type forgeFileContentFetcherAdapter struct {
	provider forgeEntities.FileAccessProvider
	repo     forgeEntities.Repository
}

func (a *forgeFileContentFetcherAdapter) GetFileContent(ctx context.Context, path string) (string, error) {
	return a.provider.GetFileContent(ctx, a.repo, path)
}

func (a *forgeFileContentFetcherAdapter) HasFile(ctx context.Context, path string) bool {
	return a.provider.HasFile(ctx, a.repo, path)
}

func (c *ReviewCommand) buildDiffs(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	files []forgeEntities.PullRequestFile,
) ([]entities.FileDiff, error) {
	var diffs []entities.FileDiff
	for _, f := range files {
		diffs = append(diffs, entities.FileDiff{
			Path:     f.Path,
			Diff:     f.Patch,
			Language: support.ClassifyFile(f.Path),
		})
	}

	// fallback: if all patches are empty (e.g. Azure DevOps), fetch the full unified diff
	if allDiffsEmpty(diffs) {
		logger.Debugf("no per-file patches available, fetching full unified diff for PR #%d", prID)
		fullDiff, err := provider.GetPullRequestDiff(ctx, repo, prID)
		if err != nil {
			return nil, fmt.Errorf("failed to get PR diff: %w", err)
		}

		// `support.LookupChunkByPath` normalises the leading slash
		// that Azure DevOps's `GetPullRequestFiles` returns on every
		// path (e.g. `/README.md`). Without the normalisation, the
		// chunk lookup would always miss for ADO PRs and the AI would
		// receive an empty diff under each file header, then correctly
		// report "no diff to review".
		chunks := support.SplitUnifiedDiff(fullDiff)
		for i := range diffs {
			if chunk, ok := support.LookupChunkByPath(chunks, diffs[i].Path); ok {
				diffs[i].Diff = chunk
			}
		}
	}

	return diffs, nil
}

func (c *ReviewCommand) postApprovalComment(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	summary string,
) {
	body := fmt.Sprintf("**[Auto-Approved]** %s", summary)
	if err := provider.PostPullRequestComment(ctx, repo, prID, body); err != nil {
		logger.Errorf("failed to post approval comment: %v", err)
	}
}

func (c *ReviewCommand) postRejectionComment(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	summary string,
) {
	body := fmt.Sprintf("**[Rejected]** %s", summary)
	if err := provider.PostPullRequestComment(ctx, repo, prID, body); err != nil {
		logger.Errorf("failed to post rejection comment: %v", err)
	}
}

func (c *ReviewCommand) postComments(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	result *entities.ReviewResult,
) {
	// Post the PR-wide summary only when there are no per-issue comments
	// (neither inline `Line > 0` threads nor PR-wide `Line <= 0`
	// annotations). The per-issue comments already carry the feedback, so
	// an extra summary thread on every push is pure noise that accumulates
	// as reviewers push fixes. The summary is still posted for clean
	// reviews (`verdict=approve` with empty `Comments`) so the operator can
	// see that the bot ran and concluded with no issues.
	if shouldPostSummary(result) {
		if err := provider.PostPullRequestComment(ctx, repo, prID, result.Summary); err != nil {
			logger.Errorf("failed to post summary comment: %v", err)
		}
	}

	for _, comment := range result.Comments {
		if comment.Line > 0 {
			err := provider.PostPullRequestThreadComment(
				ctx, repo, prID, comment.FilePath, comment.Line, comment.Body,
			)
			if err != nil {
				logger.Errorf("failed to post inline comment on %s:%d: %v", comment.FilePath, comment.Line, err)
			}
		} else {
			body := fmt.Sprintf("**[%s]** %s: %s", comment.Severity, comment.FilePath, comment.Body)
			if err := provider.PostPullRequestComment(ctx, repo, prID, body); err != nil {
				logger.Errorf("failed to post comment: %v", err)
			}
		}
	}
}

// shouldPostSummary decides whether the PR-wide summary thread should be
// emitted alongside the per-issue comments. The summary is suppressed
// whenever `result.Comments` is non-empty — that is, whenever the review
// carries any per-issue feedback, regardless of whether each item lands as
// an inline thread (`Line > 0`) or a PR-wide annotation (`Line <= 0`).
// Each push otherwise produced a fresh duplicate summary even when the
// per-issue feedback already covered the same content. A non-empty summary
// with zero comments is still posted so clean reviews (`verdict=approve`,
// "no issues found") leave a visible signal that the bot ran.
func shouldPostSummary(result *entities.ReviewResult) bool {
	return result.Summary != "" && len(result.Comments) == 0
}

func allDiffsEmpty(diffs []entities.FileDiff) bool {
	for _, d := range diffs {
		if d.Diff != "" {
			return false
		}
	}
	return true
}
