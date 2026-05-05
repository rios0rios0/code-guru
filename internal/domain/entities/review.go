package entities

import forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"

// FileDiff represents a single file change within a pull request.
type FileDiff struct {
	Path     string
	Diff     string
	Language string
}

// ReviewComment represents a single review comment to post on a PR.
type ReviewComment struct {
	FilePath   string `json:"file"`
	Line       int    `json:"line"`
	EndLine    int    `json:"end_line,omitempty"`
	Body       string `json:"body"`
	Severity   string `json:"severity"`
	Suggestion string `json:"suggestion,omitempty"`
}

// ReviewResult aggregates all review output for a single PR.
type ReviewResult struct {
	PullRequestURL string          `json:"pull_request_url,omitempty"`
	Verdict        string          `json:"verdict,omitempty"`
	Comments       []ReviewComment `json:"comments"`
	Summary        string          `json:"summary"`

	// ThreadResolutions carries the AI's per-thread verdict on every
	// prior bot review thread that was surfaced in the conversation
	// block of the user prompt. Populated only on the mention re-review
	// path (`ReviewOptions.UserMentioned == true`); empty on first-pass
	// reviews where there are no prior threads to classify.
	//
	// Each entry tells the post-pipeline what to do with the existing
	// thread: `resolved` triggers a thread-status update + a short
	// confirmation reply, `outstanding` posts a restated reply, and
	// `outdated` is a soft "no longer applicable" close. The shape lets
	// the LLM make a structured decision instead of re-emitting (or
	// silently dropping) findings via `Comments`, which is the failure
	// mode that drove this PR — every re-review used to flood the PR
	// with reworded duplicates of the same comment.
	ThreadResolutions []ThreadResolution `json:"thread_resolutions,omitempty"`
}

// ReviewRequest encapsulates the input needed to perform a review.
type ReviewRequest struct {
	Repository  forgeEntities.Repository
	PullRequest forgeEntities.PullRequestDetail
	Diffs       []FileDiff
	Rules       []Rule

	// Conversation carries the bot's prior inline review threads on this
	// PR plus every reply on each one, so a re-review session triggered
	// by an `@code-guru` mention can address user pushback or follow-up
	// questions inline rather than re-emitting the same comment shape
	// the user just discussed. Empty for first-pass reviews; populated
	// only when `ReviewOptions.UserMentioned` is true. See
	// `support.BuildReviewConversation` for the assembly contract.
	Conversation []ReviewThread
}

// ReviewThread is one inline review conversation: the bot's original
// comment plus every reply (from the user, the bot itself, or other
// reviewers) in chronological order. Used as input to the LLM prompt
// during a re-review so the model can read the dialogue before
// deciding whether to repeat, withdraw, or respond to the original
// finding.
type ReviewThread struct {
	FilePath string
	Line     int
	// ThreadID is the provider-specific thread identifier (Azure
	// DevOps thread ID; GitHub review-comment ID stand-in). Used by
	// the post-pipeline to call `UpdatePullRequestThreadStatus` when
	// the LLM marks the thread as resolved, so the operator does not
	// have to dismiss a "fixed" thread by hand. Zero on platforms
	// that do not return a usable thread ID.
	ThreadID int64
	// RootCommentID is the provider's ID for the top-level bot
	// comment that started the thread. Surfaced so the post-pipeline
	// can dedup follow-up replies against the same root and so future
	// "edit-on-second-push" features have a stable handle on the
	// original comment.
	RootCommentID int64
	// Comments is the chronological message list — index 0 is the
	// thread root (always one of the bot's prior comments because
	// the conversation walk starts from the bot's own thread roots).
	Comments []ReviewMessage
}

// ThreadResolution is the AI's verdict on a single prior bot review
// thread on the mention re-review path. The post-pipeline turns it into
// a thread-status update + a short reply so the PR author sees the bot
// engaging with each existing thread instead of opening a parallel
// flood of new comments that say the same thing.
//
// Status values map to provider thread states:
//
//   - `resolved`   — the diff or the user's reply addressed the
//     original concern; the bot posts a brief confirmation and updates
//     the thread status to `fixed` so it stops blocking review.
//   - `outstanding` — the original concern is still valid; the bot
//     posts a short restated reply and leaves the thread `active`.
//   - `outdated`   — the original concern no longer applies (e.g. the
//     code in question was deleted, the conversation moved on); the
//     bot soft-closes the thread without making a content claim.
//
// `ID` is the synthetic per-prompt identifier (e.g. `T1`, `T2`) the
// user prompt renders next to each prior thread (`### Thread T1 on
// <file>:<line>`). It is the durable, unambiguous key for matching a
// resolution back to its thread when multiple historical bot threads
// share the same `<file>:<line>` anchor — without it the post-pipeline
// would collapse them onto one entry and silently lose every
// resolution past the first. `FilePath` + `Line` remain on the entry
// as a human-readable hint and as a fallback for LLM responses that
// drop the id (defensive); the post-pipeline only falls back when the
// anchor is unique within the prompt.
type ThreadResolution struct {
	ID          string `json:"id,omitempty"`
	FilePath    string `json:"file"`
	Line        int    `json:"line"`
	Status      string `json:"status"`
	Explanation string `json:"explanation,omitempty"`
}

// ReviewMessage is one entry in a ReviewThread's reply chain. Author
// is the platform-specific identity (login on GitHub; uniqueName /
// displayName on Azure DevOps); the LLM reads it as a free-form
// label, so providers populate whatever shape is most readable.
type ReviewMessage struct {
	Author string
	Body   string
}

// Rule represents a single review rule loaded from a markdown file.
type Rule struct {
	Name      string
	Category  string
	Content   string
	FileGlobs []string
}
