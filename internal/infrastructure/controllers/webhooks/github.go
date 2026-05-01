package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
	logger "github.com/sirupsen/logrus"
)

// ghEvent is the minimal subset of a GitHub pull_request webhook payload that
// HandleGitHub needs.
type ghEvent struct {
	Action       string         `json:"action"`
	PullRequest  ghPullRequest  `json:"pull_request"`
	Repository   ghRepository   `json:"repository"`
	Installation ghInstallation `json:"installation"`
}

type ghPullRequest struct {
	Number  int      `json:"number"`
	Title   string   `json:"title"`
	HTMLURL string   `json:"html_url"`
	State   string   `json:"state"`
	Head    ghBranch `json:"head"`
	Base    ghBranch `json:"base"`
	User    ghUser   `json:"user"`
}

type ghBranch struct {
	Ref string `json:"ref"`
}

type ghUser struct {
	Login string `json:"login"`
}

type ghRepository struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
	Owner    ghUser `json:"owner"`
}

type ghInstallation struct {
	ID int64 `json:"id"`
}

// supportedGitHubActions lists the pull_request actions HandleGitHub acts on.
//
//nolint:gochecknoglobals // read-only lookup table used as a constant
var supportedGitHubActions = map[string]struct{}{
	"opened":      {},
	"synchronize": {},
	"reopened":    {},
}

const fullNameSegments = 2

// HandleGitHub processes GitHub App webhook events.
//
// Auth: HMAC-SHA256 via the X-Hub-Signature-256 header validated against
// Settings.Server.WebhookSecret. Supported events: pull_request with action
// in {opened, synchronize, reopened}.
//
//nolint:funlen // Single-shot HTTP handler whose length is proportional to its required validation flow.
func (d *Dispatcher) HandleGitHub(w http.ResponseWriter, r *http.Request) {
	if !d.enforceSourceIPAllowlist(w, r, "GitHub") {
		return
	}

	defer func() { _ = r.Body.Close() }()
	body, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	if authErr := VerifyHMACSHA256(
		d.settings.Server.WebhookSecret,
		body,
		r.Header.Get("X-Hub-Signature-256"),
	); authErr != nil {
		logger.Warnf("GitHub webhook rejected: %v", authErr)
		status := http.StatusUnauthorized
		if errors.Is(authErr, ErrMissingHeader) {
			status = http.StatusBadRequest
		}
		writeError(w, status, "unauthorized")
		return
	}

	if eventType := r.Header.Get("X-Github-Event"); eventType != "pull_request" {
		logger.Debugf("GitHub webhook: ignoring event %q", eventType)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	event, ok := parseGitHubEvent(w, body)
	if !ok {
		return
	}

	if _, supported := supportedGitHubActions[event.Action]; !supported {
		logger.Debugf("GitHub webhook: ignoring action %q", event.Action)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	owner, repoName, splitErr := splitFullName(event.Repository.FullName)
	if splitErr != nil {
		writeError(w, http.StatusBadRequest, "invalid repository.full_name")
		return
	}
	if !d.allowedOrganization(owner) {
		logger.Warnf("GitHub webhook: org %q not on allowlist", owner)
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	provider, ok := d.buildGitHubProvider(r.Context(), w, event.Installation.ID)
	if !ok {
		return
	}

	job := buildGitHubJob(provider, owner, repoName, event)

	dedupKey := fmt.Sprintf("gh:%s/%s:%d", owner, repoName, job.PR.ID)
	if d.dedupSeen(dedupKey) {
		logger.Debugf("GitHub webhook: duplicate delivery for PR #%d in %s/%s — skipping", job.PR.ID, owner, repoName)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "duplicate")
		return
	}

	if submitErr := d.submitter.Submit(job); submitErr != nil {
		logger.Errorf("GitHub webhook: submit failed: %v", submitErr)
		// Roll back the dedup record so a webhook retry inside the
		// TTL is not silently dropped — the cache must only retain
		// keys that actually made it onto the worker queue.
		d.dedupForget(dedupKey)
		writeError(w, http.StatusServiceUnavailable, "queue full")
		return
	}

	logger.Infof("GitHub webhook: enqueued PR #%d in %s/%s", job.PR.ID, owner, repoName)
	w.WriteHeader(http.StatusAccepted)
	_, _ = fmt.Fprint(w, "accepted")
}

// parseGitHubEvent unmarshals the payload into a ghEvent or writes a 400 response
// and returns ok=false. The caller should return immediately on ok=false.
func parseGitHubEvent(w http.ResponseWriter, body []byte) (ghEvent, bool) {
	var event ghEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return ghEvent{}, false
	}
	return event, true
}

// buildGitHubProvider resolves a token (App installation or PAT) and returns the
// configured ReviewProvider, or writes an error response and returns ok=false.
func (d *Dispatcher) buildGitHubProvider(
	ctx context.Context, w http.ResponseWriter, installationID int64,
) (forgeEntities.ReviewProvider, bool) {
	token, err := d.resolveGitHubToken(ctx, installationID)
	if err != nil {
		logger.Errorf("GitHub webhook: token resolution failed: %v", err)
		writeError(w, http.StatusInternalServerError, "token error")
		return nil, false
	}
	provider, err := d.providerRegistry.GetReviewProvider("github", token)
	if err != nil {
		logger.Errorf("GitHub webhook: failed to build provider: %v", err)
		writeError(w, http.StatusInternalServerError, "provider error")
		return nil, false
	}
	return provider, true
}

func buildGitHubJob(provider forgeEntities.ReviewProvider, owner, repoName string, event ghEvent) Job {
	return Job{
		Provider: provider,
		Repo: forgeEntities.Repository{
			Name:         repoName,
			Organization: owner,
			RemoteURL:    event.Repository.HTMLURL,
		},
		PR: forgeEntities.PullRequestDetail{
			PullRequest: forgeEntities.PullRequest{
				ID:     event.PullRequest.Number,
				Title:  event.PullRequest.Title,
				URL:    event.PullRequest.HTMLURL,
				Status: event.PullRequest.State,
			},
			SourceBranch: event.PullRequest.Head.Ref,
			TargetBranch: event.PullRequest.Base.Ref,
			Author:       event.PullRequest.User.Login,
		},
		CIPassed: false,
	}
}

// resolveGitHubToken returns either an App installation token (when the App is
// configured and an installation ID is present) or a configured PAT.
func (d *Dispatcher) resolveGitHubToken(ctx context.Context, installationID int64) (string, error) {
	if d.githubTokenizer != nil && installationID != 0 {
		return d.githubTokenizer.InstallationToken(ctx, installationID)
	}
	if pat := d.findToken("github"); pat != "" {
		return pat, nil
	}
	return "", errors.New("no github_app private key and no github PAT configured")
}

func splitFullName(fullName string) (string, string, error) {
	parts := strings.SplitN(fullName, "/", fullNameSegments)
	if len(parts) != fullNameSegments || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid full_name %q", fullName)
	}
	return parts[0], parts[1], nil
}
