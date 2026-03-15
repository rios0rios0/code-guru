package repositories

import "github.com/rios0rios0/codeguru/internal/domain/entities"

// StubTokenRepository is a test double for the TokenRepository interface.
type StubTokenRepository struct {
	Token    *entities.AuthToken
	SaveErr  error
	LoadErr  error
	ClearErr error
}

// SaveToken stores the token or returns a configured error.
func (r *StubTokenRepository) SaveToken(token entities.AuthToken) error {
	if r.SaveErr != nil {
		return r.SaveErr
	}
	r.Token = &token
	return nil
}

// LoadToken returns the configured token or error.
func (r *StubTokenRepository) LoadToken() (*entities.AuthToken, error) {
	return r.Token, r.LoadErr
}

// ClearToken clears the token or returns a configured error.
func (r *StubTokenRepository) ClearToken() error {
	if r.ClearErr != nil {
		return r.ClearErr
	}
	r.Token = nil
	return nil
}
