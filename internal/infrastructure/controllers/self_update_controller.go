package controllers

import (
	logger "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// SelfUpdateController handles the "self-update" subcommand.
type SelfUpdateController struct {
	command commands.SelfUpdate
}

// NewSelfUpdateController creates a new SelfUpdateController.
func NewSelfUpdateController(command commands.SelfUpdate) *SelfUpdateController {
	return &SelfUpdateController{command: command}
}

// GetBind returns the Cobra command metadata.
func (c *SelfUpdateController) GetBind() entities.ControllerBind {
	return entities.ControllerBind{
		Use:   "self-update",
		Short: "Update code-guru to the latest version",
		Long:  "Download and install the latest version of code-guru from GitHub releases.",
	}
}

// BindFlags registers command-specific flags for the self-update subcommand.
func (c *SelfUpdateController) BindFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("force", false, "Skip confirmation prompts")
}

// Execute performs the self-update.
func (c *SelfUpdateController) Execute(cmd *cobra.Command, _ []string) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	force, _ := cmd.Flags().GetBool("force")

	err := c.command.Execute(dryRun, force)
	if err != nil {
		logger.Fatalf("Self-update failed: %s", err)
	}
}
