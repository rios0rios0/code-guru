package webhooks

import (
	"context"
	"fmt"
	"net/http"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	infraRepos "github.com/rios0rios0/codeguru/internal/infrastructure/repositories"
)

// Dispatcher bridges webhook events to the domain review logic.
type Dispatcher struct {
	aiFactory        *infraRepos.AIReviewerFactory
	rulesFactory     *infraRepos.RulesRepositoryFactory
	detectorRegistry repositories.TrivialDetectorRegistry
	settings         *entities.Settings
}

// NewDispatcher creates a new webhook dispatcher.
func NewDispatcher(
	aiFactory *infraRepos.AIReviewerFactory,
	rulesFactory *infraRepos.RulesRepositoryFactory,
	detectorRegistry repositories.TrivialDetectorRegistry,
	settings *entities.Settings,
) *Dispatcher {
	return &Dispatcher{
		aiFactory:        aiFactory,
		rulesFactory:     rulesFactory,
		detectorRegistry: detectorRegistry,
		settings:         settings,
	}
}

// HandleGitHub processes GitHub App webhook events.
func (d *Dispatcher) HandleGitHub(w http.ResponseWriter, _ *http.Request) {
	// TODO: implement GitHub webhook handling:
	// 1. Verify HMAC-SHA256 signature from X-Hub-Signature-256 header
	// 2. Parse check_suite.completed event payload
	// 3. Extract repo, installation ID, head SHA, conclusion
	// 4. Get installation access token (App JWT → installation token)
	// 5. Find PR associated with the head SHA
	// 6. Build gitforge ReviewProvider with installation token
	// 7. Call HandlePR with ciPassed from conclusion
	logger.Warn("GitHub webhook handler not yet implemented")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = fmt.Fprint(w, "GitHub webhook handler not yet implemented")
}

// HandleAzureDevOps processes Azure DevOps Service Hook events.
func (d *Dispatcher) HandleAzureDevOps(w http.ResponseWriter, _ *http.Request) {
	// TODO: implement Azure DevOps webhook handling:
	// 1. Parse build.complete event payload
	// 2. Extract repo, project, build result, associated PR
	// 3. Build gitforge provider with PAT
	// 4. Call HandlePR with ciPassed from build result
	logger.Warn("Azure DevOps webhook handler not yet implemented")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = fmt.Fprint(w, "Azure DevOps webhook handler not yet implemented")
}

// HandlePR performs a review and optionally merges the PR.
func (d *Dispatcher) HandlePR(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	pr forgeEntities.PullRequestDetail,
	ciPassed bool,
) error {
	aiReviewer := d.aiFactory.Create(d.settings)
	rulesRepo := d.rulesFactory.Create(d.settings)
	reviewCmd := commands.NewReviewCommand(aiReviewer, rulesRepo, d.detectorRegistry)

	result, err := reviewCmd.Execute(ctx, provider, repo, pr, commands.ReviewOptions{
		CIPassed: ciPassed,
	})
	if err != nil {
		return fmt.Errorf("review failed for PR #%d: %w", pr.ID, err)
	}

	logger.Infof("PR #%d review complete: verdict=%s, comments=%d", pr.ID, result.Verdict, len(result.Comments))

	// TODO: auto-merge via provider.MergePullRequest() once gitforge adds this method
	if result.Verdict == "approve" && ciPassed {
		logger.Infof("PR #%d approved and CI passed -- auto-merge pending gitforge support", pr.ID)
	}

	return nil
}
