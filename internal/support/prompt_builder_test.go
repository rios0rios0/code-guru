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

	t.Run("should handle empty rules", func(t *testing.T) {
		// given
		var rules []entities.Rule

		// when
		result := support.BuildSystemPrompt(rules)

		// then
		assert.NotEmpty(t, result)
		assert.Contains(t, result, "senior code reviewer")
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
