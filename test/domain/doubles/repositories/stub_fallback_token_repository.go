package repositories

import "context"

// FallbackTokenRequest is one GetToken call a StubFallbackTokenRepository
// received.
type FallbackTokenRequest struct {
	ProviderType string
	Organization string
}

// StubFallbackTokenRepository is a hand-rolled double for the fallback token
// of the person running a command. It answers with Tokens[organization] when
// that is set, else with Token, or with Err, and records every request so a
// test can tell whether, when and for what it was asked.
type StubFallbackTokenRepository struct {
	Token  string
	Tokens map[string]string
	Err    error

	Requests []FallbackTokenRequest
}

// GetToken records the request and returns the canned token or error.
func (r *StubFallbackTokenRepository) GetToken(
	_ context.Context, providerType, organization string,
) (string, error) {
	r.Requests = append(r.Requests, FallbackTokenRequest{ProviderType: providerType, Organization: organization})
	if r.Err != nil {
		return "", r.Err
	}
	if token, ok := r.Tokens[organization]; ok {
		return token, nil
	}
	return r.Token, nil
}
