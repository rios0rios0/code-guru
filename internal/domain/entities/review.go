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
	// Comments is the chronological message list — index 0 is the
	// thread root (always one of the bot's prior comments because
	// the conversation walk starts from the bot's own thread roots).
	Comments []ReviewMessage
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
