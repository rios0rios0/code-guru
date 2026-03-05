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
	Comments       []ReviewComment `json:"comments"`
	Summary        string          `json:"summary"`
}

// ReviewRequest encapsulates the input needed to perform a review.
type ReviewRequest struct {
	Repository  forgeEntities.Repository
	PullRequest forgeEntities.PullRequestDetail
	Diffs       []FileDiff
	Rules       []Rule
}

// Rule represents a single review rule loaded from a markdown file.
type Rule struct {
	Name      string
	Category  string
	Content   string
	FileGlobs []string
}
