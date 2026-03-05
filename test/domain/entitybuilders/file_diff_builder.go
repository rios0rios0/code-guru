//go:build integration || unit || test

package entitybuilders

import (
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	testkit "github.com/rios0rios0/testkit/pkg/test"
)

type FileDiffBuilder struct {
	*testkit.BaseBuilder
	path     string
	diff     string
	language string
}

func NewFileDiffBuilder() *FileDiffBuilder {
	return &FileDiffBuilder{
		BaseBuilder: testkit.NewBaseBuilder(),
		path:        "test.go",
		diff:        "+test line",
		language:    "golang",
	}
}

func (b *FileDiffBuilder) WithPath(path string) *FileDiffBuilder {
	b.path = path
	return b
}

func (b *FileDiffBuilder) WithDiff(diff string) *FileDiffBuilder {
	b.diff = diff
	return b
}

func (b *FileDiffBuilder) WithLanguage(language string) *FileDiffBuilder {
	b.language = language
	return b
}

func (b *FileDiffBuilder) Build() interface{} {
	return b.BuildFileDiff()
}

func (b *FileDiffBuilder) BuildFileDiff() entities.FileDiff {
	return entities.FileDiff{
		Path:     b.path,
		Diff:     b.diff,
		Language: b.language,
	}
}

func (b *FileDiffBuilder) Reset() testkit.Builder {
	b.BaseBuilder.Reset()
	b.path = "test.go"
	b.diff = "+test line"
	b.language = "golang"
	return b
}

func (b *FileDiffBuilder) Clone() testkit.Builder {
	clone := &FileDiffBuilder{
		BaseBuilder: b.BaseBuilder.Clone().(*testkit.BaseBuilder),
		path:        b.path,
		diff:        b.diff,
		language:    b.language,
	}
	return clone
}
