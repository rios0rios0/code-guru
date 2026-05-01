//go:build unit

package webhooks_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	configEntities "github.com/rios0rios0/gitforge/pkg/config/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
	doubles "github.com/rios0rios0/codeguru/test/infrastructure/doubles/repositories"
)

const (
	ghSecret = "gh-test-secret"
	ghOwner  = "rios0rios0"
	ghRepo   = "demo"
)

func defaultGitHubSettings() *entities.Settings {
	return &entities.Settings{
		Providers: []configEntities.ProviderConfig{
			{Type: "github", Token: "ghp_test123"},
		},
		Server: entities.ServerConfig{
			WebhookSecret:        ghSecret,
			AllowedOrganizations: []string{ghOwner},
		},
	}
}

const ghOpenedPayload = `{
  "action": "opened",
  "pull_request": {
    "number": 7,
    "title": "Add new feature",
    "html_url": "https://github.com/rios0rios0/demo/pull/7",
    "state": "open",
    "head": {"ref": "feat/x"},
    "base": {"ref": "main"},
    "user": {"login": "octocat"}
  },
  "repository": {
    "name": "demo",
    "full_name": "rios0rios0/demo",
    "html_url": "https://github.com/rios0rios0/demo",
    "owner": {"login": "rios0rios0"}
  },
  "installation": {"id": 1234}
}`

func githubRequest(t *testing.T, secret, body, eventType string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewBufferString(body))
	req.Header.Set("X-GitHub-Event", eventType)
	req.Header.Set("X-Hub-Signature-256", computeHMACHeader(secret, body))
	return req
}

func newDispatcherWithGitHubTokenizer(t *testing.T, settings *entities.Settings) (*webhooks.Dispatcher, *doubles.StubWebhookSubmitter) {
	t.Helper()
	d, sub := newDispatcherWithSettings(t, settings)
	d.SetGitHubTokenizer(&doubles.StubGitHubTokenizer{Token: "installation-token-xyz"})
	return d, sub
}

func TestHandleGitHub(t *testing.T) {
	t.Parallel()

	t.Run("should respond 202 (Accepted) when an opened PR is enqueued", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithGitHubTokenizer(t, defaultGitHubSettings())
		req := githubRequest(t, ghSecret, ghOpenedPayload, "pull_request")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusAccepted, w.Code)
		jobs := sub.Jobs()
		require.Len(t, jobs, 1)
		assert.Equal(t, 7, jobs[0].PR.ID)
		assert.Equal(t, ghRepo, jobs[0].Repo.Name)
		assert.Equal(t, ghOwner, jobs[0].Repo.Organization)
	})

	t.Run("should respond 401 (Unauthorized) when the HMAC is invalid", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithGitHubTokenizer(t, defaultGitHubSettings())
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewBufferString(ghOpenedPayload))
		req.Header.Set("X-GitHub-Event", "pull_request")
		req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 400 (Bad Request) when the signature header is missing", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithGitHubTokenizer(t, defaultGitHubSettings())
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewBufferString(ghOpenedPayload))
		req.Header.Set("X-GitHub-Event", "pull_request")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 204 (No Content) when the action is ignored", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithGitHubTokenizer(t, defaultGitHubSettings())
		body := `{"action":"closed","pull_request":{"number":1},"repository":{"full_name":"rios0rios0/demo"},"installation":{"id":1}}`
		req := githubRequest(t, ghSecret, body, "pull_request")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 204 (No Content) when the event type is unsupported", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithGitHubTokenizer(t, defaultGitHubSettings())
		req := githubRequest(t, ghSecret, ghOpenedPayload, "issue_comment")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 400 (Bad Request) when the JSON is malformed", func(t *testing.T) {
		// given
		d, _ := newDispatcherWithGitHubTokenizer(t, defaultGitHubSettings())
		req := githubRequest(t, ghSecret, `{not json`, "pull_request")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("should respond 403 (Forbidden) when the org is not on the allowlist", func(t *testing.T) {
		// given
		settings := defaultGitHubSettings()
		settings.Server.AllowedOrganizations = []string{"someoneelse"}
		d, sub := newDispatcherWithGitHubTokenizer(t, settings)
		req := githubRequest(t, ghSecret, ghOpenedPayload, "pull_request")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should fall back to the configured PAT when no GitHub App tokenizer is wired", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithSettings(t, defaultGitHubSettings()) // no SetGitHubTokenizer
		req := githubRequest(t, ghSecret, ghOpenedPayload, "pull_request")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusAccepted, w.Code)
		require.Len(t, sub.Jobs(), 1)
	})

	t.Run("should accept a single untyped provider entry as the catch-all PAT", func(t *testing.T) {
		// given - mirrors the env-only configuration where CODE_GURU_PROVIDER_TOKEN
		// populates a single ProviderConfig entry without a Type.
		settings := &entities.Settings{
			Providers: []configEntities.ProviderConfig{
				{Token: "ghp_envtest"}, // no Type set
			},
			Server: entities.ServerConfig{
				WebhookSecret:        ghSecret,
				AllowedOrganizations: []string{ghOwner},
			},
		}
		d, sub := newDispatcherWithSettings(t, settings)
		req := githubRequest(t, ghSecret, ghOpenedPayload, "pull_request")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusAccepted, w.Code)
		require.Len(t, sub.Jobs(), 1)
	})

	t.Run("should respond 403 (Forbidden) when CF-Connecting-IP is outside AllowedSourceCIDRs", func(t *testing.T) {
		// given: source-IP allowlist runs before HMAC verification, so an
		// off-list request never gets a chance to brute-force the signature.
		settings := defaultGitHubSettings()
		settings.Server.AllowedSourceCIDRs = []string{"140.82.112.0/20"} // GitHub Hooks range example
		d, sub := newDispatcherWithGitHubTokenizer(t, settings)
		req := githubRequest(t, ghSecret, ghOpenedPayload, "pull_request")
		req.Header.Set("CF-Connecting-IP", "8.8.8.8")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 202 (Accepted) when CF-Connecting-IP is inside AllowedSourceCIDRs", func(t *testing.T) {
		// given
		settings := defaultGitHubSettings()
		settings.Server.AllowedSourceCIDRs = []string{"140.82.112.0/20"}
		d, sub := newDispatcherWithGitHubTokenizer(t, settings)
		req := githubRequest(t, ghSecret, ghOpenedPayload, "pull_request")
		req.Header.Set("CF-Connecting-IP", "140.82.112.42")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Len(t, sub.Jobs(), 1)
	})

	t.Run("should short-circuit a duplicate webhook delivery without enqueueing a second job", func(t *testing.T) {
		// given: GitHub sometimes redelivers a webhook (e.g. on a 5xx
		// response). The dedup cache must accept the first and refuse
		// the second, mirroring the ADO contract. Pinned per Copilot
		// review on PR #100 thread `PRRT_kwDOJKAEo85-5zEz`.
		d, sub := newDispatcherWithGitHubTokenizer(t, defaultGitHubSettings())
		req1 := githubRequest(t, ghSecret, ghOpenedPayload, "pull_request")
		req2 := githubRequest(t, ghSecret, ghOpenedPayload, "pull_request")
		w1 := httptest.NewRecorder()
		w2 := httptest.NewRecorder()

		// when
		d.HandleGitHub(w1, req1)
		d.HandleGitHub(w2, req2)

		// then
		assert.Equal(t, http.StatusAccepted, w1.Code)
		assert.Equal(t, http.StatusOK, w2.Code, "the duplicate must return 200 (acknowledged) without enqueueing")
		assert.Contains(t, w2.Body.String(), "duplicate")
		assert.Len(t, sub.Jobs(), 1, "exactly one job survives the dedup gate")
	})

	t.Run("should let a webhook retry through after Submit fails (rollback contract)", func(t *testing.T) {
		// given: a submitter wired to fail the first call, succeed
		// the second. Without the rollback in `dedupForget`, the
		// retry inside the TTL would be silently dropped because
		// the cache would still say "duplicate". Pinned per Copilot
		// review on PR #100 thread `PRRT_kwDOJKAEo85-5zE-`.
		d, sub := newDispatcherWithGitHubTokenizer(t, defaultGitHubSettings())
		// Wire the dispatcher to a fresh stub that fails the first
		// Submit; we override the default stub here so the saturation
		// behaviour is local to this row.
		failing := doubles.NewStubWebhookSubmitter().WithError(errSubmitterFull)
		d.SetSubmitter(failing)
		req1 := githubRequest(t, ghSecret, ghOpenedPayload, "pull_request")
		w1 := httptest.NewRecorder()

		// when (1): first delivery fails at Submit
		d.HandleGitHub(w1, req1)

		// then (1)
		require.Equal(t, http.StatusServiceUnavailable, w1.Code)
		require.Empty(t, failing.Jobs(), "Submit failed so no job is captured")

		// when (2): retry now hits a healthy submitter — must be allowed through
		d.SetSubmitter(sub)
		req2 := githubRequest(t, ghSecret, ghOpenedPayload, "pull_request")
		w2 := httptest.NewRecorder()
		d.HandleGitHub(w2, req2)

		// then (2)
		assert.Equal(t, http.StatusAccepted, w2.Code, "retry within TTL must NOT be dropped just because the previous attempt was recorded")
		assert.Len(t, sub.Jobs(), 1, "the retry reaches the worker queue")
	})
}
