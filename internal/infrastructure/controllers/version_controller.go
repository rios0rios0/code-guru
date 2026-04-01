package controllers

import (
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

type VersionController struct {
	command commands.Version
}

func NewVersionController(command commands.Version) *VersionController {
	return &VersionController{command: command}
}

func (it *VersionController) GetBind() entities.ControllerBind {
	return entities.ControllerBind{
		Use:   "version",
		Short: "Show code-guru version",
		Long:  "Display the version information for code-guru.",
	}
}

func (it *VersionController) Execute(_ *cobra.Command, _ []string) {
	it.command.Execute()
}
