package controllers_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers"
)

func TestReviewControllerBindFlags(t *testing.T) {
	t.Parallel()

	t.Run("should register the flags the kubernetes executor passes to a review Job", func(t *testing.T) {
		t.Parallel()

		// given
		cmd := &cobra.Command{Use: "review"}

		// when
		(&controllers.ReviewController{}).BindFlags(cmd)
		parseErr := cmd.Flags().Parse([]string{"--mentioned", "--job-name", "code-guru-review-abc"})

		// then
		require.NoError(t, parseErr)
		mentioned, err := cmd.Flags().GetBool("mentioned")
		require.NoError(t, err)
		assert.True(t, mentioned)
		jobName, err := cmd.Flags().GetString("job-name")
		require.NoError(t, err)
		assert.Equal(t, "code-guru-review-abc", jobName)
	})

	t.Run("should default to a first review outside any review Job", func(t *testing.T) {
		t.Parallel()

		// given
		cmd := &cobra.Command{Use: "review"}

		// when
		(&controllers.ReviewController{}).BindFlags(cmd)
		parseErr := cmd.Flags().Parse(nil)

		// then
		require.NoError(t, parseErr)
		mentioned, _ := cmd.Flags().GetBool("mentioned")
		jobName, _ := cmd.Flags().GetString("job-name")
		assert.False(t, mentioned)
		assert.Empty(t, jobName)
	})
}
