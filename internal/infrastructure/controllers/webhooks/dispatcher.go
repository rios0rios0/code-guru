package webhooks

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
	registry "github.com/rios0rios0/gitforge/pkg/registry/infrastructure"
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	infraRepos "github.com/rios0rios0/codeguru/internal/infrastructure/repositories"
)

// Submitter is the subset of *Pool used by handlers; defining it here lets tests
// substitute the pool without spinning up real workers.
type Submitter interface {
	Submit(job Job) error
}

// Dispatcher bridges webhook events to the domain review logic.
type Dispatcher struct {
	aiFactory             *infraRepos.AIReviewerFactory
	rulesFactory          *infraRepos.RulesRepositoryFactory
	detectorRegistry      repositories.TrivialDetectorRegistry
	settings              *entities.Settings
	providerRegistry      *registry.ProviderRegistry
	submitter             Submitter
	githubTokenizer       GitHubTokenizer
	adoHydrator           ADOResourceHydrator
	allowedSourcePrefixes []netip.Prefix
}

// GitHubTokenizer resolves an installation access token for a GitHub App.
// Production code uses the JWT-based exchanger; tests can substitute a stub.
type GitHubTokenizer interface {
	InstallationToken(ctx context.Context, installationID int64) (string, error)
}

// NewDispatcher creates a new webhook dispatcher.
func NewDispatcher(
	aiFactory *infraRepos.AIReviewerFactory,
	rulesFactory *infraRepos.RulesRepositoryFactory,
	detectorRegistry repositories.TrivialDetectorRegistry,
	settings *entities.Settings,
	providerRegistry *registry.ProviderRegistry,
) *Dispatcher {
	return &Dispatcher{
		aiFactory:             aiFactory,
		rulesFactory:          rulesFactory,
		detectorRegistry:      detectorRegistry,
		settings:              settings,
		providerRegistry:      providerRegistry,
		adoHydrator:           NewHTTPADOHydrator(nil),
		allowedSourcePrefixes: parseAllowedCIDRs(settings.Server.AllowedSourceCIDRs),
	}
}

// SetADOHydrator overrides the default HTTP-based ADO PR hydrator. Tests
// substitute a stub that returns a canned `adoResource` without touching the
// network; production code does not need to call this.
//
// **Concurrency contract:** must be called during initialisation, before
// the HTTP server starts handling webhook requests. The setter does not
// synchronise with `HandleAzureDevOps`, so swapping the hydrator at
// runtime would race with in-flight deliveries. The same contract applies
// to `SetSubmitter` and `SetGitHubTokenizer` — all three are wired once
// during DI bootstrap and never touched again.
func (d *Dispatcher) SetADOHydrator(h ADOResourceHydrator) {
	d.adoHydrator = h
}

// parseAllowedCIDRs validates and parses each CIDR entry once at startup so
// hot-path requests don't re-parse the same strings. Invalid entries are
// logged and skipped — this keeps the dispatcher boot-able if a single typo
// slips through, while still surfacing the problem in the operator log.
func parseAllowedCIDRs(raw []string) []netip.Prefix {
	if len(raw) == 0 {
		return nil
	}
	parsed := make([]netip.Prefix, 0, len(raw))
	for _, c := range raw {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(c)
		if err != nil {
			logger.Warnf("dispatcher: ignoring invalid CIDR %q in Server.AllowedSourceCIDRs: %v", c, err)
			continue
		}
		parsed = append(parsed, prefix)
	}
	return parsed
}

// SetSubmitter wires the worker pool used to enqueue review jobs. Called by the
// serve controller after the pool is constructed (the pool's handler closes over
// the dispatcher, so the cycle is broken at construction time).
func (d *Dispatcher) SetSubmitter(s Submitter) {
	d.submitter = s
}

// SetGitHubTokenizer wires the GitHub App installation token exchanger.
func (d *Dispatcher) SetGitHubTokenizer(t GitHubTokenizer) {
	d.githubTokenizer = t
}

// Settings exposes the loaded settings for handler-side allowlist checks.
func (d *Dispatcher) Settings() *entities.Settings {
	return d.settings
}

// HandlePR performs a review of a single PR. Called by worker goroutines.
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

	if result.Verdict == "approve" && ciPassed {
		logger.Infof("PR #%d approved and CI passed -- auto-merge pending gitforge support", pr.ID)
	}

	return nil
}

// findToken returns the configured token for a given provider type. As a
// fallback (used by the env-only configuration where CODE_GURU_PROVIDER_TOKEN
// populates a single entry without a Type), a lone untyped provider entry is
// treated as the catch-all token for any provider.
func (d *Dispatcher) findToken(providerType string) string {
	for _, p := range d.settings.Providers {
		if p.Type == providerType {
			return p.Token
		}
	}
	if len(d.settings.Providers) == 1 && d.settings.Providers[0].Type == "" {
		return d.settings.Providers[0].Token
	}
	return ""
}

// allowedOrganization returns true when the org is on the allowlist (or the
// allowlist is empty, which means "allow all").
func (d *Dispatcher) allowedOrganization(org string) bool {
	allowed := d.settings.Server.AllowedOrganizations
	if len(allowed) == 0 {
		return true
	}
	return slices.Contains(allowed, org)
}

// allowedProject returns true when the project is on the allowlist (or the
// allowlist is empty). Empty project (e.g. GitHub) is always allowed.
func (d *Dispatcher) allowedProject(project string) bool {
	allowed := d.settings.Server.AllowedProjects
	if len(allowed) == 0 || project == "" {
		return true
	}
	return slices.Contains(allowed, project)
}

// writeError writes a status code and a short text body. Used for 4xx responses
// where the body content does not matter to the sender (webhook services ignore it).
func writeError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_, _ = fmt.Fprint(w, msg)
}
