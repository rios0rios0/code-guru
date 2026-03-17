package support

import (
	"encoding/json"
	"regexp"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// jsonCodeBlockPattern matches content inside markdown code fences.
var jsonCodeBlockPattern = regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.+?)\\n```")

const defaultVerdict = "comment"

// ParseReviewResponse parses an AI response string into a ReviewResult.
// It tries direct JSON parsing first, then extracts from markdown code fences,
// and falls back to treating the entire content as a summary.
func ParseReviewResponse(content string) (*entities.ReviewResult, error) {
	// try direct JSON parsing first
	var result entities.ReviewResult
	if err := json.Unmarshal([]byte(content), &result); err == nil {
		normalizeVerdict(&result)
		return &result, nil
	}

	// try extracting JSON from markdown code fences (```json ... ```)
	if matches := jsonCodeBlockPattern.FindStringSubmatch(content); len(matches) > 1 {
		var fencedResult entities.ReviewResult
		if err := json.Unmarshal([]byte(matches[1]), &fencedResult); err == nil {
			normalizeVerdict(&fencedResult)
			return &fencedResult, nil
		}
	}

	// final fallback: treat entire response as a summary
	logger.Warn("failed to parse AI response as JSON, treating as plain text")
	return &entities.ReviewResult{
		Verdict: defaultVerdict,
		Summary: content,
	}, nil
}

// normalizeVerdict ensures the verdict field has a valid value.
func normalizeVerdict(result *entities.ReviewResult) {
	switch result.Verdict {
	case "approve", "request_changes", "comment":
		// valid, keep as-is
	default:
		result.Verdict = defaultVerdict
	}
}
