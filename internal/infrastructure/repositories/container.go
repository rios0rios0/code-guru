package repositories

import (
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	anthropicRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/anthropic"
	claudeRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/claude"
	openaiRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/openai"
	rulesRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/rules"
	selfupdateRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/selfupdate"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/trivial"
	"github.com/rios0rios0/gitforge/pkg/providers/infrastructure/azuredevops"
	"github.com/rios0rios0/gitforge/pkg/providers/infrastructure/github"
	registry "github.com/rios0rios0/gitforge/pkg/registry/infrastructure"
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

	// Trivial detector registry. Built from `Settings.Trivial.Adapters`
	// so the webhook dispatcher (which receives this via DI and never
	// rebuilds it) honours `CODE_GURU_TRIVIAL_ADAPTERS`. The CLI
	// `review` controller still builds its own registry per-call from a
	// fresh `NewSettingsFromEnv()` read; this provider is the source of
	// truth for the long-lived `serve` path. An empty / disabled
	// adapter list yields an empty registry, which short-circuits to
	// `Detected=false` — i.e. trivial detection is opt-in and silent
	// when not configured.
	if err := container.Provide(func(s *entities.Settings) repositories.TrivialDetectorRegistry {
		if s == nil || !s.Trivial.Enabled || len(s.Trivial.Adapters) == 0 {
			return trivial.NewDetectorRegistry(nil)
		}
		return trivial.NewDetectorRegistry(s.Trivial.Adapters)
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

// Create returns the appropriate AI reviewer based on the backend setting.
func (f *AIReviewerFactory) Create(settings *entities.Settings) repositories.AIReviewerRepository {
	switch settings.AI.Backend {
	case "openai":
		return openaiRepo.NewAIReviewerRepository(settings.AI.OpenAI.APIKey, settings.AI.OpenAI.Model)
	case "anthropic":
		return anthropicRepo.NewAIReviewerRepository(settings.AI.Anthropic.APIKey, settings.AI.Anthropic.Model)
	case "claude":
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
func (f *RulesRepositoryFactory) Create(settings *entities.Settings) repositories.RulesRepository {
	return rulesRepo.NewFilesystemRulesRepository(settings.Rules.Path, settings.Rules.Categories)
}
