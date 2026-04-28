package repositories

import "context"

// StubGitHubTokenizer returns a canned installation token without contacting GitHub.
type StubGitHubTokenizer struct {
	Token string
	Err   error
}

// InstallationToken returns the canned token (or the canned error).
func (s *StubGitHubTokenizer) InstallationToken(_ context.Context, _ int64) (string, error) {
	if s.Err != nil {
		return "", s.Err
	}
	return s.Token, nil
}
