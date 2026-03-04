package support

import "path/filepath"

// languageMap maps file extensions to language categories used for rule filtering.
var languageMap = map[string]string{ //nolint:gochecknoglobals // read-only lookup table used as a constant
	".go":   "golang",
	".js":   "javascript",
	".ts":   "javascript",
	".jsx":  "javascript",
	".tsx":  "javascript",
	".java": "java",
	".py":   "python",
	".yaml": "yaml",
	".yml":  "yaml",
}

// ClassifyFile returns the language category for a file path based on its extension.
// Returns an empty string if the extension is not recognized.
func ClassifyFile(path string) string {
	ext := filepath.Ext(path)
	if lang, ok := languageMap[ext]; ok {
		return lang
	}
	return ""
}

// ClassifyFiles returns the unique set of language categories for the given file paths.
func ClassifyFiles(paths []string) []string {
	seen := make(map[string]bool)
	var languages []string

	for _, path := range paths {
		lang := ClassifyFile(path)
		if lang != "" && !seen[lang] {
			seen[lang] = true
			languages = append(languages, lang)
		}
	}

	return languages
}
