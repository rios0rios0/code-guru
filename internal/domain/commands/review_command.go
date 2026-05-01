package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

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

	// Post a "reviewing" marker so the PR author sees the bot has the
	// work and is on it — without this, on a complex diff the bot can
	// take 5-10 minutes (or fail silently mid-review, see PR #98) and
	// the author would have no signal to wait. The marker is
	// intentionally placed AFTER the trivial-detection gate so trivial
	// PRs (auto-approve / auto-reject — already-instant signal) don't
	// get the noise of an extra "reviewing" thread on top of the
	// verdict. Skipped under DryRun so `terra plan`-style invocations
	// stay silent.
	if !opts.DryRun {
		c.postReviewingMarker(ctx, provider, repo, pr.ID)
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
		// Post a user-visible failure annotation so the PR author
		// understands the silence after the "reviewing" marker is a
		// crash, not the bot still working. Without this notice, the
		// marker promises a review that never arrives — captured live
		// across PRs `#12009` / `#12050` / `#12076` / `#12095` /
		// `#12100` / `#12102` on `2026-05-01` where ~half of all
		// reviews failed silently with `claude CLI failed: exit
		// status 1`. With dedup (PR #100) only one pod attempts the
		// review per PR, so a single failure means total silence
		// unless we annotate. Best-effort: a failure to post the
		// annotation logs at `Warn` and the original error still
		// bubbles up to the worker.
		if !opts.DryRun {
			c.postReviewFailedAnnotation(ctx, provider, repo, pr.ID, err)
		}
		return nil, fmt.Errorf("AI review failed: %w", err)
	}

	result.PullRequestURL = pr.URL

	if !opts.DryRun {
		c.postComments(ctx, provider, repo, pr.ID, result)
		// After the inline / summary comments land, post a tiny
		// completion notice so the PR author sees a visible
		// transition from "🤖 reviewing" (PR #102) to "✅ done".
		// Without this, the marker stays open-ended and the author
		// has to count comments to infer the bot finished. The
		// notice is intentionally a separate thread (not an edit of
		// the marker) because gitforge does not yet expose a thread-
		// update method — see task `#47`. Once that lands, this can
		// be replaced with marker-thread auto-close.
		c.postReviewCompleteAnnotation(ctx, provider, repo, pr.ID, result)
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

// postReviewFailedAnnotation drops a single PR-wide notice when the
// AI step crashes, so the PR author understands the silence after
// the "reviewing" marker is a failure rather than the bot still
// working. Without this notice (and combined with the dedup gate
// from PR #100 — only one pod attempts the review per PR), a
// `claude CLI failed: exit status 1` left the PR with the marker
// promising a review that never arrives.
//
// The `reviewErr` is included in summary form so an operator
// glancing at the PR thread immediately knows whether to look at
// the pod logs (claude crash) vs. retry (transient outage). The
// raw error is truncated and quoted via `support.TruncateForLog`
// to bound the rendered body and prevent any provider-side parser
// breaking on a multi-megabyte error from a runaway claude.
//
// Best-effort: a failure to post the annotation logs at `Warn` and
// the original `reviewErr` still bubbles up to the worker via
// `Execute`'s return — the annotation is operator-visible UX, not
// a correctness gate.
func (c *ReviewCommand) postReviewFailedAnnotation(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	reviewErr error,
) {
	body := buildReviewFailedBody(time.Now().UTC(), reviewErr)
	logger.Infof("PR #%d: posting 'review failed' annotation", prID)

	timeoutCtx, cancel := context.WithTimeout(ctx, reviewingMarkerPostTimeout)
	defer cancel()
	if err := provider.PostPullRequestComment(timeoutCtx, repo, prID, body); err != nil {
		logger.Warnf("PR #%d: failed to post 'review failed' annotation: %v", prID, err)
	}
}

// buildReviewFailedBody renders the PR-wide failure notice. Pure
// function — exposed via `export_test.go` so the formatting
// contract is unit-testable without standing up a stub provider.
// Forces UTC inside the helper for the same reason
// `buildReviewingMarkerBody` does (per Copilot review on PR #102).
// Truncates the error to keep the rendered body bounded under a
// runaway-claude failure mode that can produce megabytes of
// unstructured text.
func buildReviewFailedBody(now time.Time, reviewErr error) string {
	errText := "(no error details)"
	if reviewErr != nil {
		errText = support.TruncateForLog(reviewErr.Error(), reviewFailedBodyErrorLimit)
	}
	// Footer formatting note: the timestamp goes inside `_..._`
	// (italic), but the error text goes inside a fenced code block
	// instead of italic — error strings can contain `_`, `*`,
	// backticks, or full file paths that would break Markdown
	// emphasis if interpolated into a single italic span. Pinned
	// per Copilot review on PR #103 thread `PRRT_kwDOJKAEo85-6CvE`.
	// `support.TruncateForLog` already wraps the value in
	// `strconv.Quote`, so a stray triple-backtick inside `errText`
	// would arrive escaped (`\`\`\``) and not close the fence.
	return fmt.Sprintf(
		"\xe2\x9a\xa0\xef\xb8\x8f **Code Guru review failed.**\n\n"+
			"The AI review step crashed before any inline comments could be produced. Please review "+
			"this PR manually — the bot will retry on the next push, but the silence after the "+
			"\"reviewing\" marker is a failure, not progress.\n\n"+
			"_Failed at %s._\n\n"+
			"```\n%s\n```",
		now.UTC().Format(time.RFC3339),
		errText,
	)
}

// reviewFailedBodyErrorLimit caps the raw error text echoed into
// the PR thread. 2 KB is enough to surface the typical
// `claude CLI failed: exit status 1 (stderr: ...; stdout: ...)`
// envelope that PR #98 introduced — long enough to be useful for
// the author/operator, short enough that the PR thread does not
// turn into a wall of text under a runaway-claude crash.
const reviewFailedBodyErrorLimit = 2048

// postReviewCompleteAnnotation drops a single PR-wide notice after
// the AI review's inline + summary comments have been posted, so
// the PR author sees an explicit "done" signal that closes out the
// "reviewing" marker (PR #102). Without this, the marker stays
// open-ended and the author has to count comments to infer that
// the bot finished — observed live on `Zest-Terraform/customer-
// clusters#12102` on `2026-05-01` where the author merged before
// realising the bot had finished and missed the 3 review comments.
//
// The body surfaces the verdict, the count of inline comments, and
// the started_at timestamp from the marker so the operator can
// link the marker thread (`Started at <ts>`) to the completion
// thread (`Completed at <ts>`) at a glance. Best-effort: a failure
// to post logs at `Warn` and never blocks the worker — completion
// notices are UX, not correctness. Wrapped in the same `5s` timeout
// as the marker so a hung provider cannot stall the review pipeline.
func (c *ReviewCommand) postReviewCompleteAnnotation(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	result *entities.ReviewResult,
) {
	body := buildReviewCompleteBody(time.Now().UTC(), result)
	logger.Infof("PR #%d: posting 'review complete' annotation (verdict=%s, comments=%d)",
		prID, result.Verdict, len(result.Comments))

	timeoutCtx, cancel := context.WithTimeout(ctx, reviewingMarkerPostTimeout)
	defer cancel()
	if err := provider.PostPullRequestComment(timeoutCtx, repo, prID, body); err != nil {
		logger.Warnf("PR #%d: failed to post 'review complete' annotation: %v", prID, err)
	}
}

// buildReviewCompleteBody renders the PR-wide completion notice. Pure
// function — exposed via `export_test.go` so the formatting contract
// is unit-testable without standing up a stub provider. Forces UTC
// inside the helper for the same reason `buildReviewingMarkerBody`
// does (per Copilot review on PR #102 thread `PRRT_kwDOJKAEo85-56Sq`).
// Surfaces the canonical verdict + inline-comment count so the
// author can see the bot's conclusion without scrolling through
// every thread.
func buildReviewCompleteBody(now time.Time, result *entities.ReviewResult) string {
	verdict := "comment"
	commentCount := 0
	if result != nil {
		if result.Verdict != "" {
			verdict = result.Verdict
		}
		commentCount = len(result.Comments)
	}
	commentsLabel := "comment"
	if commentCount != 1 {
		commentsLabel = "comments"
	}
	return fmt.Sprintf(
		"\xe2\x9c\x85 **Code Guru review complete.**\n\n"+
			"Verdict: `%s` \xc2\xb7 %d inline %s.\n\n"+
			"_Completed at %s._",
		verdict,
		commentCount,
		commentsLabel,
		now.UTC().Format(time.RFC3339),
	)
}

// postReviewingMarker drops a single PR-wide acknowledgement so the
// author knows the bot has picked up the PR and is doing the work.
// The body is intentionally short — the rich feedback lands as
// inline threads + a summary when the AI completes. The marker
// closes the gap between webhook-receive and review-complete (which
// can be 5-10 minutes on complex diffs), removing the failure mode
// observed on `Zest-Terraform/customer-clusters#12102` where the
// author merged at the 7-minute mark and missed the 3 well-grounded
// comments that landed 4 minutes later.
//
// Emits an `Info` log line BEFORE the network call so an operator
// can correlate the timestamp shown in the PR thread (`Started at
// <ts>`) with the same `started_at=<ts>` field on the log line, even
// if the provider call hangs or times out and never returns.
//
// Best-effort: the provider call is wrapped in a short
// `context.WithTimeout` so a slow/hung provider cannot stall the
// worker (the marker is UX, not a correctness gate); a failure
// logs at `Warn` and the review continues.
func (c *ReviewCommand) postReviewingMarker(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
) {
	startedAt := time.Now().UTC()
	body := buildReviewingMarkerBody(startedAt)
	logger.Infof(
		"PR #%d: posting 'reviewing' marker (started_at=%s)",
		prID, startedAt.Format(time.RFC3339),
	)

	timeoutCtx, cancel := context.WithTimeout(ctx, reviewingMarkerPostTimeout)
	defer cancel()
	if err := provider.PostPullRequestComment(timeoutCtx, repo, prID, body); err != nil {
		logger.Warnf("PR #%d: failed to post 'reviewing' marker: %v", prID, err)
	}
}

// reviewingMarkerPostTimeout caps how long a single marker post can
// hold the worker. The marker is UX, not a correctness gate, so a
// hung provider must not stall the AI review pipeline behind it.
// `5s` covers the p99 latency we see for ADO `POST .../threads`
// from inside the AKS cluster while staying short enough that a
// genuine outage manifests as a fast `Warn` instead of a wedged
// goroutine.
const reviewingMarkerPostTimeout = 5 * time.Second

// buildReviewingMarkerBody renders the PR-wide marker body. Pure
// function — exposed via `export_test.go` so the formatting contract
// is unit-testable without standing up a stub provider. Forces UTC
// inside the helper (rather than trusting the caller) so the
// "RFC 3339 in UTC" contract holds regardless of the input
// `time.Time`'s `Location` — pinned per Copilot review on PR #102
// thread `PRRT_kwDOJKAEo85-56Sq`.
func buildReviewingMarkerBody(now time.Time) string {
	return fmt.Sprintf(
		"\xf0\x9f\xa4\x96 **Code Guru is reviewing this PR.**\n\n"+
			"Please wait for the review to complete before merging — typically 1-3 minutes for "+
			"small PRs, longer for complex diffs. Comments will be posted as inline threads when "+
			"the review finishes.\n\n_Started at %s._",
		now.UTC().Format(time.RFC3339),
	)
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
// warning — observed live on `Zest-App/integrator-tenablevm#12095`
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
