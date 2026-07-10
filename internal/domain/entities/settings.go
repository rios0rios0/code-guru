package entities

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

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

	// BotIdentities lists the account identities code-guru posts review
	// comments under (a service-account login / email). On the re-review
	// path the bot uses these to recognise its OWN prior comments so it
	// can read the dialogue (its earlier findings plus the author's
	// replies) and resolve each thread instead of re-posting the same
	// findings. The built-in GitHub App shape (`code-guru[bot]`) is
	// always recognised, and the bot also self-detects its identity from
	// the author of its own PR-wide status annotations — so this is only
	// required when neither of those covers the deployment. Honours
	// `CODE_GURU_BOT_IDENTITIES` (comma-separated).
	BotIdentities []string `yaml:"bot_identities"`
}

// ProviderConfig is an alias for gitforge's ProviderConfig to maintain backward compatibility.
type ProviderConfig = configEntities.ProviderConfig

// AIConfig holds settings for the AI review backend.
type AIConfig struct {
	Backend   string          `yaml:"backend"`
	OpenAI    OpenAIConfig    `yaml:"openai"`
	Claude    ClaudeConfig    `yaml:"claude"`
	Anthropic AnthropicConfig `yaml:"anthropic"`

	// SubmitNativeReview, when set, controls whether the bot also records
	// a native pull request review (Approved / Changes Requested) on
	// GitHub or Azure DevOps. Comment-only verdicts are not submitted as
	// native reviews (support.MapVerdictToReview returns ok=false for
	// them). The native review surfaces the verdict in the platform's
	// reviewer panel.
	//
	// Tri-state pointer so YAML / env "unset" can mean "use the default":
	// nil resolves to true via NativeReviewSubmissionEnabled (default ON).
	// Operators that want to opt out explicitly set
	// `submit_native_review: false` in YAML or
	// CODE_GURU_AI_SUBMIT_NATIVE_REVIEW=false. Call sites should always
	// read the resolved value via NativeReviewSubmissionEnabled rather
	// than dereferencing the pointer directly.
	SubmitNativeReview *bool `yaml:"submit_native_review"`

	// ReviewDrafts, when true, lets the bot review draft PRs as well. By
	// default draft PRs are skipped — most teams treat drafts as
	// work-in-progress that should not consume review budget. Override via
	// CODE_GURU_AI_REVIEW_DRAFTS=true.
	ReviewDrafts bool `yaml:"review_drafts"`

	// MaxAttempts is the total number of times the AI backend is invoked
	// per review before giving up (1 = no retry). LLM output is non-
	// deterministic, so a re-sample usually turns a non-JSON or transient-
	// error response (e.g. the claude CLI's dropped-socket error) into a
	// clean review — retrying avoids a "review failed" annotation on the PR
	// for what is almost always a recoverable blip. Resolve via
	// ReviewAttempts() (defaults to 3 when unset). Honours
	// CODE_GURU_AI_MAX_ATTEMPTS.
	MaxAttempts int `yaml:"max_attempts"`

	// ProjectGuidelines, when set, controls whether the bot loads the
	// reviewed repository's own root `CLAUDE.md` and forwards it to the
	// LLM as project-specific review context, so the review honours the
	// project's own conventions in addition to the operator-configured
	// rules. Works on every provider that supports API file access
	// (GitHub, Azure DevOps); the fetch is best-effort — a missing file
	// or a provider error never fails the review.
	//
	// Tri-state pointer so YAML / env "unset" can mean "use the default":
	// nil resolves to true via ProjectGuidelinesEnabled (default ON).
	// Operators that want to opt out explicitly set
	// `project_guidelines: false` in YAML or
	// CODE_GURU_AI_PROJECT_GUIDELINES=false. Call sites should always
	// read the resolved value via ProjectGuidelinesEnabled rather than
	// dereferencing the pointer directly.
	ProjectGuidelines *bool `yaml:"project_guidelines"`
}

// defaultReviewAttempts is the attempt budget applied when AI.MaxAttempts is
// unset or non-positive. 3 (one initial call plus two retries) clears the
// overwhelming majority of the transient / non-JSON failures observed in
// production while bounding worst-case latency to roughly 3x a single review.
const defaultReviewAttempts = 3

// ReviewAttempts resolves the per-review AI attempt budget. An unset or
// non-positive MaxAttempts falls back to defaultReviewAttempts so existing
// deployments pick up retries automatically; an explicit value (e.g.
// `max_attempts: 1` to disable retries, or a higher value for a flaky
// backend) wins.
func (a AIConfig) ReviewAttempts() int {
	if a.MaxAttempts <= 0 {
		return defaultReviewAttempts
	}
	return a.MaxAttempts
}

// NativeReviewSubmissionEnabled resolves the tri-state SubmitNativeReview
// pointer into a single boolean. nil (the YAML / env "unset" state) returns
// true so deployments that never wire the flag pick up the new default
// behaviour automatically; an explicit `submit_native_review: false` in YAML
// or `CODE_GURU_AI_SUBMIT_NATIVE_REVIEW=false` returns false. Callers should
// always go through this helper rather than dereferencing the pointer.
func (a AIConfig) NativeReviewSubmissionEnabled() bool {
	if a.SubmitNativeReview == nil {
		return true
	}
	return *a.SubmitNativeReview
}

// ProjectGuidelinesEnabled resolves the tri-state ProjectGuidelines pointer
// into a single boolean. nil (the YAML / env "unset" state) returns true so
// deployments that never wire the flag pick up the reviewed repository's
// CLAUDE.md automatically; an explicit `project_guidelines: false` in YAML
// or `CODE_GURU_AI_PROJECT_GUIDELINES=false` returns false. Callers should
// always go through this helper rather than dereferencing the pointer.
func (a AIConfig) ProjectGuidelinesEnabled() bool {
	if a.ProjectGuidelines == nil {
		return true
	}
	return *a.ProjectGuidelines
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
	// AutoMerge, when true, calls the provider's merge endpoint after a
	// trivial-approve verdict. Off by default — this bypasses human
	// review and merges cross-system, so the gate is "operator must
	// explicitly opt in". Honours `CODE_GURU_TRIVIAL_AUTO_MERGE`.
	AutoMerge bool `yaml:"auto_merge"`
	// MergeStrategy is the gitforge merge strategy applied when
	// AutoMerge fires (`"merge"` / `"squash"` / `"rebase"`). Empty
	// falls back to the platform default. Honours
	// `CODE_GURU_TRIVIAL_MERGE_STRATEGY`.
	MergeStrategy string `yaml:"merge_strategy"`
	// BypassPolicy, when true, asks the provider to skip branch
	// policies (`Required reviewers`, `Minimum approver count`, etc.)
	// when AutoMerge fires. Off by default — bypass strictly requires
	// the bot's identity to hold the platform-level
	// `Bypass policies when completing pull requests` permission, so
	// turning this on without that permission turns previously-working
	// auto-merges into hard 403s. Operators in environments where the
	// bot has merge permission but NOT bypass permission should leave
	// this off and let `Required reviewers` policies remain
	// authoritative. Honours `CODE_GURU_TRIVIAL_BYPASS_POLICIES`.
	BypassPolicy bool `yaml:"bypass_policy"`
	// AutoMergeAllowedAuthors restricts auto-merge to PRs opened by one of
	// the listed account identities (e.g. the autobump / autoupdate /
	// config-automation service account). Triviality decides whether a PR
	// is eligible to auto-merge; this allowlist decides whether its author
	// is trusted to merge unattended. When non-empty, only matching authors
	// auto-merge — a human's docs PR is approved but left for a human to
	// merge. When empty, auto-merge falls back to "any author" for
	// backward compatibility (not recommended with BypassPolicy, which then
	// force-merges every trivial PR past `Required reviewers`). Honours
	// `CODE_GURU_TRIVIAL_AUTO_MERGE_AUTHORS` (comma-separated).
	AutoMergeAllowedAuthors []string `yaml:"auto_merge_allowed_authors"`
}

// ServerConfig holds settings for the webhook server.
type ServerConfig struct {
	Port                 int           `yaml:"port"`
	WebhookSecret        string        `yaml:"webhook_secret"`
	QueueSize            int           `yaml:"queue_size"`
	Workers              int           `yaml:"workers"`
	ShutdownTimeout      time.Duration `yaml:"shutdown_timeout"`
	AllowedOrganizations []string      `yaml:"allowed_organizations"`
	AllowedProjects      []string      `yaml:"allowed_projects"`
	AllowedSourceCIDRs   []string      `yaml:"allowed_source_cidrs"`
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
	settings.AI.OpenAI.APIKey = configEntities.ResolveToken(settings.AI.OpenAI.APIKey)
	settings.AI.Anthropic.APIKey = configEntities.ResolveToken(settings.AI.Anthropic.APIKey)
	// Webhook server fields support the same ${ENV_VAR}/file-path expansion as
	// provider tokens. Resolve them here so the serve command can read literals
	// like "${CODE_GURU_WEBHOOK_SECRET}" from YAML and have them expanded before
	// reaching the auth/JWT code paths.
	settings.Server.WebhookSecret = configEntities.ResolveToken(settings.Server.WebhookSecret)
	settings.GitHubApp.PrivateKey = configEntities.ResolveToken(settings.GitHubApp.PrivateKey)

	// Env vars take precedence over YAML for the small set of fields
	// where deployments commonly use a config file as a baseline and
	// override per-environment via env. Expand with discipline —
	// every override here erodes the "config file is authoritative"
	// guarantee, so only add fields that genuinely need it.
	if envAdapters := parseTrivialAdaptersEnv(); len(envAdapters) > 0 {
		settings.Trivial.Enabled = true
		settings.Trivial.Adapters = envAdapters
	}
	if raw := strings.TrimSpace(os.Getenv("CODE_GURU_TRIVIAL_AUTO_MERGE")); raw != "" {
		if v, parseErr := strconv.ParseBool(raw); parseErr == nil {
			settings.Trivial.AutoMerge = v
		}
	}
	if raw := strings.TrimSpace(os.Getenv("CODE_GURU_TRIVIAL_MERGE_STRATEGY")); raw != "" {
		settings.Trivial.MergeStrategy = raw
	}
	if raw := strings.TrimSpace(os.Getenv("CODE_GURU_TRIVIAL_BYPASS_POLICIES")); raw != "" {
		if v, parseErr := strconv.ParseBool(raw); parseErr == nil {
			settings.Trivial.BypassPolicy = v
		}
	}
	if authors := splitCSV(os.Getenv("CODE_GURU_TRIVIAL_AUTO_MERGE_AUTHORS")); len(authors) > 0 {
		settings.Trivial.AutoMergeAllowedAuthors = authors
	}
	if ids := splitCSV(os.Getenv("CODE_GURU_BOT_IDENTITIES")); len(ids) > 0 {
		settings.BotIdentities = ids
	}
	if raw := strings.TrimSpace(os.Getenv("CODE_GURU_AI_MAX_ATTEMPTS")); raw != "" {
		if v, parseErr := strconv.Atoi(raw); parseErr == nil && v > 0 {
			settings.AI.MaxAttempts = v
		}
	}

	if validateErr := validateSettings(&settings); validateErr != nil {
		return nil, validateErr
	}

	return &settings, nil
}

// parseTrivialAdaptersEnv parses `CODE_GURU_TRIVIAL_ADAPTERS` into a
// trimmed slice of adapter names. Returns nil when unset or empty.
// Lives at the package level so `NewSettings` (YAML path) and
// `NewSettingsFromEnv` (env-only path) share the same parsing.
func parseTrivialAdaptersEnv() []string {
	raw := os.Getenv("CODE_GURU_TRIVIAL_ADAPTERS")
	if raw == "" {
		return nil
	}
	var adapters []string
	for a := range strings.SplitSeq(raw, ",") {
		if trimmed := strings.TrimSpace(a); trimmed != "" {
			adapters = append(adapters, trimmed)
		}
	}
	return adapters
}

// NewSettingsFromEnv builds settings entirely from environment variables.
func NewSettingsFromEnv() (*Settings, error) {
	maxTurns, _ := strconv.Atoi(envOrDefault("CODE_GURU_CLAUDE_MAX_TURNS", "1"))
	maxAttempts, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CODE_GURU_AI_MAX_ATTEMPTS")))
	port, _ := strconv.Atoi(envOrDefault("CODE_GURU_PORT", "8080"))
	appID, _ := strconv.ParseInt(os.Getenv("CODE_GURU_GITHUB_APP_ID"), 10, 64)
	queueSize, _ := strconv.Atoi(envOrDefault("CODE_GURU_SERVER_QUEUE_SIZE", "100"))
	workers, _ := strconv.Atoi(envOrDefault("CODE_GURU_SERVER_WORKERS", strconv.Itoa(runtime.NumCPU())))
	shutdownTimeout, _ := time.ParseDuration(envOrDefault("CODE_GURU_SERVER_SHUTDOWN_TIMEOUT", "30s"))

	adapters := parseTrivialAdaptersEnv()

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
			SubmitNativeReview: parseOptionalBoolEnv("CODE_GURU_AI_SUBMIT_NATIVE_REVIEW"),
			ReviewDrafts:       parseBoolEnv("CODE_GURU_AI_REVIEW_DRAFTS", false),
			MaxAttempts:        maxAttempts,
			ProjectGuidelines:  parseOptionalBoolEnv("CODE_GURU_AI_PROJECT_GUIDELINES"),
		},
		Rules: RulesConfig{
			Path: os.Getenv("CODE_GURU_RULES_PATH"),
		},
		Trivial: TrivialConfig{
			Enabled:                 len(adapters) > 0,
			Adapters:                adapters,
			AutoMerge:               parseBoolEnv("CODE_GURU_TRIVIAL_AUTO_MERGE", false),
			MergeStrategy:           strings.TrimSpace(os.Getenv("CODE_GURU_TRIVIAL_MERGE_STRATEGY")),
			BypassPolicy:            parseBoolEnv("CODE_GURU_TRIVIAL_BYPASS_POLICIES", false),
			AutoMergeAllowedAuthors: splitCSV(os.Getenv("CODE_GURU_TRIVIAL_AUTO_MERGE_AUTHORS")),
		},
		Server: ServerConfig{
			Port:                 port,
			WebhookSecret:        os.Getenv("CODE_GURU_WEBHOOK_SECRET"),
			QueueSize:            queueSize,
			Workers:              workers,
			ShutdownTimeout:      shutdownTimeout,
			AllowedOrganizations: splitCSV(os.Getenv("CODE_GURU_SERVER_ALLOWED_ORGANIZATIONS")),
			AllowedProjects:      splitCSV(os.Getenv("CODE_GURU_SERVER_ALLOWED_PROJECTS")),
			AllowedSourceCIDRs:   splitCSV(os.Getenv("CODE_GURU_SERVER_ALLOWED_SOURCE_CIDRS")),
		},
		GitHubApp: GitHubAppConfig{
			AppID:      appID,
			PrivateKey: os.Getenv("CODE_GURU_GITHUB_PRIVATE_KEY"),
		},
		BotIdentities: splitCSV(os.Getenv("CODE_GURU_BOT_IDENTITIES")),
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

	validBackends := map[string]bool{
		"openai":    true,
		"claude":    true,
		"anthropic": true,
	}
	if !validBackends[settings.AI.Backend] {
		return fmt.Errorf("ai.backend %q is not supported (valid: openai, claude, anthropic)", settings.AI.Backend)
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

// parseBoolEnv reads a boolean environment variable. Truthy values are the
// strconv defaults (`1`, `t`, `true` — case-insensitive); any non-empty value
// the parser rejects falls back to the provided default rather than panicking,
// so a typo does not silently flip behaviour. Surrounding whitespace is
// trimmed before parsing so values shipped via Helm/templating (which often
// leave a trailing newline or space, e.g. `"false "`) parse correctly.
func parseBoolEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

// parseOptionalBoolEnv reads a tri-state boolean env var. An unset variable
// (or one that is whitespace-only after trimming) returns nil so downstream
// resolvers (e.g. NativeReviewSubmissionEnabled) can apply their default. A
// set-but-unparseable value also returns nil so a typo does not silently
// flip behaviour — operators see the default instead. Truthy values follow
// the [strconv.ParseBool] defaults. Surrounding whitespace is trimmed before
// parsing so Helm-rendered values like `"false "` survive the round-trip and
// the operator's explicit opt-out is honoured (without trimming, ParseBool
// would reject the trailing space and the resolver would fall back to the
// default ON, which is exactly the silent flip this branch tries to avoid).
func parseOptionalBoolEnv(key string) *bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}
	return &parsed
}

// splitCSV parses a comma-separated string into a slice, trimming whitespace and skipping empties.
func splitCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for v := range strings.SplitSeq(raw, ",") {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
