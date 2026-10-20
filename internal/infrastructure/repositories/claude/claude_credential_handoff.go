package claude

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The model credential reaches the agent's CLI over an inherited socket, never
// through its environment. A process's initial environment stays readable
// through /proc/<pid>/environ by every same-user process for as long as the
// process lives, so a credential put there is one `/proc` read away from any
// command the agent runs, and inherited by every one of them unless the CLI's
// subprocess environment scrub strips it. The scrub needs bubblewrap and
// socat, which a hardened pod cannot run. A socket needs neither: the CLI
// reads the credential from the descriptor named by `*_FILE_DESCRIPTOR` up to
// the first newline, closes it, and keeps the value in memory, where a
// descendant cannot reach it (reading another process's memory takes ptrace
// rights over it, which Yama's default scope gives only to its ancestors).
//
// It must be a socket. The CLI reads a socket directly and closes it; a pipe
// it reopens through /proc/self/fd — which does not exist on macOS — and
// leaves the inherited descriptor open for the whole run.

// descriptorCredential is a model credential the CLI can read from an
// inherited descriptor: `name` is the variable the server holds it in,
// `descriptorEnv` the variable that tells the CLI which descriptor to read.
type descriptorCredential struct {
	name          string
	descriptorEnv string
}

// agentDescriptorCredentials are the credentials handed to the CLI over a
// descriptor, in the order their descriptors are numbered.
//
//nolint:gochecknoglobals // read-only list used as a constant
var agentDescriptorCredentials = []descriptorCredential{
	{name: "CLAUDE_CODE_OAUTH_TOKEN", descriptorEnv: "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"},
	{name: "ANTHROPIC_API_KEY", descriptorEnv: "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR"},
}

// agentEnvironmentCredentials are the credentials the CLI reads only from its
// environment, which every command the agent runs would otherwise inherit:
// ANTHROPIC_AUTH_TOKEN has no descriptor variant. Only the subprocess
// environment scrub keeps them from those commands.
//
//nolint:gochecknoglobals // read-only list used as a constant
var agentEnvironmentCredentials = []string{"ANTHROPIC_AUTH_TOKEN"}

// environmentCredentialNames lists the credentials that reach the CLI through
// its environment on this platform: ANTHROPIC_AUTH_TOKEN everywhere, and the
// descriptor credentials too where a descriptor cannot be handed over.
func environmentCredentialNames() []string {
	names := append([]string{}, agentEnvironmentCredentials...)
	if !descriptorHandoffSupported {
		for _, credential := range agentDescriptorCredentials {
			names = append(names, credential.name)
		}
	}
	return names
}

// credentialNames lists every model credential the CLI may be given, however
// it travels: the values to redact from what the agent returns.
func credentialNames() []string {
	names := append([]string{}, agentEnvironmentCredentials...)
	for _, credential := range agentDescriptorCredentials {
		names = append(names, credential.name)
	}
	return names
}

// firstExtraFileDescriptor is the number exec gives ExtraFiles[0] in the child:
// 0, 1 and 2 are stdin, stdout and stderr.
const firstExtraFileDescriptor = 3

// errCredentialSpansLines rejects a credential the CLI would cut short: it
// reads the descriptor up to the first newline.
var errCredentialSpansLines = errors.New("the model credential spans more than one line")

// credentialHandoff is the set of descriptors one CLI run inherits, and the
// variables that name them.
type credentialHandoff struct {
	files []*os.File
	env   []string
}

// handOverCredentials opens one socket per descriptor credential set in the
// environment `lookup` reads, writes the credential into it and keeps the
// CLI's end, ready for [exec.Cmd.ExtraFiles]. The server's end is closed before
// it returns, so the value sits only in the socket's buffer until the CLI
// reads it. A credential with surrounding whitespace is trimmed, as the CLI
// would; one that spans lines is refused.
func handOverCredentials(lookup func(string) (string, bool)) (*credentialHandoff, error) {
	handoff := &credentialHandoff{}
	if !descriptorHandoffSupported {
		return handoff, nil
	}
	for _, credential := range agentDescriptorCredentials {
		raw, ok := lookup(credential.name)
		value := strings.TrimSpace(raw)
		if !ok || value == "" {
			continue
		}
		if strings.ContainsAny(value, "\r\n") {
			handoff.Close()
			return nil, fmt.Errorf("%w: %s", errCredentialSpansLines, credential.name)
		}
		file, err := credentialSocket(value)
		if err != nil {
			handoff.Close()
			return nil, fmt.Errorf("handing %s to the claude CLI: %w", credential.name, err)
		}
		descriptor := firstExtraFileDescriptor + len(handoff.files)
		handoff.files = append(handoff.files, file)
		handoff.env = append(handoff.env, credential.descriptorEnv+"="+strconv.Itoa(descriptor))
	}
	return handoff, nil
}

// Files returns the descriptors for [exec.Cmd.ExtraFiles], in the order Env
// numbers them.
func (h *credentialHandoff) Files() []*os.File {
	return h.files
}

// Env returns the `*_FILE_DESCRIPTOR` variables naming the descriptors.
func (h *credentialHandoff) Env() []string {
	return h.env
}

// Close closes this process's copies of the CLI's ends. Call it once the CLI
// has started (it holds its own copies) or when it never will. Safe to call
// more than once.
func (h *credentialHandoff) Close() {
	for _, file := range h.files {
		_ = file.Close()
	}
}
