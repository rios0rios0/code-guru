package webhooks_test

import (
	"context"
	"sync"
	"testing"
	"time"

	configEntities "github.com/rios0rios0/gitforge/v4/pkg/config/domain/entities"
	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	registry "github.com/rios0rios0/gitforge/v4/pkg/registry/infrastructure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
	infraRepos "github.com/rios0rios0/codeguru/internal/infrastructure/repositories"
	doubles "github.com/rios0rios0/codeguru/test/infrastructure/doubles/repositories"
)

// jobPathProvider is a gitforge ReviewProvider stub answering exactly what the
// executor's triage and failure reporting call; anything else panics on the
// nil embedded interface, by design.
type jobPathProvider struct {
	forgeEntities.ReviewProvider

	mu       sync.Mutex
	files    []forgeEntities.PullRequestFile
	comments []forgeEntities.PullRequestComment
	status   string
	posted   []string
}

func (p *jobPathProvider) Name() string { return "azuredevops" }

func (p *jobPathProvider) GetPullRequestFiles(
	_ context.Context, _ forgeEntities.Repository, _ int,
) ([]forgeEntities.PullRequestFile, error) {
	return p.files, nil
}

func (p *jobPathProvider) ListPullRequestComments(
	_ context.Context, _ forgeEntities.Repository, _ int,
) ([]forgeEntities.PullRequestComment, error) {
	return p.comments, nil
}

func (p *jobPathProvider) GetPullRequestStatus(_ context.Context, _ forgeEntities.Repository, _ int) (string, error) {
	if p.status == "" {
		return "active", nil
	}
	return p.status, nil
}

func (p *jobPathProvider) PostPullRequestComment(
	_ context.Context, _ forgeEntities.Repository, _ int, body string, _ ...forgeEntities.CommentOption,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.posted = append(p.posted, body)
	return nil
}

func (p *jobPathProvider) postedBodies() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.posted...)
}

// newReviewJobDispatcher builds a dispatcher on the Kubernetes executor, with
// `provider` behind the registry's azuredevops factory so the failure reporter
// resolves it like a real one.
func newReviewJobDispatcher(
	t *testing.T, provider *jobPathProvider, client *doubles.InMemoryReviewJobClient,
) *webhooks.Dispatcher {
	t.Helper()
	reg := registry.NewProviderRegistry()
	reg.RegisterFactory("azuredevops", func(string) forgeEntities.ForgeProvider { return provider })
	settings := &entities.Settings{
		AI:        entities.AIConfig{Backend: "claude"},
		Providers: []configEntities.ProviderConfig{{Type: "azuredevops", Token: "fixture-token-placeholder"}},
	}
	d := webhooks.NewDispatcher(
		infraRepos.NewAIReviewerFactory(), infraRepos.NewRulesRepositoryFactory(), nil, nil, settings, reg,
	)
	d.SetReviewJobLauncher(newLauncher(t, client, 1))
	return d
}

func reviewJobFor(provider *jobPathProvider, prID int, draft bool) webhooks.Job {
	return webhooks.Job{
		Provider: provider,
		Repo: forgeEntities.Repository{
			ID: "repo-uuid", Name: "My Repo", Organization: "ExampleOrg", Project: "Platform Team",
		},
		PR: forgeEntities.PullRequestDetail{
			PullRequest: forgeEntities.PullRequest{ID: prID, Title: "feat: x"},
			IsDraft:     draft,
		},
	}
}

func TestDispatcherHandleJobWithKubernetesExecutor(t *testing.T) {
	t.Parallel()

	t.Run("should hand a pull request that passes the gates to a running review job", func(t *testing.T) {
		t.Parallel()

		// given
		provider := &jobPathProvider{files: []forgeEntities.PullRequestFile{{Path: "/main.go"}}}
		client := doubles.NewInMemoryReviewJobClient()
		d := newReviewJobDispatcher(t, provider, client)

		// when
		err := d.HandleJob(context.Background(), reviewJobFor(provider, 42, false))

		// then
		require.NoError(t, err)
		jobs, _ := client.List(context.Background(), "code-guru/review-job=true")
		require.Len(t, jobs, 1)
		assert.False(t, jobs[0].Suspended())
		assert.Empty(t, provider.postedBodies(), "a review that starts at once needs no queued notice")
	})

	t.Run("should post a queued notice when every review slot is busy", func(t *testing.T) {
		t.Parallel()

		// given: one review already running, on a cap of one.
		provider := &jobPathProvider{files: []forgeEntities.PullRequestFile{{Path: "/main.go"}}}
		client := doubles.NewInMemoryReviewJobClient()
		d := newReviewJobDispatcher(t, provider, client)
		require.NoError(t, d.HandleJob(context.Background(), reviewJobFor(provider, 41, false)))

		// when
		err := d.HandleJob(context.Background(), reviewJobFor(provider, 42, false))

		// then
		require.NoError(t, err)
		posted := provider.postedBodies()
		require.Len(t, posted, 1)
		assert.Contains(t, posted[0], "**Code Guru queued this pull request for review.**")
		assert.NotContains(t, posted[0], "**Code Guru review",
			"the queued notice must not trip the review-once gate the review Job checks when it starts")
	})

	t.Run("should settle a draft without creating a job", func(t *testing.T) {
		t.Parallel()

		// given
		provider := &jobPathProvider{files: []forgeEntities.PullRequestFile{{Path: "/main.go"}}}
		client := doubles.NewInMemoryReviewJobClient()
		d := newReviewJobDispatcher(t, provider, client)

		// when
		err := d.HandleJob(context.Background(), reviewJobFor(provider, 42, true))

		// then
		require.NoError(t, err)
		jobs, _ := client.List(context.Background(), "code-guru/review-job=true")
		assert.Empty(t, jobs, "the gates run in the server so a draft never costs a pod")
	})

	t.Run("should settle an already-reviewed pull request without creating a job", func(t *testing.T) {
		t.Parallel()

		// given
		provider := &jobPathProvider{
			files:    []forgeEntities.PullRequestFile{{Path: "/main.go"}},
			comments: []forgeEntities.PullRequestComment{{Body: "✅ **Code Guru review complete.**"}},
		}
		client := doubles.NewInMemoryReviewJobClient()
		d := newReviewJobDispatcher(t, provider, client)

		// when
		err := d.HandleJob(context.Background(), reviewJobFor(provider, 42, false))

		// then
		require.NoError(t, err)
		jobs, _ := client.List(context.Background(), "code-guru/review-job=true")
		assert.Empty(t, jobs)
	})
}

// failedJobFor builds a failed review Job carrying the annotations the
// launcher stamps, started at `started`.
func failedJobFor(started time.Time) webhooks.ReviewJob {
	return webhooks.ReviewJob{
		Metadata: webhooks.ReviewJobMetadata{
			Name:              "code-guru-review-x",
			CreationTimestamp: started.Add(-time.Minute),
			Annotations: map[string]string{
				"code-guru/provider":        "azuredevops",
				"code-guru/organization":    "ExampleOrg",
				"code-guru/project":         "Platform Team",
				"code-guru/repository":      "My Repo",
				"code-guru/repository-id":   "repo-uuid",
				"code-guru/pull-request-id": "42",
			},
		},
		Status: webhooks.ReviewJobStatus{StartTime: &started},
	}
}

func TestDispatcherReportReviewJobFailure(t *testing.T) {
	t.Parallel()

	t.Run("should post the failure when the review pod reported nothing", func(t *testing.T) {
		t.Parallel()

		// given
		provider := &jobPathProvider{
			comments: []forgeEntities.PullRequestComment{{Body: "🤖 **Code Guru is reviewing this PR.**"}},
		}
		d := newReviewJobDispatcher(t, provider, doubles.NewInMemoryReviewJobClient())

		// when
		err := d.ReportReviewJobFailure(context.Background(), failedJobFor(time.Now().UTC()), "DeadlineExceeded")

		// then
		require.NoError(t, err)
		posted := provider.postedBodies()
		require.Len(t, posted, 1)
		assert.Contains(t, posted[0], "**Code Guru review failed.**", "the notice sets the review-once gate")
		assert.Contains(t, posted[0], "ran past its time limit")
		assert.Contains(t, posted[0], "review job `code-guru-review-x`", "it names the Job whose logs to read")
	})

	t.Run("should stay silent when the review pod already reported its own failure", func(t *testing.T) {
		t.Parallel()

		// given: the notice the Job's own pod posted before exiting non-zero.
		started := time.Now().UTC().Add(-10 * time.Minute)
		ownNotice := "⚠️ **Code Guru review failed.**\n\nThe AI backend errored.\n\n_Failed at " +
			started.Add(5*time.Minute).Format(time.RFC3339) + " · review job `code-guru-review-x`._"
		provider := &jobPathProvider{comments: []forgeEntities.PullRequestComment{{Body: ownNotice}}}
		d := newReviewJobDispatcher(t, provider, doubles.NewInMemoryReviewJobClient())

		// when
		err := d.ReportReviewJobFailure(context.Background(), failedJobFor(started), "BackoffLimitExceeded")

		// then
		require.NoError(t, err)
		assert.Empty(t, provider.postedBodies())
	})

	t.Run(
		"should report a re-review that died right after the review queued ahead of it completed",
		func(t *testing.T) {
			t.Parallel()

			// given: the review ahead posted its completion seconds before this
			// re-review Job started — then the re-review ran out of memory.
			started := time.Now().UTC()
			earlier := "✅ **Code Guru review complete.**\n\n_Completed at " +
				started.Add(-5*time.Second).Format(time.RFC3339) + " · review job `code-guru-review-w`._"
			provider := &jobPathProvider{comments: []forgeEntities.PullRequestComment{{Body: earlier}}}
			d := newReviewJobDispatcher(t, provider, doubles.NewInMemoryReviewJobClient())

			// when
			err := d.ReportReviewJobFailure(context.Background(), failedJobFor(started), "DeadlineExceeded")

			// then
			require.NoError(t, err)
			assert.Len(t, provider.postedBodies(), 1)
		},
	)

	t.Run("should post nothing on a closed pull request", func(t *testing.T) {
		t.Parallel()

		// given
		provider := &jobPathProvider{status: "completed"}
		d := newReviewJobDispatcher(t, provider, doubles.NewInMemoryReviewJobClient())

		// when
		err := d.ReportReviewJobFailure(context.Background(), failedJobFor(time.Now().UTC()), "DeadlineExceeded")

		// then
		require.NoError(t, err)
		assert.Empty(t, provider.postedBodies())
	})

	t.Run("should refuse a job whose annotations name no pull request", func(t *testing.T) {
		t.Parallel()

		// given
		provider := &jobPathProvider{}
		d := newReviewJobDispatcher(t, provider, doubles.NewInMemoryReviewJobClient())
		job := failedJobFor(time.Now().UTC())
		delete(job.Metadata.Annotations, "code-guru/pull-request-id")

		// when
		err := d.ReportReviewJobFailure(context.Background(), job, "DeadlineExceeded")

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pull-request-id")
		assert.Empty(t, provider.postedBodies())
	})
}
