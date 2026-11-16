package autobump

import (
	"path"
	"strings"
)

// builtinVersionFiles mirrors the `version_files` paths of AutoBump's built-in
// defaults (`configs/autobump.yaml` in the AutoBump repository, compiled into
// its binary), keyed by AutoBump language name. AutoBump edits these files
// when a project's own `.autobump.yaml` does not say otherwise, so a release
// pull request made from the defaults touches them and nothing else besides
// the changelog. A file is only edited when it exists, so a project carries
// at most a few of them.
//
// Keep this in step with AutoBump. A path missing here only costs an LLM
// review of the release pull request that touches it; it can never approve
// anything, because every version file still has to pass the bump
// detectors' version-only diff check.
//
//nolint:gochecknoglobals // constant lookup map
var builtinVersionFiles = map[string][]string{
	"go": {
		// the swaggo `@version` general-info annotation
		"main.go",
		"cmd/main.go",
		"cmd/*/main.go",
		// the generated swaggo docs, under either spec name
		"docs/docs.go",
		"cmd/docs/docs.go",
		"cmd/*/docs/docs.go",
		"docs/swagger.json",
		"cmd/docs/swagger.json",
		"cmd/*/docs/swagger.json",
		"docs/openapi.json",
		"cmd/docs/openapi.json",
		"cmd/*/docs/openapi.json",
		"docs/swagger.yaml",
		"cmd/docs/swagger.yaml",
		"cmd/*/docs/swagger.yaml",
		"docs/openapi.yaml",
		"cmd/docs/openapi.yaml",
		"cmd/*/docs/openapi.yaml",
	},
	"typescript": {
		"package.json",
	},
	"python": {
		"{project_name}/__init__.py",
	},
}

// BuiltinVersionFilePatterns returns the version-file path patterns AutoBump's
// built-in defaults declare for a language, with the {project_name}
// placeholder replaced by repoName. The patterns are relative to the
// repository root and use `path.Match` syntax. Nil for a language the
// defaults give no version file.
func BuiltinVersionFilePatterns(language, repoName string) []string {
	defaults := builtinVersionFiles[language]
	if len(defaults) == 0 {
		return nil
	}
	patterns := make([]string, 0, len(defaults))
	for _, pattern := range defaults {
		patterns = append(patterns, strings.ReplaceAll(pattern, "{project_name}", repoName))
	}
	return patterns
}

// MatchesAny reports whether filePath, relative to the repository root,
// matches one of the patterns.
func MatchesAny(patterns []string, filePath string) bool {
	for _, pattern := range patterns {
		if matched, err := path.Match(pattern, filePath); err == nil && matched {
			return true
		}
	}
	return false
}
