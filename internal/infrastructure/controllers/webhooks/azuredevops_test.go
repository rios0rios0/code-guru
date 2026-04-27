//go:build unit

package webhooks_test

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	configEntities "github.com/rios0rios0/gitforge/pkg/config/domain/entities"
	"github.com/rios0rios0/gitforge/pkg/providers/infrastructure/azuredevops"
	"github.com/rios0rios0/gitforge/pkg/providers/infrastructure/github"
	registry "github.com/rios0rios0/gitforge/pkg/registry/infrastructure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	infraRepos "github.com/rios0rios0/codeguru/internal/infrastructure/repositories"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
	doubles "github.com/rios0rios0/codeguru/test/domain/doubles/repositories"
)

const (
	adoSecret      = "ado-test-secret"
	adoOrgSlug     = "ZestSecurity"
	adoProjectName = "Platform"
	adoRepoName    = "demo-repo"
)

func newTestRegistry() *registry.ProviderRegistry {
	r := registry.NewProviderRegistry()
	r.RegisterFactory("github", github.NewProvider)
	r.RegisterFactory("azuredevops", azuredevops.NewProvider)
	return r
}

func newDispatcherWithSettings(t *testing.T, settings *entities.Settings) (*webhooks.Dispatcher, *doubles.StubWebhookSubmitter) {
	t.Helper()
	d := webhooks.NewDispatcher(
		infraRepos.NewAIReviewerFactory(),
		infraRepos.NewRulesRepositoryFactory(),
		nil,
		settings,
		newTestRegistry(),
	)
	sub := doubles.NewStubWebhookSubmitter()
	d.SetSubmitter(sub)
	return d, sub
}

func defaultADOSettings() *entities.Settings {
	return &entities.Settings{
		Providers: []configEntities.ProviderConfig{
			{Type: "azuredevops", Token: "ado-pat-test"},
		},
		Server: entities.ServerConfig{
			WebhookSecret:        adoSecret,
			AllowedOrganizations: []string{adoOrgSlug},
			AllowedProjects:      []string{adoProjectName},
		},
	}
}

func adoBasicAuth(secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(webhooks.BasicAuthUsername+":"+secret))
}

const adoActivePRPayload = `{
  "eventType": "git.pullrequest.created",
  "resource": {
    "pullRequestId": 42,
    "status": "active",
    "title": "Add feature X",
    "url": "https://dev.azure.com/ZestSecurity/Platform/_git/demo-repo/pullrequest/42",
    "sourceRefName": "refs/heads/feat/x",
    "targetRefName": "refs/heads/main",
    "repository": {
      "name": "demo-repo",
      "remoteUrl": "https://dev.azure.com/ZestSecurity/Platform/_git/demo-repo",
      "project": {"name": "Platform"}
    }
  }
}`

func TestHandleAzureDevOps(t *testing.T) {
	t.Parallel()

	t.Run("should respond 202 (Accepted) when an active PR is enqueued", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusAccepted, w.Code)
		jobs := sub.Jobs()
		require.Len(t, jobs, 1)
		assert.Equal(t, 42, jobs[0].PR.ID)
		assert.Equal(t, adoRepoName, jobs[0].Repo.Name)
		assert.Equal(t, adoProjectName, jobs[0].Repo.Project)
		assert.Equal(t, adoOrgSlug, jobs[0].Repo.Organization)
		assert.False(t, jobs[0].CIPassed)
	})

	t.Run("should respond 401 (Unauthorized) when basic auth is wrong", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload))
		req.Header.Set("Authorization", adoBasicAuth("wrong"))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 400 (Bad Request) when the auth header is missing", func(t *testing.T) {
		// given
		d, _ := newDispatcherWithSettings(t, defaultADOSettings())
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("should respond 204 (No Content) when the PR is not active", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		payload := `{"eventType":"git.pullrequest.updated","resource":{"pullRequestId":1,"status":"abandoned","repository":{"name":"r","remoteUrl":"https://dev.azure.com/ZestSecurity/Platform/_git/r","project":{"name":"Platform"}}}}`
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(payload))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 204 (No Content) when the event is unsupported", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		payload := `{"eventType":"git.push","resource":{}}`
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(payload))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 400 (Bad Request) when the JSON is malformed", func(t *testing.T) {
		// given
		d, _ := newDispatcherWithSettings(t, defaultADOSettings())
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(`{not json`))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("should respond 403 (Forbidden) when the project is not on the allowlist", func(t *testing.T) {
		// given
		settings := defaultADOSettings()
		settings.Server.AllowedProjects = []string{"OtherProject"}
		d, sub := newDispatcherWithSettings(t, settings)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Empty(t, sub.Jobs())
	})
}
