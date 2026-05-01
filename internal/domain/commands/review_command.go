package commands

import (
	"context"
	"fmt"
	"strings"

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

	comments := c.dropStaleComments(ctx, provider, repo, prID, result.Comments)

	for _, comment := range comments {
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

// dropStaleComments removes any review comment whose `FilePath` is not in
// the current set of changed files on the PR. The AI's response is built
// from a diff snapshot taken at the start of `Execute`; if the user
// pushes another commit (or rebases / squashes) between that snapshot
// and `postComments`, the AI's findings can reference files the latest
// iteration no longer touches. ADO renders such comments with a
// "this file no longer exists in the latest pull request changes"
// warning — observed live on `internal-app/internal-integrator#NNNN`
// where every bot comment carried that banner because the PR had been
// rewritten between the webhook firing and the review completing.
//
// The check is best-effort: if `GetPullRequestFiles` fails (e.g.
// transient ADO outage) we fall back to posting all comments so the
// behaviour never regresses below today's baseline. The path
// comparison normalises a leading `/` so an AI response with
// `internal/foo.go` matches an ADO-style `/internal/foo.go` (the same
// normalisation `support.LookupChunkByPath` uses on the diff side).
func (c *ReviewCommand) dropStaleComments(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	comments []entities.ReviewComment,
) []entities.ReviewComment {
	if len(comments) == 0 {
		return comments
	}
	files, err := provider.GetPullRequestFiles(ctx, repo, prID)
	if err != nil {
		logger.Warnf(
			"PR #%d: skipping staleness check, GetPullRequestFiles failed: %v — posting all %d comments",
			prID, err, len(comments),
		)
		return comments
	}
	live := make(map[string]struct{}, len(files))
	for _, f := range files {
		live[normalizeFilePath(f.Path)] = struct{}{}
	}
	kept, dropped := filterStaleComments(comments, live)
	if len(dropped) > 0 {
		logger.Warnf(
			"PR #%d: dropped %d stale comment(s) referencing files no longer in the latest iteration: %s",
			prID, len(dropped), summarizeStaleFilePaths(dropped),
		)
	}
	return kept
}

// filterStaleComments partitions `comments` into the ones whose
// `FilePath` is present in `livePaths` and the ones whose path is no
// longer there. Pure function — exposed via `export_test.go` so the
// test suite can pin the contract without standing up a stub
// `forgeEntities.ReviewProvider`.
//
// PR-wide comments (`Line <= 0`, posted via `PostPullRequestComment`)
// and comments with no `FilePath` at all are always kept: they are
// rendered as repository-wide annotations rather than inline threads,
// so they cannot trigger ADO's "file no longer exists" warning even
// if the path string happens to look stale. Only inline comments
// (`Line > 0`) are subject to the staleness drop.
func filterStaleComments(
	comments []entities.ReviewComment,
	livePaths map[string]struct{},
) ([]entities.ReviewComment, []entities.ReviewComment) {
	var kept, dropped []entities.ReviewComment
	for _, comment := range comments {
		if comment.Line <= 0 || comment.FilePath == "" {
			kept = append(kept, comment)
			continue
		}
		if _, ok := livePaths[normalizeFilePath(comment.FilePath)]; ok {
			kept = append(kept, comment)
		} else {
			dropped = append(dropped, comment)
		}
	}
	return kept, dropped
}

// normalizeFilePath strips a leading `/` so AI responses
// (`internal/foo.go`) match ADO-shape paths (`/internal/foo.go`)
// when the staleness filter compares them. Mirrors the same
// normalisation `support.LookupChunkByPath` performs on the diff
// side so both halves of the pipeline use one rule.
func normalizeFilePath(p string) string { return strings.TrimPrefix(p, "/") }

// summarizeStaleFilePaths joins up to the first eight unique dropped
// file paths so the operator log line stays bounded under runs that
// drop a large batch (e.g. a squash that rewrites every file in the
// PR). The trailing "(+N more)" sentinel preserves the count without
// echoing the full list.
//
// Deduplication is keyed on the **normalised** path so an AI response
// that mentions both `internal/foo.go` and `/internal/foo.go` is
// counted once (`normalizeFilePath` strips a single leading `/`,
// matching the rule used on the lookup side). The first form
// encountered is the one printed, so the operator log preserves
// whichever shape the AI actually emitted.
func summarizeStaleFilePaths(dropped []entities.ReviewComment) string {
	const maxShown = 8
	seen := make(map[string]struct{}, len(dropped))
	paths := make([]string, 0, len(dropped))
	for _, c := range dropped {
		key := normalizeFilePath(c.FilePath)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		paths = append(paths, c.FilePath)
	}
	if len(paths) <= maxShown {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s (+%d more)", strings.Join(paths[:maxShown], ", "), len(paths)-maxShown)
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
