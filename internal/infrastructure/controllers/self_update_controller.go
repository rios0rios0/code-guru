package controllers

import (
	logger "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

type SelfUpdateController struct {
	command commands.SelfUpdate
}

func NewSelfUpdateController(command commands.SelfUpdate) *SelfUpdateController {
	return &SelfUpdateController{command: command}
}

func (it *SelfUpdateController) GetBind() entities.ControllerBind {
	return entities.ControllerBind{
		Use:   "self-update",
		Short: "Update code-guru to the latest version",
		Long:  "Download and install the latest version of code-guru from GitHub releases.",
	}
}

func (it *SelfUpdateController) Execute(cmd *cobra.Command, _ []string) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	force, _ := cmd.Flags().GetBool("force")

	err := it.command.Execute(dryRun, force)
	if err != nil {
		logger.Fatalf("Self-update failed: %s", err)
	}
}
