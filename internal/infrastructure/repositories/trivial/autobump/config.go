package autobump

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the relevant parts of an .autobump.yaml file.
type Config struct {
	Languages map[string]LanguageConfig `yaml:"languages"`
}

// LanguageConfig holds detection rules for a single language.
type LanguageConfig struct {
	VersionFiles []VersionFile `yaml:"version_files"`
}

// VersionFile describes a file whose version string is updated during a bump.
type VersionFile struct {
	Path string `yaml:"path"`
}

// ParseConfig parses the YAML content of an .autobump.yaml file.
func ParseConfig(content string) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ResolveVersionFilePaths returns the version file paths for a language,
// replacing the {project_name} placeholder with repoName.
func ResolveVersionFilePaths(cfg *Config, language, repoName string) []string {
	if cfg == nil || cfg.Languages == nil {
		return nil
	}

	lang, ok := cfg.Languages[language]
	if !ok {
		return nil
	}

	paths := make([]string, 0, len(lang.VersionFiles))
	for _, vf := range lang.VersionFiles {
		resolved := strings.ReplaceAll(vf.Path, "{project_name}", repoName)
		paths = append(paths, resolved)
	}
	return paths
}
