package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/support"
)

// agentToolset is the built-in tool set offered to the agent (`--tools`).
// Grep and Glob must be named explicitly — on Linux they are not part of the
// CLI's default set — and Skill must be named for the installed skills to be
// invocable at all.
const agentToolset = "Read,Grep,Glob,Bash,Skill"

// incompleteSubtypes are the CLI result subtypes that mean the run stopped at
// one of its bounds rather than failed: the turn cap, the spend cap, or the
// structured-output retry cap.
//
//nolint:gochecknoglobals // read-only lookup table used as a constant
var incompleteSubtypes = map[string]bool{
	"error_max_turns":                     true,
	"error_max_budget_usd":                true,
	"error_max_structured_output_retries": true,
}

// agentCredentialEnv are the variables the agent's CLI process may inherit to
// authenticate (or reach) the model, and nothing else of the server's own
// environment: the provider token, the webhook secret and every other
// `CODE_GURU_*` value stay behind. They are also the values redacted from the
// review before it is posted.
//
//nolint:gochecknoglobals // read-only list used as a constant
var agentCredentialEnv = []string{
	"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
}

// agentPassthroughEnv are the non-secret variables the CLI process inherits:
// the locale, proxy and CA settings of the host, and the endpoint and model
// pins a deployment behind an LLM gateway needs (the CLI otherwise asks the
// gateway for its own default models by name, which a gateway may not serve).
//
//nolint:gochecknoglobals // read-only list used as a constant
var agentPassthroughEnv = []string{
	"PATH", "LANG", "LC_ALL",
	"ANTHROPIC_BASE_URL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL",
	"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
}

// agentHardeningEnv switches off every way the CLI could pick up
// instructions, tools or credentials from somewhere the bot did not put them,
// and keeps git from reading configuration or paging.
//
//nolint:gochecknoglobals // read-only list used as a constant
var agentHardeningEnv = []string{
	// No CLAUDE.md, AGENTS.md or auto-memory is loaded as instructions: the
	// repository's own guidance is read deliberately, as data, in pass one.
	"CLAUDE_CODE_DISABLE_CLAUDE_MDS=1",
	"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1",
	// claude.ai connectors attached to a subscription account must not
	// become tools of an unattended review.
	"ENABLE_CLAUDEAI_MCP_SERVERS=false",
	"DISABLE_AUTOUPDATER=1",
	"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_CONFIG_GLOBAL=" + os.DevNull,
	"GIT_TERMINAL_PROMPT=0",
	"GIT_OPTIONAL_LOCKS=0",
	"GIT_PAGER=cat",
	"PAGER=cat",
}

// subprocessEnvScrubEnv strips credentials from every subprocess the agent
// starts (its git commands included); the CLI keeps them for its own API
// calls. With it set the CLI switches `--permission-mode dontAsk` back to
// `default` (and says so on stderr); `--permission-prompts none` is what keeps
// that deny-by-default, so neither flag may be dropped on its own. The CLI
// runs the scrub inside bubblewrap and refuses to start without it, so it is
// set only where ClaudeAgenticConfig.SubprocessEnvScrubEnabled says so.
const subprocessEnvScrubEnv = "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1"

// bubblewrapBinary is the isolation tool the CLI needs to run the scrub.
const bubblewrapBinary = "bwrap"

// bubblewrapOnPath reports whether bubblewrap is installed where the CLI,
// which inherits this process's PATH, looks for it.
func bubblewrapOnPath() bool {
	_, err := exec.LookPath(bubblewrapBinary)
	return err == nil
}

// agentFailureLogLimit caps each captured stream quoted in an error.
const agentFailureLogLimit = 4096

// agentDenialLogLimit caps how many permission denials are named in the log.
const agentDenialLogLimit = 5

// agentRawLogLimit caps the raw CLI result logged at debug level.
const agentRawLogLimit = 64 * 1024

// agentWaitDelay bounds how long a killed CLI's leftover children may keep its
// pipes open before Run gives up on them.
const agentWaitDelay = 10 * time.Second

// AgenticReviewerRepository implements the AI reviewer as a Claude Code agent
// working over a checkout of the pull request (see
// repositories.WorkspaceReviewer). Each review runs the CLI headless in a
// sandbox prepared next to the checkout, with a read-only tool surface, and
// reads back a review validated against support.AgenticReviewSchema.
type AgenticReviewerRepository struct {
	binaryPath   string
	model        string
	config       entities.ClaudeAgenticConfig
	configSource string

	// bubblewrapAvailable reports whether the subprocess environment scrub
	// can run; see ClaudeAgenticConfig.SubprocessEnvScrub.
	bubblewrapAvailable func() bool
}

// NewAgenticReviewerRepository builds the agentic reviewer. `configSource` is
// the operator's Claude configuration tree whose skills are installed into
// each run (empty installs none).
func NewAgenticReviewerRepository(
	binaryPath, model string, config entities.ClaudeAgenticConfig,
) *AgenticReviewerRepository {
	if binaryPath == "" {
		binaryPath = defaultBinary
	}
	if model == "" {
		model = defaultModel
	}
	return &AgenticReviewerRepository{
		binaryPath:          binaryPath,
		model:               model,
		config:              config,
		configSource:        config.ResolvedConfigPath(),
		bubblewrapAvailable: bubblewrapOnPath,
	}
}

// Name returns the backend identifier.
func (r *AgenticReviewerRepository) Name() string {
	return backendName
}

// RequiresWorkspace marks this reviewer as one that reads a checkout.
func (r *AgenticReviewerRepository) RequiresWorkspace() bool {
	return true
}

// ReviewDiff runs one agentic review over `request.Workspace`.
func (r *AgenticReviewerRepository) ReviewDiff(
	ctx context.Context,
	request entities.ReviewRequest,
) (*entities.ReviewResult, error) {
	workspace := request.Workspace
	if workspace == nil || workspace.Path == "" || workspace.Root == "" {
		return nil, errors.New("the agentic reviewer needs a checkout of the pull request")
	}
	minConfidence := r.config.ConfidenceThreshold()

	sandbox, err := prepareAgentSandbox(
		workspace.Root, r.configSource, support.BuildAgenticSystemPrompt(request, minConfidence),
	)
	if err != nil {
		return nil, err
	}

	// The server process holds the provider token. Making it non-dumpable
	// before the agent starts keeps a same-user child from reading it out of
	// /proc/<pid>/environ or /proc/<pid>/mem.
	support.HardenAgainstChildInspection()

	runCtx, cancel := context.WithTimeout(ctx, r.config.ReviewTimeout())
	defer cancel()

	scrub := r.config.SubprocessEnvScrubEnabled(r.bubblewrapAvailable)
	if !scrub && r.config.SubprocessEnvScrub == nil {
		logger.Warnf("PR #%d: bubblewrap (`%s`) is not installed, so the agent runs without "+
			"CLAUDE_CODE_SUBPROCESS_ENV_SCRUB: its git commands inherit the model credential, behind the "+
			"read-only Bash allow-list, the /proc read denials and the output redaction. Install bubblewrap, "+
			"or set ai.claude.agentic.subprocess_env_scrub to decide explicitly",
			request.PullRequest.ID, bubblewrapBinary)
	}

	//nolint:gosec // G204: the binary path is trusted configuration; every argument is a fixed flag or a value the bot produced
	cmd := exec.CommandContext(runCtx, r.binaryPath, r.args(sandbox)...)
	cmd.Dir = workspace.Path
	cmd.Env = r.environment(sandbox, scrub)
	// When the timeout kills the CLI, a child it started (a git command)
	// can still hold the output pipes open; without a bound, Run would
	// wait for that child instead of returning the timeout.
	cmd.WaitDelay = agentWaitDelay
	cmd.Stdin = strings.NewReader(support.BuildAgenticUserPrompt(request))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	started := time.Now()
	logger.Infof(
		"PR #%d: starting the agentic review (model: %s, timeout: %s, max turns: %d, subprocess env scrub: %t)",
		request.PullRequest.ID,
		r.model,
		r.config.ReviewTimeout(),
		r.config.ReviewMaxTurns(),
		scrub,
	)
	runErr := cmd.Run()

	if runCtx.Err() != nil && ctx.Err() == nil {
		return nil, fmt.Errorf(
			"%w: the run exceeded its %s timeout",
			support.ErrAgenticReviewIncomplete,
			r.config.ReviewTimeout(),
		)
	}
	result, err := r.interpret(stdout.Bytes(), stderr.Bytes(), runErr)
	if err != nil {
		return nil, err
	}

	support.ApplyConfidenceThreshold(result, minConfidence)
	support.RedactSecrets(result, credentialValues()...)
	logger.Infof("PR #%d: agentic review finished in %s (%d finding(s), %d dropped)",
		request.PullRequest.ID, time.Since(started).Round(time.Second), len(result.Comments), len(result.Dropped))
	return result, nil
}

// args builds the CLI invocation. The tool and permission rules are given
// both here and in the settings file: a settings file that fails validation
// is silently ignored in print mode, and the command line is not.
func (r *AgenticReviewerRepository) args(sandbox *agentSandbox) []string {
	args := []string{
		"--print",
		"--model", r.model,
		"--output-format", "json",
		"--json-schema", support.AgenticReviewSchema,
		"--max-turns", strconv.Itoa(r.config.ReviewMaxTurns()),
		"--tools", agentToolset,
		"--allowedTools", strings.Join(agentAllowedTools, ","),
		"--disallowedTools", strings.Join(agentDeniedTools, ","),
		"--permission-mode", "dontAsk",
		"--permission-prompts", "none",
		// Only the user source: the sandbox configuration directory the bot
		// wrote. The checkout's `.claude/settings*.json` (hooks, env,
		// permissions, apiKeyHelper), its `.mcp.json` and its skills are
		// untrusted input and never load.
		"--setting-sources", "user",
		"--settings", sandbox.settingsPath,
		"--strict-mcp-config",
		"--no-session-persistence",
		"--append-system-prompt-file", sandbox.systemPromptPath,
	}
	if effort := strings.TrimSpace(r.config.Effort); effort != "" {
		args = append(args, "--effort", effort)
	}
	if r.config.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(r.config.MaxBudgetUSD, 'f', -1, 64))
	}
	if fallback := strings.TrimSpace(r.config.FallbackModel); fallback != "" {
		args = append(args, "--fallback-model", fallback)
	}
	return args
}

// environment builds the CLI's environment from allowlists (see the
// agent*Env lists) plus the sandbox's own home, configuration and temp
// directories, so the run sees neither the pod's home directory nor any of
// the server's secrets. `scrub` adds the subprocess environment scrub.
func (r *AgenticReviewerRepository) environment(sandbox *agentSandbox, scrub bool) []string {
	env := []string{
		"HOME=" + sandbox.homeDir,
		"CLAUDE_CONFIG_DIR=" + sandbox.configDir,
		"TMPDIR=" + sandbox.tmpDir,
	}
	for _, name := range slices.Concat(agentCredentialEnv, agentPassthroughEnv) {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	env = append(env, agentHardeningEnv...)
	if scrub {
		env = append(env, subprocessEnvScrubEnv)
	}
	return env
}

// credentialValues returns the values of the credential variables the agent
// could have seen, for redaction.
func credentialValues() []string {
	var values []string
	for _, name := range agentCredentialEnv {
		if value := os.Getenv(name); value != "" {
			values = append(values, value)
		}
	}
	return values
}

// agentEnvelope is the CLI's `--output-format json` result object.
type agentEnvelope struct {
	Subtype           string            `json:"subtype"`
	IsError           bool              `json:"is_error"`
	Result            string            `json:"result"`
	StructuredOutput  json.RawMessage   `json:"structured_output"`
	NumTurns          int               `json:"num_turns"`
	TotalCostUSD      float64           `json:"total_cost_usd"`
	PermissionDenials []json.RawMessage `json:"permission_denials"`
	Errors            []json.RawMessage `json:"errors"`
}

// interpret turns the CLI's output into a review or a classified error.
func (r *AgenticReviewerRepository) interpret(stdout, stderr []byte, runErr error) (*entities.ReviewResult, error) {
	detail := func() string {
		return fmt.Sprintf("stderr: %s; stdout: %s",
			support.TruncateBytesForLog(stderr, agentFailureLogLimit),
			support.TruncateBytesForLog(stdout, agentFailureLogLimit))
	}

	// The raw answer is diagnostic material only: it may quote the diff, so
	// it is logged at debug level and never posted.
	logger.WithField("stdout", support.TruncateBytesForLog(stdout, agentRawLogLimit)).
		Debug("agentic review: raw CLI result (gated behind DEBUG)")

	var envelope agentEnvelope
	if jsonErr := json.Unmarshal(bytes.TrimSpace(stdout), &envelope); jsonErr != nil {
		if runErr != nil {
			if looksLikeContextWindowFailure(stdout) || looksLikeContextWindowFailure(stderr) {
				return nil, fmt.Errorf(
					"%w (claude CLI failed: %w; %s)",
					support.ErrContextWindowExceeded,
					runErr,
					detail(),
				)
			}
			return nil, fmt.Errorf("claude CLI failed: %w (%s)", runErr, detail())
		}
		return nil, fmt.Errorf(
			"%w: the claude CLI printed no result object (%s)",
			support.ErrUnparseableResponse,
			detail(),
		)
	}
	logRunStats(envelope)

	switch {
	case incompleteSubtypes[envelope.Subtype]:
		return nil, fmt.Errorf(
			"%w (%s after %d turn(s))",
			support.ErrAgenticReviewIncomplete,
			envelope.Subtype,
			envelope.NumTurns,
		)
	case envelope.IsError || runErr != nil:
		if looksLikeContextWindowFailure(stdout) || looksLikeContextWindowFailure(stderr) {
			return nil, fmt.Errorf("%w (%s)", support.ErrContextWindowExceeded, detail())
		}
		return nil, fmt.Errorf(
			"claude CLI reported an error (subtype %q, exit: %w; %s)",
			envelope.Subtype,
			runErr,
			detail(),
		)
	case len(bytes.TrimSpace(envelope.StructuredOutput)) > 0 && string(envelope.StructuredOutput) != "null":
		return support.ParseAgenticReviewResponse(string(envelope.StructuredOutput))
	case strings.TrimSpace(envelope.Result) != "":
		// A success without structured output should not happen with
		// --json-schema; parse the final text as a fallback rather than
		// lose a finished review.
		logger.Warn("agentic review: the CLI returned no structured output; parsing the final text instead")
		return support.ParseAgenticReviewResponse(envelope.Result)
	default:
		return nil, fmt.Errorf(
			"%w: the agentic review returned neither structured output nor text",
			support.ErrUnparseableResponse,
		)
	}
}

// logRunStats records what the run cost and, when the agent was refused tools,
// which ones — a burst of denials is the first sign the settings file was
// rejected or the allowlist no longer matches what the prompt asks for.
func logRunStats(envelope agentEnvelope) {
	fields := logger.Fields{
		"subtype":            envelope.Subtype,
		"turns":              envelope.NumTurns,
		"cost_usd":           envelope.TotalCostUSD,
		"permission_denials": len(envelope.PermissionDenials),
	}
	if len(envelope.PermissionDenials) > 0 {
		var names []string
		for i, denial := range envelope.PermissionDenials {
			if i == agentDenialLogLimit {
				break
			}
			var parsed struct {
				ToolName string `json:"tool_name"`
			}
			if json.Unmarshal(denial, &parsed) == nil && parsed.ToolName != "" {
				names = append(names, parsed.ToolName)
			}
		}
		fields["denied_tools"] = strings.Join(names, ",")
	}
	logger.WithFields(fields).Info("agentic review: CLI run finished")
}
