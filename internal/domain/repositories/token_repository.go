package repositories

import "github.com/rios0rios0/codeguru/internal/domain/entities"

// TokenRepository abstracts storage and retrieval of OAuth tokens.
type TokenRepository interface {
	// SaveToken persists an OAuth token to storage.
	SaveToken(token entities.AuthToken) error

	// LoadToken retrieves the stored OAuth token, or returns nil if none exists.
	LoadToken() (*entities.AuthToken, error)

	// ClearToken removes the stored OAuth token.
	ClearToken() error
}
