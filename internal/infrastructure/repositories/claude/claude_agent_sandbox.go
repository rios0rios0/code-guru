package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	logger "github.com/sirupsen/logrus"
)

// The agent sandbox is the per-review Claude Code environment: a fresh
// configuration directory, home and temp directory next to the checkout (never
// inside it, so the agent's file tools — confined to the checkout — cannot
// read them), the operator's skills installed into it, and the settings that
// pin down what the agent may do. Nothing is shared between reviews, and
// nothing of the pod's own home directory is visible to the run.

const (
	sandboxDirName    = "agent"
	sandboxConfigDir  = "config"
	sandboxHomeDir    = "home"
	sandboxTmpDir     = "tmp"
	settingsFileName  = "settings.json"
	systemPromptName  = "system-prompt.md"
	sandboxDirMode    = 0o700
	sandboxFileMode   = 0o600
	maxInstalledBytes = 16 << 20
)

// installedConfigDirs are the parts of an operator's Claude configuration tree
// that are copied into the sandbox. Skills and commands only:
//
//   - rules are not copied, because the review prompt embeds the rules the
//     review command selected for the pull request's languages — the same
//     selection the diff path uses — and a second, auto-loaded copy would put
//     every rule into the context twice;
//   - agents are not copied, because the review runs with no subagent tool;
//   - settings, hooks, MCP servers and credentials are never copied: the
//     sandbox's settings are written by the bot, below, and nothing an
//     operator's tree says may widen them.
//
//nolint:gochecknoglobals // read-only list used as a constant
var installedConfigDirs = []string{"skills", "commands"}

// agentAllowedTools are the tool rules the agent is pre-approved for. Bash is
// limited to read-only git commands, written in the `:*` suffix form (the
// documented equivalent of a trailing ` *`). They are passed on the command
// line as ONE comma-joined argument; the CLI splits rules on commas and on
// spaces outside parentheses, so the space inside `git diff` stays in its
// rule.
//
//nolint:gochecknoglobals // read-only list used as a constant
var agentAllowedTools = []string{
	"Read", "Grep", "Glob", "Skill",
	"Bash(git diff:*)", "Bash(git log:*)", "Bash(git show:*)", "Bash(git blame:*)",
	"Bash(git status:*)", "Bash(git ls-files:*)", "Bash(git rev-parse:*)", "Bash(git merge-base:*)",
}

// agentDeniedTools are whole tools the agent must never use, passed on the
// command line so they hold even if the settings file were rejected.
//
//nolint:gochecknoglobals // read-only list used as a constant
var agentDeniedTools = []string{
	"Agent", "Task", "Edit", "Write", "NotebookEdit", "WebFetch", "WebSearch",
}

// agentDeniedPatterns close the ways an allowed git command could read outside
// the checkout or write a file: `--no-index` and `blame --contents` read any
// path, `--output` writes one, an external diff or textconv driver executes a
// program, and a global option before the subcommand (`git -c …`, `git -C …`,
// `git -p`) changes what git runs. Deny rules win over allow rules. They live
// in the settings file only: their wildcards sit mid-pattern, which a JSON
// array carries unambiguously.
//
//nolint:gochecknoglobals // read-only list used as a constant
var agentDeniedPatterns = []string{
	"Bash(git -*)",
	"Bash(git *--no-index*)",
	"Bash(git *--contents*)",
	"Bash(git *--output*)",
	"Bash(git *--ext-diff*)",
	"Bash(git *--textconv*)",
	"Bash(*/proc/*)",
	"Bash(*/sys/*)",
	"Read(//proc/**)",
	"Read(//sys/**)",
	"Read(//etc/**)",
}

// agentSandbox is one review's prepared sandbox.
type agentSandbox struct {
	root             string
	configDir        string
	homeDir          string
	tmpDir           string
	settingsPath     string
	systemPromptPath string
}

// prepareAgentSandbox creates the sandbox under `parent` (the workspace root),
// installs the operator's skills from `configSource` (optional) and writes the
// settings and the system prompt.
func prepareAgentSandbox(parent, configSource, systemPrompt string) (*agentSandbox, error) {
	root := filepath.Join(parent, sandboxDirName)
	sandbox := &agentSandbox{
		root:             root,
		configDir:        filepath.Join(root, sandboxConfigDir),
		homeDir:          filepath.Join(root, sandboxHomeDir),
		tmpDir:           filepath.Join(root, sandboxTmpDir),
		settingsPath:     filepath.Join(root, settingsFileName),
		systemPromptPath: filepath.Join(root, systemPromptName),
	}
	for _, dir := range []string{sandbox.configDir, sandbox.homeDir, sandbox.tmpDir} {
		// 0700 is the least a directory can have and still be entered;
		// the files written into it are 0600.
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
		if err := os.MkdirAll(dir, sandboxDirMode); err != nil {
			return nil, fmt.Errorf("creating the agent sandbox: %w", err)
		}
	}
	if configSource != "" {
		if err := installClaudeConfig(configSource, sandbox.configDir); err != nil {
			return nil, err
		}
	}
	settings, err := json.MarshalIndent(agentSettings(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding the agent settings: %w", err)
	}
	if err = os.WriteFile(sandbox.settingsPath, settings, sandboxFileMode); err != nil {
		return nil, fmt.Errorf("writing the agent settings: %w", err)
	}
	if err = os.WriteFile(sandbox.systemPromptPath, []byte(systemPrompt), sandboxFileMode); err != nil {
		return nil, fmt.Errorf("writing the agent system prompt: %w", err)
	}
	return sandbox, nil
}

// agentSettings is the settings file passed with `--settings`. Together with
// the command-line flags it is the agent's whole permission surface:
//
//   - `dontAsk` with explicit allow rules: anything not pre-approved is
//     denied, never prompted (there is nobody to answer a prompt);
//   - `blockReadsOutsideWorkingDirectories`: the file tools, and the shell
//     commands Claude Code recognises as file reads, cannot leave the
//     checkout — the pod's environment, service-account token and the
//     sandbox's own files are out of reach;
//   - `disableAllHooks` and the `managed-only` instruction-file setting: no
//     hook runs and no AGENTS.md or CLAUDE.md is auto-loaded as instructions
//     (the review reads the repository's own files deliberately, as data);
//   - bypass mode can never be switched on.
func agentSettings() map[string]any {
	return map[string]any{
		"disableAllHooks": true,
		"permissions": map[string]any{
			"defaultMode":                         "dontAsk",
			"disableBypassPermissionsMode":        "disable",
			"blockReadsOutsideWorkingDirectories": true,
			"allow":                               agentAllowedTools,
			"deny": append(
				append([]string{}, agentDeniedTools...),
				agentDeniedPatterns...),
		},
		"pluginConfigs": map[string]any{
			"cc-plugin-agents-md@builtin": map[string]any{
				"options": map[string]any{"instructionFiles": "managed-only"},
			},
		},
	}
}

// installClaudeConfig copies the operator tree's skills and commands into the
// sandbox configuration directory. Symlinks are skipped rather than followed,
// the total size is bounded, and every Markdown file has its granting
// frontmatter keys removed (see sanitizeFrontmatter), or is skipped when its
// frontmatter cannot be read with certainty: a skill that pre-approves
// `Bash(make *)` for itself would otherwise let the agent run the pull
// request's own build — arbitrary code — the moment it invoked the skill.
//
// Both trees are opened as `os.Root`s, so every read and write is resolved
// inside its root by the kernel: a symlink swapped in mid-walk, or a name with
// `..` in it, cannot make the copy read or write anything outside them.
func installClaudeConfig(source, target string) error {
	from, err := os.OpenRoot(source)
	if err != nil {
		return fmt.Errorf("opening the Claude configuration to install: %w", err)
	}
	defer func() { _ = from.Close() }()
	to, err := os.OpenRoot(target)
	if err != nil {
		return fmt.Errorf("opening the agent configuration directory: %w", err)
	}
	defer func() { _ = to.Close() }()

	installer := &configInstaller{from: from, to: to}
	for _, dir := range installedConfigDirs {
		if _, statErr := from.Stat(dir); errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		if walkErr := fs.WalkDir(from.FS(), dir, installer.copyEntry); walkErr != nil {
			return fmt.Errorf("installing the Claude configuration: %w", walkErr)
		}
	}
	return nil
}

// configInstaller copies one tree into another, root-scoped on both sides,
// keeping a running total against maxInstalledBytes.
type configInstaller struct {
	from      *os.Root
	to        *os.Root
	installed int64
}

// copyEntry is the [fs.WalkDir] callback: directories are recreated, regular
// files copied (Markdown with its grants stripped, or not at all), anything
// else — symlinks first of all — skipped.
func (i *configInstaller) copyEntry(path string, entry fs.DirEntry, walkErr error) error {
	switch {
	case walkErr != nil:
		return walkErr
	case entry.IsDir():
		return i.to.MkdirAll(path, sandboxDirMode)
	case !entry.Type().IsRegular():
		return nil
	}
	data, err := i.from.ReadFile(path)
	if err != nil {
		return err
	}
	i.installed += int64(len(data))
	if i.installed > maxInstalledBytes {
		return fmt.Errorf("the Claude configuration to install exceeds %d bytes", maxInstalledBytes)
	}
	if strings.EqualFold(filepath.Ext(path), ".md") {
		sanitized, sanitizeErr := sanitizeFrontmatter(string(data))
		if sanitizeErr != nil {
			logger.Warnf("agentic review: not installing %s: %v", path, sanitizeErr)
			return nil
		}
		data = []byte(sanitized)
	}
	return i.to.WriteFile(path, data, sandboxFileMode)
}
