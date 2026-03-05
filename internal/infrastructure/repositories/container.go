package repositories

import (
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	claudeRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/claude"
	openaiRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/openai"
	rulesRepo "github.com/rios0rios0/codeguru/internal/infrastructure/repositories/rules"
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
