package repositories

import "github.com/rios0rios0/codeguru/internal/domain/entities"

// StubRulesRepository is a test double that returns canned rules.
type StubRulesRepository struct {
	AllRules         []entities.Rule
	AllErr           error
	LanguageRules    []entities.Rule
	LanguageErr      error
	LastLanguages    []string
	LastChangedFiles []string
}

// LoadAll returns the canned rules.
func (r *StubRulesRepository) LoadAll() ([]entities.Rule, error) {
	return r.AllRules, r.AllErr
}

// LoadForLanguages stores the arguments and returns the canned rules.
func (r *StubRulesRepository) LoadForLanguages(
	languages []string,
	changedFiles []string,
) ([]entities.Rule, error) {
	r.LastLanguages = languages
	r.LastChangedFiles = changedFiles
	return r.LanguageRules, r.LanguageErr
}
