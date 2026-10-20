package repositories

import (
	"os"
	"path/filepath"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	anthropicRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/anthropic"
	claudeRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/claude"
	openaiRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/openai"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/prmetadata"
	rulesRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/rules"
	selfupdateRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/selfupdate"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/trivial"
	workspaceRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/workspace"
	"github.com/rios0rios0/gitforge/v4/pkg/providers/infrastructure/azuredevops"
	"github.com/rios0rios0/gitforge/v4/pkg/providers/infrastructure/github"
	registry "github.com/rios0rios0/gitforge/v4/pkg/registry/infrastructure"
	"go.uber.org/dig"
)

// RegisterProviders registers all repository providers with the DIG container.
func RegisterProviders(container *dig.Container) error {
	// register the gitforge provider registry
	if err := container.Provide(func() *registry.ProviderRegistry {
		reg := registry.NewProviderRegistry()
		reg.RegisterFactory("github", github.NewProvider)
		reg.RegisterFactory("azuredevops", azuredevops.NewProvider)
		return reg
	}); err != nil {
		return err
	}

	// register the AI reviewer factory (selected by settings at controller level)
	if err := container.Provide(NewAIReviewerFactory); err != nil {
		return err
	}

	// register the rules repository factory
	if err := container.Provide(NewRulesRepositoryFactory); err != nil {
		return err
	}

	// Trivial detector registry, built from `Settings.Trivial` so the
	// webhook dispatcher (which receives this registry via DI and
	// never rebuilds it) sees the configured adapter list. Both this
	// provider and the CLI `review` controller delegate to
	// `trivial.NewDetectorRegistryFromConfig` so the
	// enabled/empty-list logic stays in one place — past production
	// bugs came from these paths drifting apart. The injected
	// `*entities.Settings` is the same struct the CLI loads via
	// `resolveSettings` (explicit `--config`, then auto-discovered
	// YAML, falling back to env), so any deployment shape that
	// populates `Trivial.Adapters` reaches both paths.
	if err := container.Provide(func(s *entities.Settings) repositories.TrivialDetectorRegistry {
		if s == nil {
			return trivial.NewDetectorRegistry(nil)
		}
		return trivial.NewDetectorRegistryFromConfig(s.Trivial)
	}); err != nil {
		return err
	}

	// Pull request metadata fetcher registry (description + commit
	// count via provider REST APIs). Stateless, so a single instance is
	// shared by the CLI controllers and the webhook dispatcher; the
	// concrete type is bound to the domain interface here so consumers
	// depend on the contract only.
	if err := container.Provide(func() repositories.PullRequestMetadataRepository {
		return prmetadata.NewRegistryPullRequestMetadataRepository()
	}); err != nil {
		return err
	}

	// register the self-updater repository
	if err := container.Provide(func(v entities.AppVersion) repositories.SelfUpdaterRepository {
		return selfupdateRepo.NewCliforgeSelfUpdaterRepository(
			"rios0rios0", "code-guru", "code-guru", string(v),
		)
	}); err != nil {
		return err
	}

	return nil
}

// AIReviewerFactory creates an AIReviewerRepository based on settings.
type AIReviewerFactory struct{}

// NewAIReviewerFactory creates a new AIReviewerFactory.
func NewAIReviewerFactory() *AIReviewerFactory {
	return &AIReviewerFactory{}
}

// Create returns the appropriate AI reviewer based on the backend setting,
// wrapped in WithRetry so a non-JSON or transient-error response is
// re-sampled instead of immediately failing the review and posting the raw
// output to the PR. The attempt budget comes from `settings.AI.ReviewAttempts()`.
func (f *AIReviewerFactory) Create(settings *entities.Settings) repositories.AIReviewerRepository {
	return WithRetry(f.createBackend(settings), settings.AI.ReviewAttempts())
}

// createBackend builds the bare backend selected by `settings.AI.Backend`,
// before the retry decorator is applied. An unrecognised backend falls back
// to the Claude CLI with defaults (the same behaviour as before retries).
func (f *AIReviewerFactory) createBackend(settings *entities.Settings) repositories.AIReviewerRepository {
	switch settings.AI.Backend {
	case "openai":
		return openaiRepo.NewAIReviewerRepository(settings.AI.OpenAI.APIKey, settings.AI.OpenAI.Model)
	case "anthropic":
		return anthropicRepo.NewAIReviewerRepository(
			settings.AI.Anthropic.APIKey,
			settings.AI.Anthropic.Model,
			anthropicRepo.WithContext1M(settings.AI.Anthropic.Context1MEnabled()),
			anthropicRepo.WithRefusalFallbackModel(settings.AI.Anthropic.RefusalFallbackModel),
		)
	case "claude":
		if settings.AI.Claude.AgenticEnabled() {
			return claudeRepo.NewAgenticReviewerRepository(
				settings.AI.Claude.BinaryPath, settings.AI.Claude.Model, settings.AI.Claude.Agentic,
			)
		}
		return claudeRepo.NewAIReviewerRepository(
			settings.AI.Claude.BinaryPath, settings.AI.Claude.Model, settings.AI.Claude.MaxTurns,
		)
	default:
		return claudeRepo.NewAIReviewerRepository("", "", 0)
	}
}

// RulesRepositoryFactory creates a RulesRepository based on settings.
type RulesRepositoryFactory struct{}

// NewRulesRepositoryFactory creates a new RulesRepositoryFactory.
func NewRulesRepositoryFactory() *RulesRepositoryFactory {
	return &RulesRepositoryFactory{}
}

// Create returns a rules repository configured from settings.
//
// In agentic mode an operator may point `ai.claude.agentic.config_path` at a
// whole Claude Code configuration tree and leave `rules.path` empty; the
// tree's `rules/` directory is then the rule corpus, so the review is not
// silently run without the standards the operator installed.
func (f *RulesRepositoryFactory) Create(settings *entities.Settings) repositories.RulesRepository {
	path := settings.Rules.Path
	if configPath := settings.AI.Claude.Agentic.ResolvedConfigPath(); path == "" &&
		settings.AI.Claude.AgenticEnabled() && configPath != "" {
		if info, err := os.Stat(filepath.Join(configPath, "rules")); err == nil && info.IsDir() {
			path = filepath.Join(configPath, "rules")
		}
	}
	return rulesRepo.NewFilesystemRulesRepository(path, settings.Rules.Categories)
}

// NewWorkspaceRepository returns the repository that checks pull requests out
// for workspace reviewers, configured from the agentic settings. Every review
// command is given one; a diff reviewer never calls it.
func NewWorkspaceRepository(settings *entities.Settings) repositories.WorkspaceRepository {
	return workspaceRepo.NewGitWorkspaceRepository(
		settings.AI.Claude.Agentic.ResolvedWorkspaceDir(), settings.AI.Claude.Agentic.CloneDepth,
	)
}
