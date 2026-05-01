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

	"github.com/rios0rios0/codeguru/internal/support"
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
	ID        string     `json:"id"`
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
//
// The handler length is driven by the number of validation guard clauses it
// has to enforce in order before touching the worker queue. Splitting further
// would scatter the request flow across multiple methods without removing any
// branches.
//
//nolint:funlen // Single-shot HTTP handler whose length is proportional to its required validation flow.
func (d *Dispatcher) HandleAzureDevOps(w http.ResponseWriter, r *http.Request) {
	if !d.enforceSourceIPAllowlist(w, r, "ADO") {
		return
	}

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
	if isClosedADOPullRequestStatus(event.Resource.Status) {
		logger.Debugf(
			"ADO webhook: PR #%d status %q is closed",
			event.Resource.PullRequestID,
			event.Resource.Status,
		)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !d.hydrateSkinnyADOResource(w, r, &event) {
		return
	}

	org := extractADOOrganization(event.Resource.Repository.RemoteURL)
	if !d.allowedOrganization(org) || !d.allowedProject(event.Resource.Repository.Project.Name) {
		// On allowlist rejection, dump the parsed shape AND a head of
		// the raw body at `Warn`. The diagnostic is intentionally NOT
		// gated behind `Debug` because the operational reality is that
		// rejection is rare (the org/project allowlist is small and
		// stable) but when it fires it always means something is
		// genuinely wrong with the wire payload — the management API
		// shows fully populated `resource.repository` blocks while the
		// pod sees them empty, and a `kubectl set env DEBUG=true` patch
		// keeps getting reverted by `terra apply` runs that race with
		// the diagnosis loop. Surfacing at `Warn` (the same level as
		// the existing `not on allowlist` line) makes the diagnostic
		// survive whatever the pod's log level happens to be set to.
		//
		// Body cap is `adoRawBodyLogLimit` (32 KB) to cover the typical
		// `git.pullrequest.*` envelope including the verbose
		// `message` / `detailedMessage` blocks plus the `resource`
		// block that sits after them — at 4 KB the cut landed right
		// before `resource`, which was the entire diagnostic of value.
		// 32 KB is still a constant per request and still eligible for
		// the `truncationSentinel` tail.
		logger.WithFields(logger.Fields{
			"event_type":   event.EventType,
			"pull_id":      event.Resource.PullRequestID,
			"status":       event.Resource.Status,
			"repo_id":      event.Resource.Repository.ID,
			"repo_name":    event.Resource.Repository.Name,
			"remote_url":   event.Resource.Repository.RemoteURL,
			"project_name": event.Resource.Repository.Project.Name,
			"body_length":  len(body),
			"body_head":    support.TruncateBytesForLog(body, adoRawBodyLogLimit),
			"parsed_org":   org,
		}).Warnf("ADO webhook: org=%q project=%q not on allowlist", org, event.Resource.Repository.Project.Name)
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
		ID:           event.Resource.Repository.ID,
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

// closedADOPullRequestStatuses lists the values of `resource.status` that
// represent a Pull Request the bot must NOT review. The check is
// allow-list-by-rejection: anything not in this set (including the empty
// string and any future enum value Azure DevOps may add) proceeds. ADO's
// `git.pullrequest.created` / `git.pullrequest.updated` events are
// observed in the wild to ship `resource.status: ""` on certain
// commit-only updates — particularly on Zest-Terraform PR #12029 captured
// in the diagnosis log — and an empty status used to short-circuit the
// handler with a 204, dropping every push silently. The new shape only
// rejects PRs that are explicitly closed.
//
//nolint:gochecknoglobals // read-only lookup table used as a constant
var closedADOPullRequestStatuses = map[string]struct{}{
	"abandoned": {},
	"completed": {},
}

func isClosedADOPullRequestStatus(status string) bool {
	_, ok := closedADOPullRequestStatuses[strings.ToLower(strings.TrimSpace(status))]
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

// hydrateSkinnyADOResource replaces a stripped-down org-wide-subscription
// `resource` block with the full envelope fetched from the ADO REST API.
// Returns true when the caller should keep going, false when a response
// has already been written and processing must stop.
//
// The function is split out of HandleAzureDevOps to keep that handler's
// cognitive complexity within the linter's 20-branch budget — the
// hydration step is a self-contained pre-filter and tests cover it
// directly via the hydrator unit tests.
func (d *Dispatcher) hydrateSkinnyADOResource(
	w http.ResponseWriter,
	r *http.Request,
	event *adoEvent,
) bool {
	if !isSkinnyADOResource(event.Resource) {
		logger.Debugf(
			"ADO webhook: PR #%d arrived with full resource block (project-scoped subscription) — skipping hydration",
			event.Resource.PullRequestID,
		)
		return true
	}

	logger.Debugf(
		"ADO webhook: PR #%d arrived with skinny resource block (org-wide subscription) — hydrating via REST API",
		event.Resource.PullRequestID,
	)

	token := d.findToken("azuredevops")
	if token == "" {
		logger.Errorf("ADO webhook: skinny payload but no azuredevops PAT configured")
		writeError(w, http.StatusInternalServerError, "no PAT configured")
		return false
	}
	if d.adoHydrator == nil {
		logger.Errorf("ADO webhook: skinny payload but no hydrator wired")
		writeError(w, http.StatusInternalServerError, "hydrator not wired")
		return false
	}

	hydrated, err := d.adoHydrator.Hydrate(r.Context(), event.Resource.URL, token)
	if err != nil {
		logger.Warnf(
			"ADO webhook: hydrate PR #%d via %q failed: %v",
			event.Resource.PullRequestID,
			event.Resource.URL,
			err,
		)
		writeError(w, http.StatusBadGateway, "hydration failed")
		return false
	}
	event.Resource = mergeHydratedADOResource(event.Resource, hydrated)

	// Re-apply the closed-status guard against the hydrated value: a
	// `git.pullrequest.updated` for an `abandoned` PR carries an empty
	// `status` in the skinny shape, so the earlier check let it through.
	if isClosedADOPullRequestStatus(event.Resource.Status) {
		logger.Debugf(
			"ADO webhook: PR #%d hydrated to status %q (closed)",
			event.Resource.PullRequestID,
			event.Resource.Status,
		)
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	return true
}

// adoRawBodyLogLimit caps the number of body bytes echoed at `Warn` on the
// allowlist-rejection diagnostic. 32 KB covers the canonical ADO payload
// envelope including the `message` / `detailedMessage` blocks AND the
// `resource` block that sits after them — at the previous 4 KB cap the
// cut landed right before `resource`, which was the entire diagnostic of
// value. The `Warn`-level emission is intentionally not gated behind
// `Debug` because the rejection itself is already a rare, operator-level
// signal and the body cap keeps the per-request cost bounded.
const adoRawBodyLogLimit = 32768
