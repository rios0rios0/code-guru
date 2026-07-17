//go:build unit

package support_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestContentSafetyRefusal(t *testing.T) {
	t.Parallel()

	t.Run("should match the sentinel via errors.Is regardless of category", func(t *testing.T) {
		t.Parallel()

		// given: refusals with and without a category, each wrapped the way a
		// backend + retry decorator would wrap them
		withCategory := fmt.Errorf("anthropic: %w", &support.ContentSafetyRefusalError{Category: "cyber"})
		noCategory := error(&support.ContentSafetyRefusalError{})

		// when / then
		require.ErrorIs(t, withCategory, support.ErrContentSafetyRefusal,
			"a categorised refusal must classify as ErrContentSafetyRefusal")
		require.ErrorIs(t, noCategory, support.ErrContentSafetyRefusal,
			"an uncategorised refusal must classify as ErrContentSafetyRefusal")
	})

	t.Run("should expose the provider category via errors.As", func(t *testing.T) {
		t.Parallel()

		// given
		wrapped := fmt.Errorf("anthropic: %w", &support.ContentSafetyRefusalError{Category: "cyber"})

		// when
		var refusal *support.ContentSafetyRefusalError
		ok := errors.As(wrapped, &refusal)

		// then
		require.True(t, ok, "errors.As must recover the typed refusal through the wrapper")
		assert.Equal(t, "cyber", refusal.Category, "the classification label must survive wrapping")
	})

	t.Run("should render the category in the error text for logs but not empty categories", func(t *testing.T) {
		t.Parallel()

		// given / when / then
		assert.Contains(t, (&support.ContentSafetyRefusalError{Category: "cyber"}).Error(), "cyber")
		assert.NotContains(t, (&support.ContentSafetyRefusalError{}).Error(), ":",
			"an empty category must not render a dangling `refusal: ` suffix")
	})
}
