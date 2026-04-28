package webhooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
	logger "github.com/sirupsen/logrus"
)

// adoEvent is the minimal subset of an Azure DevOps Service Hook payload that
// HandleAzureDevOps needs. Unknown fields are ignored.
type adoEvent struct {
	EventType string      `json:"eventType"`
	Resource  adoResource `json:"resource"`
}

type adoResource struct {
	PullRequestID int           `json:"pullRequestId"`
	Status        string        `json:"status"`
	Title         string        `json:"title"`
	URL           string        `json:"url"`
	SourceRefName string        `json:"sourceRefName"`
	TargetRefName string        `json:"targetRefName"`
	Repository    adoRepository `json:"repository"`
}

type adoRepository struct {
	Name      string     `json:"name"`
	RemoteURL string     `json:"remoteUrl"`
	Project   adoProject `json:"project"`
}

type adoProject struct {
	Name string `json:"name"`
}

// HandleAzureDevOps processes Azure DevOps Service Hook events.
//
// Auth: Basic. ADO does not support HMAC signing on Service Hooks.
// Supported events: git.pullrequest.created and git.pullrequest.updated for
// active PRs. All other events return 204 No Content.
func (d *Dispatcher) HandleAzureDevOps(w http.ResponseWriter, r *http.Request) {
	if authErr := VerifyBasicAuth(d.settings.Server.WebhookSecret, r.Header.Get("Authorization")); authErr != nil {
		logger.Warnf("ADO webhook rejected: %v", authErr)
		status := http.StatusUnauthorized
		if errors.Is(authErr, ErrMissingHeader) {
			status = http.StatusBadRequest
		}
		writeError(w, status, "unauthorized")
		return
	}

	defer func() { _ = r.Body.Close() }()
	body, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	var event adoEvent
	if jsonErr := json.Unmarshal(body, &event); jsonErr != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}

	if !isSupportedADOEvent(event.EventType) {
		logger.Debugf("ADO webhook: ignoring event type %q", event.EventType)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !strings.EqualFold(event.Resource.Status, "active") {
		logger.Debugf(
			"ADO webhook: PR #%d status %q is not active",
			event.Resource.PullRequestID,
			event.Resource.Status,
		)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	org := extractADOOrganization(event.Resource.Repository.RemoteURL)
	if !d.allowedOrganization(org) || !d.allowedProject(event.Resource.Repository.Project.Name) {
		logger.Warnf("ADO webhook: org=%q project=%q not on allowlist", org, event.Resource.Repository.Project.Name)
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	token := d.findToken("azuredevops")
	if token == "" {
		logger.Errorf("ADO webhook: no azuredevops PAT configured")
		writeError(w, http.StatusInternalServerError, "no PAT configured")
		return
	}

	provider, providerErr := d.providerRegistry.GetReviewProvider("azuredevops", token)
	if providerErr != nil {
		logger.Errorf("ADO webhook: failed to build provider: %v", providerErr)
		writeError(w, http.StatusInternalServerError, "provider error")
		return
	}

	repo := forgeEntities.Repository{
		Name:         event.Resource.Repository.Name,
		Organization: org,
		Project:      event.Resource.Repository.Project.Name,
		RemoteURL:    event.Resource.Repository.RemoteURL,
	}
	pr := forgeEntities.PullRequestDetail{
		PullRequest: forgeEntities.PullRequest{
			ID:     event.Resource.PullRequestID,
			Title:  event.Resource.Title,
			URL:    event.Resource.URL,
			Status: event.Resource.Status,
		},
		SourceBranch: refToBranch(event.Resource.SourceRefName),
		TargetBranch: refToBranch(event.Resource.TargetRefName),
	}

	if submitErr := d.submitter.Submit(Job{Provider: provider, Repo: repo, PR: pr, CIPassed: false}); submitErr != nil {
		logger.Errorf("ADO webhook: submit failed: %v", submitErr)
		writeError(w, http.StatusServiceUnavailable, "queue full")
		return
	}

	logger.Infof("ADO webhook: enqueued PR #%d in %s/%s", pr.ID, repo.Project, repo.Name)
	w.WriteHeader(http.StatusAccepted)
	_, _ = fmt.Fprint(w, "accepted")
}

// supportedADOEvents lists the event types HandleAzureDevOps acts on. Defined as a
// map (Mapper pattern) to make extension trivial.
//
//nolint:gochecknoglobals // read-only lookup table used as a constant
var supportedADOEvents = map[string]struct{}{
	"git.pullrequest.created": {},
	"git.pullrequest.updated": {},
}

func isSupportedADOEvent(eventType string) bool {
	_, ok := supportedADOEvents[eventType]
	return ok
}

// extractADOOrganization parses the org slug out of an ADO remote URL of the form
// https://dev.azure.com/{org}/{project}/_git/{repo} or
// https://{org}.visualstudio.com/{project}/_git/{repo}.
func extractADOOrganization(remoteURL string) string {
	if remoteURL == "" {
		return ""
	}
	u, err := url.Parse(remoteURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "dev.azure.com" {
		segments := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		if len(segments) > 0 {
			return segments[0]
		}
		return ""
	}
	if before, ok := strings.CutSuffix(host, ".visualstudio.com"); ok {
		return before
	}
	return ""
}

// refToBranch trims the refs/heads/ prefix from a Git ref name.
func refToBranch(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}
