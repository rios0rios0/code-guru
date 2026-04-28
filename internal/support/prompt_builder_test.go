//go:build unit

package support_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/support"
	entitybuilders "github.com/rios0rios0/codeguru/test/domain/entitybuilders"
)

func TestBuildSystemPrompt(t *testing.T) {
	t.Parallel()

	t.Run("should include all rule names and content", func(t *testing.T) {
		// given
		rules := []entities.Rule{
			entitybuilders.NewRuleBuilder().WithName("security").WithContent("never expose secrets").BuildRule(),
			entitybuilders.NewRuleBuilder().WithName("golang").WithContent("use gofmt").BuildRule(),
		}

		// when
		result := support.BuildSystemPrompt(rules)

		// then
		assert.Contains(t, result, "### security")
		assert.Contains(t, result, "never expose secrets")
		assert.Contains(t, result, "### golang")
		assert.Contains(t, result, "use gofmt")
	})

	t.Run("should include JSON response instructions", func(t *testing.T) {
		// given
		rules := []entities.Rule{
			entitybuilders.NewRuleBuilder().WithName("test").WithContent("test content").BuildRule(),
		}

		// when
		result := support.BuildSystemPrompt(rules)

		// then
		assert.Contains(t, result, "CRITICAL")
		assert.Contains(t, result, "JSON")
	})

	t.Run("should fall back to a no-rules template when no rules are provided", func(t *testing.T) {
		// given
		var rules []entities.Rule

		// when
		result := support.BuildSystemPrompt(rules)

		// then: the no-rules template asks for a general best-practices review
		// and (critically) does NOT instruct the model to skip anything outside
		// the rule set — the with-rules template has that constraint and would
		// produce zero comments against an empty rules block.
		assert.NotEmpty(t, result)
		assert.Contains(t, result, "senior code reviewer")
		assert.Contains(t, result, "best practices")
		assert.NotContains(t, result, "Rules to enforce")
		assert.NotContains(t, result, "Do NOT comment on style preferences not covered by the rules")
	})

	t.Run("should keep the rules-block instruction when rules are provided", func(t *testing.T) {
		// given
		rules := []entities.Rule{
			entitybuilders.NewRuleBuilder().WithName("security").WithContent("never expose secrets").BuildRule(),
		}

		// when
		result := support.BuildSystemPrompt(rules)

		// then
		assert.Contains(t, result, "Rules to enforce")
		assert.Contains(t, result, "Do NOT comment on style preferences not covered by the rules")
	})

	t.Run("should instruct the model to set an explicit approve verdict on a clean review (both templates)", func(t *testing.T) {
		// given
		rulesProvided := []entities.Rule{
			entitybuilders.NewRuleBuilder().WithName("security").WithContent("never expose secrets").BuildRule(),
		}
		var rulesEmpty []entities.Rule

		// when
		withRules := support.BuildSystemPrompt(rulesProvided)
		noRules := support.BuildSystemPrompt(rulesEmpty)

		// then: both templates must include `"verdict": "approve"` in the
		// no-issues example, otherwise ParseReviewResponse would fall back to
		// `comment` and downstream automation can never reach a clean approve.
		assert.Contains(t, withRules, `"verdict": "approve", "summary": "No issues found.", "comments": []`)
		assert.Contains(t, noRules, `"verdict": "approve", "summary": "No issues found.", "comments": []`)
	})

	t.Run("should not include best-practices wording when rules are provided", func(t *testing.T) {
		// given
		rules := []entities.Rule{
			entitybuilders.NewRuleBuilder().WithName("security").WithContent("never expose secrets").BuildRule(),
		}

		// when
		result := support.BuildSystemPrompt(rules)

		// then: the no-rules template wording must not leak into rules-mode
		// (otherwise the model could be tempted to ignore the rules).
		assert.NotContains(t, result, "widely-accepted software engineering best practices")
	})
}

func TestBuildUserPrompt(t *testing.T) {
	t.Parallel()

	t.Run("should include PR metadata and diffs", func(t *testing.T) {
		// given
		diffs := []entities.FileDiff{
			entitybuilders.NewFileDiffBuilder().WithPath("main.go").WithDiff("+fmt.Println(\"hello\")").WithLanguage("golang").BuildFileDiff(),
			entitybuilders.NewFileDiffBuilder().WithPath("README.md").WithDiff("+# Title").WithLanguage("").BuildFileDiff(),
		}

		// when
		result := support.BuildUserPrompt("fix: changed button", "feat/branch", "main", diffs)

		// then
		assert.Contains(t, result, "fix: changed button")
		assert.Contains(t, result, "feat/branch -> main")
		assert.Contains(t, result, "main.go")
		assert.Contains(t, result, "golang")
		assert.Contains(t, result, "+fmt.Println")
		assert.Contains(t, result, "README.md")
		assert.Contains(t, result, "text") // fallback for empty language
	})

	t.Run("should wrap diffs in code fences", func(t *testing.T) {
		// given
		diffs := []entities.FileDiff{
			entitybuilders.NewFileDiffBuilder().WithPath("app.go").WithDiff("+line1\n-line2").WithLanguage("golang").BuildFileDiff(),
		}

		// when
		result := support.BuildUserPrompt("title", "src", "main", diffs)

		// then
		assert.True(t, strings.Contains(result, "```diff"))
	})
}
