package commands_test

import (
	"context"
	"errors"
	"testing"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/support"
	doubles "github.com/rios0rios0/codeguru/test/domain/doubles/repositories"
)

// runnerReviewFiles is the pull request every case reviews.
func runnerReviewFiles() []forgeEntities.PullRequestFile {
	return []forgeEntities.PullRequestFile{{Path: "internal/foo.go", Patch: "@@ -1 +1 @@\n-old\n+new\n"}}
}

// reviewWithFindings is an AI reviewer that returns one inline finding and
// one about the pull request as a whole.
func reviewWithFindings() *doubles.StubAIReviewerRepository {
	return &doubles.StubAIReviewerRepository{
		NameValue: "stub",
		Result: &entities.ReviewResult{
			Verdict: "request_changes",
			Summary: "One blocking issue.",
			Comments: []entities.ReviewComment{
				{FilePath: "internal/foo.go", Line: 1, Severity: "error", Body: "closes the wrong handle"},
				{FilePath: "internal/foo.go", Severity: "warning", Body: "the package has no tests"},
			},
		},
	}
}

// postedAs is what the PR-wide comments a review posted look like when the
// forge lists them back, posted by `author`.
func postedAs(author string, provider *recordingReviewProvider) []forgeEntities.PullRequestComment {
	comments := make([]forgeEntities.PullRequestComment, 0, len(provider.calls))
	for i, call := range provider.calls {
		comments = append(comments, forgeEntities.PullRequestComment{ID: int64(i + 1), Author: author, Body: call.body})
	}
	return comments
}

func bodies(comments []forgeEntities.PullRequestComment) []string {
	texts := make([]string, 0, len(comments))
	for _, comment := range comments {
		texts = append(texts, comment.Body)
	}
	return texts
}

func TestExecuteOnRunnerCredential(t *testing.T) {
	t.Parallel()

	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	pr := forgeEntities.PullRequestDetail{ID: 7, Title: "feat", URL: "https://example/pr/7"}

	t.Run("should post the findings and the vote but nothing the bot recognises itself by", func(t *testing.T) {
		t.Parallel()

		// given
		provider := &recordingReviewProvider{files: runnerReviewFiles()}
		command := commands.NewReviewCommand(reviewWithFindings(), &doubles.StubRulesRepository{}, nil, nil)

		// when
		_, err := command.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{
			RunnerCredential: true, SubmitNativeReview: true,
		})

		// then
		require.NoError(t, err)
		require.Len(t, provider.threadComments, 1, "the inline finding is still posted")
		require.Len(t, provider.calls, 1, "only the PR-wide finding is posted PR-wide")
		assert.Contains(t, provider.calls[0].body, "the package has no tests")
		require.Len(t, provider.submissions, 1, "the vote is still cast")
		posted := postedAs("alice", provider)
		assert.Empty(t, support.DetectBotAuthors(posted), "nothing posted may make alice look like the bot")
		assert.False(t, support.HasCompletedReviewMarker(bodies(posted)),
			"nothing posted may stop the bot reviewing the pull request")
	})

	t.Run("should post the bot's status notices when the review runs on a configured token", func(t *testing.T) {
		t.Parallel()

		// given
		provider := &recordingReviewProvider{files: runnerReviewFiles()}
		command := commands.NewReviewCommand(reviewWithFindings(), &doubles.StubRulesRepository{}, nil, nil)

		// when
		_, err := command.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{})

		// then
		require.NoError(t, err)
		posted := postedAs("code-guru-service-account", provider)
		assert.Equal(t, []string{"code-guru-service-account"}, support.DetectBotAuthors(posted))
		assert.True(t, support.HasCompletedReviewMarker(bodies(posted)))
	})

	t.Run("should post no failure notice when the review fails on the runner's own credential", func(t *testing.T) {
		t.Parallel()

		// given
		provider := &recordingReviewProvider{files: runnerReviewFiles()}
		ai := &doubles.StubAIReviewerRepository{NameValue: "stub", Err: errors.New("claude CLI failed: exit status 1")}
		command := commands.NewReviewCommand(ai, &doubles.StubRulesRepository{}, nil, nil)

		// when
		_, err := command.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{
			RunnerCredential: true,
		})

		// then
		require.Error(t, err)
		assert.Empty(t, provider.calls, "neither the reviewing marker nor the failure notice is posted")
	})

	t.Run("should settle a trivial pull request without a completion notice on the runner's own credential",
		func(t *testing.T) {
			t.Parallel()

			// given
			provider := &recordingReviewProvider{files: runnerReviewFiles()}
			registry := &recordingRegistry{verdict: "approve"}
			command := commands.NewReviewCommand(failingAIReviewer{}, &doubles.StubRulesRepository{}, registry, nil)

			// when
			result, err := command.Execute(context.Background(), provider, repo, pr, commands.ReviewOptions{
				RunnerCredential: true, SubmitNativeReview: true,
			})

			// then
			require.NoError(t, err)
			assert.Equal(t, "approve", result.Verdict)
			assert.Empty(t, provider.calls)
			assert.Len(t, provider.submissions, 1)
		})
}

func TestExecuteRefreshesTheProviderBeforePosting(t *testing.T) {
	t.Parallel()

	repo := forgeEntities.Repository{ID: "repo-1", Name: "demo"}
	pr := forgeEntities.PullRequestDetail{ID: 7, Title: "feat", URL: "https://example/pr/7"}

	t.Run("should post the results with the provider the refresh returns", func(t *testing.T) {
		t.Parallel()

		// given: the provider the review started with, and the one built
		// with a token obtained once the AI is done.
		started := &recordingReviewProvider{files: runnerReviewFiles()}
		refreshed := &recordingReviewProvider{files: runnerReviewFiles()}
		command := commands.NewReviewCommand(reviewWithFindings(), &doubles.StubRulesRepository{}, nil, nil)

		// when
		_, err := command.Execute(context.Background(), started, repo, pr, commands.ReviewOptions{
			RunnerCredential:   true,
			SubmitNativeReview: true,
			RefreshProvider: func(context.Context) (forgeEntities.ReviewProvider, error) {
				return refreshed, nil
			},
		})

		// then
		require.NoError(t, err)
		assert.Empty(t, started.threadComments)
		assert.Empty(t, started.submissions)
		assert.Len(t, refreshed.threadComments, 1)
		assert.Len(t, refreshed.submissions, 1)
	})

	t.Run("should post with the provider it started with when the refresh fails", func(t *testing.T) {
		t.Parallel()

		// given
		started := &recordingReviewProvider{files: runnerReviewFiles()}
		command := commands.NewReviewCommand(reviewWithFindings(), &doubles.StubRulesRepository{}, nil, nil)

		// when
		_, err := command.Execute(context.Background(), started, repo, pr, commands.ReviewOptions{
			RunnerCredential: true,
			RefreshProvider: func(context.Context) (forgeEntities.ReviewProvider, error) {
				return nil, errors.New("no GitHub credential available")
			},
		})

		// then
		require.NoError(t, err)
		assert.Len(t, started.threadComments, 1)
	})
}
