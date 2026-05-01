//go:build unit

package webhooks_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// stubADOHydrator is a hand-rolled ADOResourceHydrator that records every
// invocation and returns either a pre-configured resource or a sticky
// error. Lives in this _test file (rather than `test/infrastructure/...`)
// because the canonical ADOResource alias only exists under the `unit`
// build tag — keeping the stub local avoids leaking that build-tag-gated
// alias into shared helper packages.
type stubADOHydrator struct {
	calls    atomic.Int32
	lastURL  atomic.Value // string
	lastTok  atomic.Value // string
	resource webhooks.ADOResource
	err      error
}

func newStubADOHydrator(resource webhooks.ADOResource) *stubADOHydrator {
	return &stubADOHydrator{resource: resource}
}

func (s *stubADOHydrator) WithError(err error) *stubADOHydrator {
	s.err = err
	return s
}

func (s *stubADOHydrator) Hydrate(_ context.Context, resourceURL, token string) (webhooks.ADOResource, error) {
	s.calls.Add(1)
	s.lastURL.Store(resourceURL)
	s.lastTok.Store(token)
	if s.err != nil {
		return webhooks.ADOResource{}, s.err
	}
	return s.resource, nil
}

func (s *stubADOHydrator) Calls() int32 { return s.calls.Load() }
func (s *stubADOHydrator) LastURL() string {
	if v := s.lastURL.Load(); v != nil {
		return v.(string)
	}
	return ""
}
func (s *stubADOHydrator) LastToken() string {
	if v := s.lastTok.Load(); v != nil {
		return v.(string)
	}
	return ""
}

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

// adoSkinnyPRPayload returns the minimal `git.pullrequest.*` payload that
// ADO **org-wide** subscriptions emit. Captured live against subscriptions
// `fea3e13f-…` and `564b23d9-…`; reproducing it here lets handler tests
// drive the hydration code path with a single source of truth.
func adoSkinnyPRPayload(eventType string, prID int) string {
	return fmt.Sprintf(`{
  "subscriptionId": "fea3e13f-f2d3-4e11-9cfd-8baefb30f8fe",
  "notificationId": 1,
  "id": "00000000-0000-0000-0000-000000000000",
  "eventType": %q,
  "publisherId": "tfs",
  "message": {"text": "Felipe Rios created pull request 12096"},
  "detailedMessage": {"text": "..."},
  "resource": {
    "url": "https://dev.azure.com/ZestSecurity/fb485dce-2f5b-4b1b-b45a-60ff2c1295c1/_apis/git/repositories/e3555597-e951-4908-b6d9-ff28ed2ae953/pullRequests/%d",
    "pullRequestId": %d
  }
}`, eventType, prID, prID)
}

// hydratedFullResource returns the canonical full ADO resource a stub
// hydrator hands back for a skinny incoming payload. Mirrors the shape
// `adoPRPayload` produces inline.
func hydratedFullResource() webhooks.ADOResource {
	return webhooks.ADOResource{
		PullRequestID: 12096,
		Status:        "active",
		Title:         "smoke",
		URL:           "https://dev.azure.com/ZestSecurity/Platform/_git/demo-repo/pullrequest/12096",
		SourceRefName: "refs/heads/feat/x",
		TargetRefName: "refs/heads/main",
		Repository: webhooks.ADORepository{
			ID:        adoRepoUUID,
			Name:      adoRepoName,
			RemoteURL: "https://dev.azure.com/ZestSecurity/Platform/_git/demo-repo",
			Project:   webhooks.ADOProject{Name: adoProjectName},
		},
	}
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

	// --- skinny payload (org-wide subscription) integration ---
	//
	// These rows pin the contract that the bot accepts BOTH wire shapes
	// ADO emits in production: the full project-scoped envelope (covered
	// above) AND the stripped-down org-wide envelope hydrated through
	// the REST API. Each row asserts: (a) the response code, (b) whether
	// the worker queue saw a job, (c) whether the hydrator was called,
	// and (d) that the project-scoped path bypasses the hydrator
	// entirely — that last assertion is what guarantees we don't
	// silently start hammering the ADO API for every delivery.

	t.Run("should respond 202 (Accepted) when a skinny org-wide payload is hydrated to an active PR", func(t *testing.T) {
		// given
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		hydrator := newStubADOHydrator(hydratedFullResource())
		d.SetADOHydrator(hydrator)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops",
			bytes.NewBufferString(adoSkinnyPRPayload("git.pullrequest.created", 12096)))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusAccepted, w.Code)
		require.Equal(t, int32(1), hydrator.Calls(),
			"hydrator must be invoked exactly once for a skinny payload")
		assert.Contains(t, hydrator.LastURL(), "/pullRequests/12096",
			"hydrator must receive the resource.url straight from the wire payload")
		assert.Equal(t, "ado-pat-test", hydrator.LastToken(),
			"hydrator must receive the configured azuredevops PAT")
		jobs := sub.Jobs()
		require.Len(t, jobs, 1)
		assert.Equal(t, 12096, jobs[0].PR.ID)
		assert.Equal(t, adoRepoUUID, jobs[0].Repo.ID,
			"after hydration the worker job must carry the canonical repository UUID")
		assert.Equal(t, adoProjectName, jobs[0].Repo.Project)
		assert.Equal(t, adoOrgSlug, jobs[0].Repo.Organization)
		assert.Equal(t, "feat/x", jobs[0].PR.SourceBranch)
		assert.Equal(t, "main", jobs[0].PR.TargetBranch)
	})

	t.Run("should NOT call the hydrator when the payload already carries a full resource block", func(t *testing.T) {
		// given: counter assertion to prevent a future "always hydrate"
		// regression — every project-scoped delivery would otherwise turn
		// into an avoidable API hop and amplify our PAT rate-limit cost.
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		hydrator := newStubADOHydrator(hydratedFullResource())
		d.SetADOHydrator(hydrator)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops",
			bytes.NewBufferString(adoActivePRPayload()))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, int32(0), hydrator.Calls(),
			"full payload must short-circuit before any API call")
		assert.Len(t, sub.Jobs(), 1)
	})

	t.Run("should respond 502 (Bad Gateway) when the hydrator surfaces an upstream error", func(t *testing.T) {
		// given: a hydrator that returns an error simulates an ADO API
		// outage, a revoked PAT, or a 4xx for a now-deleted PR — all
		// situations the bot must NOT confuse with a malformed payload.
		// 502 (rather than 500) tells ADO that the upstream is at fault,
		// which keeps the subscription on the right side of the
		// circuit-breaker for transient errors.
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		hydrator := newStubADOHydrator(webhooks.ADOResource{}).WithError(errors.New("hydration GET returned 503 Service Unavailable"))
		d.SetADOHydrator(hydrator)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops",
			bytes.NewBufferString(adoSkinnyPRPayload("git.pullrequest.updated", 7777)))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusBadGateway, w.Code)
		assert.Equal(t, int32(1), hydrator.Calls())
		assert.Empty(t, sub.Jobs(), "no job should be enqueued on a hydration failure")
	})

	t.Run("should respond 204 (No Content) when the hydrated payload reports a closed PR", func(t *testing.T) {
		// given: a `git.pullrequest.updated` for an `abandoned` PR carries
		// an empty `status` in the skinny shape, so the early closed-status
		// guard let it through. The post-hydration re-check is what catches
		// it before the worker queue. Pinning this scenario stops a future
		// "let me unify the closed-status check" refactor from removing the
		// second pass.
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		hydrated := hydratedFullResource()
		hydrated.Status = "abandoned"
		hydrator := newStubADOHydrator(hydrated)
		d.SetADOHydrator(hydrator)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops",
			bytes.NewBufferString(adoSkinnyPRPayload("git.pullrequest.updated", 12096)))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, int32(1), hydrator.Calls(),
			"hydrator still runs because the skinny payload's status is empty")
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 500 when a skinny payload arrives but no PAT is configured", func(t *testing.T) {
		// given: a defensive 500 (rather than 503) because this is a
		// configuration error on our side — neither retry nor probation
		// would help, the operator has to fix the settings.
		settings := defaultADOSettings()
		settings.Providers = nil
		d, sub := newDispatcherWithSettings(t, settings)
		hydrator := newStubADOHydrator(hydratedFullResource())
		d.SetADOHydrator(hydrator)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops",
			bytes.NewBufferString(adoSkinnyPRPayload("git.pullrequest.created", 12096)))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, int32(0), hydrator.Calls(),
			"hydrator must NOT be called when there is no PAT to authenticate with")
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 403 (Forbidden) when a hydrated payload reveals a project not on the allowlist", func(t *testing.T) {
		// given: the org-wide subscription delivers events for projects we
		// do not want to review. After hydration, the regular allowlist
		// check applies to the canonical fields and rejects the delivery
		// — the bot must not confuse "off-list project" with "broken
		// payload" (the 403 keeps the subscription happy because it stays
		// well below the consecutive-4xx probation threshold for legitimate
		// allowlist filtering).
		settings := defaultADOSettings()
		settings.Server.AllowedProjects = []string{"OnlyThisProject"}
		d, sub := newDispatcherWithSettings(t, settings)
		hydrator := newStubADOHydrator(hydratedFullResource())
		d.SetADOHydrator(hydrator)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops",
			bytes.NewBufferString(adoSkinnyPRPayload("git.pullrequest.created", 12096)))
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, int32(1), hydrator.Calls(),
			"hydration runs first; the allowlist check then trims the delivery")
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should short-circuit a duplicate webhook delivery without enqueueing a second job", func(t *testing.T) {
		// given: simulating ADO's `pullrequest.created` +
		// `pullrequest.updated` double-fire — both events for the
		// same PR within seconds, both routed to the same pod. The
		// dedup cache must accept the first and refuse the second.
		// Pinned per the duplicate-comment incident on
		// `Zest-Terraform/pipelines#12101` on `2026-05-01`.
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		req1 := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload()))
		req1.Header.Set("Authorization", adoBasicAuth(adoSecret))
		req2 := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload()))
		req2.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w1 := httptest.NewRecorder()
		w2 := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w1, req1)
		d.HandleAzureDevOps(w2, req2)

		// then
		assert.Equal(t, http.StatusAccepted, w1.Code, "the first delivery enqueues normally")
		assert.Equal(t, http.StatusOK, w2.Code, "the duplicate returns 200 (acknowledged) without enqueueing")
		assert.Contains(t, w2.Body.String(), "duplicate")
		assert.Len(t, sub.Jobs(), 1, "exactly one job survives the dedup gate")
	})

	t.Run("should NOT short-circuit two distinct PRs that arrive in quick succession", func(t *testing.T) {
		// given: defensive — the dedup key is `(provider, repo_id, pr_id)`,
		// so a real second PR with a different `pullRequestId` must
		// always pass through. Without this row a future "let me
		// widen the dedup key" refactor would silently swallow real
		// traffic.
		d, sub := newDispatcherWithSettings(t, defaultADOSettings())
		req1 := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(adoActivePRPayload()))
		req1.Header.Set("Authorization", adoBasicAuth(adoSecret))
		// distinct PR ID = 43
		payload2 := strings.Replace(adoActivePRPayload(), `"pullRequestId": 42`, `"pullRequestId": 43`, 1)
		req2 := httptest.NewRequest(http.MethodPost, "/webhooks/azuredevops", bytes.NewBufferString(payload2))
		req2.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w1 := httptest.NewRecorder()
		w2 := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w1, req1)
		d.HandleAzureDevOps(w2, req2)

		// then
		assert.Equal(t, http.StatusAccepted, w1.Code)
		assert.Equal(t, http.StatusAccepted, w2.Code, "PR #43 is a different key — must be enqueued")
		assert.Len(t, sub.Jobs(), 2, "both distinct PRs reach the worker queue")
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
