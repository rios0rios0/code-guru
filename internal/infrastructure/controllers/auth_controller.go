package controllers

import (
	logger "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// AuthController handles the "auth" subcommand group.
type AuthController struct {
	authCommand *commands.AuthCommand
}

// NewAuthController creates a new AuthController.
func NewAuthController(authCommand *commands.AuthCommand) *AuthController {
	return &AuthController{authCommand: authCommand}
}

// GetBind returns the Cobra command metadata.
func (c *AuthController) GetBind() entities.ControllerBind {
	return entities.ControllerBind{
		Use:   "auth",
		Short: "Manage authentication (login, logout, status)",
	}
}

// Execute handles the auth subcommand (shows help if no sub-subcommand given).
func (c *AuthController) Execute(cmd *cobra.Command, _ []string) {
	_ = cmd.Help()
}

// BuildAuthCommand builds the Cobra command with login/logout/status sub-subcommands.
func (c *AuthController) BuildAuthCommand() *cobra.Command {
	bind := c.GetBind()

	//nolint:exhaustruct // minimal Command initialization with required fields only
	authCmd := &cobra.Command{
		Use:   bind.Use,
		Short: bind.Short,
		Run: func(cmd *cobra.Command, args []string) {
			c.Execute(cmd, args)
		},
	}

	//nolint:exhaustruct // minimal Command initialization with required fields only
	authCmd.AddCommand(&cobra.Command{
		Use:   "login",
		Short: "Authenticate with Anthropic via OAuth",
		Run: func(_ *cobra.Command, _ []string) {
			if err := c.authCommand.Login(); err != nil {
				logger.Errorf("login failed: %v", err)
			}
		},
	})

	//nolint:exhaustruct // minimal Command initialization with required fields only
	authCmd.AddCommand(&cobra.Command{
		Use:   "logout",
		Short: "Clear stored authentication token",
		Run: func(_ *cobra.Command, _ []string) {
			if err := c.authCommand.Logout(); err != nil {
				logger.Errorf("logout failed: %v", err)
			}
		},
	})

	//nolint:exhaustruct // minimal Command initialization with required fields only
	authCmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show current authentication status",
		Run: func(_ *cobra.Command, _ []string) {
			if err := c.authCommand.Status(); err != nil {
				logger.Errorf("status check failed: %v", err)
			}
		},
	})

	return authCmd
}
