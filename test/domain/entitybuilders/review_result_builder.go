//go:build integration || unit || test

package entitybuilders

import (
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	testkit "github.com/rios0rios0/testkit/pkg/test"
)

type ReviewResultBuilder struct {
	*testkit.BaseBuilder
	pullRequestURL string
	verdict        string
	comments       []entities.ReviewComment
	summary        string
}

func NewReviewResultBuilder() *ReviewResultBuilder {
	return &ReviewResultBuilder{
		BaseBuilder:    testkit.NewBaseBuilder(),
		pullRequestURL: "",
		verdict:        "comment",
		comments:       nil,
		summary:        "No issues found.",
	}
}

func (b *ReviewResultBuilder) WithPullRequestURL(pullRequestURL string) *ReviewResultBuilder {
	b.pullRequestURL = pullRequestURL
	return b
}

func (b *ReviewResultBuilder) WithVerdict(verdict string) *ReviewResultBuilder {
	b.verdict = verdict
	return b
}

func (b *ReviewResultBuilder) WithComments(comments []entities.ReviewComment) *ReviewResultBuilder {
	b.comments = comments
	return b
}

func (b *ReviewResultBuilder) WithSummary(summary string) *ReviewResultBuilder {
	b.summary = summary
	return b
}

func (b *ReviewResultBuilder) Build() interface{} {
	return b.BuildReviewResult()
}

func (b *ReviewResultBuilder) BuildReviewResult() entities.ReviewResult {
	return entities.ReviewResult{
		PullRequestURL: b.pullRequestURL,
		Verdict:        b.verdict,
		Comments:       b.comments,
		Summary:        b.summary,
	}
}

func (b *ReviewResultBuilder) Reset() testkit.Builder {
	b.BaseBuilder.Reset()
	b.pullRequestURL = ""
	b.verdict = "comment"
	b.comments = nil
	b.summary = "No issues found."
	return b
}

func (b *ReviewResultBuilder) Clone() testkit.Builder {
	clone := &ReviewResultBuilder{
		BaseBuilder:    b.BaseBuilder.Clone().(*testkit.BaseBuilder),
		pullRequestURL: b.pullRequestURL,
		verdict:        b.verdict,
		summary:        b.summary,
	}
	if b.comments != nil {
		clone.comments = make([]entities.ReviewComment, len(b.comments))
		copy(clone.comments, b.comments)
	}
	return clone
}
