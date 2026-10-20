package controllers

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	configHelpers "github.com/rios0rios0/gitforge/v4/pkg/config/domain/helpers"
	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	registry "github.com/rios0rios0/gitforge/v4/pkg/registry/infrastructure"
	logger "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	domainRepos "github.com/rios0rios0/codeguru/internal/domain/repositories"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
	infraRepos "github.com/rios0rios0/codeguru/internal/infrastructure/repositories"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/trivial"
	"github.com/rios0rios0/codeguru/internal/support"
)

// reviewFailureExitCode is the exit status of a `review` run that could not
// review the pull request. A non-zero status is what tells a CI step — and a
// Kubernetes review Job — that the review did not happen: exiting 0 after
// logging the error made a failed review indistinguishable from a clean one.
const reviewFailureExitCode = 1

// exitProcess terminates the process; a variable so tests can intercept it.
//
//nolint:gochecknoglobals // test seam over os.Exit
var exitProcess = os.Exit

// failReviewf logs the failure and exits with reviewFailureExitCode. The
// `return` after each call only matters when a test has replaced exitProcess.
func failReviewf(format string, args ...any) {
	logger.Errorf(format, args...)
	exitProcess(reviewFailureExitCode)
}

// ReviewController handles reviewing a single PR by URL.
type ReviewController struct {
	providerRegistry  *registry.ProviderRegistry
	aiReviewerFactory *infraRepos.AIReviewerFactory
	rulesRepoFactory  *infraRepos.RulesRepositoryFactory
	metadataRepo      domainRepos.PullRequestMetadataRepository

	mintInstallationToken installationTokenMinter
}

// githubInstallationIDEnv names the GitHub App installation a review Job's
// pull request came through.
const githubInstallationIDEnv = "CODE_GURU_GITHUB_INSTALLATION_ID"

// installationTokenMinter mints a token for one installation of the GitHub
// App.
type installationTokenMinter func(
	ctx context.Context, app entities.GitHubAppConfig, installationID int64,
) (string, error)

// NewReviewController creates a new ReviewController.
func NewReviewController(
	providerRegistry *registry.ProviderRegistry,
	aiReviewerFactory *infraRepos.AIReviewerFactory,
	rulesRepoFactory *infraRepos.RulesRepositoryFactory,
	metadataRepo domainRepos.PullRequestMetadataRepository,
) *ReviewController {
	return &ReviewController{
		providerRegistry:      providerRegistry,
		aiReviewerFactory:     aiReviewerFactory,
		rulesRepoFactory:      rulesRepoFactory,
		metadataRepo:          metadataRepo,
		mintInstallationToken: mintGitHubInstallationToken,
	}
}

// GetBind returns the Cobra command metadata.
func (c *ReviewController) GetBind() entities.ControllerBind {
	return entities.ControllerBind{
		Use:   "review [pr-url]",
		Short: "Review a single pull request by URL",
	}
}

// BindFlags registers the review-only flags.
//
// `--mentioned` runs the review as if the bot had been mentioned in a comment:
// the review-once gate is bypassed and the bot's earlier threads are read back
// and resolved. The webhook server's Kubernetes executor passes it to the
// review Job of a mention, which is otherwise the same `review` invocation a
// person would run.
//
// `--job-name` is set by the same executor on every review Job: the name is
// stamped on the review's completion and failure notices, which is how the
// server recognises this Job's own outcome when the Job fails afterwards.
func (c *ReviewController) BindFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("mentioned", false, "review as a requested re-review (bypass the review-once gate)")
	cmd.Flags().String("job-name", "",
		"the Kubernetes review Job running this review, stamped on its outcome notices (set by the server)")
}

// Execute performs the review of a single PR.
func (c *ReviewController) Execute(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	if len(args) == 0 {
		failReviewf("PR URL is required")
		return
	}

	prURL := args[0]
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	verbose, _ := cmd.Flags().GetBool("verbose")
	// Only the `review` subcommand defines the flag; the bare
	// `code-guru <pr-url>` form reads false.
	mentioned, _ := cmd.Flags().GetBool("mentioned")
	jobName, _ := cmd.Flags().GetString("job-name")

	if verbose {
		logger.SetLevel(logger.DebugLevel)
	}

	// parse PR URL
	parsed, err := support.ParsePullRequestURL(prURL)
	if err != nil {
		failReviewf("failed to parse PR URL: %v", err)
		return
	}

	// load settings
	settings, err := c.loadSettings(cmd)
	if err != nil {
		failReviewf("failed to load settings: %v", err)
		return
	}

	// find token for the provider
	token, err := c.resolveToken(ctx, settings, parsed.ProviderType, os.Getenv(githubInstallationIDEnv))
	if err != nil {
		failReviewf("%v", err)
		return
	}

	// get review provider
	reviewProvider, err := c.providerRegistry.GetReviewProvider(parsed.ProviderType, token)
	if err != nil {
		failReviewf("failed to get review provider: %v", err)
		return
	}

	// build repo entity
	repo := forgeEntities.Repository{
		Name:         parsed.RepoName,
		Organization: parsed.Organization,
		Project:      parsed.Project,
	}

	// find the specific PR
	targetPR, err := findOpenPullRequest(ctx, reviewProvider, repo, parsed.PRID)
	if err != nil {
		failReviewf("%v", err)
		return
	}

	// Use the same helper as the long-lived webhook DI provider so
	// the CLI and `serve` paths cannot drift on what counts as
	// "trivial detection enabled".
	detectorRegistry := trivial.NewDetectorRegistryFromConfig(settings.Trivial)

	// create the review command with settings-based dependencies
	aiReviewer := c.aiReviewerFactory.Create(settings)
	rulesRepo := c.rulesRepoFactory.Create(settings)
	reviewCmd := commands.NewReviewCommand(aiReviewer, rulesRepo, detectorRegistry, c.metadataRepo).
		WithWorkspace(infraRepos.NewWorkspaceRepository(settings)).
		WithJobName(jobName)

	// TODO: query CI status automatically via provider.GetPullRequestCheckStatus() once gitforge adds this method
	ciPassed := false

	result, err := reviewCmd.Execute(ctx, reviewProvider, repo, *targetPR, commands.ReviewOptions{
		DryRun:                    dryRun,
		Verbose:                   verbose,
		CIPassed:                  ciPassed,
		SubmitNativeReview:        settings.AI.NativeReviewSubmissionEnabled(),
		MaxGuidelinesBytes:        settings.AI.GuidelinesBytes(),
		MaxPRDescriptionBytes:     settings.AI.PRDescriptionBytes(),
		ReviewDrafts:              settings.AI.ReviewDrafts,
		UserMentioned:             mentioned,
		TrivialAutoMerge:          settings.Trivial.AutoMerge,
		TrivialMergeStrategy:      settings.Trivial.MergeStrategy,
		TrivialBypassPolicy:       settings.Trivial.BypassPolicy,
		TrivialAutoMergeAuthors:   settings.Trivial.AutoMergeAllowedAuthors,
		TrivialDeleteSourceBranch: settings.Trivial.DeleteSourceBranchEnabled(),
		BotIdentities:             settings.BotIdentities,
		LoadProjectGuidelines:     settings.AI.ProjectGuidelinesEnabled(),
		LoadPullRequestMetadata:   settings.AI.PullRequestMetadataEnabled(),
		BatchLargeReviews:         settings.AI.BatchLargeReviewsEnabled(),
		MaxReviewBatches:          settings.AI.ReviewBatches(),
	})
	if err != nil {
		failReviewf("review failed: %v", err)
		return
	}

	c.printResult(result, dryRun)
}

func (c *ReviewController) loadSettings(cmd *cobra.Command) (*entities.Settings, error) {
	configPath, _ := cmd.Flags().GetString("config")
	backendOverride, _ := cmd.Flags().GetString("backend")
	rulesPathOverride, _ := cmd.Flags().GetString("rules-path")

	settings, err := c.resolveSettings(configPath)
	if err != nil {
		return nil, err
	}

	// apply CLI overrides
	if backendOverride != "" {
		settings.AI.Backend = backendOverride
	}
	if rulesPathOverride != "" {
		settings.Rules.Path = rulesPathOverride
	}

	return settings, nil
}

func (c *ReviewController) resolveSettings(configPath string) (*entities.Settings, error) {
	if configPath != "" {
		return entities.NewSettings(configPath)
	}

	cfgPath, _ := configHelpers.FindConfigFile("code-guru")
	if cfgPath == "" {
		// no config file; try environment variables
		return entities.NewSettingsFromEnv()
	}

	return entities.NewSettings(cfgPath)
}

func (c *ReviewController) findToken(settings *entities.Settings, providerType string) string {
	for _, p := range settings.Providers {
		if p.Type == providerType {
			return p.Token
		}
	}
	// fallback to environment variable
	if token := os.Getenv("CODE_GURU_PROVIDER_TOKEN"); token != "" {
		return token
	}
	return ""
}

// findOpenPullRequest looks the pull request up among the repository's open
// ones, which is where the review gets its branches, author and draft flag.
func findOpenPullRequest(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
) (*forgeEntities.PullRequestDetail, error) {
	prs, err := provider.ListOpenPullRequests(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to list PRs: %w", err)
	}
	for i := range prs {
		if prs[i].ID == prID {
			return &prs[i], nil
		}
	}
	return nil, fmt.Errorf("PR #%d not found or not open in %s/%s", prID, repo.Organization, repo.Name)
}

// resolveToken picks the credential the webhook server picked for the same
// pull request (its `resolveGitHubToken`): on GitHub, an App installation
// token whenever the App is configured and `installationID` names an
// installation — CODE_GURU_GITHUB_INSTALLATION_ID, which the server's
// Kubernetes executor sets on the review Job of a pull request it received
// through the App, so it is the identity that has access to the repository —
// and the configured token otherwise. A failed exchange fails the review
// rather than falling back to a token of another identity, with other access
// and another author name on what it posts. Installation tokens expire after
// an hour, so the pod mints its own instead of being handed one that might
// lapse while it waits in the queue.
func (c *ReviewController) resolveToken(
	ctx context.Context, settings *entities.Settings, providerType, installationID string,
) (string, error) {
	id, _ := strconv.ParseInt(strings.TrimSpace(installationID), 10, 64)
	app := settings.GitHubApp
	if providerType == "github" && id != 0 && app.AppID != 0 && app.PrivateKey != "" {
		return c.mintInstallationToken(ctx, app, id)
	}
	if token := c.findToken(settings, providerType); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("no token configured for provider %q", providerType)
}

// mintGitHubInstallationToken exchanges the App's credentials for a token of
// one of its installations.
func mintGitHubInstallationToken(
	ctx context.Context, app entities.GitHubAppConfig, installationID int64,
) (string, error) {
	tokenizer, err := webhooks.NewGitHubAppTokenizer(app.AppID, app.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("failed to initialise the GitHub App tokenizer: %w", err)
	}
	token, err := tokenizer.InstallationToken(ctx, installationID)
	if err != nil {
		return "", fmt.Errorf("failed to mint a GitHub App installation token: %w", err)
	}
	return token, nil
}

func (c *ReviewController) printResult(result *entities.ReviewResult, dryRun bool) {
	if dryRun {
		_, _ = fmt.Fprintln(os.Stdout, "--- DRY RUN (comments not posted) ---")
	}

	// emit verdict for machine parsing
	_, _ = fmt.Fprintf(os.Stdout, "VERDICT:%s\n", result.Verdict)

	if result.Summary != "" {
		_, _ = fmt.Fprintf(os.Stdout, "\nSummary: %s\n", result.Summary)
	}

	if len(result.Comments) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "\nNo issues found.")
		return
	}

	_, _ = fmt.Fprintf(os.Stdout, "\nFound %d comments:\n", len(result.Comments))
	for _, comment := range result.Comments {
		_, _ = fmt.Fprintf(os.Stdout, "  [%s] %s", comment.Severity, comment.FilePath)
		if comment.Line > 0 {
			_, _ = fmt.Fprintf(os.Stdout, ":%d", comment.Line)
		}
		_, _ = fmt.Fprintf(os.Stdout, " - %s\n", comment.Body)
	}
}
