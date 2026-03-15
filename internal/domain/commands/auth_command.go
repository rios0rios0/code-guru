package commands

import (
	"fmt"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

// AuthCommand handles OAuth authentication flows.
type AuthCommand struct {
	tokenRepo repositories.TokenRepository
}

// NewAuthCommand creates a new AuthCommand.
func NewAuthCommand(tokenRepo repositories.TokenRepository) *AuthCommand {
	return &AuthCommand{tokenRepo: tokenRepo}
}

// Login performs the OAuth 2.0 PKCE authentication flow.
func (c *AuthCommand) Login() error {
	// TODO: implement OAuth 2.0 PKCE flow:
	// 1. Generate code verifier and challenge
	// 2. Open browser to Anthropic's authorization endpoint
	// 3. Start local HTTP server to receive callback
	// 4. Exchange authorization code for tokens
	// 5. Store tokens via TokenRepository
	return fmt.Errorf("OAuth login not yet implemented -- use API key via CODE_GURU_ANTHROPIC_API_KEY")
}

// Logout clears the stored OAuth token.
func (c *AuthCommand) Logout() error {
	if err := c.tokenRepo.ClearToken(); err != nil {
		return fmt.Errorf("failed to clear token: %w", err)
	}
	logger.Info("logged out successfully")
	return nil
}

// Status displays the current authentication state.
func (c *AuthCommand) Status() error {
	token, err := c.tokenRepo.LoadToken()
	if err != nil {
		return fmt.Errorf("failed to load token: %w", err)
	}

	if token == nil {
		logger.Info("not authenticated -- run 'code-guru auth login' to authenticate")
		return nil
	}

	if token.IsExpired() {
		logger.Warn("token expired -- run 'code-guru auth login' to re-authenticate")
		return nil
	}

	logger.Info("authenticated (token valid)")
	return nil
}
