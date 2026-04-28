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
	doubles "github.com/rios0rios0/codeguru/test/domain/doubles/repositories"
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
}
