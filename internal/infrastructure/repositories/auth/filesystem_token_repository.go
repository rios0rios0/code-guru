package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

const tokenFileName = "auth.json"

// FilesystemTokenRepository stores OAuth tokens in a local file.
type FilesystemTokenRepository struct {
	configDir string
}

// NewFilesystemTokenRepository creates a new filesystem-backed token repository.
func NewFilesystemTokenRepository() (*FilesystemTokenRepository, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return &FilesystemTokenRepository{
		configDir: filepath.Join(home, ".config", "code-guru"),
	}, nil
}

// SaveToken persists an OAuth token to the filesystem.
func (r *FilesystemTokenRepository) SaveToken(token entities.AuthToken) error {
	if err := os.MkdirAll(r.configDir, 0o700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(
		token,
		"",
		"  ",
	) //nolint:gosec // intentionally serializing auth token for local storage
	if err != nil {
		return fmt.Errorf("failed to marshal token: %w", err)
	}

	tokenPath := filepath.Join(r.configDir, tokenFileName)
	if writeErr := os.WriteFile(tokenPath, data, 0o600); writeErr != nil {
		return fmt.Errorf("failed to write token file: %w", writeErr)
	}

	return nil
}

// LoadToken retrieves the stored OAuth token from the filesystem.
func (r *FilesystemTokenRepository) LoadToken() (*entities.AuthToken, error) {
	tokenPath := filepath.Join(r.configDir, tokenFileName)

	data, err := os.ReadFile(tokenPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil //nolint:nilnil // nil token signals "not found" per TokenRepository contract
		}
		return nil, fmt.Errorf("failed to read token file: %w", err)
	}

	var token entities.AuthToken
	if unmarshalErr := json.Unmarshal(data, &token); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to parse token file: %w", unmarshalErr)
	}

	return &token, nil
}

// ClearToken removes the stored OAuth token from the filesystem.
func (r *FilesystemTokenRepository) ClearToken() error {
	tokenPath := filepath.Join(r.configDir, tokenFileName)

	if err := os.Remove(tokenPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove token file: %w", err)
	}

	return nil
}
