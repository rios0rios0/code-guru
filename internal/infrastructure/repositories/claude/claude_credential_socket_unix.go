//go:build linux || darwin

package claude

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// descriptorHandoffSupported reports that this platform hands the model
// credential to the CLI over a socket (see claude_credential_handoff.go).
const descriptorHandoffSupported = true

// maxCredentialBytes bounds a credential written into a socket nobody reads
// yet: the write must fit the socket's send buffer or it would block before
// the CLI starts. macOS gives a local stream socket 8 KiB; Anthropic's keys and
// tokens are around a hundred bytes.
const maxCredentialBytes = 4096

// errCredentialTooLarge refuses a credential that might not fit the buffer.
var errCredentialTooLarge = errors.New("the model credential is too long to hand over (4 KiB at most)")

// credentialSocket returns the CLI's end of a connected socket pair whose other
// end has already sent `value` and a newline, and been closed. Both ends are
// close-on-exec — [exec.Cmd] duplicates the CLI's end into the child — and are
// created under [syscall.ForkLock] so no other goroutine's fork can inherit them
// between creation and the flag being set (macOS has no SOCK_CLOEXEC).
func credentialSocket(value string) (*os.File, error) {
	if len(value)+1 > maxCredentialBytes {
		return nil, errCredentialTooLarge
	}
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("creating the credential socket: %w", err)
	}
	server := os.NewFile(uintptr(fds[0]), "claude credential (server end)")
	cli := os.NewFile(uintptr(fds[1]), "claude credential")
	_, writeErr := server.WriteString(value + "\n")
	if err = errors.Join(writeErr, server.Close()); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("writing the credential socket: %w", err)
	}
	return cli, nil
}
