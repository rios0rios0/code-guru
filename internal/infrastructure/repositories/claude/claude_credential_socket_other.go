//go:build !unix

package claude

import (
	"errors"
	"os"
)

// descriptorHandoffSupported reports that this platform cannot hand the model
// credential to the CLI over an inherited descriptor ([exec.Cmd.ExtraFiles] is
// Unix-only), so every credential travels in the CLI's environment and is
// treated like ANTHROPIC_AUTH_TOKEN (see AgenticReviewerRepository).
const descriptorHandoffSupported = false

// credentialSocket is never called where descriptorHandoffSupported is false.
func credentialSocket(string) (*os.File, error) {
	return nil, errors.New("handing a credential over a descriptor is not supported on this platform")
}
