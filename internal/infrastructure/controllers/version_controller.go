package controllers

import (
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// VersionController handles the "version" subcommand.
type VersionController struct {
	command commands.Version
}

// NewVersionController creates a new VersionController.
func NewVersionController(command commands.Version) *VersionController {
	return &VersionController{command: command}
}

// GetBind returns the Cobra command metadata.
func (c *VersionController) GetBind() entities.ControllerBind {
	return entities.ControllerBind{
		Use:   "version",
		Short: "Show code-guru version",
		Long:  "Display the version information for code-guru.",
	}
}

// Execute prints the current version.
func (c *VersionController) Execute(_ *cobra.Command, _ []string) {
	c.command.Execute()
}
