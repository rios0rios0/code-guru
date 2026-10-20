package webhooks_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
	"github.com/rios0rios0/codeguru/internal/support"
	doubles "github.com/rios0rios0/codeguru/test/infrastructure/doubles/repositories"
)

// reviewJobTemplateYAML is an operator-owned template: two containers (a
// sidecar first), its own labels, an env entry the executor must keep, and a
// stale namespace the executor must drop.
const reviewJobTemplateYAML = `
apiVersion: batch/v1
kind: Job
metadata:
  namespace: stale
  labels:
    team: platform
spec:
  activeDeadlineSeconds: 2400
  template:
    metadata:
      labels:
        app: code-guru
    spec:
      containers:
        - name: helper
          image: busybox
        - name: review
          image: ghcr.io/example/code-guru:1.0.0
          env:
            - name: CODE_GURU_BACKEND
              value: claude
`

func adoReviewRequest() webhooks.ReviewJobRequest {
	return webhooks.ReviewJobRequest{
		Provider: "azuredevops",
		Repository: forgeEntities.Repository{
			ID: "repo-uuid", Name: "My Repo", Organization: "ExampleOrg", Project: "Platform Team",
		},
		PullRequestID: 42,
	}
}

func defaultTemplate(t *testing.T) *webhooks.ReviewJobTemplate {
	t.Helper()
	template, err := webhooks.ParseReviewJobTemplate([]byte(reviewJobTemplateYAML))
	require.NoError(t, err)
	return template
}

func TestParseReviewJobTemplate(t *testing.T) {
	t.Parallel()

	t.Run("should accept a JSON template", func(t *testing.T) {
		t.Parallel()

		// given
		manifest := `{"spec":{"template":{"spec":{"containers":[{"name":"review","image":"x"}]}}}}`

		// when
		template, err := webhooks.ParseReviewJobTemplate([]byte(manifest))

		// then
		require.NoError(t, err)
		assert.NotNil(t, template)
	})

	t.Run("should reject a template without containers", func(t *testing.T) {
		t.Parallel()

		// given
		manifest := "apiVersion: batch/v1\nkind: Job\nspec: {}\n"

		// when
		_, err := webhooks.ParseReviewJobTemplate([]byte(manifest))

		// then
		require.ErrorContains(t, err, "no spec.template.spec.containers")
	})

	t.Run("should reject a manifest that is not a Job", func(t *testing.T) {
		t.Parallel()

		// given
		manifest := "kind: Deployment\nspec:\n  template:\n    spec:\n      containers: [{name: review}]\n"

		// when
		_, err := webhooks.ParseReviewJobTemplate([]byte(manifest))

		// then
		require.ErrorContains(t, err, "must be a Job")
	})

	t.Run("should reject restartPolicy Always, which a Job does not allow", func(t *testing.T) {
		t.Parallel()

		// given
		manifest := "spec:\n  template:\n    spec:\n      restartPolicy: Always\n      containers: [{name: review}]\n"

		// when
		_, err := webhooks.ParseReviewJobTemplate([]byte(manifest))

		// then
		require.ErrorContains(t, err, "restartPolicy Always")
	})

	t.Run("should require a template path", func(t *testing.T) {
		t.Parallel()

		// given
		blank := "  "

		// when
		_, err := webhooks.LoadReviewJobTemplate(blank)

		// then
		require.ErrorContains(t, err, "job_template is required")
	})
}

func TestReviewJobTemplateRender(t *testing.T) {
	t.Parallel()

	t.Run("should fill in only the per-review fields and keep the operator's pod", func(t *testing.T) {
		t.Parallel()

		// given
		template := defaultTemplate(t)

		// when
		manifest, err := template.Render(adoReviewRequest(), "code-guru-review-abc")

		// then
		require.NoError(t, err)
		metadata := manifest["metadata"].(map[string]any)
		assert.Equal(t, "code-guru-review-abc", metadata["name"])
		assert.NotContains(t, metadata, "namespace", "the namespace is the one the client posts to")
		labels := metadata["labels"].(map[string]any)
		assert.Equal(t, "platform", labels["team"], "the template's own labels survive")
		assert.Equal(t, "true", labels["code-guru/review-job"])
		assert.Equal(t, "review", labels["app.kubernetes.io/component"])
		annotations := metadata["annotations"].(map[string]any)
		assert.Equal(t, "https://dev.azure.com/ExampleOrg/Platform%20Team/_git/My%20Repo/pullrequest/42",
			annotations["code-guru/pull-request-url"])
		assert.Equal(t, "repo-uuid", annotations["code-guru/repository-id"])

		spec := manifest["spec"].(map[string]any)
		assert.Equal(t, true, spec["suspend"], "every review Job is created queued; only the scheduler starts it")
		assert.InDelta(t, 2400, spec["activeDeadlineSeconds"], 0, "the operator's deadline is kept")
		assert.InDelta(t, 0, spec["backoffLimit"], 0)
		assert.InDelta(t, 900, spec["ttlSecondsAfterFinished"], 0)

		pod := spec["template"].(map[string]any)
		podLabels := pod["metadata"].(map[string]any)["labels"].(map[string]any)
		assert.Equal(t, "code-guru", podLabels["app"])
		assert.Equal(t, "true", podLabels["code-guru/review-job"], "a NetworkPolicy can select review pods")
		podSpec := pod["spec"].(map[string]any)
		assert.Equal(t, "Never", podSpec["restartPolicy"])
		containers := podSpec["containers"].([]any)
		assert.NotContains(t, containers[0].(map[string]any), "args", "only the review container is pointed at the PR")
		review := containers[1].(map[string]any)
		assert.Equal(
			t,
			[]any{
				"review", "https://dev.azure.com/ExampleOrg/Platform%20Team/_git/My%20Repo/pullrequest/42",
				"--job-name", "code-guru-review-abc",
			},
			review["args"],
			"the Job's name rides into its outcome notices",
		)
	})

	t.Run("should give a template without a deadline the executor's default", func(t *testing.T) {
		t.Parallel()

		// given: a pod that never starts would otherwise hold a review slot
		// forever.
		template, err := webhooks.ParseReviewJobTemplate([]byte(
			"spec:\n  template:\n    spec:\n      containers:\n        - name: review\n          image: x\n",
		))
		require.NoError(t, err)

		// when
		manifest, renderErr := template.Render(adoReviewRequest(), "n")

		// then
		require.NoError(t, renderErr)
		assert.InDelta(t, 3600, manifest["spec"].(map[string]any)["activeDeadlineSeconds"], 0)
	})

	t.Run("should build a URL the review command parses back to the same pull request", func(t *testing.T) {
		t.Parallel()

		// given
		template := defaultTemplate(t)
		request := adoReviewRequest()

		// when
		manifest, err := template.Render(request, "n")

		// then
		require.NoError(t, err)
		url := manifest["metadata"].(map[string]any)["annotations"].(map[string]any)["code-guru/pull-request-url"]
		parsed, parseErr := support.ParsePullRequestURL(url.(string))
		require.NoError(t, parseErr)
		assert.Equal(t, "azuredevops", parsed.ProviderType)
		assert.Equal(t, "ExampleOrg", parsed.Organization)
		assert.Equal(t, "Platform Team", parsed.Project)
		assert.Equal(t, "My Repo", parsed.RepoName)
		assert.Equal(t, 42, parsed.PRID)
	})

	t.Run("should pass a mention and a GitHub App installation to the pod", func(t *testing.T) {
		t.Parallel()

		// given
		template := defaultTemplate(t)
		request := webhooks.ReviewJobRequest{
			Provider:             "github",
			Repository:           forgeEntities.Repository{Name: "app", Organization: "acme"},
			PullRequestID:        7,
			UserMentioned:        true,
			GitHubInstallationID: 99,
		}

		// when
		manifest, err := template.Render(request, "n")

		// then
		require.NoError(t, err)
		podSpec := manifest["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
		review := podSpec["containers"].([]any)[1].(map[string]any)
		assert.Equal(
			t, []any{"review", "https://github.com/acme/app/pull/7", "--mentioned", "--job-name", "n"}, review["args"],
		)
		env := review["env"].([]any)
		assert.Contains(t, env, map[string]any{"name": "CODE_GURU_GITHUB_INSTALLATION_ID", "value": "99"})
		assert.Contains(t, env, map[string]any{"name": "CODE_GURU_BACKEND", "value": "claude"},
			"the operator's environment is kept")
	})

	t.Run("should refuse a provider the executor cannot build a URL for", func(t *testing.T) {
		t.Parallel()

		// given
		template := defaultTemplate(t)
		request := adoReviewRequest()
		request.Provider = "gitlab"

		// when
		_, err := template.Render(request, "n")

		// then
		require.ErrorContains(t, err, "not supported by the kubernetes executor")
	})

	t.Run("should not leak one render into the next", func(t *testing.T) {
		t.Parallel()

		// given
		template := defaultTemplate(t)
		mention := adoReviewRequest()
		mention.UserMentioned = true
		_, err := template.Render(mention, "first")
		require.NoError(t, err)

		// when
		second, err := template.Render(adoReviewRequest(), "second")

		// then
		require.NoError(t, err)
		podSpec := second["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
		review := podSpec["containers"].([]any)[1].(map[string]any)
		assert.NotContains(t, review["args"], "--mentioned", "each render starts from a fresh copy of the template")
	})
}

func newLauncher(t *testing.T, client *doubles.InMemoryReviewJobClient, maxConcurrent int) *webhooks.ReviewJobLauncher {
	t.Helper()
	scheduler := webhooks.NewReviewJobScheduler(client, maxConcurrent, time.Minute, nil)
	return webhooks.NewReviewJobLauncher(client, defaultTemplate(t), scheduler)
}

// newLockedLauncher is newLauncher with the scheduler and the launcher sharing
// one lock set, the way the in-cluster executor wires them.
func newLockedLauncher(
	t *testing.T, client *doubles.InMemoryReviewJobClient, maxConcurrent int, locks *doubles.InMemoryReviewJobLocks,
) *webhooks.ReviewJobLauncher {
	t.Helper()
	scheduler := webhooks.NewReviewJobScheduler(client, maxConcurrent, time.Minute, nil).WithLocks(locks)
	return webhooks.NewReviewJobLauncher(client, defaultTemplate(t), scheduler).WithLocks(locks)
}

func TestReviewJobLauncherEnqueue(t *testing.T) {
	t.Parallel()

	t.Run("should create a Job and start it at once when a slot is free", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		launcher := newLauncher(t, client, 2)

		// when
		outcome, err := launcher.Enqueue(context.Background(), adoReviewRequest())

		// then
		require.NoError(t, err)
		assert.False(t, outcome.Queued)
		assert.False(t, outcome.Duplicate)
		assert.True(t, strings.HasPrefix(outcome.JobName, "code-guru-review-"))
		assert.LessOrEqual(t, len(outcome.JobName), 63, "the name becomes the pod's job-name label")
		job, ok := client.Job(outcome.JobName)
		require.True(t, ok)
		assert.False(t, job.Suspended(), "a free slot releases the Job in the same call")
	})

	t.Run("should queue the Job, with its position, when every slot is busy", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		launcher := newLauncher(t, client, 1)
		first := adoReviewRequest()
		second := adoReviewRequest()
		second.PullRequestID = 43
		third := adoReviewRequest()
		third.PullRequestID = 44
		_, err := launcher.Enqueue(context.Background(), first)
		require.NoError(t, err)
		_, err = launcher.Enqueue(context.Background(), second)
		require.NoError(t, err)

		// when
		outcome, err := launcher.Enqueue(context.Background(), third)

		// then
		require.NoError(t, err)
		assert.True(t, outcome.Queued)
		assert.Equal(t, 2, outcome.QueuePosition, "one Job ahead of it is still waiting")
		job, _ := client.Job(outcome.JobName)
		assert.True(t, job.Suspended())
	})

	t.Run("should not create a second Job while one is unfinished for the same pull request", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		launcher := newLauncher(t, client, 1)
		first, err := launcher.Enqueue(context.Background(), adoReviewRequest())
		require.NoError(t, err)

		// when
		again, err := launcher.Enqueue(context.Background(), adoReviewRequest())

		// then
		require.NoError(t, err)
		assert.True(t, again.Duplicate)
		assert.Equal(t, first.JobName, again.JobName)
		jobs, _ := client.List(context.Background(), "code-guru/review-job=true")
		assert.Len(t, jobs, 1)
	})

	t.Run("should queue one requested re-review behind the review still running for the pull request",
		func(t *testing.T) {
			t.Parallel()

			// given: a review is running when someone mentions the bot.
			client := doubles.NewInMemoryReviewJobClient()
			launcher := newLauncher(t, client, 3)
			_, err := launcher.Enqueue(context.Background(), adoReviewRequest())
			require.NoError(t, err)
			mention := adoReviewRequest()
			mention.UserMentioned = true

			// when
			reReview, err := launcher.Enqueue(context.Background(), mention)
			again, againErr := launcher.Enqueue(context.Background(), mention)

			// then
			require.NoError(t, err)
			assert.False(t, reReview.Duplicate, "a mention during a review must not go unanswered")
			assert.True(t, reReview.Queued)
			assert.True(t, reReview.BehindOwnPullRequest, "it waits for the review, not for a slot")
			require.NoError(t, againErr)
			assert.True(t, again.Duplicate, "one queued re-review per pull request is enough")
		})

	t.Run("should queue a re-review behind a re-review that is already running", func(t *testing.T) {
		t.Parallel()

		// given: a requested re-review is running — it checked the code out
		// before whatever the author pushed since — when the bot is mentioned
		// again.
		client := doubles.NewInMemoryReviewJobClient()
		launcher := newLauncher(t, client, 3)
		mention := adoReviewRequest()
		mention.UserMentioned = true
		running, err := launcher.Enqueue(context.Background(), mention)
		require.NoError(t, err)
		require.False(t, running.Queued, "the first re-review starts at once")

		// when
		next, err := launcher.Enqueue(context.Background(), mention)

		// then
		require.NoError(t, err)
		assert.False(t, next.Duplicate, "a running re-review cannot answer a request made after it started")
		assert.True(t, next.Queued)
		assert.True(t, next.BehindOwnPullRequest)
		assert.NotEqual(t, running.JobName, next.JobName)
	})

	t.Run("should hold the pull request's lock across the listing and the create", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		locks := doubles.NewInMemoryReviewJobLocks()
		launcher := newLockedLauncher(t, client, 1, locks)
		var heldWhileListing atomic.Bool
		client.AfterList = func() {
			if locks.HeldWithPrefix("review-job-enqueue:") {
				heldWhileListing.Store(true)
			}
		}

		// when
		outcome, err := launcher.Enqueue(context.Background(), adoReviewRequest())

		// then
		require.NoError(t, err)
		assert.False(t, outcome.Duplicate)
		assert.True(t, heldWhileListing.Load(), "a second delivery must not list until this one has created")
		assert.False(t, locks.HeldWithPrefix("review-job-enqueue:"), "the lock is released afterwards")
	})

	t.Run("should wait for a concurrent enqueue of the same pull request to finish", func(t *testing.T) {
		t.Parallel()

		// given: another replica holds the pull request's lock for the next
		// two attempts.
		client := doubles.NewInMemoryReviewJobClient()
		locks := doubles.NewInMemoryReviewJobLocks()
		locks.BusyFor("review-job-enqueue:", 2)
		launcher := newLockedLauncher(t, client, 1, locks)

		// when
		outcome, err := launcher.Enqueue(context.Background(), adoReviewRequest())

		// then
		require.NoError(t, err)
		_, created := client.Job(outcome.JobName)
		assert.True(t, created)
		attempts := 0
		for _, key := range locks.TryLocks {
			if strings.HasPrefix(key, "review-job-enqueue:") {
				attempts++
			}
		}
		assert.Equal(t, 3, attempts, "two refusals, then the lock")
	})

	t.Run("should stamp each Job with the time it was queued", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		launcher := newLauncher(t, client, 1)

		// when
		outcome, err := launcher.Enqueue(context.Background(), adoReviewRequest())

		// then
		require.NoError(t, err)
		job, _ := client.Job(outcome.JobName)
		stamp, parseErr := time.Parse(time.RFC3339Nano, job.Metadata.Annotations["code-guru/enqueued-at"])
		require.NoError(t, parseErr, "the scheduler orders a burst by this stamp")
		assert.WithinDuration(t, time.Now(), stamp, time.Minute)
	})

	t.Run("should create a new Job once the previous one for the pull request finished", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		launcher := newLauncher(t, client, 1)
		first, err := launcher.Enqueue(context.Background(), adoReviewRequest())
		require.NoError(t, err)
		client.Finish(first.JobName, "")

		// when
		again, err := launcher.Enqueue(context.Background(), adoReviewRequest())

		// then
		require.NoError(t, err)
		assert.False(t, again.Duplicate)
		assert.NotEqual(t, first.JobName, again.JobName)
	})

	t.Run("should report a failed create", func(t *testing.T) {
		t.Parallel()

		// given
		client := doubles.NewInMemoryReviewJobClient()
		client.CreateErr = errors.New("forbidden")
		launcher := newLauncher(t, client, 1)

		// when
		_, err := launcher.Enqueue(context.Background(), adoReviewRequest())

		// then
		require.ErrorContains(t, err, "creating the review job for PR #42")
	})
}
