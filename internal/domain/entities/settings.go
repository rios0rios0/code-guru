package entities

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	configEntities "github.com/rios0rios0/gitforge/pkg/config/domain/entities"
	"gopkg.in/yaml.v3"
)

// Settings is the top-level configuration for code-guru, loaded from YAML.
type Settings struct {
	Providers []ProviderConfig `yaml:"providers"`
	AI        AIConfig         `yaml:"ai"`
	Rules     RulesConfig      `yaml:"rules"`
	Trivial   TrivialConfig    `yaml:"trivial"`
	Server    ServerConfig     `yaml:"server"`
	GitHubApp GitHubAppConfig  `yaml:"github_app"`
}

// ProviderConfig is an alias for gitforge's ProviderConfig to maintain backward compatibility.
type ProviderConfig = configEntities.ProviderConfig

// AIConfig holds settings for the AI review backend.
type AIConfig struct {
	Backend   string          `yaml:"backend"`
	OpenAI    OpenAIConfig    `yaml:"openai"`
	Claude    ClaudeConfig    `yaml:"claude"`
	Anthropic AnthropicConfig `yaml:"anthropic"`
}

// OpenAIConfig holds OpenAI-specific settings.
type OpenAIConfig struct {
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
}

// ClaudeConfig holds Claude CLI-specific settings.
type ClaudeConfig struct {
	BinaryPath string `yaml:"binary_path"`
	Model      string `yaml:"model"`
	MaxTurns   int    `yaml:"max_turns"`
}

// AnthropicConfig holds Anthropic API-specific settings.
type AnthropicConfig struct {
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
}

// RulesConfig configures where review rules are loaded from.
type RulesConfig struct {
	Path       string   `yaml:"path"`
	Categories []string `yaml:"categories"`
}

// TrivialConfig configures trivial PR detection.
type TrivialConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Adapters []string `yaml:"adapters"`
}

// ServerConfig holds settings for the webhook server.
type ServerConfig struct {
	Port          int    `yaml:"port"`
	WebhookSecret string `yaml:"webhook_secret"`
}

// GitHubAppConfig holds GitHub App authentication settings.
type GitHubAppConfig struct {
	AppID      int64  `yaml:"app_id"`
	PrivateKey string `yaml:"private_key"`
}

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
		settings.Providers[i].Token = settings.Providers[i].ResolveToken()
	}
	settings.AI.OpenAI.APIKey = (&configEntities.ProviderConfig{Token: settings.AI.OpenAI.APIKey}).ResolveToken()
	settings.AI.Anthropic.APIKey = (&configEntities.ProviderConfig{Token: settings.AI.Anthropic.APIKey}).ResolveToken()

	if validateErr := validateSettings(&settings); validateErr != nil {
		return nil, validateErr
	}

	return &settings, nil
}

// NewSettingsFromEnv builds settings entirely from environment variables.
func NewSettingsFromEnv() (*Settings, error) {
	maxTurns, _ := strconv.Atoi(envOrDefault("CODE_GURU_CLAUDE_MAX_TURNS", "1"))
	port, _ := strconv.Atoi(envOrDefault("CODE_GURU_PORT", "8080"))
	appID, _ := strconv.ParseInt(os.Getenv("CODE_GURU_GITHUB_APP_ID"), 10, 64)

	var adapters []string
	if raw := os.Getenv("CODE_GURU_TRIVIAL_ADAPTERS"); raw != "" {
		adapters = strings.Split(raw, ",")
	}

	settings := &Settings{
		AI: AIConfig{
			Backend: envOrDefault("CODE_GURU_BACKEND", "openai"),
			OpenAI: OpenAIConfig{
				APIKey: os.Getenv("CODE_GURU_OPENAI_API_KEY"),
				Model:  envOrDefault("CODE_GURU_OPENAI_MODEL", "gpt-4o"),
			},
			Claude: ClaudeConfig{
				BinaryPath: envOrDefault("CODE_GURU_CLAUDE_BINARY_PATH", "claude"),
				Model:      envOrDefault("CODE_GURU_CLAUDE_MODEL", "sonnet"),
				MaxTurns:   maxTurns,
			},
			Anthropic: AnthropicConfig{
				APIKey: os.Getenv("CODE_GURU_ANTHROPIC_API_KEY"),
				Model:  envOrDefault("CODE_GURU_ANTHROPIC_MODEL", "claude-sonnet-4-20250514"),
			},
		},
		Rules: RulesConfig{
			Path: os.Getenv("CODE_GURU_RULES_PATH"),
		},
		Trivial: TrivialConfig{
			Enabled:  len(adapters) > 0,
			Adapters: adapters,
		},
		Server: ServerConfig{
			Port:          port,
			WebhookSecret: os.Getenv("CODE_GURU_WEBHOOK_SECRET"),
		},
		GitHubApp: GitHubAppConfig{
			AppID:      appID,
			PrivateKey: os.Getenv("CODE_GURU_GITHUB_PRIVATE_KEY"),
		},
	}

	if token := os.Getenv("CODE_GURU_PROVIDER_TOKEN"); token != "" {
		settings.Providers = []ProviderConfig{{Token: token}}
	}

	if err := validateSettings(settings); err != nil {
		return nil, err
	}

	return settings, nil
}

func validateSettings(settings *Settings) error {
	if settings.AI.Backend == "" {
		return errors.New("ai.backend is required (openai, claude, or anthropic)")
	}

	if settings.AI.Backend == "openai" && settings.AI.OpenAI.APIKey == "" {
		return errors.New("ai.openai.api_key is required when backend is openai")
	}

	if settings.AI.Backend == "anthropic" && settings.AI.Anthropic.APIKey == "" {
		return errors.New("ai.anthropic.api_key is required when backend is anthropic")
	}

	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
