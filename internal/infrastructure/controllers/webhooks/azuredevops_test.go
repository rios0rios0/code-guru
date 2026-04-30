//go:build unit

package webhooks_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
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
	doubles "github.com/rios0rios0/codeguru/test/infrastructure/doubles/repositories"
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

const adoRepoUUID = "11111111-2222-3333-4444-555555555555"

// adoActivePRPayload returns the canonical ADO `git.pullrequest.created`
// payload used across the test suite. Built via Sprintf so adoRepoUUID
// stays the single source of truth for the repository UUID — both the
// JSON body and any assertions compare against the same constant.
func adoActivePRPayload() string {
	return adoPRPayload("git.pullrequest.created", "active")
}

// adoPRPayload renders the canonical ADO PR payload with caller-supplied
// `eventType` and `resource.status`. Used by the closed-status and
// empty-status test cases so the body stays a faithful clone of a real
// delivery (title / url / refs all populated) instead of a hand-trimmed
// minimal blob — the realistic shape catches downstream parsing issues
// the minimal version would silently miss.
func adoPRPayload(eventType, status string) string {
	return fmt.Sprintf(`{
  "eventType": %q,
  "resource": {
    "pullRequestId": 42,
    "status": %q,
    "title": "Add feature X",
    "url": "https://dev.azure.com/ZestSecurity/Platform/_git/demo-repo/pullrequest/42",
    "sourceRefName": "refs/heads/feat/x",
    "targetRefName": "refs/heads/main",
    "repository": {
      "id": %q,
      "name": "demo-repo",
      "remoteUrl": "https://dev.azure.com/ZestSecurity/Platform/_git/demo-repo",
      "project": {"name": "Platform"}
    }
  }
}`, eventType, status, adoRepoUUID)
}

func TestHandleAzureDevOps(t *testing.T) {
	t.Parallel()

	t.Run("should respond 202 (Accepted) when an active PR is enqueued", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload()))
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
		assert.Equal(t, adoRepoUUID, jobs[0].Repo.ID,
			"Repo.ID must be populated from resource.repository.id so the gitforge ADO provider can use the UUID instead of falling back to the repo name")
		assert.Equal(t, adoProjectName, jobs[0].Repo.Project)
		assert.Equal(t, adoOrgSlug, jobs[0].Repo.Organization)
		assert.False(t, jobs[0].CIPassed)
	})

	t.Run("should respond 401 (Unauthorized) when basic auth is wrong", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload()))
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
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload()))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("should respond 204 (No Content) when the PR is abandoned", func(t *testing.T) {
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

	t.Run("should respond 204 (No Content) when the PR is completed", func(t *testing.T) {
		// given: `completed` is the second known closed value alongside
		// `abandoned`. Anything else proceeds — see the empty-status case
		// below.
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		payload := adoPRPayload("git.pullrequest.updated", "completed")
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(payload))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 204 (No Content) for closed status with mixed case and surrounding whitespace", func(t *testing.T) {
		// given: the `isClosedADOPullRequestStatus` predicate normalises
		// via `strings.TrimSpace` + `strings.ToLower`, so a payload that
		// ships ` Completed ` (mixed case + leading/trailing whitespace)
		// must still short-circuit. Without this test the case- and
		// whitespace-tolerance lives in the predicate but is unverified at
		// the handler boundary, leaving room for a future "fix" to drop
		// the normalisation and silently re-introduce the original bug.
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		payload := adoPRPayload("git.pullrequest.updated", " Completed ")
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(payload))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 202 (Accepted) when status is empty", func(t *testing.T) {
		// given: ADO's `git.pullrequest.updated` payload is observed in
		// the wild to ship `resource.status: ""` on commit-only updates —
		// captured live on Zest-Terraform PR #12029 where every push was
		// silently 204'd. The handler's old strict-active check rejected
		// every such delivery; the new check only rejects KNOWN closed
		// states, so an empty (or any unknown) status proceeds and the
		// PR is enqueued — with the canonical payload shape so the test
		// reflects a real delivery (title / url / refs / repo UUID all
		// populated) rather than a trimmed minimal blob.
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		payload := adoPRPayload("git.pullrequest.updated", "")
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(payload))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusAccepted, w.Code)
		jobs := sub.Jobs()
		require.Len(t, jobs, 1)
		assert.Equal(t, 42, jobs[0].PR.ID, "PR ID must round-trip from resource.pullRequestId")
		assert.Empty(t, jobs[0].PR.Status, "PR.Status must propagate the original empty value so downstream consumers see what ADO actually sent")
		assert.Equal(t, adoRepoUUID, jobs[0].Repo.ID, "Repo.ID must be populated from resource.repository.id even when status is empty")
		assert.Equal(t, adoRepoName, jobs[0].Repo.Name)
		assert.Equal(t, adoProjectName, jobs[0].Repo.Project)
		assert.Equal(t, adoOrgSlug, jobs[0].Repo.Organization)
		assert.Equal(t, "feat/x", jobs[0].PR.SourceBranch, "SourceBranch must be parsed (refs/heads/ stripped) regardless of status")
		assert.Equal(t, "main", jobs[0].PR.TargetBranch)
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
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload()))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 403 (Forbidden) when CF-Connecting-IP is outside AllowedSourceCIDRs", func(t *testing.T) {
		// given: a settings with a strict allowlist that excludes 8.8.8.8
		settings := defaultADOSettings()
		settings.Server.AllowedSourceCIDRs = []string{"13.107.6.0/24", "13.107.9.0/24"}
		d, sub := newDispatcherWithSettings(t, settings)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload()))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		req.Header.Set("CF-Connecting-IP", "8.8.8.8")
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then: rejected before basic-auth even runs, so the queue stays empty
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 202 (Accepted) when CF-Connecting-IP is inside AllowedSourceCIDRs", func(t *testing.T) {
		// given
		settings := defaultADOSettings()
		settings.Server.AllowedSourceCIDRs = []string{"13.107.6.0/24"}
		d, sub := newDispatcherWithSettings(t, settings)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload()))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		req.Header.Set("CF-Connecting-IP", "13.107.6.42")
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Len(t, sub.Jobs(), 1)
	})

	t.Run("should accept any source IP when AllowedSourceCIDRs is empty (default)", func(t *testing.T) {
		// given: defaultADOSettings() does not set AllowedSourceCIDRs, so the
		// list is nil — the allowlist is intentionally permissive when no
		// CIDRs are configured.
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload()))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		req.Header.Set("CF-Connecting-IP", "8.8.8.8")
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Len(t, sub.Jobs(), 1)
	})

	t.Run("should fall back to X-Forwarded-For when CF-Connecting-IP is absent", func(t *testing.T) {
		// given: only the leftmost XFF entry is the original client; the
		// second entry is a proxy hop that should NOT be used for matching.
		settings := defaultADOSettings()
		settings.Server.AllowedSourceCIDRs = []string{"13.107.6.0/24"}
		d, sub := newDispatcherWithSettings(t, settings)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload()))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		req.Header.Set("X-Forwarded-For", "13.107.6.42, 10.0.0.1, 172.16.90.7")
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Len(t, sub.Jobs(), 1)
	})
}
