package support_test

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/support"
)

func TestApplyConfidenceThreshold(t *testing.T) {
	t.Parallel()

	t.Run("should move scored findings below the threshold to dropped, with the reason", func(t *testing.T) {
		t.Parallel()

		// given
		result := &entities.ReviewResult{Comments: []entities.ReviewComment{
			{FilePath: "a.go", Line: 1, Dimension: "bug", Confidence: 90, Body: "kept"},
			{FilePath: "b.go", Line: 2, Dimension: "design", Confidence: 60, Body: "weak"},
			{FilePath: "c.go", Line: 3, Dimension: "security", Confidence: 0, Body: "unscored by mistake"},
		}}

		// when
		support.ApplyConfidenceThreshold(result, 80)

		// then
		assert.Len(t, result.Comments, 1)
		assert.Equal(t, "kept", result.Comments[0].Body)
		assert.Len(t, result.Dropped, 2, "a zero score on a dimensioned finding is still below the bar")
		assert.Equal(t, entities.DroppedFinding{
			FilePath: "b.go", Line: 2, Dimension: "design", Confidence: 60,
			Reason: "below the confidence threshold of 80",
		}, result.Dropped[0])
	})

	t.Run("should leave diff-path comments, which carry no dimension, untouched", func(t *testing.T) {
		t.Parallel()

		// given
		result := &entities.ReviewResult{Comments: []entities.ReviewComment{{FilePath: "a.go", Line: 1, Body: "x"}}}

		// when
		support.ApplyConfidenceThreshold(result, 80)

		// then
		assert.Len(t, result.Comments, 1)
		assert.Empty(t, result.Dropped)
	})
}

func TestRedactSecrets(t *testing.T) {
	t.Parallel()

	t.Run("should redact a secret verbatim and in its base64 and hex encodings everywhere", func(t *testing.T) {
		t.Parallel()

		// given
		secret := "fixture-token-placeholder-0123456789"
		encoded := base64.StdEncoding.EncodeToString([]byte(secret))
		result := &entities.ReviewResult{
			Summary:  "leaked " + secret,
			Coverage: entities.ReviewCoverage{Security: "b64 " + encoded},
			Comments: []entities.ReviewComment{{Body: "hex " + hex.EncodeToString([]byte(secret)), Suggestion: secret}},
			Dropped:  []entities.DroppedFinding{{Reason: secret}},
			ThreadResolutions: []entities.ThreadResolution{
				{Explanation: base64.RawURLEncoding.EncodeToString([]byte(secret))},
			},
		}

		// when
		support.RedactSecrets(result, secret)

		// then
		assert.Equal(t, "leaked [redacted]", result.Summary)
		assert.Equal(t, "b64 [redacted]", result.Coverage.Security)
		assert.Equal(t, "hex [redacted]", result.Comments[0].Body)
		assert.Equal(t, "[redacted]", result.Comments[0].Suggestion)
		assert.Equal(t, "[redacted]", result.Dropped[0].Reason)
		assert.Equal(t, "[redacted]", result.ThreadResolutions[0].Explanation)
	})

	t.Run("should redact anything shaped like an Anthropic credential even when not handed", func(t *testing.T) {
		t.Parallel()

		// given: the shape assembled at run time so no scanner reads it as one.
		shaped := "sk-" + "ant-" + "oat01-" + "abcdefghijklmnopqrstuvwxyz"
		result := &entities.ReviewResult{Summary: "token " + shaped + " end"}

		// when
		support.RedactSecrets(result)

		// then
		assert.Equal(t, "token [redacted] end", result.Summary)
	})

	t.Run("should ignore short values that would mangle ordinary words", func(t *testing.T) {
		t.Parallel()

		// given
		result := &entities.ReviewResult{Summary: "the main branch is true"}

		// when
		support.RedactSecrets(result, "main", "true", "")

		// then
		assert.Equal(t, "the main branch is true", result.Summary)
	})
}

// setEveryString writes value into every string field reachable from v,
// through structs, pointers and slices, and returns how many it wrote.
func setEveryString(v reflect.Value, value string) int {
	//nolint:exhaustive // only the kinds that can hold or lead to a string matter here
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return 0
		}
		return setEveryString(v.Elem(), value)
	case reflect.Struct:
		count := 0
		for _, field := range v.Fields() {
			if field.CanSet() {
				count += setEveryString(field, value)
			}
		}
		return count
	case reflect.Slice:
		count := 0
		for i := range v.Len() {
			count += setEveryString(v.Index(i), value)
		}
		return count
	case reflect.String:
		v.SetString(value)
		return 1
	default:
		return 0
	}
}

// stringsContaining lists the string fields reachable from v that contain
// needle, by their Go path.
func stringsContaining(v reflect.Value, path, needle string) []string {
	//nolint:exhaustive // only the kinds that can hold or lead to a string matter here
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return stringsContaining(v.Elem(), path, needle)
	case reflect.Struct:
		var found []string
		for i := range v.NumField() {
			found = append(found, stringsContaining(v.Field(i), path+"."+v.Type().Field(i).Name, needle)...)
		}
		return found
	case reflect.Slice:
		var found []string
		for i := range v.Len() {
			found = append(found, stringsContaining(v.Index(i), fmt.Sprintf("%s[%d]", path, i), needle)...)
		}
		return found
	case reflect.String:
		if strings.Contains(v.String(), needle) {
			return []string{path}
		}
		return nil
	default:
		return nil
	}
}

func TestRedactSecretsCoversEveryField(t *testing.T) {
	t.Parallel()

	t.Run("should redact every string field a review result carries, including ones added later", func(t *testing.T) {
		t.Parallel()

		// given: a secret in every string field of every element type.
		secret := "fixture-provider-token-placeholder"
		result := &entities.ReviewResult{
			Comments:          []entities.ReviewComment{{}},
			Dropped:           []entities.DroppedFinding{{}},
			ThreadResolutions: []entities.ThreadResolution{{}},
		}
		written := setEveryString(reflect.ValueOf(result), "leak "+secret)

		// when
		support.RedactSecrets(result, secret)

		// then
		require.Positive(t, written)
		assert.Empty(t, stringsContaining(reflect.ValueOf(result), "result", secret),
			"every field the bot can post must go through the redactor")
	})
}

func TestStampReviewJob(t *testing.T) {
	t.Parallel()

	t.Run("should write the Job's name into the notice's closing line", func(t *testing.T) {
		t.Parallel()

		// given
		body := "⚠️ **Code Guru review failed.**\n\n_Failed at 2026-10-19T12:30:00Z._"

		// when
		stamped := support.StampReviewJob(body, "code-guru-review-abc")

		// then
		assert.Equal(t,
			"⚠️ **Code Guru review failed.**\n\n_Failed at 2026-10-19T12:30:00Z · review job `code-guru-review-abc`._",
			stamped)
	})

	t.Run("should leave the notice as it was outside a review Job", func(t *testing.T) {
		t.Parallel()

		// given
		body := "✅ **Code Guru review complete.**\n\n_Completed at 2026-10-19T12:07:00Z._"

		// when
		stamped := support.StampReviewJob(body, "")

		// then
		assert.Equal(t, body, stamped)
	})
}

func TestHasReviewOutcomeOf(t *testing.T) {
	t.Parallel()

	stamp := func(body string) string { return support.StampReviewJob(body, "code-guru-review-b") }

	t.Run("should find the Job's own completion and failure notices", func(t *testing.T) {
		t.Parallel()

		// given
		completed := []string{stamp("✅ **Code Guru review complete.**\n\n_Completed at 2026-10-19T12:07:00Z._")}
		failed := []string{stamp("⚠️ **Code Guru review failed.**\n\n_Failed at 2026-10-19T12:30:00Z._")}

		// when
		foundCompleted := support.HasReviewOutcomeOf(completed, "code-guru-review-b")
		foundFailed := support.HasReviewOutcomeOf(failed, "code-guru-review-b")

		// then
		assert.True(t, foundCompleted)
		assert.True(t, foundFailed)
	})

	t.Run("should ignore the outcome of the review queued ahead of it, however recent", func(t *testing.T) {
		t.Parallel()

		// given: the previous review completed seconds before this Job
		// started, exactly what a re-review queued behind it looks like.
		bodies := []string{support.StampReviewJob(
			"✅ **Code Guru review complete.**\n\n_Completed at 2026-10-19T12:07:00Z._", "code-guru-review-a",
		)}

		// when
		found := support.HasReviewOutcomeOf(bodies, "code-guru-review-b")

		// then
		assert.False(t, found)
	})

	t.Run("should ignore a stamp that is not on a review outcome", func(t *testing.T) {
		t.Parallel()

		// given: the queued notice is not an outcome, and a person can quote
		// a Job's name.
		bodies := []string{
			stamp("⏳ **Code Guru queued this pull request for review.**\n\n_Queued at 2026-10-19T12:01:00Z._"),
			"see _Failed at 2026-10-19T12:05:00Z · review job `code-guru-review-b`._ above",
		}

		// when
		found := support.HasReviewOutcomeOf(bodies, "code-guru-review-b")

		// then
		assert.False(t, found)
	})

	t.Run("should match nothing for an empty Job name", func(t *testing.T) {
		t.Parallel()

		// given
		bodies := []string{"✅ **Code Guru review complete.**\n\n_Completed at 2026-10-19T12:07:00Z._"}

		// when
		found := support.HasReviewOutcomeOf(bodies, "")

		// then
		assert.False(t, found)
	})
}
