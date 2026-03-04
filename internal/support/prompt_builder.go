package support

import (
	"fmt"
	"strings"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

const systemPromptTemplate = `You are a senior code reviewer. Review the following code changes for issues, improvements, and adherence to the team's coding standards.

Rules to enforce:
---
%s
---

CRITICAL: Respond with ONLY a valid JSON object. Do NOT wrap it in markdown code blocks or add any text outside the JSON.

Response schema:
{
  "summary": "Brief overall assessment of the PR",
  "comments": [
    {
      "file": "path/to/file.go",
      "line": 42,
      "end_line": 45,
      "severity": "error",
      "body": "Explanation of the issue and suggested fix",
      "suggestion": "optional replacement code"
    }
  ]
}

Line number rules:
- "line" MUST be a line number in the NEW version of the file (right side of the diff)
- Use the @@ hunk headers to compute absolute line numbers (e.g., @@ -10,5 +12,7 @@ means new file starts at line 12)
- Only comment on added (+) or modified lines that appear in the diff
- "end_line" is optional; use it only for multi-line issues
- Do NOT comment on lines outside the diff

Severity levels:
- "error": bugs, security issues, or rule violations that must be fixed
- "warning": potential improvements or non-critical rule deviations
- "info": suggestions or minor observations

Guidelines:
- Only comment on actual issues or clear improvements
- Do NOT comment on style preferences not covered by the rules
- If there are no issues, return {"summary": "No issues found.", "comments": []}
- Reference the specific rule being violated when applicable
- Keep comments concise and actionable
- "suggestion" is optional; include it when proposing replacement code`

// BuildSystemPrompt assembles the system prompt from the given rules.
func BuildSystemPrompt(rules []entities.Rule) string {
	var rulesContent strings.Builder
	for _, rule := range rules {
		fmt.Fprintf(&rulesContent, "### %s\n\n", rule.Name)
		rulesContent.WriteString(rule.Content)
		rulesContent.WriteString("\n\n")
	}

	return fmt.Sprintf(systemPromptTemplate, rulesContent.String())
}

// BuildUserPrompt assembles the user prompt from PR metadata and file diffs.
func BuildUserPrompt(title string, sourceBranch string, targetBranch string, diffs []entities.FileDiff) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Pull request: %s\n", title)
	fmt.Fprintf(&prompt, "Branch: %s -> %s\n\n", sourceBranch, targetBranch)
	prompt.WriteString("Files changed:\n\n")

	for _, diff := range diffs {
		lang := diff.Language
		if lang == "" {
			lang = "text"
		}
		fmt.Fprintf(&prompt, "### File: %s (%s)\n", diff.Path, lang)
		prompt.WriteString("```diff\n")
		prompt.WriteString(diff.Diff)
		prompt.WriteString("\n```\n\n")
	}

	return prompt.String()
}
