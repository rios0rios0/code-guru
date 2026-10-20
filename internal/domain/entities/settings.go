package entities

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	configEntities "github.com/rios0rios0/gitforge/v4/pkg/config/domain/entities"
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
	// comments under (a service-account login / email). It drives TWO
	// behaviours, so read both before adding an entry:
	//
	//  1. Self-recognition. On the re-review path the bot uses these to
	//     recognise its OWN prior comments so it can read the dialogue
	//     (its earlier findings plus the author's replies) and resolve
	//     each thread instead of re-posting the same findings; the
	//     webhook handlers also use them to skip comments the bot itself
	//     authored, which would otherwise loop forever. The built-in
	//     GitHub App shape (`code-guru[bot]`) is always recognised, and
	//     the bot self-detects its identity from the author of its own
	//     PR-wide status annotations — so this is only required when
	//     neither of those covers the deployment.
	//
	//  2. Mention triggers. Each entry is also normalised into the
	//     `@`-mentions that request a re-review (see
	//     `support.HasMention`), because users @-mention the account they
	//     see on the PR rather than the built-in `@code-guru` — which
	//     stays accepted regardless. `code-guru[bot]` accepts
	//     `@code-guru`; `svc-codeguru@corp.example` accepts
	//     `@svc-codeguru`; an Azure DevOps identity GUID accepts the
	//     `@<guid>` markup the ADO comment box substitutes for an
	//     @-autocompleted mention.
	//
	// Because of (2), list ONLY accounts this bot posts under. A shared
	// automation account added here to quieten (1) becomes a live
	// re-review trigger: anyone typing its name in a PR comment starts a
	// full review. Use `Trivial.AutoMergeAllowedAuthors` for third-party
	// automation accounts instead.
	//
	// Honours `CODE_GURU_BOT_IDENTITIES` (comma-separated).
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
	// reviewed repository's own root guidelines file (`AGENTS.md`, else
	// `CLAUDE.md`) and forwards it to the
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

	// PRMetadata, when set, controls whether the bot fetches the pull
	// request's author-supplied metadata (description and commit count)
	// from the provider and forwards it to the LLM as intent context —
	// so the model can judge whether the diff actually does what the
	// title, branch name, and description claim, and flag undocumented
	// scope creep. The fetch is best-effort: an unsupported provider or
	// a fetch error never fails the review.
	//
	// Tri-state pointer so YAML / env "unset" can mean "use the default":
	// nil resolves to true via PullRequestMetadataEnabled (default ON).
	// Operators that want to opt out explicitly set
	// `pr_metadata: false` in YAML or CODE_GURU_AI_PR_METADATA=false.
	// Call sites should always read the resolved value via
	// PullRequestMetadataEnabled rather than dereferencing the pointer
	// directly.
	PRMetadata *bool `yaml:"pr_metadata"`

	// MaxGuidelinesBytes bounds how much of the reviewed repository's own
	// root guidelines file (`AGENTS.md`, else `CLAUDE.md`) is forwarded to
	// the LLM. Resolve via GuidelinesBytes()
	// (defaults to defaultMaxGuidelinesBytes when unset or non-positive).
	// Honours CODE_GURU_AI_MAX_GUIDELINES_BYTES.
	//
	// Lower this when the configured backend has a SMALL context window
	// (see the defaultMaxGuidelinesBytes doc): the default is sized for a
	// 1M-token window and a genuinely huge guidelines file could otherwise
	// crowd out the diff on a 128K/200K-token model.
	MaxGuidelinesBytes int `yaml:"max_guidelines_bytes"`

	// MaxPRDescriptionBytes bounds how much of the pull request's
	// description is forwarded to the LLM. Resolve via
	// PRDescriptionBytes() (defaults to defaultMaxPRDescriptionBytes when
	// unset or non-positive). Honours
	// CODE_GURU_AI_MAX_PR_DESCRIPTION_BYTES.
	MaxPRDescriptionBytes int `yaml:"max_pr_description_bytes"`

	// BatchLargeReviews, when set, controls whether a pull request that
	// does not fit the model's context window is reviewed in BATCHES —
	// its files split into chunks that fit, reviewed one after another,
	// and merged into a single review — instead of being abandoned with
	// a "too large, split your PR" notice. The batched run costs one
	// model call per batch and takes proportionally longer, which is why
	// it announces itself on the pull request.
	//
	// Tri-state pointer so YAML / env "unset" can mean "use the default":
	// nil resolves to true via BatchLargeReviewsEnabled (default ON).
	// Operators that want the old give-up behaviour — typically to cap
	// spend on a paid backend — explicitly set
	// `batch_large_reviews: false` in YAML or
	// CODE_GURU_AI_BATCH_LARGE_REVIEWS=false. Call sites should always
	// read the resolved value via BatchLargeReviewsEnabled rather than
	// dereferencing the pointer directly.
	BatchLargeReviews *bool `yaml:"batch_large_reviews"`

	// MaxReviewBatches caps how many batches a single batched review may
	// consume. Resolve via ReviewBatches() (defaults to
	// defaultMaxReviewBatches when unset or non-positive). Honours
	// CODE_GURU_AI_MAX_REVIEW_BATCHES.
	MaxReviewBatches int `yaml:"max_review_batches"`
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

// defaultMaxReviewBatches is the batch cap applied when
// AI.MaxReviewBatches is unset or non-positive.
//
// 20 sequential model calls is a deliberately generous ceiling: it covers a
// ~4 MB diff even on a 128K-token model (the smallest window any supported
// backend runs), while still bounding a pathological change — a vendored
// dependency drop, an accidental `node_modules` commit — to a review that
// finishes rather than one that runs for hours and costs proportionally.
// Files still queued when the cap is reached are reported as unreviewed in
// the merged summary, so the author is never told a truncated review was
// complete.
const defaultMaxReviewBatches = 20

// ReviewBatches resolves the per-review batch cap. An unset or non-positive
// MaxReviewBatches falls back to defaultMaxReviewBatches; an explicit value
// wins (raise it for very large repositories, lower it to cap spend).
func (a AIConfig) ReviewBatches() int {
	if a.MaxReviewBatches <= 0 {
		return defaultMaxReviewBatches
	}
	return a.MaxReviewBatches
}

// BatchLargeReviewsEnabled resolves the tri-state BatchLargeReviews pointer
// into a single boolean. nil (the YAML / env "unset" state) returns true so
// deployments that never wire the flag review oversized pull requests in
// batches automatically instead of skipping them; an explicit
// `batch_large_reviews: false` in YAML or
// `CODE_GURU_AI_BATCH_LARGE_REVIEWS=false` returns false, restoring the
// "post a too-large notice and review nothing" behaviour. Callers should
// always go through this helper rather than dereferencing the pointer.
func (a AIConfig) BatchLargeReviewsEnabled() bool {
	if a.BatchLargeReviews == nil {
		return true
	}
	return *a.BatchLargeReviews
}

// defaultMaxGuidelinesBytes is the guidelines budget applied when
// AI.MaxGuidelinesBytes is unset or non-positive. 1 MiB is roughly 256k
// tokens at ~4 bytes/token, i.e. about a QUARTER of a 1M-token context
// window, leaving the remaining ~75% for the diff and the rest of the
// prompt.
//
// It is a ceiling, not an allocation: real guidelines files are tens of
// kilobytes, so for almost every repository this bound never binds and the
// whole document is sent. The previous 32 KiB ceiling was sized for a 200K
// window and silently truncated large, well-maintained guidelines files
// mid-document — the model then judged the diff against a partial copy of
// the project's conventions, which is worse than a long prompt.
//
// SMALL-WINDOW BACKENDS: this default assumes a 1M-token window (Anthropic
// with `ai.anthropic.context_1m`, on by default, or the Claude CLI).
// Operators on a 128K-token model (e.g. OpenAI `gpt-4o`) or on Anthropic
// with the 1M beta disabled (200K) should lower `ai.max_guidelines_bytes`
// accordingly — a pathologically large guidelines file could otherwise
// consume the window the diff needs.
const defaultMaxGuidelinesBytes = 1024 * 1024

// GuidelinesBytes resolves the project-guidelines byte budget. An unset or
// non-positive MaxGuidelinesBytes falls back to defaultMaxGuidelinesBytes so
// existing deployments pick up the larger budget automatically; an explicit
// value wins (lower it for small-window backends).
func (a AIConfig) GuidelinesBytes() int {
	if a.MaxGuidelinesBytes <= 0 {
		return defaultMaxGuidelinesBytes
	}
	return a.MaxGuidelinesBytes
}

// defaultMaxPRDescriptionBytes is the description budget applied when
// AI.MaxPRDescriptionBytes is unset or non-positive. 64 KiB is roughly 16k
// tokens — four times the previous 16 KiB ceiling, so a thorough
// hand-written description survives intact, while still refusing to spend a
// meaningful share of the window on the release-bot descriptions that paste
// entire upstream changelogs into the body. Sized deliberately below the
// guidelines budget: a description is intent context, whereas the
// guidelines are the standard the diff is judged against.
const defaultMaxPRDescriptionBytes = 64 * 1024

// PRDescriptionBytes resolves the PR-description byte budget. An unset or
// non-positive MaxPRDescriptionBytes falls back to
// defaultMaxPRDescriptionBytes; an explicit value wins.
func (a AIConfig) PRDescriptionBytes() int {
	if a.MaxPRDescriptionBytes <= 0 {
		return defaultMaxPRDescriptionBytes
	}
	return a.MaxPRDescriptionBytes
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
// guidelines file (`AGENTS.md`, else `CLAUDE.md`) automatically; an explicit `project_guidelines: false` in YAML
// or `CODE_GURU_AI_PROJECT_GUIDELINES=false` returns false. Callers should
// always go through this helper rather than dereferencing the pointer.
func (a AIConfig) ProjectGuidelinesEnabled() bool {
	if a.ProjectGuidelines == nil {
		return true
	}
	return *a.ProjectGuidelines
}

// PullRequestMetadataEnabled resolves the tri-state PRMetadata pointer into a
// single boolean. nil (the YAML / env "unset" state) returns true so
// deployments that never wire the flag pick up the PR description / commit
// count context automatically; an explicit `pr_metadata: false` in YAML or
// `CODE_GURU_AI_PR_METADATA=false` returns false. Callers should always go
// through this helper rather than dereferencing the pointer.
func (a AIConfig) PullRequestMetadataEnabled() bool {
	if a.PRMetadata == nil {
		return true
	}
	return *a.PRMetadata
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

	// Mode selects how the Claude CLI reviews a pull request:
	//
	//   - `diff` (the default, also selected by an empty value) sends the
	//     assembled diff, rules and context to the model in ONE tool-less
	//     turn and reads a JSON review back. Cheap and fast, but the model
	//     sees nothing beyond the hunks it is handed.
	//   - `agentic` checks the pull request out into a disposable workspace,
	//     installs the configured Claude Code rules and skills, and runs
	//     Claude Code as a read-only agent over the checkout: it reads whole
	//     files, the git history and the repository's own guidelines, runs
	//     six review passes and returns findings scored for confidence. It
	//     takes minutes rather than seconds and needs `git` next to the CLI.
	//
	// Honours CODE_GURU_CLAUDE_MODE. Resolve via AgenticEnabled.
	Mode string `yaml:"mode"`

	// Agentic configures the `agentic` mode. Ignored in `diff` mode.
	Agentic ClaudeAgenticConfig `yaml:"agentic"`
}

// claudeModeAgentic and claudeModeDiff are the two accepted ClaudeConfig.Mode
// values; an empty Mode means claudeModeDiff.
const (
	claudeModeAgentic = "agentic"
	claudeModeDiff    = "diff"
)

// AgenticEnabled reports whether the Claude backend runs in `agentic` mode.
// The comparison ignores case and surrounding whitespace so a value rendered by
// a templating tool (`"Agentic "`) is not silently read as `diff`.
func (c ClaudeConfig) AgenticEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(c.Mode), claudeModeAgentic)
}

// ClaudeAgenticConfig configures the agentic Claude Code review: where the
// pull request is checked out, which Claude Code configuration is installed
// for the run, and the bounds every run is held to.
type ClaudeAgenticConfig struct {
	// ConfigPath is a directory laid out like a Claude Code configuration
	// directory, typically the tree `aisync` installs into `~/.claude`. Its
	// `skills/` and `commands/` are installed into the fresh, per-review
	// configuration directory, with every tool grant stripped from their
	// frontmatter, so the review can use the same skills a developer's own
	// Claude Code would. Its `rules/` is the rule corpus when `rules.path`
	// is empty; either way the rules selected for the pull request's
	// languages are embedded into the review prompt, never copied into the
	// configuration directory, where they would be loaded a second time.
	// Agents, settings, hooks, MCP servers and CLAUDE.md are never
	// installed, and symlinks are never followed. Environment variables are
	// expanded, as in `rules.path`. Honours
	// CODE_GURU_CLAUDE_AGENTIC_CONFIG_PATH.
	ConfigPath string `yaml:"config_path"`

	// WorkspaceDir is the directory each review's checkout is created
	// under. Empty means the OS temporary directory. Environment variables
	// are expanded. Honours CODE_GURU_CLAUDE_AGENTIC_WORKSPACE_DIR.
	WorkspaceDir string `yaml:"workspace_dir"`

	// CloneDepth limits how much history is fetched for the source and
	// target branches. 0 (the default) fetches their full history, which
	// the git-history review pass needs; a positive value fetches that
	// many commits and deepens on demand until the merge-base is found.
	// Honours CODE_GURU_CLAUDE_AGENTIC_CLONE_DEPTH.
	CloneDepth int `yaml:"clone_depth"`

	// Timeout caps one agentic run's wall-clock time. Resolve via
	// ReviewTimeout (defaults to defaultAgenticTimeout). It must stay
	// below any external deadline the run sits under — a Kubernetes Job's
	// `activeDeadlineSeconds`, for instance — so the bot can still post a
	// "review failed" notice before the process is killed. Honours
	// CODE_GURU_CLAUDE_AGENTIC_TIMEOUT.
	Timeout time.Duration `yaml:"timeout"`

	// MaxTurns caps the agent loop. Resolve via ReviewMaxTurns (defaults to
	// defaultAgenticMaxTurns). Honours CODE_GURU_CLAUDE_AGENTIC_MAX_TURNS.
	MaxTurns int `yaml:"max_turns"`

	// Effort is forwarded to the CLI's `--effort` flag (`low`, `medium`,
	// `high`, `xhigh` or `max`) when set; empty leaves the model default.
	// Honours CODE_GURU_CLAUDE_AGENTIC_EFFORT.
	Effort string `yaml:"effort"`

	// MaxBudgetUSD caps the spend of one run through the CLI's
	// `--max-budget-usd`; 0 (the default) sets no cap. Honours
	// CODE_GURU_CLAUDE_AGENTIC_MAX_BUDGET_USD.
	MaxBudgetUSD float64 `yaml:"max_budget_usd"`

	// FallbackModel is forwarded to the CLI's `--fallback-model`, used when
	// the primary model is overloaded. Empty disables it. Honours
	// CODE_GURU_CLAUDE_AGENTIC_FALLBACK_MODEL.
	FallbackModel string `yaml:"fallback_model"`

	// MinConfidence is the 0-100 score a finding must reach to be posted.
	// Findings below it are not discarded silently: they are listed, with
	// their score and reason, in the completion annotation. Resolve via
	// ConfidenceThreshold (defaults to defaultAgenticMinConfidence).
	// Honours CODE_GURU_CLAUDE_AGENTIC_MIN_CONFIDENCE.
	MinConfidence int `yaml:"min_confidence"`

	// SubprocessEnvScrub controls CLAUDE_CODE_SUBPROCESS_ENV_SCRUB, which
	// strips credentials from every subprocess the agent starts (its git
	// commands). The CLI runs that isolation through bubblewrap and refuses
	// to start without it, so nil resolves, through
	// SubprocessEnvScrubEnabled, to "wherever `bwrap` is installed": a
	// workstation that has it keeps the scrub, and a container image without
	// it — a hardened pod could not run bubblewrap anyway, having no user
	// namespaces — reviews without it, behind the read-only Bash allow-list,
	// the `/proc` read denials and the output redaction. `true` requires the
	// scrub, so a run without bubblewrap fails; `false` never asks for it.
	// Honours CODE_GURU_CLAUDE_AGENTIC_SUBPROCESS_ENV_SCRUB.
	SubprocessEnvScrub *bool `yaml:"subprocess_env_scrub"`
}

// defaultAgenticTimeout is the wall-clock budget of one agentic run when
// ClaudeAgenticConfig.Timeout is unset. Measured agentic reviews of ordinary
// pull requests finish in three to twelve minutes; thirty leaves room for a
// large change while still ending a run that has stopped making progress.
const defaultAgenticTimeout = 30 * time.Minute

// defaultAgenticMaxTurns bounds the agent loop when
// ClaudeAgenticConfig.MaxTurns is unset. A thorough review of a mid-sized pull
// request reads a few dozen files and runs a handful of git commands, well
// under this ceiling; the cap exists to stop a looping run, not to ration a
// healthy one.
const defaultAgenticMaxTurns = 200

// defaultAgenticMinConfidence is the confidence a finding must reach to be
// posted when ClaudeAgenticConfig.MinConfidence is unset. 80 is the bar the
// review prompt defines as "verified and likely hit in practice".
const defaultAgenticMinConfidence = 80

// maxConfidence is the top of the 0-100 confidence scale.
const maxConfidence = 100

// ResolvedConfigPath is ConfigPath with environment variables expanded, so a
// configuration file can say `${HOME}/.claude` the way `rules.path` can.
func (c ClaudeAgenticConfig) ResolvedConfigPath() string {
	return os.ExpandEnv(strings.TrimSpace(c.ConfigPath))
}

// ResolvedWorkspaceDir is WorkspaceDir with environment variables expanded.
func (c ClaudeAgenticConfig) ResolvedWorkspaceDir() string {
	return os.ExpandEnv(strings.TrimSpace(c.WorkspaceDir))
}

// ReviewTimeout resolves the agentic run's wall-clock budget.
func (c ClaudeAgenticConfig) ReviewTimeout() time.Duration {
	if c.Timeout <= 0 {
		return defaultAgenticTimeout
	}
	return c.Timeout
}

// ReviewMaxTurns resolves the agentic run's turn cap.
func (c ClaudeAgenticConfig) ReviewMaxTurns() int {
	if c.MaxTurns <= 0 {
		return defaultAgenticMaxTurns
	}
	return c.MaxTurns
}

// ConfidenceThreshold resolves the minimum confidence a finding needs to be
// posted, clamped to the 0-100 scale. An unset (zero) or negative value falls
// back to defaultAgenticMinConfidence.
func (c ClaudeAgenticConfig) ConfidenceThreshold() int {
	if c.MinConfidence <= 0 {
		return defaultAgenticMinConfidence
	}
	return min(c.MinConfidence, maxConfidence)
}

// SubprocessEnvScrubEnabled resolves SubprocessEnvScrub: an explicit value
// wins, and unset follows `bubblewrapAvailable`, which reports whether the
// isolation the CLI runs the scrub in is installed.
func (c ClaudeAgenticConfig) SubprocessEnvScrubEnabled(bubblewrapAvailable func() bool) bool {
	if c.SubprocessEnvScrub != nil {
		return *c.SubprocessEnvScrub
	}
	return bubblewrapAvailable()
}

// AnthropicConfig holds Anthropic API-specific settings.
type AnthropicConfig struct {
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`

	// Context1M, when set, controls whether the Anthropic backend requests
	// the 1M-token context window (the `context-1m-2025-08-07` beta) instead
	// of the default 200K. Enabling it lets pull requests up to ~5x larger be
	// reviewed in a single pass before they overflow the window and fail with
	// a "too large" annotation. For prompts under 200K tokens the beta is a
	// no-op, so it is safe on small PRs; very large prompts (200K–1M tokens)
	// may incur Anthropic long-context pricing, which is why it is toggleable.
	//
	// Tri-state pointer so YAML / env "unset" can mean "use the default":
	// nil resolves to true via Context1MEnabled (default ON). Operators whose
	// account or model does not support the beta can opt out with
	// `context_1m: false` in YAML or CODE_GURU_ANTHROPIC_CONTEXT_1M=false.
	// Call sites should always read the resolved value via Context1MEnabled
	// rather than dereferencing the pointer directly.
	Context1M *bool `yaml:"context_1m"`

	// RefusalFallbackModel, when set, is the Anthropic model the backend
	// re-issues the review against if the primary model declines the content
	// on content-safety grounds (`stop_reason: "refusal"`, common for
	// security-related code). Safety-classifier coverage varies by model, so a
	// fallback to a different model can produce a review where the primary
	// refused. Empty (the default) disables the fallback: a refusal is reported
	// as-is with the "content-safety declined" annotation. Honours
	// CODE_GURU_ANTHROPIC_REFUSAL_FALLBACK_MODEL.
	RefusalFallbackModel string `yaml:"refusal_fallback_model"`
}

// Context1MEnabled resolves the tri-state Context1M pointer into a single
// boolean. nil (the YAML / env "unset" state) returns true so deployments that
// never wire the flag pick up the larger context window automatically; an
// explicit `context_1m: false` in YAML or `CODE_GURU_ANTHROPIC_CONTEXT_1M=false`
// returns false. Callers should always go through this helper rather than
// dereferencing the pointer.
func (c AnthropicConfig) Context1MEnabled() bool {
	if c.Context1M == nil {
		return true
	}

	return *c.Context1M
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
	// DeleteSourceBranch, when set, controls whether an auto-merged trivial
	// PR also has its source branch deleted once the merge completes (gitforge's
	// `WithDeleteSourceBranch` merge option). Tri-state: nil resolves to
	// true via DeleteSourceBranchEnabled (default ON) — the common desire is a
	// clean branch list after the bot merges an automation PR. Only takes
	// effect when AutoMerge actually fires; without a merge there is no branch
	// to delete. Set `delete_source_branch: false` in YAML or
	// CODE_GURU_TRIVIAL_DELETE_SOURCE_BRANCH=false to keep the branch. Branch
	// deletion is best-effort in gitforge: a failure never fails the merge.
	DeleteSourceBranch *bool `yaml:"delete_source_branch"`
}

// DeleteSourceBranchEnabled resolves the tri-state DeleteSourceBranch pointer
// into a plain bool: an unset value (nil) defaults to true, so a trivial
// auto-merge deletes the source branch unless an operator explicitly opts out
// with `delete_source_branch: false` or CODE_GURU_TRIVIAL_DELETE_SOURCE_BRANCH=false.
// Callers should always go through this method rather than dereferencing the
// pointer so the default is applied consistently.
func (t TrivialConfig) DeleteSourceBranchEnabled() bool {
	if t.DeleteSourceBranch == nil {
		return true
	}
	return *t.DeleteSourceBranch
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

	// Executor selects where a webhook-triggered review runs once the
	// cheap gates have passed:
	//
	//   - `inprocess` (the default, also selected by an empty value) runs
	//     the whole review inside the server's own worker pool.
	//   - `kubernetes` keeps the gates (draft, review-once, trivial
	//     detection) in the server, then hands each review that needs the
	//     AI to its own Kubernetes Job, built from `kubernetes.job_template`.
	//     The Job's pod runs `code-guru review` for that one pull request
	//     and exits; Kubernetes deletes it afterwards. Concurrency is
	//     capped fleet-wide by `kubernetes.max_concurrent_jobs`.
	//
	// Honours CODE_GURU_SERVER_EXECUTOR. Resolve via KubernetesExecutorEnabled.
	Executor string `yaml:"executor"`

	// Kubernetes configures the `kubernetes` executor. Ignored otherwise.
	Kubernetes KubernetesExecutorConfig `yaml:"kubernetes"`
}

// serverExecutorKubernetes and serverExecutorInProcess are the two accepted
// ServerConfig.Executor values; an empty Executor means serverExecutorInProcess.
const (
	serverExecutorKubernetes = "kubernetes"
	serverExecutorInProcess  = "inprocess"
)

// KubernetesExecutorEnabled reports whether webhook reviews run as Kubernetes
// Jobs. Case and surrounding whitespace are ignored, as for AgenticEnabled.
func (s ServerConfig) KubernetesExecutorEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(s.Executor), serverExecutorKubernetes)
}

// KubernetesExecutorConfig configures the Kubernetes Job executor.
type KubernetesExecutorConfig struct {
	// Namespace is where review Jobs are created. Empty means the
	// namespace the server pod itself runs in. Honours
	// CODE_GURU_SERVER_KUBERNETES_NAMESPACE.
	Namespace string `yaml:"namespace"`

	// JobTemplate is the path of a `batch/v1` Job manifest (YAML or JSON)
	// every review Job is created from. The operator owns everything about
	// the pod — image, resources, volumes, security context, environment —
	// and the bot fills in only the per-review parts: the name, labels,
	// annotations, the container's arguments, and `spec.suspend`. Required
	// by the `kubernetes` executor. Honours
	// CODE_GURU_SERVER_KUBERNETES_JOB_TEMPLATE.
	JobTemplate string `yaml:"job_template"`

	// MaxConcurrentJobs caps how many review Jobs run at once across every
	// server replica. Further reviews wait, in arrival order, as suspended
	// Jobs and start as running ones finish — so a burst of pull requests
	// queues instead of exhausting the cluster or the AI quota. Resolve via
	// ConcurrentJobs (defaults to defaultMaxConcurrentReviewJobs). Honours
	// CODE_GURU_SERVER_KUBERNETES_MAX_CONCURRENT_JOBS.
	MaxConcurrentJobs int `yaml:"max_concurrent_jobs"`

	// PollInterval is how often the scheduler reconciles queued and failed
	// Jobs on top of the reconcile every enqueue triggers. Resolve via
	// SchedulerInterval (defaults to defaultReviewJobPollInterval). Honours
	// CODE_GURU_SERVER_KUBERNETES_POLL_INTERVAL.
	PollInterval time.Duration `yaml:"poll_interval"`
}

// defaultMaxConcurrentReviewJobs is the fleet-wide cap on running review Jobs
// when KubernetesExecutorConfig.MaxConcurrentJobs is unset.
const defaultMaxConcurrentReviewJobs = 3

// defaultReviewJobPollInterval is the scheduler's reconcile cadence when
// KubernetesExecutorConfig.PollInterval is unset. A queued Job starts within
// one interval of a running one finishing.
const defaultReviewJobPollInterval = 15 * time.Second

// ConcurrentJobs resolves the fleet-wide cap on running review Jobs.
func (k KubernetesExecutorConfig) ConcurrentJobs() int {
	if k.MaxConcurrentJobs <= 0 {
		return defaultMaxConcurrentReviewJobs
	}
	return k.MaxConcurrentJobs
}

// SchedulerInterval resolves the scheduler's reconcile cadence.
func (k KubernetesExecutorConfig) SchedulerInterval() time.Duration {
	if k.PollInterval <= 0 {
		return defaultReviewJobPollInterval
	}
	return k.PollInterval
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
	// Tri-state kill switch for the default-ON source-branch deletion: only an
	// explicit CODE_GURU_TRIVIAL_DELETE_SOURCE_BRANCH overrides the YAML value,
	// so an unset env var leaves whatever the config file said (or nil = ON).
	if v := parseOptionalBoolEnv("CODE_GURU_TRIVIAL_DELETE_SOURCE_BRANCH"); v != nil {
		settings.Trivial.DeleteSourceBranch = v
	}
	if ids := splitCSV(os.Getenv("CODE_GURU_BOT_IDENTITIES")); len(ids) > 0 {
		settings.BotIdentities = ids
	}
	applyNumericAIEnvOverrides(&settings.AI)
	// Kill switches for the default-ON AI features. Deployments commonly ship a
	// YAML baseline and flip per-environment behaviour via env (the same
	// argument as CODE_GURU_AI_MAX_ATTEMPTS above); without these overrides an
	// operator's opt-out on a YAML-configured pod would be silently ignored.
	applyTristateAIEnvOverrides(&settings.AI)

	if validateErr := validateSettings(&settings); validateErr != nil {
		return nil, validateErr
	}

	return &settings, nil
}

// applyNumericAIEnvOverrides applies the integer AI env overrides onto an
// AIConfig already populated from YAML: the per-review attempt budget and the
// two prompt budgets. A deployment commonly ships a YAML baseline and tunes
// these per environment — a small-context-window backend, for instance, must
// be able to lower the guidelines budget without re-rendering its YAML.
//
// Each override fires only when the value parses to a POSITIVE integer, so a
// typo'd or empty variable leaves the YAML value untouched rather than zeroing
// the budget (which the resolvers would then read as "unset" anyway, but the
// explicit guard keeps the YAML value intact for round-tripping).
//
// Extracted from NewSettings so that function stays within the
// cognitive-complexity budget as the override list grows, mirroring
// applyTristateAIEnvOverrides below.
func applyNumericAIEnvOverrides(ai *AIConfig) {
	overrides := map[string]*int{
		"CODE_GURU_AI_MAX_ATTEMPTS":             &ai.MaxAttempts,
		"CODE_GURU_AI_MAX_GUIDELINES_BYTES":     &ai.MaxGuidelinesBytes,
		"CODE_GURU_AI_MAX_PR_DESCRIPTION_BYTES": &ai.MaxPRDescriptionBytes,
		"CODE_GURU_AI_MAX_REVIEW_BATCHES":       &ai.MaxReviewBatches,
	}
	for envVar, target := range overrides {
		raw := strings.TrimSpace(os.Getenv(envVar))
		if raw == "" {
			continue
		}
		if v, parseErr := strconv.Atoi(raw); parseErr == nil && v > 0 {
			*target = v
		}
	}
}

// applyTristateAIEnvOverrides applies the env kill switches for the default-ON
// tri-state AI features onto an AIConfig already populated from YAML. Each
// override fires only when the env var parses to a bool; nil (unset or
// unparseable) leaves the YAML value untouched. Extracted from NewSettings so
// that function stays within the cognitive-complexity budget as the tri-state
// list grows.
func applyTristateAIEnvOverrides(ai *AIConfig) {
	if v := parseOptionalBoolEnv("CODE_GURU_AI_PROJECT_GUIDELINES"); v != nil {
		ai.ProjectGuidelines = v
	}
	if v := parseOptionalBoolEnv("CODE_GURU_AI_PR_METADATA"); v != nil {
		ai.PRMetadata = v
	}
	if v := parseOptionalBoolEnv("CODE_GURU_AI_BATCH_LARGE_REVIEWS"); v != nil {
		ai.BatchLargeReviews = v
	}
	if v := parseOptionalBoolEnv("CODE_GURU_ANTHROPIC_CONTEXT_1M"); v != nil {
		ai.Anthropic.Context1M = v
	}
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
	maxAttempts, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CODE_GURU_AI_MAX_ATTEMPTS")))
	maxGuidelines, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CODE_GURU_AI_MAX_GUIDELINES_BYTES")))
	maxPRDescription, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CODE_GURU_AI_MAX_PR_DESCRIPTION_BYTES")))
	maxReviewBatches, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CODE_GURU_AI_MAX_REVIEW_BATCHES")))
	appID, _ := strconv.ParseInt(os.Getenv("CODE_GURU_GITHUB_APP_ID"), 10, 64)

	adapters := parseTrivialAdaptersEnv()

	settings := &Settings{
		AI: AIConfig{
			Backend: envOrDefault("CODE_GURU_BACKEND", "openai"),
			OpenAI: OpenAIConfig{
				APIKey: os.Getenv("CODE_GURU_OPENAI_API_KEY"),
				Model:  envOrDefault("CODE_GURU_OPENAI_MODEL", "gpt-4o"),
			},
			Claude: claudeConfigFromEnv(),
			Anthropic: AnthropicConfig{
				APIKey:               os.Getenv("CODE_GURU_ANTHROPIC_API_KEY"),
				Model:                envOrDefault("CODE_GURU_ANTHROPIC_MODEL", "claude-sonnet-4-20250514"),
				Context1M:            parseOptionalBoolEnv("CODE_GURU_ANTHROPIC_CONTEXT_1M"),
				RefusalFallbackModel: os.Getenv("CODE_GURU_ANTHROPIC_REFUSAL_FALLBACK_MODEL"),
			},
			SubmitNativeReview:    parseOptionalBoolEnv("CODE_GURU_AI_SUBMIT_NATIVE_REVIEW"),
			ReviewDrafts:          parseBoolEnv("CODE_GURU_AI_REVIEW_DRAFTS", false),
			MaxAttempts:           maxAttempts,
			ProjectGuidelines:     parseOptionalBoolEnv("CODE_GURU_AI_PROJECT_GUIDELINES"),
			PRMetadata:            parseOptionalBoolEnv("CODE_GURU_AI_PR_METADATA"),
			MaxGuidelinesBytes:    maxGuidelines,
			MaxPRDescriptionBytes: maxPRDescription,
			BatchLargeReviews:     parseOptionalBoolEnv("CODE_GURU_AI_BATCH_LARGE_REVIEWS"),
			MaxReviewBatches:      maxReviewBatches,
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
			DeleteSourceBranch:      parseOptionalBoolEnv("CODE_GURU_TRIVIAL_DELETE_SOURCE_BRANCH"),
		},
		Server: serverConfigFromEnv(),
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

// claudeConfigFromEnv builds the Claude CLI settings, agentic mode included,
// from CODE_GURU_CLAUDE_* variables. Split out of NewSettingsFromEnv so that
// function stays within the length budget as the env surface grows.
func claudeConfigFromEnv() ClaudeConfig {
	maxTurns, _ := strconv.Atoi(envOrDefault("CODE_GURU_CLAUDE_MAX_TURNS", "1"))
	cloneDepth, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CODE_GURU_CLAUDE_AGENTIC_CLONE_DEPTH")))
	agenticTimeout, _ := time.ParseDuration(strings.TrimSpace(os.Getenv("CODE_GURU_CLAUDE_AGENTIC_TIMEOUT")))
	agenticMaxTurns, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CODE_GURU_CLAUDE_AGENTIC_MAX_TURNS")))
	maxBudget, _ := strconv.ParseFloat(strings.TrimSpace(os.Getenv("CODE_GURU_CLAUDE_AGENTIC_MAX_BUDGET_USD")), 64)
	minConfidence, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CODE_GURU_CLAUDE_AGENTIC_MIN_CONFIDENCE")))

	return ClaudeConfig{
		BinaryPath: envOrDefault("CODE_GURU_CLAUDE_BINARY_PATH", "claude"),
		Model:      envOrDefault("CODE_GURU_CLAUDE_MODEL", "sonnet"),
		MaxTurns:   maxTurns,
		Mode:       strings.TrimSpace(os.Getenv("CODE_GURU_CLAUDE_MODE")),
		Agentic: ClaudeAgenticConfig{
			ConfigPath:    strings.TrimSpace(os.Getenv("CODE_GURU_CLAUDE_AGENTIC_CONFIG_PATH")),
			WorkspaceDir:  strings.TrimSpace(os.Getenv("CODE_GURU_CLAUDE_AGENTIC_WORKSPACE_DIR")),
			CloneDepth:    cloneDepth,
			Timeout:       agenticTimeout,
			MaxTurns:      agenticMaxTurns,
			Effort:        strings.TrimSpace(os.Getenv("CODE_GURU_CLAUDE_AGENTIC_EFFORT")),
			MaxBudgetUSD:  maxBudget,
			FallbackModel: strings.TrimSpace(os.Getenv("CODE_GURU_CLAUDE_AGENTIC_FALLBACK_MODEL")),
			MinConfidence: minConfidence,
			SubprocessEnvScrub: parseOptionalBoolEnv(
				"CODE_GURU_CLAUDE_AGENTIC_SUBPROCESS_ENV_SCRUB",
			),
		},
	}
}

// serverConfigFromEnv builds the webhook server settings, the Kubernetes
// executor included, from CODE_GURU_SERVER_* (and CODE_GURU_PORT /
// CODE_GURU_WEBHOOK_SECRET) variables. Split out of NewSettingsFromEnv for the
// same reason as claudeConfigFromEnv.
func serverConfigFromEnv() ServerConfig {
	port, _ := strconv.Atoi(envOrDefault("CODE_GURU_PORT", "8080"))
	queueSize, _ := strconv.Atoi(envOrDefault("CODE_GURU_SERVER_QUEUE_SIZE", "100"))
	workers, _ := strconv.Atoi(envOrDefault("CODE_GURU_SERVER_WORKERS", strconv.Itoa(runtime.NumCPU())))
	shutdownTimeout, _ := time.ParseDuration(envOrDefault("CODE_GURU_SERVER_SHUTDOWN_TIMEOUT", "30s"))
	maxJobs, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("CODE_GURU_SERVER_KUBERNETES_MAX_CONCURRENT_JOBS")))
	pollInterval, _ := time.ParseDuration(strings.TrimSpace(os.Getenv("CODE_GURU_SERVER_KUBERNETES_POLL_INTERVAL")))

	return ServerConfig{
		Port:                 port,
		WebhookSecret:        os.Getenv("CODE_GURU_WEBHOOK_SECRET"),
		QueueSize:            queueSize,
		Workers:              workers,
		ShutdownTimeout:      shutdownTimeout,
		AllowedOrganizations: splitCSV(os.Getenv("CODE_GURU_SERVER_ALLOWED_ORGANIZATIONS")),
		AllowedProjects:      splitCSV(os.Getenv("CODE_GURU_SERVER_ALLOWED_PROJECTS")),
		AllowedSourceCIDRs:   splitCSV(os.Getenv("CODE_GURU_SERVER_ALLOWED_SOURCE_CIDRS")),
		Executor:             strings.TrimSpace(os.Getenv("CODE_GURU_SERVER_EXECUTOR")),
		Kubernetes: KubernetesExecutorConfig{
			Namespace:         strings.TrimSpace(os.Getenv("CODE_GURU_SERVER_KUBERNETES_NAMESPACE")),
			JobTemplate:       strings.TrimSpace(os.Getenv("CODE_GURU_SERVER_KUBERNETES_JOB_TEMPLATE")),
			MaxConcurrentJobs: maxJobs,
			PollInterval:      pollInterval,
		},
	}
}

func validateSettings(settings *Settings) error {
	if settings.AI.Backend == "" {
		return errors.New("ai.backend is required (openai, claude, or anthropic)")
	}

	if err := validateModes(settings); err != nil {
		return err
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

// validateModes rejects an unrecognised `ai.claude.mode` or `server.executor`.
// Both default to their conservative value when empty, so the only way to get
// here is a typo — and silently treating `agentc` as `diff` would leave an
// operator believing the agentic review is running when it is not.
func validateModes(settings *Settings) error {
	validClaudeModes := map[string]bool{"": true, claudeModeDiff: true, claudeModeAgentic: true}
	if mode := strings.ToLower(strings.TrimSpace(settings.AI.Claude.Mode)); !validClaudeModes[mode] {
		return fmt.Errorf("ai.claude.mode %q is not supported (valid: diff, agentic)", settings.AI.Claude.Mode)
	}
	if settings.AI.Claude.AgenticEnabled() && settings.AI.Backend != "claude" {
		return fmt.Errorf("ai.claude.mode agentic requires ai.backend claude, got %q", settings.AI.Backend)
	}

	validExecutors := map[string]bool{"": true, serverExecutorInProcess: true, serverExecutorKubernetes: true}
	if executor := strings.ToLower(strings.TrimSpace(settings.Server.Executor)); !validExecutors[executor] {
		return fmt.Errorf(
			"server.executor %q is not supported (valid: inprocess, kubernetes)",
			settings.Server.Executor,
		)
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
