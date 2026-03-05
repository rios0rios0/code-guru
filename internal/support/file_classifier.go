package support

import (
	"github.com/rios0rios0/langforge/pkg/domain/entities"
)

// languageToRuleCategory maps langforge Language constants to code-guru rule category names.
//
//nolint:gochecknoglobals // read-only lookup table used as a constant
var languageToRuleCategory = map[entities.Language]string{
	entities.LanguageGo:        "golang",
	entities.LanguageNode:      "javascript",
	entities.LanguagePython:    "python",
	entities.LanguageJava:      "java",
	entities.LanguageCSharp:    "csharp",
	entities.LanguageTerraform: "terraform",
	entities.LanguageYAML:      "yaml",
}

// ClassifyFile returns the rule category for a file path based on its extension.
// Returns an empty string if the extension is not recognized.
func ClassifyFile(path string) string {
	lang := entities.ClassifyFileByExtension(path)
	if cat, ok := languageToRuleCategory[lang]; ok {
		return cat
	}
	return ""
}

// ClassifyFiles returns the unique set of rule categories for the given file paths.
func ClassifyFiles(paths []string) []string {
	langs := entities.ClassifyFilesByExtension(paths)
	seen := make(map[string]bool, len(langs))
	var categories []string
	for _, lang := range langs {
		if cat, ok := languageToRuleCategory[lang]; ok && !seen[cat] {
			seen[cat] = true
			categories = append(categories, cat)
		}
	}
	return categories
}
