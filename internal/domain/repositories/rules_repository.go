package repositories

import "github.com/rios0rios0/codeguru/internal/domain/entities"

// RulesRepository loads review rules from a configured source.
type RulesRepository interface {
	// LoadAll returns all available rules.
	LoadAll() ([]entities.Rule, error)

	// LoadForLanguages returns rules relevant to the given language categories.
	// changedFiles is used for glob-based matching from rule frontmatter.
	LoadForLanguages(languages []string, changedFiles []string) ([]entities.Rule, error)
}
