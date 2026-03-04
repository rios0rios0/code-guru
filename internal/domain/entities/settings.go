package entities

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	logger "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// Settings is the top-level configuration for code-guru, loaded from YAML.
type Settings struct {
	Providers []ProviderConfig `yaml:"providers"`
	AI        AIConfig         `yaml:"ai"`
	Rules     RulesConfig      `yaml:"rules"`
}

// ProviderConfig describes a single Git hosting provider instance.
type ProviderConfig struct {
	Type          string   `yaml:"type"`
	Token         string   `yaml:"token"`
	Organizations []string `yaml:"organizations"`
}

// AIConfig holds settings for the AI review backend.
type AIConfig struct {
	Backend string       `yaml:"backend"`
	OpenAI  OpenAIConfig `yaml:"openai"`
	Claude  ClaudeConfig `yaml:"claude"`
}

// OpenAIConfig holds OpenAI-specific settings.
type OpenAIConfig struct {
	APIKey string `yaml:"api_key"` //nolint:gosec // G117: field name matches secret pattern but this is a config struct, not a secret value
	Model  string `yaml:"model"`
}

// ClaudeConfig holds Claude CLI-specific settings.
type ClaudeConfig struct {
	BinaryPath string `yaml:"binary_path"`
	Model      string `yaml:"model"`
	MaxTurns   int    `yaml:"max_turns"`
}

// RulesConfig configures where review rules are loaded from.
type RulesConfig struct {
	Path       string   `yaml:"path"`
	Categories []string `yaml:"categories"`
}

// envVarPattern matches ${VAR_NAME} placeholders.
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)}`)

// NewSettings reads and parses a configuration file, expanding environment variables.
func NewSettings(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}

	var settings Settings
	if unmarshalErr := yaml.Unmarshal(data, &settings); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", unmarshalErr)
	}

	for i := range settings.Providers {
		settings.Providers[i].Token = resolveToken(settings.Providers[i].Token)
	}
	settings.AI.OpenAI.APIKey = resolveToken(settings.AI.OpenAI.APIKey)

	if validateErr := validateSettings(&settings); validateErr != nil {
		return nil, validateErr
	}

	return &settings, nil
}

// FindConfigFile searches for a configuration file in standard locations.
func FindConfigFile() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}

	locations := []string{
		".",
		".config",
		"configs",
	}
	if homeDir != "" {
		locations = append(
			locations,
			homeDir,
			filepath.Join(homeDir, ".config"),
		)
	}

	patterns := []string{
		".code-guru.yaml",
		".code-guru.yml",
		"code-guru.yaml",
		"code-guru.yml",
	}

	for _, loc := range locations {
		for _, pat := range patterns {
			p := filepath.Join(loc, pat)
			if _, statErr := os.Stat(p); statErr == nil {
				return p, nil
			}
		}
	}

	return "", errors.New("config file not found in default locations")
}

func resolveToken(raw string) string {
	if raw == "" {
		return raw
	}

	resolved := envVarPattern.ReplaceAllStringFunc(raw, func(match string) string {
		varName := envVarPattern.FindStringSubmatch(match)[1]
		if val := os.Getenv(varName); val != "" {
			return val
		}
		logger.Warnf("environment variable %q is not set", varName)
		return ""
	})

	if _, statErr := os.Stat(resolved); statErr == nil {
		data, readErr := os.ReadFile(resolved)
		if readErr != nil {
			logger.Warnf("failed to read token file: %v", readErr)
			return resolved
		}
		return strings.TrimSpace(string(data))
	}

	return resolved
}

func validateSettings(settings *Settings) error {
	if settings.AI.Backend == "" {
		return errors.New("ai.backend is required (openai or claude)")
	}

	if settings.AI.Backend == "openai" && settings.AI.OpenAI.APIKey == "" {
		return errors.New("ai.openai.api_key is required when backend is openai")
	}

	return nil
}
