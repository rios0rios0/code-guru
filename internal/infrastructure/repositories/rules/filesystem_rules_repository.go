package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	logger "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// universalCategories are rule categories that apply regardless of language.
var universalCategories = map[string]bool{ //nolint:gochecknoglobals // read-only lookup table used as a constant
	"architecture":    true,
	"ci-cd":           true,
	"code-style":      true,
	"design-patterns": true,
	"documentation":   true,
	"git-flow":        true,
	"security":        true,
	"testing":         true,
}

// FilesystemRulesRepository loads review rules from markdown files on disk.
type FilesystemRulesRepository struct {
	path       string
	categories []string
}

// NewFilesystemRulesRepository creates a new filesystem-based rules repository.
func NewFilesystemRulesRepository(path string, categories []string) *FilesystemRulesRepository {
	return &FilesystemRulesRepository{
		path:       path,
		categories: categories,
	}
}

// LoadAll returns all available rules from the configured path.
func (r *FilesystemRulesRepository) LoadAll() ([]entities.Rule, error) {
	if r.path == "" {
		return nil, nil
	}

	expandedPath := os.ExpandEnv(r.path)

	entries, err := os.ReadDir(expandedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read rules directory %q: %w", expandedPath, err)
	}

	var rules []entities.Rule
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".md")
		content, readErr := os.ReadFile(filepath.Join(expandedPath, entry.Name()))
		if readErr != nil {
			logger.Warnf("failed to read rule file %q: %v", entry.Name(), readErr)
			continue
		}

		body, globs := stripFrontmatter(string(content))

		rule := entities.Rule{
			Name:      name,
			Category:  name,
			Content:   body,
			FileGlobs: globs,
		}

		if len(r.categories) > 0 && !r.matchesCategory(name) {
			continue
		}

		rules = append(rules, rule)
	}

	return rules, nil
}

// LoadForLanguages returns rules relevant to the given language categories.
// Always includes universal rules (architecture, testing, security, etc.).
// Uses frontmatter file globs when available for precise matching.
func (r *FilesystemRulesRepository) LoadForLanguages(
	languages []string,
	changedFiles []string,
) ([]entities.Rule, error) {
	allRules, err := r.LoadAll()
	if err != nil {
		return nil, err
	}

	languageSet := make(map[string]bool)
	for _, lang := range languages {
		languageSet[lang] = true
	}

	var filtered []entities.Rule
	for _, rule := range allRules {
		if universalCategories[rule.Category] {
			filtered = append(filtered, rule)
			continue
		}

		if languageSet[rule.Category] {
			filtered = append(filtered, rule)
			continue
		}

		if len(rule.FileGlobs) > 0 && matchesAnyGlob(rule.FileGlobs, changedFiles) {
			filtered = append(filtered, rule)
		}
	}

	return filtered, nil
}

func (r *FilesystemRulesRepository) matchesCategory(name string) bool {
	return slices.Contains(r.categories, name)
}

// stripFrontmatter removes YAML frontmatter from markdown content and extracts path globs.
func stripFrontmatter(content string) (string, []string) {
	if !strings.HasPrefix(content, "---") {
		return content, nil
	}

	end := strings.Index(content[3:], "\n---")
	if end == -1 {
		return content, nil
	}

	frontmatterBlock := content[3 : end+3]
	body := strings.TrimSpace(content[end+3+4:]) // skip past closing "---\n"

	var fm struct {
		Paths []string `yaml:"paths"`
	}
	if unmarshalErr := yaml.Unmarshal([]byte(frontmatterBlock), &fm); unmarshalErr != nil {
		logger.Debugf("failed to parse frontmatter: %v", unmarshalErr)
		return content, nil
	}

	return body, fm.Paths
}

// matchesAnyGlob checks if any of the changed files match any of the provided glob patterns.
func matchesAnyGlob(globs []string, files []string) bool {
	for _, glob := range globs {
		for _, file := range files {
			matched, err := filepath.Match(glob, file)
			if err != nil {
				continue
			}
			if matched {
				return true
			}

			// filepath.Match does not support "**" recursion, so check the base name
			// against a simplified pattern (e.g., "**/*.go" -> "*.go")
			if strings.HasPrefix(glob, "**"+string(filepath.Separator)) || strings.HasPrefix(glob, "**/") {
				simpleGlob := glob[strings.LastIndex(glob, "/")+1:]
				baseName := filepath.Base(file)
				if baseMatched, matchErr := filepath.Match(simpleGlob, baseName); matchErr == nil && baseMatched {
					return true
				}
			}
		}
	}
	return false
}
