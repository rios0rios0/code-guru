package claude

import (
	"os"
	"slices"
)

// SanitizeFrontmatter exposes the skill frontmatter sanitiser, so its parsing
// contract can be pinned case by case without staging an operator tree.
func SanitizeFrontmatter(content string) (string, error) {
	return sanitizeFrontmatter(content)
}

// WithScrubSandboxForTest fixes what the reviewer finds when it checks for the
// bubblewrap and socat install the subprocess environment scrub needs.
func (r *AgenticReviewerRepository) WithScrubSandboxForTest(installed bool) *AgenticReviewerRepository {
	r.scrubSandboxAvailable = func() bool { return installed }
	return r
}

// WithCredentialsForTest replaces the model credentials the reviewer finds in
// its environment with `credentials` (nil: none), so a test does not depend on
// the shell it runs in; every other variable is still read from the process.
func (r *AgenticReviewerRepository) WithCredentialsForTest(credentials map[string]string) *AgenticReviewerRepository {
	r.lookupEnv = func(name string) (string, bool) {
		if slices.Contains(credentialNames(), name) {
			value, ok := credentials[name]
			return value, ok
		}
		return os.LookupEnv(name)
	}
	return r
}

// HandOverCredentialsForTest exposes the descriptor handoff, so the socket
// contract can be pinned without a CLI on the other end.
func HandOverCredentialsForTest(lookup func(string) (string, bool)) ([]*os.File, []string, func(), error) {
	handoff, err := handOverCredentials(lookup)
	if err != nil {
		return nil, nil, nil, err
	}
	return handoff.Files(), handoff.Env(), handoff.Close, nil
}

// DescriptorHandoffSupported reports whether this platform hands the model
// credential over a descriptor.
const DescriptorHandoffSupported = descriptorHandoffSupported
