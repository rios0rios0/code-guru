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

// subprocessEnvScrubEnv strips credentials from the environment of every
// subprocess the agent starts (its git commands included) and runs each one in
// a sandbox with its own PID namespace and /proc, so none can read the CLI's
// environment either; the CLI keeps the credentials for its own API calls.
// With it set the CLI switches `--permission-mode dontAsk` back to `default`
// (and says so on stderr); `--permission-prompts none` is what keeps that
// deny-by-default, so neither flag may be dropped on its own. The sandbox needs
// scrubSandboxTools, so the scrub is set only where
// ClaudeAgenticConfig.SubprocessEnvScrubEnabled says so.
const subprocessEnvScrubEnv = "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1"

// scrubSandboxTools are the programs the CLI's subprocess sandbox needs. With
// bubblewrap missing the CLI refuses to start; with socat missing it starts,
// and then every command the agent runs fails ("Sandbox dependencies not
// available: socat not installed"), so a review would lose its git history
// pass without saying so.
//
//nolint:gochecknoglobals // read-only list used as a constant
var scrubSandboxTools = []string{"bwrap", "socat"}

// scrubSandboxInstalled reports whether every scrubSandboxTools program is
// installed where the CLI, which inherits this process's PATH, looks for it.
func scrubSandboxInstalled() bool {
	for _, tool := range scrubSandboxTools {
		if _, err := exec.LookPath(tool); err != nil {
			return false
		}
	}
	return true
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

	// scrubSandboxAvailable reports whether the subprocess environment
	// scrub can run; see ClaudeAgenticConfig.SubprocessEnvScrub.
	scrubSandboxAvailable func() bool

	// lookupEnv reads this process's environment, where the model
	// credential and the passthrough variables come from.
	lookupEnv func(string) (string, bool)
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
		binaryPath:            binaryPath,
		model:                 model,
		config:                config,
		configSource:          config.ResolvedConfigPath(),
		scrubSandboxAvailable: scrubSandboxInstalled,
		lookupEnv:             os.LookupEnv,
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
	scrub, err := r.resolveSubprocessEnvScrub(request.PullRequest.ID)
	if err != nil {
		return nil, err
	}
	handoff, err := handOverCredentials(r.lookupEnv)
	if err != nil {
		return nil, err
	}
	// The CLI holds its own copies once it has started; this process's copies
	// are closed then, or here when it never starts.
	defer handoff.Close()
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

	started := time.Now()
	logger.Infof(
		"PR #%d: starting the agentic review (model: %s, timeout: %s, max turns: %d, subprocess env scrub: %t)",
		request.PullRequest.ID,
		r.model,
		r.config.ReviewTimeout(),
		r.config.ReviewMaxTurns(),
		scrub,
	)
	stdout, stderr, runErr := r.run(
		runCtx,
		workspace.Path,
		sandbox,
		scrub,
		handoff,
		support.BuildAgenticUserPrompt(request),
	)
	if runCtx.Err() != nil && ctx.Err() == nil {
		return nil, fmt.Errorf(
			"%w: the run exceeded its %s timeout",
			support.ErrAgenticReviewIncomplete,
			r.config.ReviewTimeout(),
		)
	}
	result, err := r.interpret(stdout, stderr, runErr)
	if err != nil {
		return nil, err
	}

	support.ApplyConfidenceThreshold(result, minConfidence)
	support.RedactSecrets(result, r.credentialValues()...)
	logger.Infof("PR #%d: agentic review finished in %s (%d finding(s), %d dropped)",
		request.PullRequest.ID, time.Since(started).Round(time.Second), len(result.Comments), len(result.Dropped))
	return result, nil
}

// resolveSubprocessEnvScrub decides whether the CLI runs the subprocess
// environment scrub. Where it cannot run and the operator left the setting
// unset, the review goes ahead only if no credential would sit in the CLI's
// environment — the OAuth token and the API key never do, they are handed over
// by descriptor — and is refused otherwise: an exposure is the operator's call
// (`subprocess_env_scrub: false`), never a default.
func (r *AgenticReviewerRepository) resolveSubprocessEnvScrub(prID int) (bool, error) {
	scrub := r.config.SubprocessEnvScrubEnabled(r.scrubSandboxAvailable)
	if scrub {
		return true, nil
	}
	exposed := r.setEnvironmentCredentials()
	switch {
	case len(exposed) == 0:
		if r.config.SubprocessEnvScrub == nil {
			logger.Infof("PR #%d: the agent runs without CLAUDE_CODE_SUBPROCESS_ENV_SCRUB, whose sandbox needs "+
				"bubblewrap and socat; no model credential travels in the CLI's environment (an OAuth token or an "+
				"API key is handed over by descriptor), so the commands the agent runs cannot read one", prID)
		}
		return false, nil
	case r.config.SubprocessEnvScrub != nil:
		logger.Warnf("PR #%d: ai.claude.agentic.subprocess_env_scrub is false, so %s reaches the CLI through its "+
			"environment, which every command the agent runs can read", prID, strings.Join(exposed, ", "))
		return false, nil
	default:
		return false, fmt.Errorf("%w: %s reaches the claude CLI only through its environment, and the "+
			"subprocess environment scrub that keeps it from the agent's commands needs bubblewrap and socat, "+
			"which are not both installed. Give the CLI CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY instead "+
			"(both are handed over by descriptor), install bubblewrap and socat where user namespaces are "+
			"allowed, or set ai.claude.agentic.subprocess_env_scrub to false to accept the exposure",
			support.ErrAgentCredentialUnprotected, strings.Join(exposed, ", "))
	}
}

// setEnvironmentCredentials names the environment-only credentials (see
// environmentCredentialNames) this process has a value for.
func (r *AgenticReviewerRepository) setEnvironmentCredentials() []string {
	var set []string
	for _, name := range environmentCredentialNames() {
		if value, _ := r.lookupEnv(name); strings.TrimSpace(value) != "" {
			set = append(set, name)
		}
	}
	return set
}

// run starts the CLI in the checkout, hands it the descriptors in `handoff`,
// feeds it the user prompt and waits for it.
func (r *AgenticReviewerRepository) run(
	runCtx context.Context, dir string, sandbox *agentSandbox, scrub bool, handoff *credentialHandoff, prompt string,
) ([]byte, []byte, error) {
	//nolint:gosec // G204: the binary path is trusted configuration; every argument is a fixed flag or a value the bot produced
	cmd := exec.CommandContext(runCtx, r.binaryPath, r.args(sandbox)...)
	cmd.Dir = dir
	cmd.Env = r.environment(sandbox, scrub, handoff.Env())
	cmd.ExtraFiles = handoff.Files()
	// When the timeout kills the CLI, a child it started (a git command)
	// can still hold the output pipes open; without a bound, Wait would
	// wait for that child instead of returning the timeout.
	cmd.WaitDelay = agentWaitDelay
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Start()
	if runErr == nil {
		handoff.Close()
		runErr = cmd.Wait()
	}
	return stdout.Bytes(), stderr.Bytes(), runErr
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

// environment builds the CLI's environment from allowlists — the environment
// credentials (environmentCredentialNames), agentPassthroughEnv and
// agentHardeningEnv — plus the sandbox's own home, configuration and temp
// directories and `descriptors`, the `*_FILE_DESCRIPTOR` variables that name
// the credentials handed over by descriptor. Nothing else of the server's
// environment reaches the run: not the pod's home directory, not the provider
// token, the webhook secret or any other `CODE_GURU_*` value. `scrub` adds the
// subprocess environment scrub.
func (r *AgenticReviewerRepository) environment(sandbox *agentSandbox, scrub bool, descriptors []string) []string {
	env := []string{
		"HOME=" + sandbox.homeDir,
		"CLAUDE_CONFIG_DIR=" + sandbox.configDir,
		"TMPDIR=" + sandbox.tmpDir,
	}
	for _, name := range slices.Concat(environmentCredentialNames(), agentPassthroughEnv) {
		if value, ok := r.lookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	env = append(env, agentHardeningEnv...)
	env = append(env, descriptors...)
	if scrub {
		env = append(env, subprocessEnvScrubEnv)
	}
	return env
}

// credentialValues returns the values of every model credential the CLI could
// have been given, for redaction.
func (r *AgenticReviewerRepository) credentialValues() []string {
	var values []string
	for _, name := range credentialNames() {
		if value, _ := r.lookupEnv(name); value != "" {
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
