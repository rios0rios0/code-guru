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

// Verdict strings emitted on the review path. Mirrors the unexported
// constants in `internal/support/verdict_mapper.go`; defined locally
// because those are private to that package.
const (
	verdictApprove = "approve"
	verdictComment = "comment"
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

	// SubmitNativeReview, when true, also records a native PR review on the
	// underlying provider (Approved / Changes Requested) so the verdict
	// surfaces in the platform's reviewer panel rather than only in the
	// completion annotation. Best-effort: a failure to submit logs at warn
	// and the existing text annotation still posts. Wired from
	// settings.AI.NativeReviewSubmissionEnabled() at each call site, which
	// resolves the tri-state YAML / env config (default true).
	SubmitNativeReview bool

	// ReviewDrafts, when false, causes Execute to short-circuit on draft
	// PRs (PullRequestDetail.IsDraft) before fetching files or calling the
	// AI. When true, drafts go through the full review path. Wired from
	// settings.AI.ReviewDrafts at each call site.
	ReviewDrafts bool

	// UserMentioned signals that this review run was triggered by a
	// user comment that contained `@code-guru` (handled by the comment-
	// event webhook). When true, the review-once gate is bypassed even
	// if the bot has already posted a "review complete" annotation —
	// the user has explicitly asked for another pass. Push-triggered
	// reviews (the default) leave this false so the gate applies.
	UserMentioned bool

	// TrivialAutoMerge, when true, calls `provider.MergePullRequest`
	// after a trivial-approve verdict. Off by default — the gate is
	// "operator explicitly opted-in" because auto-merge is a
	// destructive cross-system action that bypasses human review.
	// Wired from `Settings.Trivial.AutoMerge` at each call site.
	// Failures degrade gracefully: a merge error logs at warn and the
	// verdict still stands.
	TrivialAutoMerge bool

	// TrivialMergeStrategy is the gitforge merge-strategy string
	// (`"merge"` / `"squash"` / `"rebase"`) used when
	// `TrivialAutoMerge` is true. Empty falls back to the platform's
	// default merge strategy. Wired from
	// `Settings.Trivial.MergeStrategy`.
	TrivialMergeStrategy string

	// TrivialBypassPolicy, when true, asks the provider to skip
	// branch policies (`Required reviewers`, `Minimum approver count`,
	// etc.) on the auto-merge call. Off by default — bypass strictly
	// requires the bot's identity to hold the platform-level
	// `Bypass policies when completing pull requests` permission, so
	// turning this on without that permission turns previously-working
	// auto-merges into hard 403s. Wired from
	// `Settings.Trivial.BypassPolicy`.
	TrivialBypassPolicy bool
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

	// Drafts are skipped by default — most teams treat draft PRs as
	// work-in-progress and do not want to spend AI budget on something
	// the author has explicitly marked unfinished. Operators that want
	// to opt back in flip `settings.AI.ReviewDrafts` (env override
	// `CODE_GURU_AI_REVIEW_DRAFTS=true`). The skip happens BEFORE the
	// "reviewing" marker so a draft PR doesn't accumulate a marker
	// thread that will never get a completion annotation. The verdict
	// is intentionally `comment` (not `approve` / `reject`) so any
	// downstream native-review submission also short-circuits.
	if skip := c.shouldSkip(ctx, provider, repo, pr, opts); skip != nil {
		return skip, nil
	}

	// fetch changed files
	files, err := provider.GetPullRequestFiles(ctx, repo, pr.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR files: %w", err)
	}

	if len(files) == 0 {
		logger.Infof("PR #%d has no changed files, skipping", pr.ID)
		return &entities.ReviewResult{PullRequestURL: pr.URL, Verdict: verdictComment, Summary: "no files changed"}, nil
	}

	// collect file paths. Normalised via `normalizeFilePath` to strip
	// the leading `/` Azure DevOps prefixes onto every path — without
	// it the bump detectors compare `/CHANGELOG.md` against their
	// `CHANGELOG.md` required-files set and incorrectly flag an
	// otherwise-valid bump PR as missing the changelog.
	var paths []string
	for _, f := range files {
		paths = append(paths, normalizeFilePath(f.Path))
	}

	// Trivial PR detection runs unconditionally — each detector
	// self-validates what counts as "trivial enough" (bump detectors
	// require a matching `.autobump.yaml`, docs-only requires every
	// file be Markdown). No CI gate: every entry point hardcodes
	// CIPassed=false, so any gate on it would silently disable the
	// entire trivial path.
	if c.detectorRegistry != nil {
		if result := c.handleTrivialDetection(ctx, provider, repo, pr, paths, opts); result != nil {
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

	// build review request and call AI. On the mention re-review path
	// we additionally walk the bot's prior inline review threads (and
	// every reply on each one) so the LLM can read the dialogue before
	// deciding whether to repeat / withdraw / respond to its earlier
	// findings — see `support.BuildReviewConversation` for the shape.
	// First-pass reviews leave Conversation nil so the prompt is
	// byte-for-byte the same as before this change landed.
	request := entities.ReviewRequest{
		Repository:   repo,
		PullRequest:  pr,
		Diffs:        diffs,
		Rules:        rules,
		Conversation: c.buildConversation(ctx, provider, repo, pr.ID, diffs, opts),
	}

	result, err := c.aiReviewer.ReviewDiff(ctx, request)
	if err != nil {
		// Post a user-visible failure annotation so the PR author
		// understands the silence after the "reviewing" marker is a
		// crash, not the bot still working. Without this notice, the
		// marker promises a review that never arrives — captured live
		// across PRs `#NNNN` / `#NNNN` / `#NNNN` / `#NNNN` /
		// `#NNNN` / `#NNNN` on `2026-05-01` where ~half of all
		// reviews failed silently with `claude CLI failed: exit
		// status 1`. With dedup (PR #100) only one pod attempts the
		// review per PR, so a single failure means total silence
		// unless we annotate. Best-effort: a failure to post the
		// annotation logs at `Warn` and the original error still
		// bubbles up to the worker. Skipped when the PR has been
		// closed mid-flight — same gate as the success path below.
		if !opts.DryRun && !isPullRequestClosed(ctx, provider, repo, pr.ID) {
			c.postReviewFailedAnnotation(ctx, provider, repo, pr.ID, err)
		}
		return nil, fmt.Errorf("AI review failed: %w", err)
	}

	result.PullRequestURL = pr.URL

	if !opts.DryRun {
		// One PR-status check gates BOTH `postComments` AND the
		// completion annotation — without the unified gate the bot
		// would still post the "review complete" notice on a merged
		// PR, undermining the skip (Copilot review on PR #105
		// thread `PRRT_kwDOJKAEo85-6f_Y`). Doing the check once at
		// the call site (instead of inside `postComments`) also
		// halves the API calls on the success path.
		if isPullRequestClosed(ctx, provider, repo, pr.ID) {
			logger.Warnf(
				"PR #%d: closed mid-flight, skipping review post (verdict=%s, comments=%d would have been posted)",
				pr.ID,
				result.Verdict,
				len(result.Comments),
			)
		} else {
			c.postComments(ctx, provider, repo, pr.ID, result)
			// After the inline / summary comments land, post a tiny
			// completion notice so the PR author sees a visible
			// transition from "reviewing" (PR #102) to "done".
			// Without this, the marker stays open-ended and the
			// author has to count comments to infer the bot
			// finished. The notice is intentionally a separate
			// thread (not an edit of the marker) because gitforge
			// does not yet expose a thread-update method — see
			// task `#47`. Once that lands, this can be replaced
			// with marker-thread auto-close.
			c.postReviewCompleteAnnotation(ctx, provider, repo, pr.ID, result)
			// Native review submission is additive UX layered on top
			// of the existing text annotation: the verdict shows up
			// in the platform's reviewer panel (Approved / Changes
			// Requested) instead of only inside the comment body.
			// Gated by `opts.SubmitNativeReview`.
			c.submitNativeReview(ctx, provider, repo, pr.ID, result.Verdict, result.Summary, opts)
		}
	}

	return result, nil
}

func (c *ReviewCommand) handleTrivialDetection(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	pr forgeEntities.PullRequestDetail,
	paths []string,
	opts ReviewOptions,
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

	if !opts.DryRun {
		// Single PR-wide comment that carries the `**Code Guru review`
		// substring so the F2 review-once gate (`alreadyReviewed`)
		// catches the next webhook delivery for the same PR. ADO
		// fires both `pullrequest.created` AND `pullrequest.updated`
		// for every new PR; without the marker the second delivery
		// (arriving seconds after the first finishes and releases the
		// dedup lease) finds no marker and re-runs the trivial path —
		// observed live on an internal smoke PR where two pods
		// posted four duplicate approvals between them.
		c.postReviewCompleteAnnotation(ctx, provider, repo, pr.ID, result)
		// Reviewer-panel vote mirrors the text annotation. Body is
		// intentionally empty so this submission does not duplicate
		// the annotation as a separate PR-wide comment on ADO; the
		// vote alone is enough to surface the verdict in the panel.
		c.submitNativeReview(ctx, provider, repo, pr.ID, detection.Verdict, "", opts)
		// Auto-merge gated by `Settings.Trivial.AutoMerge` (env
		// `CODE_GURU_TRIVIAL_AUTO_MERGE=true`). Best-effort: a merge
		// failure logs at warn and the verdict still stands; the PR
		// author can complete the merge manually from the platform UI.
		if detection.Verdict == verdictApprove && opts.TrivialAutoMerge {
			c.autoMergeTrivial(ctx, provider, repo, pr.ID, opts.TrivialMergeStrategy, opts.TrivialBypassPolicy)
		}
	}

	return result
}

// autoMergeTrivial completes the PR via the underlying provider after
// a trivial-approve verdict. `strategy` maps to gitforge's
// `MergePullRequest` strategy string (`"merge"` / `"squash"` /
// `"rebase"` / `"rebaseMerge"`); empty falls back to the platform
// default.
//
// `bypassPolicy` is opt-in via `Settings.Trivial.BypassPolicy`
// (env `CODE_GURU_TRIVIAL_BYPASS_POLICIES`). When true, the bot
// passes `gitforge.WithBypassPolicy(...)` so ADO skips branch
// policies (`Required reviewers`, `Minimum approver count`) that
// would otherwise reject the merge with
// `GitPullRequestUpdateRejectedByPolicyException`. The bot's
// identity must hold the platform-level
// `Bypass policies when completing pull requests` permission for
// the bypass to take effect; without it ADO still returns 403.
// Bypass is kept as a separate flag (rather than baked into
// AutoMerge) so deployments where the bot has merge permission but
// NOT bypass permission still benefit from polite-merge auto-
// completion, instead of every auto-merge attempt becoming a
// hard 403. On GitHub the option is a no-op — bypass there is
// governed by the authenticated user's permission model rather than
// a per-call flag.
func (c *ReviewCommand) autoMergeTrivial(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	strategy string,
	bypassPolicy bool,
) {
	logger.Infof("PR #%d: auto-merging (strategy=%q, bypass=%v) per trivial PR policy", prID, strategy, bypassPolicy)

	var mergeOpts []forgeEntities.MergeOption
	if bypassPolicy {
		mergeOpts = append(mergeOpts, forgeEntities.WithBypassPolicy("auto-merged by code-guru trivial PR policy"))
	}
	if err := provider.MergePullRequest(ctx, repo, prID, strategy, mergeOpts...); err != nil {
		logger.Warnf("PR #%d: auto-merge failed: %v -- the trivial-approve verdict still stands", prID, err)
	}
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
	if err := provider.PostPullRequestComment(
		timeoutCtx,
		repo,
		prID,
		body,
		forgeEntities.WithThreadStatus(annotationThreadStatus),
	); err != nil {
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
// the bot finished — observed live on `internal-terraform/customer-
// clusters#NNNN` on `2026-05-01` where the author merged before
// realising the bot had finished and missed the 3 review comments.
//
// The body surfaces the verdict, the count of inline (`Line > 0`)
// comments, and the completion timestamp in the same RFC 3339 UTC
// shape the marker uses (`Started at <ts>`) so a reader can pair
// the marker thread with the completion thread at a glance.
// Best-effort: a failure to post logs at `Warn` and never blocks
// the worker — completion notices are UX, not correctness. Wrapped
// in the same `5s` timeout as the marker so a hung provider cannot
// stall the review pipeline.
//
// Both the log line and the body use `reviewCompletionStats` so a
// nil `result` (defensive — production never passes one, but a
// future refactor might) degrades gracefully instead of panicking
// on `result.Verdict` / `len(result.Comments)` — pinned per
// Copilot review on PR #104 thread `PRRT_kwDOJKAEo85-6Eqy`.
func (c *ReviewCommand) postReviewCompleteAnnotation(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	result *entities.ReviewResult,
) {
	stats := reviewCompletionStats(result)
	body := buildReviewCompleteBody(time.Now().UTC(), result)
	logger.Infof("PR #%d: posting 'review complete' annotation (verdict=%s, inline_comments=%d)",
		prID, stats.verdict, stats.inlineCommentCount)

	timeoutCtx, cancel := context.WithTimeout(ctx, reviewingMarkerPostTimeout)
	defer cancel()
	if err := provider.PostPullRequestComment(
		timeoutCtx,
		repo,
		prID,
		body,
		forgeEntities.WithThreadStatus(annotationThreadStatus),
	); err != nil {
		logger.Warnf("PR #%d: failed to post 'review complete' annotation: %v", prID, err)
	}
}

// completionStats collapses the bits of `*entities.ReviewResult`
// the completion notice cares about (verdict + inline-comment
// count) into a value that is safe to consume even when the
// caller's `result` pointer is nil. Defined as a type so the
// `verdict` / `inlineCommentCount` pair stays self-documenting
// at every call site.
type completionStats struct {
	verdict            string
	inlineCommentCount int
}

// reviewCompletionStats reads the verdict + inline-comment count
// off a `*entities.ReviewResult` defensively. A nil pointer or an
// empty `Verdict` falls back to the canonical `comment` verdict.
// The inline-comment count only counts comments with `Line > 0` —
// PR-wide comments (`Line <= 0`) are repository-wide annotations
// and don't render as "inline threads" in the bot's vocabulary, so
// they shouldn't inflate the "X inline comments" label on the
// completion notice — pinned per Copilot review on PR #104 thread
// `PRRT_kwDOJKAEo85-6ErC`.
func reviewCompletionStats(result *entities.ReviewResult) completionStats {
	stats := completionStats{verdict: verdictComment}
	if result == nil {
		return stats
	}
	if result.Verdict != "" {
		stats.verdict = result.Verdict
	}
	for _, c := range result.Comments {
		if c.Line > 0 {
			stats.inlineCommentCount++
		}
	}
	return stats
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
	stats := reviewCompletionStats(result)
	commentsLabel := "comment"
	if stats.inlineCommentCount != 1 {
		commentsLabel = "comments"
	}
	// Surface the verdict's human-readable rationale when present.
	// Mainly for the trivial fast path — every detector emits a
	// `Summary` like `"Documentation-only change detected (...)"` or
	// `"bump-go version bump is incomplete per .autobump.yaml: …"`,
	// and dropping it would leave the PR author with only the
	// verdict label. The LLM path typically leaves Summary empty
	// (the rationale lands as inline comments), so the section is
	// rendered only when there is something to say — preserving the
	// original two-paragraph layout the LLM path has shipped with.
	reasonBlock := ""
	if result != nil && strings.TrimSpace(result.Summary) != "" {
		reasonBlock = result.Summary + "\n\n"
	}
	return fmt.Sprintf(
		"\xe2\x9c\x85 **Code Guru review complete.**\n\n"+
			"Verdict: `%s` \xc2\xb7 %d inline %s.\n\n"+
			"%s"+
			"_Completed at %s._",
		stats.verdict,
		stats.inlineCommentCount,
		commentsLabel,
		reasonBlock,
		now.UTC().Format(time.RFC3339),
	)
}

// postReviewingMarker drops a single PR-wide acknowledgement so the
// author knows the bot has picked up the PR and is doing the work.
// The body is intentionally short — the rich feedback lands as
// inline threads + a summary when the AI completes. The marker
// closes the gap between webhook-receive and review-complete (which
// can be 5-10 minutes on complex diffs), removing the failure mode
// observed on `internal-terraform/internal-customer-app#NNNN` where the
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
	if err := provider.PostPullRequestComment(
		timeoutCtx,
		repo,
		prID,
		body,
		forgeEntities.WithThreadStatus(annotationThreadStatus),
	); err != nil {
		logger.Warnf("PR #%d: failed to post 'reviewing' marker: %v", prID, err)
	}
}

// submitNativeReview records a native PR review (Approved / Changes Requested)
// on the underlying provider so the verdict surfaces in the platform's reviewer
// panel rather than only as text. The text annotation is still posted by the
// caller — this method is purely additive UX, gated by `opts.SubmitNativeReview`
// so existing deployments keep their previous behaviour until operators opt in.
//
// The verdict-to-submission translation lives in `support.MapVerdictToReview`;
// a verdict the mapper does not recognise (e.g. `comment`) returns ok=false and
// this function quietly skips the API call. Failures are logged at `Warn` and
// swallowed: the native review is operator-visible polish, not a correctness
// gate, so a hung provider or a permission failure must not stall the worker
// or cause the surrounding `Execute` to return an error.
func (c *ReviewCommand) submitNativeReview(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	verdict, summary string,
	opts ReviewOptions,
) {
	if !opts.SubmitNativeReview {
		return
	}
	sub, ok := support.MapVerdictToReview(verdict, summary)
	if !ok {
		return
	}
	// Mirror the marker / annotation helpers' timeout: the native review
	// is best-effort UX, so a slow or hung provider must not stall the
	// worker on this path either. `reviewingMarkerPostTimeout` (5s)
	// matches the same SLO used for the surrounding annotation posts so
	// a single review pipeline shares one cap on provider latency.
	timeoutCtx, cancel := context.WithTimeout(ctx, reviewingMarkerPostTimeout)
	defer cancel()
	if err := provider.SubmitPullRequestReview(timeoutCtx, repo, prID, sub); err != nil {
		logger.Warnf(
			"PR #%d: native review submission failed (verdict=%s): %v — text annotation still posted",
			prID, verdict, err,
		)
	}
}

// annotationThreadStatus is the initial thread status used for every
// PR-wide informational annotation the bot posts (the "reviewing"
// marker, the "review complete" notice, and the "review failed"
// notice). Azure DevOps treats `closed` as "the discussion is done"
// — exactly the right shape because none of these threads ask the
// reviewer for action; they exist purely to give the PR author a
// status signal in line with the rest of the comment activity.
// Without this, every successful review left two unresolved
// "informational" threads on the PR (the marker + the completion
// notice) that the author had to dismiss by hand. GitHub's REST
// review surface has no per-comment status and silently ignores the
// option (`forgeEntities.WithThreadStatus` is documented as no-op there),
// so this is effectively ADO-only behaviour with provider-agnostic
// wiring.
const annotationThreadStatus = "closed"

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
	// Note: the closed-PR re-check that decides whether to post at
	// all lives in `Execute` so a single `GetPullRequestStatus` call
	// gates BOTH `postComments` and the sibling
	// `postReviewCompleteAnnotation`. Originally added per task
	// `#43` and refined per Copilot review on PR #105 thread
	// `PRRT_kwDOJKAEo85-6f_Y`. The webhook payload's `status` is
	// captured at delivery time, but the AI step can take 5–10
	// minutes on complex diffs (PR `#NNNN` on `2026-05-01` took
	// 8.5 minutes), and the PR may have been merged or abandoned in
	// the meantime — the call site re-check catches that.

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
	comments = c.dropDuplicateComments(ctx, provider, repo, prID, comments)

	for _, comment := range comments {
		if comment.Line > 0 {
			// `_` discards the new thread ID gitforge returns (post
			// `0.9.7`). The inline comment threads are not currently
			// updated after creation — the marker thread (PR #102) is
			// the only candidate for auto-close, and it goes through
			// `PostPullRequestComment`, not this path. Capturing the
			// ID here is reserved for a future "edit on second push"
			// feature.
			_, err := provider.PostPullRequestThreadComment(
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

// shouldSkip evaluates the early-return gates that fire BEFORE the
// command fetches files or calls the AI. Returns a non-nil result
// (with the documented `verdict=comment` short-circuit shape) when the
// review must be skipped; returns nil when the normal flow proceeds.
// Pulled out of `Execute` so the method's cognitive complexity stays
// inside the linter's budget — each gate's reasoning lives next to its
// own check instead of being interleaved with the AI pipeline below.
//
// Gate order:
//
//  1. Draft PRs (unless `ai.review_drafts=true`) — drafts are
//     work-in-progress; reviewing them flooded operators with churn.
//  2. Review-once (unless the run was triggered by an `@code-guru`
//     mention) — if the bot already posted a "review complete" /
//     "review failed" marker, follow-up pushes to the same PR are
//     no-ops by design; users that want a re-review say so explicitly
//     via the mention path.
func (c *ReviewCommand) shouldSkip(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	pr forgeEntities.PullRequestDetail,
	opts ReviewOptions,
) *entities.ReviewResult {
	if pr.IsDraft && !opts.ReviewDrafts {
		logger.Infof("PR #%d is a draft, skipping review (set ai.review_drafts=true to override)", pr.ID)
		return &entities.ReviewResult{
			PullRequestURL: pr.URL,
			Verdict:        verdictComment,
			Summary:        "skipped: pull request is a draft",
		}
	}
	if !opts.UserMentioned && c.alreadyReviewed(ctx, provider, repo, pr.ID) {
		logger.Infof("PR #%d already reviewed; skipping (mention @code-guru in a comment to request re-review)", pr.ID)
		return &entities.ReviewResult{
			PullRequestURL: pr.URL,
			Verdict:        verdictComment,
			Summary:        "skipped: pull request has already been reviewed; mention @code-guru in a comment to request re-review",
		}
	}
	return nil
}

// buildConversation walks the PR's existing inline comments and groups
// the bot's prior review threads + every reply into the
// `[]entities.ReviewThread` shape the LLM prompt renders before the
// diff. The walk only fires on the `@code-guru` mention path
// (`opts.UserMentioned == true`); first-pass reviews return nil so the
// prompt is byte-for-byte identical to its pre-conversation shape and
// the LLM's output distribution does not drift on the path where there
// is nothing for the model to read.
//
// Best-effort: a `ListPullRequestComments` failure logs at warn and
// returns nil so the re-review still runs (the LLM just loses the
// dialogue context — it will likely re-emit the same finding, which
// the F3 dedup will then drop). The tighter degraded mode beats the
// alternative of failing the whole review when the comments-list call
// has a transient blip.
func (c *ReviewCommand) buildConversation(
	ctx context.Context,
	lister pullRequestCommentLister,
	repo forgeEntities.Repository,
	prID int,
	diffs []entities.FileDiff,
	opts ReviewOptions,
) []entities.ReviewThread {
	if !opts.UserMentioned {
		return nil
	}
	comments, err := lister.ListPullRequestComments(ctx, repo, prID)
	if err != nil {
		logger.Warnf(
			"PR #%d: ListPullRequestComments failed during re-review conversation walk (%v); proceeding without prior conversation context",
			prID,
			err,
		)
		return nil
	}
	// Live-file set lets the assembler drop conversation threads
	// anchored to files the current diff no longer touches — the LLM
	// would otherwise try to "respond" inline on stale anchors that
	// the post-pipeline's dropStaleComments would then drop, wasting
	// tokens.
	liveFiles := make(map[string]struct{}, len(diffs))
	for _, d := range diffs {
		liveFiles[normalizeFilePath(d.Path)] = struct{}{}
	}
	threads := support.BuildReviewConversation(comments, support.IsBotAuthor(), liveFiles)
	if len(threads) > 0 {
		logger.Infof(
			"PR #%d: re-review will include %d prior bot thread(s) as LLM conversation context",
			prID,
			len(threads),
		)
	}
	return threads
}

// alreadyReviewed reports whether the bot has already posted a "review
// complete" / "review failed" annotation on the PR. The check fetches
// every PR-wide comment via gitforge's `ListPullRequestComments` and
// scans the bodies for `support.HasCompletedReviewMarker` — the marker
// substring lives on the bodies the bot itself writes, so the check is
// self-contained and needs no per-PR state outside of what the PR
// already carries.
//
// Best-effort: a fetch failure logs at warn and returns false so the
// gate degrades to "process the webhook" — never worse than today's
// no-gate baseline. The narrow `pullRequestCommentLister` interface
// keeps the unit test surface small (a 1-method stub instead of a full
// `forgeEntities.ReviewProvider`).
func (c *ReviewCommand) alreadyReviewed(
	ctx context.Context,
	lister pullRequestCommentLister,
	repo forgeEntities.Repository,
	prID int,
) bool {
	comments, err := lister.ListPullRequestComments(ctx, repo, prID)
	if err != nil {
		logger.Warnf(
			"PR #%d: ListPullRequestComments failed (%v); proceeding with review under the assumption nothing has been posted yet",
			prID,
			err,
		)
		return false
	}
	bodies := make([]string, 0, len(comments))
	for _, comment := range comments {
		bodies = append(bodies, comment.Body)
	}
	return support.HasCompletedReviewMarker(bodies)
}

// pullRequestCommentLister is the narrow subset of
// `forgeEntities.ReviewProvider` that `alreadyReviewed` consumes.
// Defined here so the test can pass a 1-method stub rather than build
// a full `ReviewProvider` for every row.
type pullRequestCommentLister interface {
	ListPullRequestComments(
		ctx context.Context, repo forgeEntities.Repository, prID int,
	) ([]forgeEntities.PullRequestComment, error)
}

// pullRequestStatusGetter is the narrow subset of
// `forgeEntities.ReviewProvider` that `isPullRequestClosed` consumes.
// Defined here so the test can pass a 1-method stub rather than
// build a full `ReviewProvider` for every row.
type pullRequestStatusGetter interface {
	GetPullRequestStatus(ctx context.Context, repo forgeEntities.Repository, prID int) (string, error)
}

// isPullRequestClosed re-checks the PR's status via the provider and
// returns true when the PR is no longer eligible for review comments.
// Best-effort: a fetch failure logs `Warn` and the function returns
// false so the caller proceeds with posting (the bot is never worse
// than today's baseline). The closed-status set covers both Azure
// DevOps' enum (`completed`, `abandoned`) and GitHub's mapped values
// from `gitforge.GetPullRequestStatus` (`closed`, `merged`). The
// comparison is case-/whitespace-tolerant for those known closed
// values; any other status — including `active`, `open`, an empty
// string, or a future enum addition the bot has not been taught
// about — is treated as not closed so posting proceeds under the
// same best-effort contract. Pinned per task `#43` and Copilot
// review on PR #105 thread `PRRT_kwDOJKAEo85-6f_d`.
func isPullRequestClosed(
	ctx context.Context,
	getter pullRequestStatusGetter,
	repo forgeEntities.Repository,
	prID int,
) bool {
	status, err := getter.GetPullRequestStatus(ctx, repo, prID)
	if err != nil {
		logger.Warnf(
			"PR #%d: GetPullRequestStatus failed (%v); proceeding with post under the assumption the PR is still active",
			prID,
			err,
		)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "abandoned", "completed", "closed", "merged":
		return true
	default:
		return false
	}
}

// dropDuplicateComments removes any inline comment whose (file, line,
// body-fingerprint) triple is already present on the PR. The fingerprint
// is the first `commentDedupBodyPrefix` bytes of the body — long enough
// that two genuinely different findings on the same line are kept, short
// enough that minor wording drift between runs (e.g. the AI rewords the
// remediation paragraph but the lede stays the same) is treated as a
// duplicate. Without this filter every push triggers a new review and
// every prior inline comment gets a duplicate twin — the operationally
// observed flooding pattern that drove this filter's introduction.
//
// PR-wide comments (`Line <= 0`, posted via `PostPullRequestComment`)
// are NOT deduped here: they are typically the summary or per-issue
// PR-wide annotations, and the F2 review-once gate above already
// suppresses entire follow-up reviews so the only path that lands a
// duplicate PR-wide comment is the explicit `@code-guru` re-review,
// which the user asked for.
//
// Best-effort: a fetch failure logs at warn and falls back to posting
// every comment so the behaviour never regresses below today's
// no-dedup baseline. The narrow `pullRequestCommentLister` interface
// keeps the unit test surface small.
func (c *ReviewCommand) dropDuplicateComments(
	ctx context.Context,
	lister pullRequestCommentLister,
	repo forgeEntities.Repository,
	prID int,
	comments []entities.ReviewComment,
) []entities.ReviewComment {
	if len(comments) == 0 {
		return comments
	}
	existing, err := lister.ListPullRequestComments(ctx, repo, prID)
	if err != nil {
		logger.Warnf(
			"PR #%d: skipping comment dedup, ListPullRequestComments failed: %v — posting all %d comments",
			prID, err, len(comments),
		)
		return comments
	}
	seen := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		if e.Line <= 0 || e.FilePath == "" {
			continue
		}
		seen[commentDedupKey(e.FilePath, e.Line, e.Body)] = struct{}{}
	}
	kept := make([]entities.ReviewComment, 0, len(comments))
	dropped := 0
	for _, comment := range comments {
		if comment.Line <= 0 || comment.FilePath == "" {
			kept = append(kept, comment)
			continue
		}
		if _, dup := seen[commentDedupKey(comment.FilePath, comment.Line, comment.Body)]; dup {
			dropped++
			continue
		}
		kept = append(kept, comment)
	}
	if dropped > 0 {
		logger.Infof(
			"PR #%d: dropped %d duplicate inline comment(s) already posted on the PR",
			prID, dropped,
		)
	}
	return kept
}

// commentDedupBodyPrefix bounds how many leading bytes of the body
// participate in the dedup fingerprint. 200 is enough to capture the
// distinguishing lede of a typical review comment ("[high] this could
// be nil-checked when …") while ignoring the remediation paragraph,
// which the AI tends to reword between runs even when the underlying
// finding is the same. A future tuning pass could swap this for a
// normalised hash if false-positive rates climb in production.
const commentDedupBodyPrefix = 200

// commentDedupKey builds the fingerprint used by `dropDuplicateComments`
// to decide whether a proposed comment matches an existing one.
// Exposed via `export_test.go` so the test suite can pin the
// fingerprint shape without driving the full filter.
func commentDedupKey(filePath string, line int, body string) string {
	normalisedPath := normalizeFilePath(filePath)
	prefix := body
	if len(prefix) > commentDedupBodyPrefix {
		prefix = prefix[:commentDedupBodyPrefix]
	}
	return fmt.Sprintf("%s:%d:%s", normalisedPath, line, prefix)
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
