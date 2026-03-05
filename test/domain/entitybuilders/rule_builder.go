//go:build integration || unit || test

package entitybuilders

import (
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	testkit "github.com/rios0rios0/testkit/pkg/test"
)

type RuleBuilder struct {
	*testkit.BaseBuilder
	name      string
	category  string
	content   string
	fileGlobs []string
}

func NewRuleBuilder() *RuleBuilder {
	return &RuleBuilder{
		BaseBuilder: testkit.NewBaseBuilder(),
		name:        "test-rule",
		category:    "",
		content:     "test rule content",
		fileGlobs:   nil,
	}
}

func (b *RuleBuilder) WithName(name string) *RuleBuilder {
	b.name = name
	return b
}

func (b *RuleBuilder) WithCategory(category string) *RuleBuilder {
	b.category = category
	return b
}

func (b *RuleBuilder) WithContent(content string) *RuleBuilder {
	b.content = content
	return b
}

func (b *RuleBuilder) WithFileGlobs(fileGlobs []string) *RuleBuilder {
	b.fileGlobs = fileGlobs
	return b
}

func (b *RuleBuilder) Build() interface{} {
	return b.BuildRule()
}

func (b *RuleBuilder) BuildRule() entities.Rule {
	return entities.Rule{
		Name:      b.name,
		Category:  b.category,
		Content:   b.content,
		FileGlobs: b.fileGlobs,
	}
}

func (b *RuleBuilder) Reset() testkit.Builder {
	b.BaseBuilder.Reset()
	b.name = "test-rule"
	b.category = ""
	b.content = "test rule content"
	b.fileGlobs = nil
	return b
}

func (b *RuleBuilder) Clone() testkit.Builder {
	clone := &RuleBuilder{
		BaseBuilder: b.BaseBuilder.Clone().(*testkit.BaseBuilder),
		name:        b.name,
		category:    b.category,
		content:     b.content,
	}
	if b.fileGlobs != nil {
		clone.fileGlobs = make([]string, len(b.fileGlobs))
		copy(clone.fileGlobs, b.fileGlobs)
	}
	return clone
}
