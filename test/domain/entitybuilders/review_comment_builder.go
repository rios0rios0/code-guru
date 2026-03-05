//go:build integration || unit || test

package entitybuilders

import (
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	testkit "github.com/rios0rios0/testkit/pkg/test"
)

type ReviewCommentBuilder struct {
	*testkit.BaseBuilder
	filePath   string
	line       int
	endLine    int
	body       string
	severity   string
	suggestion string
}

func NewReviewCommentBuilder() *ReviewCommentBuilder {
	return &ReviewCommentBuilder{
		BaseBuilder: testkit.NewBaseBuilder(),
		filePath:    "test.go",
		line:        1,
		endLine:     0,
		body:        "test comment",
		severity:    "info",
		suggestion:  "",
	}
}

func (b *ReviewCommentBuilder) WithFilePath(filePath string) *ReviewCommentBuilder {
	b.filePath = filePath
	return b
}

func (b *ReviewCommentBuilder) WithLine(line int) *ReviewCommentBuilder {
	b.line = line
	return b
}

func (b *ReviewCommentBuilder) WithEndLine(endLine int) *ReviewCommentBuilder {
	b.endLine = endLine
	return b
}

func (b *ReviewCommentBuilder) WithBody(body string) *ReviewCommentBuilder {
	b.body = body
	return b
}

func (b *ReviewCommentBuilder) WithSeverity(severity string) *ReviewCommentBuilder {
	b.severity = severity
	return b
}

func (b *ReviewCommentBuilder) WithSuggestion(suggestion string) *ReviewCommentBuilder {
	b.suggestion = suggestion
	return b
}

func (b *ReviewCommentBuilder) Build() interface{} {
	return b.BuildReviewComment()
}

func (b *ReviewCommentBuilder) BuildReviewComment() entities.ReviewComment {
	return entities.ReviewComment{
		FilePath:   b.filePath,
		Line:       b.line,
		EndLine:    b.endLine,
		Body:       b.body,
		Severity:   b.severity,
		Suggestion: b.suggestion,
	}
}

func (b *ReviewCommentBuilder) Reset() testkit.Builder {
	b.BaseBuilder.Reset()
	b.filePath = "test.go"
	b.line = 1
	b.endLine = 0
	b.body = "test comment"
	b.severity = "info"
	b.suggestion = ""
	return b
}

func (b *ReviewCommentBuilder) Clone() testkit.Builder {
	clone := &ReviewCommentBuilder{
		BaseBuilder: b.BaseBuilder.Clone().(*testkit.BaseBuilder),
		filePath:    b.filePath,
		line:        b.line,
		endLine:     b.endLine,
		body:        b.body,
		severity:    b.severity,
		suggestion:  b.suggestion,
	}
	return clone
}
